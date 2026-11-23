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
	// runLogEvents and runLogBytes bound one finished run's replay log.
	runLogEvents = 4096
	runLogBytes  = 4 << 20
	// runRetainedRuns bounds how many finished runs stay replayable.
	runRetainedRuns = 16
	// maxRunRequestBytes bounds the POST body.
	maxRunRequestBytes = 1 << 20
)

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
// Regenerate turns the same endpoint into the "retry the last turn" mode: the
// backend rewinds the last exchange (dropping the trailing assistant reply and
// its user message) and runs the user message again. It exists as a *mode* of
// the one send path rather than as a WS method on purpose — see
// docs/TRANSPORT.md §4.6: a two-step "reset over WS, then run" protocol leaves
// a rewound-but-not-running session behind whenever the second step is missed.
// Text is not sent in this mode (the backend already has the prompt).
type runRequest struct {
	SessionID   string   `json:"sessionId"`
	Text        string   `json:"text"`
	Attachments []string `json:"attachments"`
	ClientRunID string   `json:"clientRunId"`
	Regenerate  bool     `json:"regenerate"`
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
	if body.Regenerate {
		// The backend re-runs the last user message it already holds; sending
		// text as well would silently append a second prompt.
		if strings.TrimSpace(body.Text) != "" {
			writeProtocolError(w, http.StatusBadRequest,
				errInvalidParams("text", "must be empty when regenerate is true"))
			return
		}
	} else if strings.TrimSpace(body.Text) == "" {
		writeProtocolError(w, http.StatusBadRequest, errInvalidParams("text", "is required"))
		return
	}
	if body.ClientRunID == "" {
		// Without it a retry after a dropped connection cannot be made
		// idempotent, and the SSE stream could not be aligned with the WS
		// session deltas (§4.6).
		writeProtocolError(w, http.StatusBadRequest, errInvalidParams("clientRunId", "is required"))
		return
	}
	if len(body.Attachments) > 0 {
		// Attachments travel as blob ids; the blob endpoints are not part of
		// this stage (§6.4). Refuse instead of silently dropping the files.
		// (Regenerate cannot carry attachments either: the retried turn reuses
		// the attachments the original message already had.)
		writeProtocolError(w, http.StatusBadRequest, appError(codeCapabilityUnsupported, "attachments_not_supported", false))
		return
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
func (g *Gateway) beginRun(ctx context.Context, body runRequest) (stream *runStream, replay bool, protocolErr *protocolError) {
	g.runMu.Lock()
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
	g.runMu.Unlock()

	chat, err := g.chatService()
	if err != nil {
		return nil, false, internalError(err)
	}
	snapshot, err := g.hub.snapshotSession(ctx, body.SessionID)
	if err != nil {
		return nil, false, internalError(err)
	}
	if !snapshot.found {
		return nil, false, appError(codeSessionNotFound, "session_not_found", false)
	}
	session, ok := snapshot.payload.(sessionDTO)
	if !ok {
		return nil, false, internalError(errors.New("gateway: session snapshot has an unexpected type"))
	}
	baseline := make(map[string]bool, len(session.Messages))
	for _, message := range session.Messages {
		baseline[message.ID] = true
	}
	stream = &runStream{
		clientRunID: body.ClientRunID,
		sessionID:   body.SessionID,
		turnID:      body.ClientRunID,
		baseline:    baseline,
		wake:        make(chan struct{}, 1),
	}
	// Register before starting the run so no delta is missed.
	g.runMu.Lock()
	if g.runs == nil {
		g.runs = make(map[string]*runStream)
	}
	g.runs[body.ClientRunID] = stream
	g.pruneRunsLocked()
	g.runMu.Unlock()

	stop, err := g.hub.observeSession(ctx, body.SessionID, stream.consume)
	if err != nil {
		g.dropRun(stream)
		return nil, false, internalError(err)
	}
	stream.stopObserving = stop

	// The observer is attached before the backend starts the turn: Regenerate
	// rewinds and spawns the run goroutine itself, so attaching afterwards would
	// drop the opening events (§4.6).
	if err := g.startTurn(chat, body); err != nil {
		stream.stopObserving()
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
		return nil, false, internalError(err)
	}
	return stream, false, nil
}

// startTurn starts the turn a run request describes: a normal send, or a
// regenerate of the last exchange (docs/TRANSPORT.md §4.6).
func (g *Gateway) startTurn(chat contracts.ChatService, body runRequest) error {
	if !body.Regenerate {
		return chat.Send(body.SessionID, body.Text)
	}
	regenerator, ok := chat.(contracts.SessionRegenerator)
	if !ok {
		return errRegenerateUnsupported
	}
	return regenerator.Regenerate(body.SessionID)
}

// errRegenerateUnsupported reports a backend without the regenerate half.
var errRegenerateUnsupported = errors.New("gateway: the chat backend cannot regenerate")

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
		}
	}
	ticker := time.NewTicker(runCoalesceInterval)
	defer ticker.Stop()
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
			s.emitMessageStart(*op.Message)
		case opMsgDelta:
			if s.baseline[op.MessageID] {
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
			if s.baseline[op.MessageID] || op.Tool == nil {
				continue
			}
			s.emitTool(*op.Tool)
		case opMsgPatch:
			if s.baseline[op.MessageID] {
				continue
			}
			s.emitPatch(op.Fields)
		case opSessionPatch:
			terminal = append(terminal, s.sessionPatchEvents(op.Fields)...)
		case opMsgReset:
			// Regenerate/compact rewrote the history mid-run: the SSE fast lane
			// cannot describe that, and the WS domain already carries it. Failing
			// the stream keeps the two stories from diverging.
			s.emit(runEvent{name: sseError, data: map[string]any{
				"code":    codeRevisionGone,
				"message": "session history was rewritten during the run; reload the session",
			}})
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

// emitTool emits tool.start while a call runs and tool.result when it settles.
func (s *runStream) emitTool(tool toolCallDTO) {
	if tool.Status == "running" {
		s.emit(runEvent{name: sseToolStart, data: map[string]any{
			"callId": tool.ID, "name": tool.Name, "input": tool.Arguments,
		}})
		return
	}
	data := map[string]any{
		"callId": tool.ID,
		"name":   tool.Name,
		"ok":     tool.Status != "error",
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
