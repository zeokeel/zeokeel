package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// The per-run token stream (docs/TRANSPORT.md §4.6).
//
// The authoritative state stays in the WebSocket session domain; this endpoint
// is the fast lane for one turn's deltas, in the SSE shape @ant-design/x-sdk's
// XRequest already speaks. It is derived from the same hub deltas the WS side
// sends, so the two can never disagree:
//
//	msg.add / msg.delta (assistant text)   -> message.delta
//	msg.delta (reasoningAppend)            -> reasoning.delta
//	msg.tool.upsert (running)              -> tool.start
//	msg.tool.upsert (ok/error/cancelled)   -> tool.result
//	msg.patch (usage)                      -> usage
//	session.patch (running:false)          -> done
//	msg.patch (error) / session.patch error -> error
//
// Three rules from the spec that shape the implementation:
//
//   - done|error is always the last event, and tool.start always precedes the
//     same callId's tool.result (deltas are ordered, so this falls out).
//   - A dropped connection does NOT stop the run: nothing here calls Cancel.
//     The user's stop is the WS run.cancel, and WS running:false is the truth.
//   - The same clientRunId is idempotent: while its run is active a second
//     request is refused (never a second run), after it finished the recorded
//     events are replayed.
const (
	// runCoalesceInterval is how long text/reasoning deltas are merged before a
	// frame goes out (§4.6 backpressure: render-side throttling starts here).
	runCoalesceInterval = 60 * time.Millisecond
	// runHeartbeatInterval is how long a silent stream may stay silent before a
	// comment frame keeps it alive. A subagent delegation or a slow tool can go
	// minutes without an event, and the browser client treats a dead stream — not a
	// slow one — as a failure; without this it aborted healthy runs at its 60s
	// stream timeout and showed nothing but `StreamTimeoutError`.
	runHeartbeatIntervalDefault = 15 * time.Second
	// runLogEvents and runLogBytes bound one finished run's replay log.
	runLogEvents = 4096
	runLogBytes  = 4 << 20
	// runRetainedRuns bounds how many finished runs stay replayable.
	runRetainedRuns = 16
	// maxRunRequestBytes bounds the POST body.
	maxRunRequestBytes = 1 << 20
)

// runHeartbeatInterval is the live value; tests shrink it. It is a variable and
// not a constant so a test can prove the heartbeat without waiting 15 seconds.
var runHeartbeatInterval = runHeartbeatIntervalDefault

// SSE event names.
const (
	sseMessageDelta   = "message.delta"
	sseReasoningDelta = "reasoning.delta"
	sseToolStart      = "tool.start"
	sseToolResult     = "tool.result"
	sseUsage          = "usage"
	sseDone           = "done"
	sseError          = "error"
)

// runEvent is one SSE frame.
type runEvent struct {
	name string
	data map[string]any
}

// terminal reports whether the event ends the stream.
func (e runEvent) terminal() bool { return e.name == sseDone || e.name == sseError }

// size is the approximate retained size of one event.
func (e runEvent) size() int {
	total := 32 + len(e.name)
	for key, value := range e.data {
		total += len(key) + 16
		if text, ok := value.(string); ok {
			total += len(text)
		}
	}
	return total
}

// runRequest is the POST /api/chat/run body.
//
// EditMessageID turns the endpoint into the "edit an earlier user message" mode:
// the backend truncates the history to just before that message and runs the
// edited text (docs/TRANSPORT.md §4.6). Like regenerate it is a *mode* of the
// one send path, and it is mutually exclusive with regenerate.
//
// Regenerate turns the same endpoint into the "retry the last turn" mode: the
// backend rewinds the last exchange (dropping the trailing assistant reply and
// its user message) and runs the user message again. It exists as a *mode* of
// the one send path rather than as a WS method on purpose — see
// docs/TRANSPORT.md §4.6: a two-step "reset over WS, then run" protocol leaves
// a rewound-but-not-running session behind whenever the second step is missed.
// Text is not sent in this mode (the backend already has the prompt).
type runRequest struct {
	SessionID string `json:"sessionId"`
	// Text is optional at the struct level because regenerate mode must omit it;
	// the mode rules in contract.json carry "required unless regenerate".
	Text        string   `json:"text,omitempty"`
	Attachments []string `json:"attachments,omitempty"`
	ClientRunID string   `json:"clientRunId"`
	Regenerate  bool     `json:"regenerate,omitempty"`
	// EditMessageID names the user message being replaced ("" = not an edit).
	EditMessageID string `json:"editMessageId,omitempty"`
}

