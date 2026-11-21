package agent

// Context observability data foundation (DSH dsh-context P0): the agent backend
// records durable session events for the context timeline and exposes a folded
// view through contracts.ContextInspector (contracts.KeyContext).
//
// Durable events (no schema migration; the payload is JSON):
//
//	context_snapshot  one per run, before the first provider call: model/window,
//	                  system-prompt size, tool catalog, history counts, which
//	                  optional injections were present.
//	model_call        one per provider call (Rig EventUsage): iteration/model/
//	                  prompt/cached/completion/total/contextTokens.
//	compact           manual /compact result (before/after/replaced/saved); an
//	                  automatic compaction inside Rig is not directly observable
//	                  and is recorded only as an INFERRED heuristic when the
//	                  prompt-token count drops sharply between two calls.
//	spill             a tool result replaced by Rig's on-disk spill summary
//	                  (path/bytes/preview parsed from the stable summary format).
//
// The folded view lives in memory per session (bounded: last 200 calls and last
// 200 audit events) and is rebuilt from the durable events at restore, so a
// restart keeps the timeline. UI modules read contracts.SessionContext and never
// import Rig.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Durable context event types.
const (
	contextSnapshotEvent = "context_snapshot"
	modelCallEvent       = "model_call"
	compactEvent         = "compact"
	spillEvent           = "spill"
)

const (
	// contextTimelineRequests bounds the folded request list per session.
	contextTimelineRequests = 200
	// contextTimelineEvents bounds the folded audit event list per session.
	contextTimelineEvents = 200
	// autoCompactInferenceMinTokens is the smallest context that can trigger the
	// inferred-auto-compaction heuristic (a drop on a tiny context is noise).
	autoCompactInferenceMinTokens = 4096
	// autoCompactInferenceDropPercent is the prompt-token drop (in percent of the
	// previous count) that marks an inferred automatic compaction.
	autoCompactInferenceDropPercent = 20
	// spillPreviewRunes bounds the stored spill preview.
	spillPreviewRunes = 240
)

// contextTimeline is the in-memory folded context view of one session.
type contextTimeline struct {
	revision uint64
	snapshot contracts.ContextSnapshot
	requests []contracts.ContextRequest
	events   []contracts.ContextEvent
	used     int
	window   int
	totalUSD float64
	costSeen bool
}

// modelCallPayload is the durable model_call event payload.
type modelCallPayload struct {
	Iteration        int    `json:"iteration"`
	Model            string `json:"model"`
	PromptTokens     int    `json:"promptTokens"`
	CachedTokens     int    `json:"cachedTokens,omitempty"`
	CompletionTokens int    `json:"completionTokens"`
	TotalTokens      int    `json:"totalTokens"`
	ContextTokens    int    `json:"contextTokens"`
}

// contextSnapshotPayload is the durable context_snapshot event payload.
type contextSnapshotPayload struct {
	Model              string                     `json:"model"`
	Window             int                        `json:"window"`
	SystemPromptBytes  int                        `json:"systemPromptBytes"`
	SystemPromptTokens int                        `json:"systemPromptTokens"`
	Sections           []contracts.ContextSection `json:"sections,omitempty"`
	Tools              []contextToolPayload       `json:"tools,omitempty"`
	ToolCount          int                        `json:"toolCount"`
	History            contracts.ContextHistory   `json:"history"`
	Injected           contracts.ContextInjected  `json:"injected"`
	EstimatedPrompt    int                        `json:"estimatedPrompt"`
}

// contextToolPayload is one tool in the snapshot payload.
type contextToolPayload struct {
	Name   string `json:"name"`
	Group  string `json:"group,omitempty"`
	Tokens int    `json:"tokens,omitempty"`
}

// compactPayload is the durable compact event payload.
type compactPayload struct {
	Kind     string `json:"kind"`
	Before   int    `json:"before"`
	After    int    `json:"after"`
	Replaced int    `json:"replaced"`
	Saved    int    `json:"saved"`
	Inferred bool   `json:"inferred,omitempty"`
	// Outcome is set when a product-side automatic compaction attempt failed to
	// produce a usable reduction ("failed"/"no-reduction"/"archive-failed");
	// empty means it succeeded (or was manual/inferred).
	Outcome string `json:"outcome,omitempty"`
}