// runStream is one run: the events it produced, and the listeners reading them.
//
// Listeners poll the log by cursor instead of receiving on their own channel, so
// a slow browser costs memory (bounded by the log cap) and never blocks the hub,
// and a reconnecting client simply starts its cursor at zero.
type runStream struct {
	clientRunID string
	sessionID   string
	turnID      string

	baseline map[string]bool
	// notBody holds message IDs that are not this turn's assistant body — the
	// user's own message, and anything else the session carries. Send appends the
	// user's prompt after the baseline is taken, so it arrives as an opMsgAdd on
	// this stream; without this the prompt is emitted as `message.delta` and the
	// browser renders it a second time as the answer's first tokens.
	notBody map[string]bool
	// startedTools remembers which calls already got their `tool.start`. A call the
	// stream first observes **already settled** (finished inside one coalescing
	// window) still needs that frame: without it the client has no tool name or
	// input and renders the step as a bare call id (the orphan `tool.result` a live
	// coding run produced), and TRANSPORT §4.6 promises start precedes result.
	startedTools map[string]bool

	mu       sync.Mutex
	log      []runEvent
	base     int
	logBytes int
	finished bool
	wake     chan struct{}

	stopObserving func()
}

// handleChatRun serves one turn as text/event-stream.
func (g *Gateway) handleChatRun(w http.ResponseWriter, r *http.Request) {
	if err := g.authorizeRequest(r, true); err != nil {
		status := http.StatusUnauthorized
		if err.Code == codeBadOrigin || err.Data["name"] == "bad_host" {
			status = http.StatusForbidden
		}
		writeProtocolError(w, status, err)
		return
	}
	var body runRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRunRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeProtocolError(w, http.StatusBadRequest, errInvalidRequest("body is not valid JSON: "+err.Error()))
		return
	}
	body.SessionID = strings.TrimSpace(body.SessionID)
	body.ClientRunID = strings.TrimSpace(body.ClientRunID)
	if body.SessionID == "" {
		writeProtocolError(w, http.StatusBadRequest, errInvalidParams("sessionId", "is required"))
		return
	}
	body.EditMessageID = strings.TrimSpace(body.EditMessageID)
	if body.Regenerate && body.EditMessageID != "" {
		// Two different rewrites of the same history: refuse instead of picking
		// one silently.
		writeProtocolError(w, http.StatusBadRequest,
			errInvalidParams("editMessageId", "is mutually exclusive with regenerate"))
		return
	}
	switch {
	case body.Regenerate:
		// The backend re-runs the last user message it already holds; sending
		// text as well would silently append a second prompt.
		if strings.TrimSpace(body.Text) != "" {
			writeProtocolError(w, http.StatusBadRequest,
				errInvalidParams("text", "must be empty when regenerate is true"))
			return
		}
	case body.EditMessageID != "":
		// The edit replaces the target message's text, so the text IS the new
		// content: empty would mean "delete the message", which the protocol
		// does not offer.
		if strings.TrimSpace(body.Text) == "" {
			writeProtocolError(w, http.StatusBadRequest,
				errInvalidParams("text", "is required when editMessageId is set"))
			return
		}
	default:
		if strings.TrimSpace(body.Text) == "" {
			writeProtocolError(w, http.StatusBadRequest, errInvalidParams("text", "is required"))
			return
		}
	}
	if body.ClientRunID == "" {
		// Without it a retry after a dropped connection cannot be made
		// idempotent, and the SSE stream could not be aligned with the WS
		// session deltas (§4.6).
		writeProtocolError(w, http.StatusBadRequest, errInvalidParams("clientRunId", "is required"))
		return
	}
	if len(body.Attachments) > 0 {
		// Attachments travel as blob ids uploaded to POST /api/blobs (§6.4). An
		// unknown id is refused rather than sending a turn with fewer files.
		// Regenerate cannot carry them: the retried turn reuses the ones the
		// original message already had.
		if body.Regenerate || body.EditMessageID != "" {
			writeProtocolError(w, http.StatusBadRequest,
				appError(codeCapabilityUnsupported, "attachments_not_supported", false))
			return
		}
		if _, problem := g.resolveAttachments(body.Attachments); problem != nil {
			writeProtocolError(w, http.StatusBadRequest, problem)
			return
		}
	}

	stream, replay, protocolErr := g.beginRun(r.Context(), body)
	if protocolErr != nil {
		status := http.StatusBadRequest
		switch protocolErr.Code {
		case codeSessionNotFound:
			status = http.StatusNotFound
		case codeSessionRunning, codeBackpressure:
			status = http.StatusConflict
		case codeNotReady:
			status = http.StatusServiceUnavailable
		}
		writeProtocolError(w, status, protocolErr)
		return
	}
	g.streamRun(w, r, stream, replay)
}

// beginRun starts a run or reports the existing one for the same clientRunId.
//
// The claim is one critical section: the lookup, the "is it finished" decision
// and the registration of this attempt all happen under runMu. It used to
// unlock between them and take a hub round-trip in the window, so two retries
// of the same clientRunId (the exact case §4.6 says the id exists for) both
// passed the check and started two turns for one prompt
// (AUDIT-2026-11-24 A-3). Every failure after the claim drops it again.
func (g *Gateway) beginRun(ctx context.Context, body runRequest) (stream *runStream, replay bool, protocolErr *protocolError) {
	stream = &runStream{
		clientRunID: body.ClientRunID,
		sessionID:   body.SessionID,
		turnID:      body.ClientRunID,
		wake:        make(chan struct{}, 1),
	}
	g.runMu.Lock()
	if g.runs == nil {
		g.runs = make(map[string]*runStream)
	}
	if existing := g.runs[body.ClientRunID]; existing != nil {
		existing.mu.Lock()
		finished := existing.finished
		existing.mu.Unlock()
		g.runMu.Unlock()
		if !finished {
			// A retry while the run is live must not start a second run. The
			// client keeps using the WS session domain until this one settles.
			return nil, false, appError(codeBackpressure, "run_already_active", true)
		}
		return existing, true, nil
	}
	g.runs[body.ClientRunID] = stream
	g.pruneRunsLocked()
	g.runMu.Unlock()

	chat, err := g.chatService()
	if err != nil {
		g.dropRun(stream)
		return nil, false, internalError(err)
	}
	snapshot, err := g.hub.snapshotSession(ctx, body.SessionID)
	if err != nil {
		g.dropRun(stream)
		return nil, false, internalError(err)
	}
	if !snapshot.found {
		g.dropRun(stream)
		return nil, false, appError(codeSessionNotFound, "session_not_found", false)
	}
	session, ok := snapshot.payload.(sessionDTO)
	if !ok {
		g.dropRun(stream)
		return nil, false, internalError(errors.New("gateway: session snapshot has an unexpected type"))
	}
	// Safe to fill in after registration: only consume() reads the baseline, and
	// the session observer that calls it is attached further down.
	baseline := make(map[string]bool, len(session.Messages))
	for _, message := range session.Messages {
		baseline[message.ID] = true
	}
	stream.baseline = baseline

	stop, err := g.hub.observeSession(ctx, body.SessionID, stream.consume)
	if err != nil {
		g.dropRun(stream)
		return nil, false, internalError(err)
	}
	// Guarded by stream.mu: emit() reads and clears it from the hub goroutine,
	// and this assignment runs on the HTTP handler goroutine
	// (AUDIT-2026-11-24 C: an unsynchronised read/write pair).
	stream.mu.Lock()
	stream.stopObserving = stop
	stream.mu.Unlock()

	// The observer is attached before the backend starts the turn: Regenerate
	// rewinds and spawns the run goroutine itself, so attaching afterwards would
	// drop the opening events (§4.6).
	if err := g.startTurn(chat, body); err != nil {
		stream.detach()
		stream.emit(runEvent{name: sseError, data: map[string]any{
			"code":    codeSessionRunning,
			"message": err.Error(),
		}})
		g.dropRun(stream)
		if errors.Is(err, contracts.ErrSessionRunning) || errors.Is(err, contracts.ErrChatCompacting) {
			return nil, false, appError(codeSessionRunning, "session_running", true)
		}
		if errors.Is(err, errRegenerateUnsupported) {
			// A build whose backend lacks the regenerate half: tell the client
			// to disable the action instead of reporting a failure.
			return nil, false, appError(codeCapabilityUnsupported, "regenerate_not_supported", false)
		}
		if errors.Is(err, errEditUnsupported) {
			return nil, false, appError(codeCapabilityUnsupported, "edit_not_supported", false)
		}
		if protocolErr := editRefusal(body.EditMessageID, err); protocolErr != nil {
			return nil, false, protocolErr
		}
		return nil, false, internalError(err)
	}
	return stream, false, nil
}