// spillPayload is the durable spill event payload.
type spillPayload struct {
	CallID string `json:"callId"`
	Tool   string `json:"tool"`
	// Path is the file-backed summary's location (empty with the store backend).
	Path string `json:"path,omitempty"`
	// Ref is the store handle from the summary's "ref:" line (empty with the
	// file backend).
	Ref     string `json:"ref,omitempty"`
	Bytes   int    `json:"bytes"`
	Preview string `json:"preview,omitempty"`
}

var _ contracts.ContextInspector = (*service)(nil)

// SessionContext implements contracts.ContextInspector: the folded context view
// of one session. ok is false when the session has no recorded context data.
func (s *service) SessionContext(sessionID string) (contracts.SessionContext, bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return contracts.SessionContext{}, false
	}
	s.contextMu.Lock()
	defer s.contextMu.Unlock()
	timeline, ok := s.contextTimelines[sessionID]
	if !ok || timeline == nil {
		return contracts.SessionContext{}, false
	}
	folded := contracts.SessionContext{
		Revision:     timeline.revision,
		Snapshot:     cloneContextSnapshot(timeline.snapshot),
		Requests:     append([]contracts.ContextRequest(nil), timeline.requests...),
		Events:       append([]contracts.ContextEvent(nil), timeline.events...),
		Used:         timeline.used,
		Window:       timeline.window,
		TotalCostUSD: timeline.totalUSD,
		CostKnown:    timeline.costSeen,
	}
	return folded, true
}

// recordContextSnapshot appends a context_snapshot event and folds it.
func (s *service) recordContextSnapshot(ctx context.Context, sessionID string, snapshot contracts.ContextSnapshot) {
	payload := contextSnapshotPayload{
		Model:              snapshot.Model,
		Window:             snapshot.Window,
		SystemPromptBytes:  snapshot.SystemPromptBytes,
		SystemPromptTokens: snapshot.SystemPromptTokens,
		Sections:           snapshot.Sections,
		ToolCount:          len(snapshot.Tools),
		History:            snapshot.History,
		Injected:           snapshot.Injected,
		EstimatedPrompt:    snapshot.EstimatedPrompt,
	}
	for _, tool := range snapshot.Tools {
		payload.Tools = append(payload.Tools, contextToolPayload{Name: tool.Name, Group: tool.Group, Tokens: tool.Tokens})
	}
	s.appendObservedEvent(ctx, sessionID, contextSnapshotEvent, payload)
}

// recordModelCall appends one model_call event and folds it into the timeline.
func (s *service) recordModelCall(ctx context.Context, sessionID string, request contracts.ContextRequest) {
	s.appendObservedEvent(ctx, sessionID, modelCallEvent, modelCallPayload{
		Iteration:        request.Iteration,
		Model:            request.Model,
		PromptTokens:     request.Prompt,
		CachedTokens:     request.Cached,
		CompletionTokens: request.Completion,
		TotalTokens:      request.Total,
		ContextTokens:    request.ContextTokens,
	})
}

// recordCompactEvent appends one compact event and folds it.
func (s *service) recordCompactEvent(ctx context.Context, sessionID, kind string, before, after, replaced, saved int, inferred bool, outcome string) {
	s.appendObservedEvent(ctx, sessionID, compactEvent, compactPayload{
		Kind: kind, Before: before, After: after, Replaced: replaced, Saved: saved,
		Inferred: inferred, Outcome: outcome,
	})
}

// recordSpillEvent appends one spill event and folds it.
func (s *service) recordSpillEvent(ctx context.Context, sessionID string, spill spillPayload) {
	if spill.Preview != "" {
		spill.Preview = truncateRunes(spill.Preview, spillPreviewRunes)
	}
	s.appendObservedEvent(ctx, sessionID, spillEvent, spill)
}