// startTurn starts the turn a run request describes: a normal send, a regenerate
// of the last exchange, or an edit of an earlier user message
// (docs/TRANSPORT.md §4.6). All three are modes of the one send path.
func (g *Gateway) startTurn(chat contracts.ChatService, body runRequest) error {
	switch {
	case body.EditMessageID != "":
		editor, ok := chat.(contracts.SessionEditor)
		if !ok {
			return errEditUnsupported
		}
		return editor.EditMessage(body.SessionID, body.EditMessageID, body.Text)
	case body.Regenerate:
		regenerator, ok := chat.(contracts.SessionRegenerator)
		if !ok {
			return errRegenerateUnsupported
		}
		return regenerator.Regenerate(body.SessionID)
	default:
		if len(body.Attachments) == 0 {
			return chat.Send(body.SessionID, body.Text)
		}
		sender, ok := chat.(contracts.AttachmentSender)
		if !ok {
			return errAttachmentsUnsupported
		}
		attachments, problem := g.resolveAttachments(body.Attachments)
		if problem != nil {
			return errAttachmentsUnsupported
		}
		return sender.SendWithAttachments(body.SessionID, body.Text, attachments)
	}
}

// errAttachmentsUnsupported reports a backend without AttachmentSender.
var errAttachmentsUnsupported = errors.New("gateway: the chat backend cannot attach files")

// errEditUnsupported reports a backend without the edit half.
var errEditUnsupported = errors.New("gateway: the chat backend cannot edit messages")

// errRegenerateUnsupported reports a backend without the regenerate half.
var errRegenerateUnsupported = errors.New("gateway: the chat backend cannot regenerate")

// editRefusal maps the backend's edit refusals onto parameter errors that name
// the field, so the client can explain ("this message was compacted away")
// instead of retrying or, worse, silently starting a fresh turn.
func editRefusal(editMessageID string, err error) *protocolError {
	if editMessageID == "" {
		return nil
	}
	refusals := []struct {
		sentinel error
		name     string
		reason   string
	}{
		{contracts.ErrEditTargetNotFound, "edit_target_not_found", "no such user message in this session"},
		{contracts.ErrEditTargetNotUser, "edit_target_not_user", "only user messages can be edited"},
		{contracts.ErrEditTargetHasAttachments, "edit_target_has_attachments", "messages with attachments cannot be edited"},
		{contracts.ErrEditTargetCompacted, "edit_target_compacted", "the message is no longer in the durable history window"},
		{contracts.ErrEditTargetMoved, "edit_target_moved", "the conversation changed while the edit was being applied"},
	}
	for _, refusal := range refusals {
		if !errors.Is(err, refusal.sentinel) {
			continue
		}
		protocolErr := errInvalidParams("editMessageId", refusal.reason)
		protocolErr.Data["name"] = refusal.name
		return protocolErr
	}
	return nil
}

// detach stops observing the session, if an observer is attached. It is the
// safe form for callers outside the stream's lock.
func (s *runStream) detach() {
	s.mu.Lock()
	stop := s.stopObserving
	s.stopObserving = nil
	s.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// dropRun forgets one run that never lived.
func (g *Gateway) dropRun(stream *runStream) {
	g.runMu.Lock()
	if g.runs[stream.clientRunID] == stream {
		delete(g.runs, stream.clientRunID)
	}
	g.runMu.Unlock()
}

// pruneRunsLocked keeps the finished-run replay table bounded.
func (g *Gateway) pruneRunsLocked() {
	if len(g.runs) <= runRetainedRuns {
		return
	}
	for id, stream := range g.runs {
		if len(g.runs) <= runRetainedRuns {
			return
		}
		stream.mu.Lock()
		finished := stream.finished
		stream.mu.Unlock()
		if finished {
			delete(g.runs, id)
		}
	}
}

// streamRun writes the SSE frames until the stream ends or the client goes away.
func (g *Gateway) streamRun(w http.ResponseWriter, r *http.Request, stream *runStream, replay bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeProtocolError(w, http.StatusInternalServerError, errInternal("response writer cannot stream"))
		return
	}
	header := w.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	_ = replay

	cursor := 0
	lastWrite := time.Now()
	var pendingText, pendingReasoning strings.Builder
	flushText := func() {
		if pendingText.Len() > 0 {
			writeSSE(w, flusher, runEvent{name: sseMessageDelta, data: map[string]any{
				"turnId": stream.turnID,
				"text":   pendingText.String(),
			}})
			pendingText.Reset()
		}
		if pendingReasoning.Len() > 0 {
			writeSSE(w, flusher, runEvent{name: sseReasoningDelta, data: map[string]any{
				"turnId": stream.turnID,
				"text":   pendingReasoning.String(),
			}})
			pendingReasoning.Reset()
			lastWrite = time.Now()
		}
	}
	ticker := time.NewTicker(runCoalesceInterval)
	defer ticker.Stop()
	heartbeat := time.NewTicker(runHeartbeatInterval)
	defer heartbeat.Stop()
	for {
		events, next, dropped, finished := stream.eventsFrom(cursor)
		if dropped {
			flushText()
			writeSSE(w, flusher, runEvent{name: sseError, data: map[string]any{
				"code":    codeBackpressure,
				"message": "the run produced more events than this stream could buffer; read the session from /ws",
			}})
			return
		}
		cursor = next
		for _, event := range events {
			switch event.name {
			case sseMessageDelta:
				if text, ok := event.data["text"].(string); ok {
					pendingText.WriteString(text)
				}
			case sseReasoningDelta:
				if text, ok := event.data["text"].(string); ok {
					pendingReasoning.WriteString(text)
				}
			default:
				// Tool, usage and terminal frames are never merged: their order
				// against the text they follow matters.
				flushText()
				writeSSE(w, flusher, event)
				lastWrite = time.Now()
				if event.terminal() {
					return
				}
			}
		}
		if finished {
			flushText()
			return
		}
		select {
		case <-stream.wake:
		case <-ticker.C:
			flushText()
		case <-heartbeat.C:
			// SSE comment: the browser's stream timer resets on any byte, and a
			// comment is not an event, so it cannot be mistaken for run output.
			if time.Since(lastWrite) >= runHeartbeatInterval {
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
				lastWrite = time.Now()
			}
		case <-r.Context().Done():
			// The connection dropped. The run keeps going (this is not a stop
			// request, §4.6); the client resyncs from the WS session domain.
			return
		}
	}
}

// writeSSE writes and flushes one frame.
func writeSSE(w http.ResponseWriter, flusher http.Flusher, event runEvent) {
	payload, err := json.Marshal(event.data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.name, payload)
	flusher.Flush()
}