// appendObservedEvent writes one observability event and folds the payload into
// the in-memory timeline. Store failures are logged and never break a run.
func (s *service) appendObservedEvent(ctx context.Context, sessionID, eventType string, payload any) {
	if s.store == nil || s.sessionDeleted(sessionID) {
		return
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		s.logger.Printf("[agent] marshal %s event: %v", eventType, err)
		return
	}
	if err := s.store.AppendEvent(ctx, rig.SessionEvent{
		SessionID: sessionID,
		Type:      eventType,
		Time:      time.Now(),
		Payload:   encoded,
	}); err != nil {
		s.logger.Printf("[agent] append %s event: %v", eventType, err)
		return
	}
	s.applyContextEvent(sessionID, eventType, encoded, time.Now(), 0)
}

// applyContextEvent folds one durable event into the session timeline. It is
// used live (right after the append) and by restore (replaying the durable log),
// so the folded view is identical after a restart. seq is the durable sequence of
// the event: the read model carries it on the folded snapshot/request/event so a
// UI can address backend reads by it (the compaction archive read-back asks for
// the archive event by this sequence). Live appends whose sequence is not needed
// pass 0.
func (s *service) applyContextEvent(sessionID, eventType string, payload []byte, at time.Time, seq int64) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || len(payload) == 0 {
		return
	}
	s.contextMu.Lock()
	defer s.contextMu.Unlock()
	if s.contextTimelines == nil {
		s.contextTimelines = make(map[string]*contextTimeline)
	}
	timeline := s.contextTimelines[sessionID]
	if timeline == nil {
		timeline = &contextTimeline{}
		s.contextTimelines[sessionID] = timeline
	}
	timeline.revision++
	now := at
	if now.IsZero() {
		now = time.Now()
	}
	switch eventType {
	case contextSnapshotEvent:
		var decoded contextSnapshotPayload
		if err := json.Unmarshal(payload, &decoded); err != nil {
			timeline.revision--
			return
		}
		timeline.snapshot = contracts.ContextSnapshot{
			At:                 now,
			Seq:                seq,
			Model:              strings.TrimSpace(decoded.Model),
			Window:             decoded.Window,
			SystemPromptBytes:  decoded.SystemPromptBytes,
			SystemPromptTokens: decoded.SystemPromptTokens,
			Sections:           append([]contracts.ContextSection(nil), decoded.Sections...),
			History:            decoded.History,
			Injected:           decoded.Injected,
			EstimatedPrompt:    decoded.EstimatedPrompt,
		}
		for _, tool := range decoded.Tools {
			timeline.snapshot.Tools = append(timeline.snapshot.Tools, contracts.ContextTool{
				Name: tool.Name, Group: tool.Group, Tokens: tool.Tokens,
			})
		}
		if decoded.Window > 0 {
			timeline.window = decoded.Window
		}
		if len(timeline.requests) == 0 && decoded.EstimatedPrompt > 0 {
			timeline.used = decoded.EstimatedPrompt
		}
	case modelCallEvent:
		var decoded modelCallPayload
		if err := json.Unmarshal(payload, &decoded); err != nil {
			timeline.revision--
			return
		}
		request := contracts.ContextRequest{
			Seq:           seq,
			Iteration:     decoded.Iteration,
			At:            now,
			Model:         strings.TrimSpace(decoded.Model),
			Prompt:        decoded.PromptTokens,
			Completion:    decoded.CompletionTokens,
			Cached:        decoded.CachedTokens,
			Total:         decoded.TotalTokens,
			ContextTokens: decoded.ContextTokens,
		}
		info := s.modelCatalogInfo(request.Model)
		if info.ContextWindow > 0 {
			timeline.window = info.ContextWindow
		}
		if cost, ok := info.Cost(decoded.PromptTokens, decoded.CachedTokens, decoded.CompletionTokens); ok {
			request.CostUSD = cost
			request.CostKnown = true
			timeline.totalUSD += cost
			timeline.costSeen = true
		}
		timeline.requests = append(timeline.requests, request)
		if len(timeline.requests) > contextTimelineRequests {
			timeline.requests = append([]contracts.ContextRequest(nil), timeline.requests[len(timeline.requests)-contextTimelineRequests:]...)
		}
		if decoded.ContextTokens > 0 {
			timeline.used = decoded.ContextTokens
		}
	case compactArchiveEventType:
		var metadata struct {
			Kind             string `json:"kind"`
			MessageCount     int    `json:"messageCount"`
			RetainedMessages int    `json:"retainedMessages"`
			Bytes            int    `json:"bytes"`
			Truncated        bool   `json:"truncated"`
			OmittedMessages  int    `json:"omittedMessages"`
			BeforeTokens     int    `json:"beforeTokens"`
			AfterTokens      int    `json:"afterTokens"`
		}
		if err := json.Unmarshal(payload, &metadata); err != nil {
			timeline.revision--
			return
		}
		detail := fmt.Sprintf("%s archive: %d messages archived", metadata.Kind, metadata.RetainedMessages)
		if metadata.Truncated {
			detail += fmt.Sprintf(", %d omitted (truncated)", metadata.OmittedMessages)
		}
		timeline.events = append(timeline.events, contracts.ContextEvent{
			Seq:              seq,
			Kind:             contracts.ContextEventArchive,
			At:               now,
			Detail:           detail,
			Before:           metadata.BeforeTokens,
			After:            metadata.AfterTokens,
			Replaced:         metadata.MessageCount,
			Saved:            metadata.RetainedMessages,
			ArchivedMessages: metadata.RetainedMessages,
			ArchivedBytes:    metadata.Bytes,
			Truncated:        metadata.Truncated,
		})
	case compactEvent:
		var decoded compactPayload
		if err := json.Unmarshal(payload, &decoded); err != nil {
			timeline.revision--
			return
		}
		detail := strings.TrimSpace(decoded.Kind)
		if detail == "" {
			detail = "manual"
		}
		timeline.events = append(timeline.events, contracts.ContextEvent{
			Seq:      seq,
			Kind:     contracts.ContextEventCompact,
			At:       now,
			Detail:   detail,
			Before:   decoded.Before,
			After:    decoded.After,
			Replaced: decoded.Replaced,
			Saved:    decoded.Saved,
			Inferred: decoded.Inferred,
			// The fold is used live and by restore replay; keep the automatic
			// compaction outcome (failed/no-reduction/archive-failed/...) in the
			// read model, not just in the durable payload.
			Outcome: decoded.Outcome,
		})
		if decoded.After > 0 {
			timeline.used = decoded.After
		}
	case contextRecoveryEventType:
		var decoded compactPayload
		if err := json.Unmarshal(payload, &decoded); err != nil {
			timeline.revision--
			return
		}
		timeline.events = append(timeline.events, contracts.ContextEvent{
			Seq:    seq,
			Kind:   contracts.ContextEventRecovery,
			At:     now,
			Detail: decoded.Kind,
			Before: decoded.Before,
			After:  decoded.After,
		})
		if decoded.After > 0 {
			timeline.used = decoded.After
		}
	case spillEvent:
		var decoded spillPayload
		if err := json.Unmarshal(payload, &decoded); err != nil {
			timeline.revision--
			return
		}
		timeline.events = append(timeline.events, contracts.ContextEvent{
			Seq:    seq,
			Kind:   contracts.ContextEventSpill,
			At:     now,
			Detail: strings.TrimSpace(decoded.Tool),
			Ref:    decoded.Ref,
			Path:   decoded.Path,
			Bytes:  decoded.Bytes,
		})
	default:
		timeline.revision--
		return
	}
	if len(timeline.events) > contextTimelineEvents {
		timeline.events = append([]contracts.ContextEvent(nil), timeline.events[len(timeline.events)-contextTimelineEvents:]...)
	}
}