// eventsFrom returns the events after cursor and the next cursor.
func (s *runStream) eventsFrom(cursor int) (events []runEvent, next int, dropped bool, finished bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cursor < s.base {
		return nil, cursor, true, s.finished
	}
	if cursor > len(s.log) {
		cursor = len(s.log)
	}
	out := make([]runEvent, len(s.log)-cursor)
	copy(out, s.log[cursor:])
	return out, len(s.log), false, s.finished
}

// emit records one event and wakes every listener. It runs inside the hub
// goroutine (the delta observer), so it only appends and pokes.
// markNotBody remembers a message that is not this turn's assistant body, so its
// later ops stay off the stream. consume runs on the hub goroutine, so no lock is
// taken here (the map is only read from the same goroutine).
func (s *runStream) markNotBody(id string) {
	if id == "" {
		return
	}
	if s.notBody == nil {
		s.notBody = make(map[string]bool, 2)
	}
	s.notBody[id] = true
}

func (s *runStream) emit(event runEvent) {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return
	}
	s.log = append(s.log, event)
	s.logBytes += event.size()
	for (len(s.log) > runLogEvents || s.logBytes > runLogBytes) && len(s.log) > 1 {
		s.logBytes -= s.log[0].size()
		s.log = s.log[1:]
		s.base++
	}
	if event.terminal() {
		s.finished = true
		// The run is over: detach from the hub outside this goroutine (emit
		// runs inside the hub goroutine, and detaching is another hub command).
		if stop := s.stopObserving; stop != nil {
			s.stopObserving = nil
			go stop()
		}
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// consume converts one session delta into SSE events. Terminal frames (done /
// error) are held back until every other op of the delta is converted, so the
// stream can never end before its last message/tool update.
func (s *runStream) consume(delta *sessionDelta) {
	var terminal []runEvent
	defer func() {
		for _, event := range terminal {
			s.emit(event)
		}
	}()
	for _, op := range delta.Ops {
		switch op.Op {
		case opMsgAdd:
			if op.Message == nil || s.baseline[op.Message.ID] {
				continue
			}
			// Only the assistant writes the turn's body (TRANSPORT §4.6). Send
			// appends the user's prompt after the baseline, so it lands here too;
			// remembering it keeps its late deltas out as well.
			if op.Message.Role != "assistant" {
				s.markNotBody(op.Message.ID)
				continue
			}
			s.emitMessageStart(*op.Message)
		case opMsgDelta:
			if s.baseline[op.MessageID] || s.notBody[op.MessageID] {
				continue
			}
			if op.TextAppend != "" {
				s.emit(runEvent{name: sseMessageDelta, data: map[string]any{
					"turnId": s.turnID, "text": op.TextAppend,
				}})
			}
			if growth := op.ReasoningAppend; growth != nil && growth.TextAppend != "" {
				s.emit(runEvent{name: sseReasoningDelta, data: map[string]any{
					"turnId":    s.turnID,
					"text":      growth.TextAppend,
					"iteration": growth.Iteration,
				}})
			}
		case opMsgToolUpsert:
			if s.baseline[op.MessageID] || s.notBody[op.MessageID] || op.Tool == nil {
				continue
			}
			s.emitTool(*op.Tool)
		case opMsgPatch:
			if s.baseline[op.MessageID] || s.notBody[op.MessageID] {
				continue
			}
			s.emitPatch(op.Fields)
		case opSessionPatch:
			terminal = append(terminal, s.sessionPatchEvents(op.Fields)...)
		case opMsgReset:
			// The history was rewritten. For the regenerate and edit modes that IS
			// the turn's start: the backend truncated before it began streaming,
			// and the WS session domain already carried the reset to the client.
			// The fast lane therefore re-baselines and keeps streaming, instead of
			// failing a run it caused itself.
			//
			// A rewrite that is NOT ours would be a compaction, and compaction is
			// rejected while a session runs (contracts.ErrChatCompacting), so this
			// cannot mask a concurrent third-party rewrite.
			s.baseline = make(map[string]bool, len(op.Messages))
			for _, message := range op.Messages {
				s.baseline[message.ID] = true
			}
		}
	}
}

// emitMessageStart emits the text, reasoning and tool frames a brand-new
// assistant message arrives with.
func (s *runStream) emitMessageStart(message messageDTO) {
	if message.Text != "" {
		s.emit(runEvent{name: sseMessageDelta, data: map[string]any{
			"turnId": s.turnID, "text": message.Text,
		}})
	}
	for _, segment := range message.ReasoningSegments {
		if segment.Text == "" {
			continue
		}
		s.emit(runEvent{name: sseReasoningDelta, data: map[string]any{
			"turnId": s.turnID, "text": segment.Text, "iteration": segment.Iteration,
		}})
	}
	for _, tool := range message.Tools {
		s.emitTool(tool)
	}
	if message.Error != "" {
		s.emitRunError(message.Error, message.ErrorKind)
	}
}

// emitTool emits tool.start the first time a call is seen and tool.result once it
// settles. A call that arrives already settled still gets its start first, so the
// client always knows the tool's name and arguments (TRANSPORT §4.6).
func (s *runStream) emitTool(tool toolCallDTO) {
	if !s.startedTools[tool.ID] {
		if s.startedTools == nil {
			s.startedTools = make(map[string]bool, 4)
		}
		s.startedTools[tool.ID] = true
		s.emit(runEvent{name: sseToolStart, data: map[string]any{
			"callId": tool.ID, "name": tool.Name, "input": tool.Arguments,
		}})
	}
	if tool.Status == "running" {
		return
	}
	data := map[string]any{
		"callId": tool.ID,
		"name":   tool.Name,
		// A cancelled call did not succeed: `ok` drives the client's status, and
		// "not error" made a stopped call render as a green 完成 (round-6 final
		// audit N3).
		"ok": tool.Status != "error" && tool.Status != "cancelled",
	}
	if tool.Status == "cancelled" {
		data["cancelled"] = true
	}
	if tool.Summary != "" {
		if tool.Status == "error" {
			data["error"] = tool.Summary
		} else {
			data["output"] = tool.Summary
		}
	}
	if tool.ErrorKind != "" {
		data["errorKind"] = tool.ErrorKind
	}
	if tool.EndedAt != nil {
		data["endedAt"] = tool.EndedAt.UTC().Format(time.RFC3339Nano)
	}
	s.emit(runEvent{name: sseToolResult, data: data})
}

// emitPatch converts a message patch into usage/error frames.
func (s *runStream) emitPatch(fields map[string]any) {
	if usage, ok := fields["usage"].(*messageUsageDTO); ok && usage != nil {
		s.emit(runEvent{name: sseUsage, data: map[string]any{
			"turnId":        s.turnID,
			"inputTokens":   usage.PromptTokens,
			"outputTokens":  usage.CompletionTokens,
			"totalTokens":   usage.TotalTokens,
			"cachedTokens":  usage.CachedTokens,
			"contextTokens": usage.ContextTokens,
			"durationMs":    usage.DurationMS,
			"iterations":    usage.Iterations,
		}})
	}
	if errorText, ok := fields["error"].(string); ok && errorText != "" {
		kind, _ := fields["errorKind"].(string)
		s.emitRunError(errorText, kind)
	}
}

// sessionPatchEvents converts a session-level patch into the terminal events it
// implies.
func (s *runStream) sessionPatchEvents(fields map[string]any) []runEvent {
	if errorText, ok := fields["error"].(string); ok && errorText != "" {
		return []runEvent{{name: sseError, data: map[string]any{
			"code":    codeInternalError,
			"message": errorText,
		}}}
	}
	if running, ok := fields["running"].(bool); ok && !running {
		return []runEvent{{name: sseDone, data: map[string]any{
			"turnId":     s.turnID,
			"stopReason": "stop",
		}}}
	}
	return nil
}

// emitRunError emits the terminal error frame.
func (s *runStream) emitRunError(message, kind string) {
	data := map[string]any{"code": codeInternalError, "message": message}
	if kind != "" {
		data["errorKind"] = kind
	}
	s.emit(runEvent{name: sseError, data: data})
}