// contextSnapshotForRun builds the request-level snapshot of one run: the final
// system prompt size, the exact tool catalog of the run, the history counts and
// which optional injections are present in the rendered prompt.
func (s *service) contextSnapshotForRun(sessionID string, cfg contracts.ChatConfig, tools []rig.Tool,
	sections []contracts.PromptSection) contracts.ContextSnapshot {
	snapshot := contracts.ContextSnapshot{
		At:                 time.Now(),
		Model:              strings.TrimSpace(cfg.Model),
		SystemPromptBytes:  len(cfg.SystemPrompt),
		SystemPromptTokens: rig.EstimateTextTokens(cfg.SystemPrompt),
	}
	for _, section := range sections {
		if strings.TrimSpace(section.Text) == "" {
			continue
		}
		snapshot.Sections = append(snapshot.Sections, contracts.ContextSection{
			ID:     section.ID,
			Label:  section.ID,
			Source: section.Source,
			Bytes:  section.Bytes,
			Tokens: section.Tokens,
			Hash:   section.Hash,
		})
	}
	info := s.modelCatalogInfo(cfg.Model)
	snapshot.Window = info.ContextWindow
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		name := strings.TrimSpace(tool.Name())
		if name == "" {
			continue
		}
		entry := contracts.ContextTool{Name: name, Group: contextToolGroup(name)}
		estimate := tool.Name() + tool.Description()
		if schema, err := json.Marshal(tool.Schema()); err == nil {
			estimate += string(schema)
		}
		entry.Tokens = rig.EstimateTextTokens(estimate)
		snapshot.Tools = append(snapshot.Tools, entry)
	}
	history, historyTokens := s.historyBreakdown(sessionID)
	snapshot.History = history
	snapshot.EstimatedPrompt = snapshot.SystemPromptTokens + historyTokens
	for _, tool := range snapshot.Tools {
		snapshot.EstimatedPrompt += tool.Tokens
	}
	snapshot.Injected = s.promptInjectionSources(sessionID, cfg.SystemPrompt)
	return snapshot
}

// historyBreakdown returns the read-model message counts of a session and the
// estimated token size of its history (bounded by the read model, no history
// load: the snapshot runs before every provider call).
func (s *service) historyBreakdown(sessionID string) (contracts.ContextHistory, int) {
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return contracts.ContextHistory{}, 0
	}
	breakdown := contracts.ContextHistory{Messages: len(state.messages)}
	var builder strings.Builder
	for index := range state.messages {
		message := &state.messages[index]
		switch message.Role {
		case contracts.ChatRoleUser:
			breakdown.User++
		case contracts.ChatRoleAssistant:
			breakdown.Assistant++
		}
		breakdown.Tool += len(message.Tools)
		builder.WriteString(message.Text)
		builder.WriteString(message.Reasoning)
	}
	s.mu.Unlock()
	return breakdown, rig.EstimateTextTokens(builder.String())
}

// promptInjectionSources reports which optional pieces were injected into the
// final rendered system prompt. The markers are the stable headings the prompt
// builders use; preset presence additionally checks the selected preset.
func (s *service) promptInjectionSources(sessionID, systemPrompt string) contracts.ContextInjected {
	injected := contracts.ContextInjected{
		AgentsMD: strings.Contains(systemPrompt, workspaceInstructionsHeading),
		Memory:   strings.Contains(systemPrompt, "## Memory"),
		Skills:   strings.Contains(systemPrompt, "## Skills"),
	}
	if preset, ok := s.agentPresetForSession(sessionID); ok {
		injected.Preset = strings.TrimSpace(preset.Persona) != "" || strings.TrimSpace(preset.Prompt) != ""
	}
	return injected
}

// contextToolGroup classifies a tool name into the coarse family the context UI
// groups by. It deliberately stays in the backend (the UI never inspects Rig
// tool types).
func contextToolGroup(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case name == "bash", name == "pwsh", strings.HasPrefix(name, "terminal_"), strings.HasPrefix(name, "job_"):
		return "shell"
	case name == "read_file", name == "write_file", name == "edit_file",
		name == "str_replace_editor", name == "list_dir", name == "glob", name == "grep":
		return "file"
	case strings.HasPrefix(name, "web_"), strings.HasPrefix(name, "mcp__"):
		return "network"
	case name == "subagent", name == "subagent_fork", name == "workflow", name == "ralph",
		strings.HasPrefix(name, "team_"), name == "spawn_teammate", name == "list_agents":
		return "orchestration"
	case strings.HasPrefix(name, "session_"), name == "memory", strings.HasPrefix(name, "memory"),
		name == "skill", name == "skills", name == "todo", name == "todo_write":
		return "context"
	case name == "get_goal", name == "create_goal", name == "update_goal", name == "goal",
		name == "ask_user", name == "present", name == "plan_review", name == "exit_plan_mode":
		return "interaction"
	default:
		return "other"
	}
}

// cloneContextSnapshot deep-copies the slices of one snapshot.
func cloneContextSnapshot(snapshot contracts.ContextSnapshot) contracts.ContextSnapshot {
	snapshot.Sections = append([]contracts.ContextSection(nil), snapshot.Sections...)
	snapshot.Tools = append([]contracts.ContextTool(nil), snapshot.Tools...)
	return snapshot
}

// clearContextTimeline drops one session's folded context view (DeleteSession:
// the durable events are deleted with the session row).
func (s *service) clearContextTimeline(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.contextMu.Lock()
	delete(s.contextTimelines, sessionID)
	s.contextMu.Unlock()
}

// foldRestoredContextEvents replays the durable observability events of one
// session during restore (oldest first) so the timeline survives a restart.
func (s *service) foldRestoredContextEvents(sessionID string, events []rig.SessionEvent) {
	for _, event := range events {
		switch event.Type {
		case contextSnapshotEvent, modelCallEvent, compactEvent, compactArchiveEventType,
			spillEvent, contextRecoveryEventType:
			eventTime := event.Time
			if eventTime.IsZero() {
				eventTime = time.Now()
			}
			s.applyContextEvent(sessionID, event.Type, event.Payload, eventTime, event.Seq)
		}
	}
}

// parseSpillSummary extracts the stable structured fields Rig renders into a
// spilled tool result:
//
//	[tool result spilled to file]
//	path: <path>      (file backend)  or  ref: <handle>  (store backend)
//	bytes: <n>
//	preview: <one line>
func parseSpillSummary(content string) (spillPayload, bool) {
	if !rig.IsSpilledToolResult(content) {
		return spillPayload{}, false
	}
	var parsed spillPayload
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "path:"):
			parsed.Path = strings.TrimSpace(strings.TrimPrefix(line, "path:"))
		case strings.HasPrefix(line, "ref:"):
			parsed.Ref = strings.TrimSpace(strings.TrimPrefix(line, "ref:"))
		case strings.HasPrefix(line, "bytes:"):
			if value, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "bytes:"))); err == nil {
				parsed.Bytes = value
			}
		case strings.HasPrefix(line, "preview:"):
			parsed.Preview = strings.TrimSpace(strings.TrimPrefix(line, "preview:"))
		}
	}
	if parsed.Path == "" && parsed.Ref == "" && parsed.Bytes == 0 {
		return spillPayload{}, false
	}
	return parsed, true
}

// autoCompactDropThreshold reports whether the prompt-token change between two
// consecutive model calls of one run looks like an automatic compaction: a large
// context shrank by at least autoCompactInferenceDropPercent. This is the only
// observable signal (Rig performs automatic compaction internally), so the
// recorded event is marked inferred.
func autoCompactDropThreshold(previous, current int) bool {
	if previous < autoCompactInferenceMinTokens || current <= 0 || current >= previous {
		return false
	}
	return (previous-current)*100 >= previous*autoCompactInferenceDropPercent
}

// contextRequestFromUsage builds the folded request record of one usage event.
func contextRequestFromUsage(iteration int, model string, usage *rig.Usage) contracts.ContextRequest {
	if usage == nil {
		return contracts.ContextRequest{}
	}
	return contracts.ContextRequest{
		Iteration:     iteration,
		Model:         strings.TrimSpace(model),
		Prompt:        usage.PromptTokens,
		Completion:    usage.CompletionTokens,
		Cached:        usage.CachedTokens,
		Total:         usage.TotalTokens,
		ContextTokens: usage.PromptTokens,
	}
}
