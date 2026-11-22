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
// The four event types, their payload structs and the fold itself are the SDK's
// (rig session/observability); this file keeps what is product-specific on top of
// that fold:
//
//   - the contracts.ContextInspector read model (contracts.SessionContext);
//   - per-call pricing from the user's model catalog (CostUSD and the totals);
//   - the catalog context-window override;
//   - the compact_archive / context_recovery audit events, which are product
//     events the SDK fold deliberately does not know;
//   - the live in-memory timeline (bounded: last 200 calls and last 200 audit
//     events), rebuilt from the durable events at restore so a restart keeps the
//     timeline. UI modules read contracts.SessionContext and never import Rig.
//
// Each durable event is folded through observability.FoldEvents (one event at a
// time), so the live view and the restore replay share the SDK decoder and
// semantics instead of a hand-written copy.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	obs "github.com/zeokeel/zeokeel-rig/session/observability"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Durable context event types. The canonical names live in the SDK fold, so the
// product writer and the SDK reader can never drift.
const (
	contextSnapshotEvent = obs.EventContextSnapshot
	modelCallEvent       = obs.EventModelCall
	compactEvent         = obs.EventCompact
	spillEvent           = obs.EventSpill
)

const (
	// contextTimelineRequests bounds the folded request list per session; it
	// mirrors the SDK fold's MaxCalls so the live overlay cannot grow past the
	// durable replay bound.
	contextTimelineRequests = obs.MaxCalls
	// contextTimelineEvents bounds the folded audit event list per session. The
	// SDK bound applies per audit list; the product keeps one combined list of
	// compact/archive/spill/recovery entries.
	contextTimelineEvents = obs.MaxEvents
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

// The durable payload structs are the SDK's: one wire definition for the writer
// and the reader, and payloads written before this migration (Go field names as
// JSON keys, e.g. "Name" instead of "name") still decode through the fold.
type (
	modelCallPayload       = obs.ModelCallPayload
	contextSnapshotPayload = obs.ContextSnapshotPayload
	compactPayload         = obs.CompactPayload
	spillPayload           = obs.SpillPayload
)

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
		Sections:           contextSectionsToPayload(snapshot.Sections),
		Tools:              contextToolsToPayload(snapshot.Tools),
		ToolCount:          len(snapshot.Tools),
		History: obs.History{
			Messages:  snapshot.History.Messages,
			User:      snapshot.History.User,
			Assistant: snapshot.History.Assistant,
			Tool:      snapshot.History.Tool,
		},
		Injected: obs.Injected{
			AgentsMD: snapshot.Injected.AgentsMD,
			Preset:   snapshot.Injected.Preset,
			Memory:   snapshot.Injected.Memory,
			Skills:   snapshot.Injected.Skills,
		},
		EstimatedPrompt: snapshot.EstimatedPrompt,
	}
	s.appendObservedEvent(ctx, sessionID, contextSnapshotEvent, payload)
}

// contextSectionsToPayload converts the contracts section model to the SDK
// payload model used by the fold.
func contextSectionsToPayload(sections []contracts.ContextSection) []obs.Section {
	if len(sections) == 0 {
		return nil
	}
	converted := make([]obs.Section, len(sections))
	for index, section := range sections {
		converted[index] = obs.Section{
			ID:     section.ID,
			Label:  section.Label,
			Source: section.Source,
			Bytes:  section.Bytes,
			Tokens: section.Tokens,
			Hash:   section.Hash,
		}
	}
	return converted
}

// contextSectionsFromPayload converts a folded section list back to contracts.
func contextSectionsFromPayload(sections []obs.Section) []contracts.ContextSection {
	if len(sections) == 0 {
		return nil
	}
	converted := make([]contracts.ContextSection, len(sections))
	for index, section := range sections {
		converted[index] = contracts.ContextSection{
			ID:     section.ID,
			Label:  section.Label,
			Source: section.Source,
			Bytes:  section.Bytes,
			Tokens: section.Tokens,
			Hash:   section.Hash,
		}
	}
	return converted
}

// contextToolsToPayload converts the contracts tool catalog to the SDK payload.
func contextToolsToPayload(tools []contracts.ContextTool) []obs.Tool {
	if len(tools) == 0 {
		return nil
	}
	converted := make([]obs.Tool, len(tools))
	for index, tool := range tools {
		converted[index] = obs.Tool{Name: tool.Name, Group: tool.Group, Tokens: tool.Tokens}
	}
	return converted
}

// contextToolsFromPayload converts a folded tool catalog back to contracts.
func contextToolsFromPayload(tools []obs.Tool) []contracts.ContextTool {
	if len(tools) == 0 {
		return nil
	}
	converted := make([]contracts.ContextTool, len(tools))
	for index, tool := range tools {
		converted[index] = contracts.ContextTool{Name: tool.Name, Group: tool.Group, Tokens: tool.Tokens}
	}
	return converted
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
		spill.Preview = truncateRunes(spill.Preview, obs.MaxPreviewRunes)
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
//
// The four context event types are decoded and folded by the SDK
// (observability.FoldEvents); this function maps the folded values onto the
// contracts read model and adds catalog pricing. compact_archive and
// context_recovery are product audit events the SDK fold does not know, so they
// stay folded here.
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
	now := at
	if now.IsZero() {
		now = time.Now()
	}
	if obs.ObservedEvent(eventType) {
		// Fold exactly this event through the SDK read model. A malformed payload
		// is skipped there (Revision stays 0), mirroring the old hand-written
		// decoder: the timeline does not change and no revision is published.
		folded := obs.FoldEvents([]rig.SessionEvent{{
			SessionID: sessionID,
			Type:      eventType,
			Time:      now,
			Seq:       seq,
			Payload:   payload,
		}}, sessionID)
		if folded.Revision == 0 {
			return
		}
		s.applyFoldedContextEvent(timeline, folded)
		timeline.revision++
		trimContextAuditEvents(timeline)
		return
	}
	var folded bool
	switch eventType {
	case compactArchiveEventType:
		folded = foldCompactArchiveEvent(timeline, payload, now, seq)
	case contextRecoveryEventType:
		folded = foldContextRecoveryEvent(timeline, payload, now, seq)
	default:
		return
	}
	if !folded {
		return
	}
	timeline.revision++
	trimContextAuditEvents(timeline)
}

// applyFoldedContextEvent maps one SDK-folded event onto the contracts read model.
// applyContextEvent folds exactly one event per call, so at most one of the
// snapshot/call/compaction/spill entries is new.
func (s *service) applyFoldedContextEvent(timeline *contextTimeline, folded obs.Timeline) {
	if folded.HasSnapshot {
		snapshot := folded.Snapshot
		timeline.snapshot = contracts.ContextSnapshot{
			At:                 snapshot.At,
			Seq:                snapshot.Seq,
			Model:              snapshot.Model,
			Window:             snapshot.Window,
			SystemPromptBytes:  snapshot.SystemPromptBytes,
			SystemPromptTokens: snapshot.SystemPromptTokens,
			Sections:           contextSectionsFromPayload(snapshot.Sections),
			Tools:              contextToolsFromPayload(snapshot.Tools),
			History: contracts.ContextHistory{
				Messages:  snapshot.History.Messages,
				User:      snapshot.History.User,
				Assistant: snapshot.History.Assistant,
				Tool:      snapshot.History.Tool,
			},
			Injected: contracts.ContextInjected{
				AgentsMD: snapshot.Injected.AgentsMD,
				Preset:   snapshot.Injected.Preset,
				Memory:   snapshot.Injected.Memory,
				Skills:   snapshot.Injected.Skills,
			},
			EstimatedPrompt: snapshot.EstimatedPrompt,
		}
		if snapshot.Window > 0 {
			timeline.window = snapshot.Window
		}
		if len(timeline.requests) == 0 && snapshot.EstimatedPrompt > 0 {
			timeline.used = snapshot.EstimatedPrompt
		}
		return
	}
	if len(folded.Calls) > 0 {
		call := folded.Calls[0]
		request := contracts.ContextRequest{
			Seq:           call.Seq,
			Iteration:     call.Iteration,
			At:            call.At,
			Model:         call.Model,
			Prompt:        call.Prompt,
			Completion:    call.Completion,
			Cached:        call.Cached,
			Total:         call.Total,
			ContextTokens: call.ContextTokens,
		}
		// The SDK fold intentionally has no model catalog: pricing and the
		// catalog context window stay product concerns.
		info := s.modelCatalogInfo(request.Model)
		if info.ContextWindow > 0 {
			timeline.window = info.ContextWindow
		}
		if cost, ok := info.Cost(call.Prompt, call.Cached, call.Completion); ok {
			request.CostUSD = cost
			request.CostKnown = true
			timeline.totalUSD += cost
			timeline.costSeen = true
		}
		timeline.requests = append(timeline.requests, request)
		if len(timeline.requests) > contextTimelineRequests {
			timeline.requests = append([]contracts.ContextRequest(nil), timeline.requests[len(timeline.requests)-contextTimelineRequests:]...)
		}
		if call.ContextTokens > 0 {
			timeline.used = call.ContextTokens
		}
		return
	}
	if len(folded.Compactions) > 0 {
		compaction := folded.Compactions[0]
		timeline.events = append(timeline.events, contracts.ContextEvent{
			Seq:      compaction.Seq,
			Kind:     contracts.ContextEventCompact,
			At:       compaction.At,
			Detail:   compaction.Kind,
			Before:   compaction.Before,
			After:    compaction.After,
			Replaced: compaction.Replaced,
			Saved:    compaction.Saved,
			Inferred: compaction.Inferred,
			// The fold is used live and by restore replay; keep the automatic
			// compaction outcome (failed/no-reduction/archive-failed/...) in the
			// read model, not just in the durable payload.
			Outcome: compaction.Outcome,
		})
		if compaction.After > 0 {
			timeline.used = compaction.After
		}
		return
	}
	if len(folded.Spills) > 0 {
		spill := folded.Spills[0]
		timeline.events = append(timeline.events, contracts.ContextEvent{
			Seq:    spill.Seq,
			Kind:   contracts.ContextEventSpill,
			At:     spill.At,
			Detail: spill.Tool,
			Ref:    spill.Ref,
			Path:   spill.Path,
			Bytes:  spill.Bytes,
		})
	}
}

// foldCompactArchiveEvent folds the product's compact_archive audit event (raw
// replaced history metadata); the SDK fold does not know this event type.
func foldCompactArchiveEvent(timeline *contextTimeline, payload []byte, at time.Time, seq int64) bool {
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
		return false
	}
	detail := fmt.Sprintf("%s archive: %d messages archived", metadata.Kind, metadata.RetainedMessages)
	if metadata.Truncated {
		detail += fmt.Sprintf(", %d omitted (truncated)", metadata.OmittedMessages)
	}
	timeline.events = append(timeline.events, contracts.ContextEvent{
		Seq:              seq,
		Kind:             contracts.ContextEventArchive,
		At:               at,
		Detail:           detail,
		Before:           metadata.BeforeTokens,
		After:            metadata.AfterTokens,
		Replaced:         metadata.MessageCount,
		Saved:            metadata.RetainedMessages,
		ArchivedMessages: metadata.RetainedMessages,
		ArchivedBytes:    metadata.Bytes,
		Truncated:        metadata.Truncated,
	})
	return true
}

// foldContextRecoveryEvent folds the product's context_recovery audit event;
// the SDK fold does not know this event type.
func foldContextRecoveryEvent(timeline *contextTimeline, payload []byte, at time.Time, seq int64) bool {
	var decoded compactPayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return false
	}
	timeline.events = append(timeline.events, contracts.ContextEvent{
		Seq:    seq,
		Kind:   contracts.ContextEventRecovery,
		At:     at,
		Detail: decoded.Kind,
		Before: decoded.Before,
		After:  decoded.After,
	})
	if decoded.After > 0 {
		timeline.used = decoded.After
	}
	return true
}

// trimContextAuditEvents bounds the combined audit list (compact + archive +
// spill + recovery), mirroring the SDK fold's per-list audit bound.
func trimContextAuditEvents(timeline *contextTimeline) {
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
		if !obs.ObservedEvent(event.Type) &&
			event.Type != compactArchiveEventType && event.Type != contextRecoveryEventType {
			continue
		}
		eventTime := event.Time
		if eventTime.IsZero() {
			eventTime = time.Now()
		}
		s.applyContextEvent(sessionID, event.Type, event.Payload, eventTime, event.Seq)
	}
}

// parseSpillSummary extracts the stable structured fields Rig renders into a
// spilled tool result:
//
//	[tool result spilled to file]
//	path: <path>      (file backend)  or  ref: <handle>  (store backend)
//	bytes: <n>
//	preview: <one line>
//
// The parsing itself is the SDK's (observability.ParseSpillSummary); this wrapper
// keeps the product payload type the durable recorder expects.
func parseSpillSummary(content string) (spillPayload, bool) {
	parsed, ok := obs.ParseSpillSummary(content)
	if !ok {
		return spillPayload{}, false
	}
	return spillPayload{
		Path:    parsed.Path,
		Ref:     parsed.Ref,
		Bytes:   parsed.Bytes,
		Preview: parsed.Preview,
	}, true
}

// autoCompactDropThreshold reports whether the prompt-token change between two
// consecutive model calls of one run looks like an automatic compaction: a large
// context shrank by at least the SDK's drop threshold. The SDK owns the heuristic
// (observability.AutoCompactDrop) because automatic compaction inside Rig is not
// directly observable; the product only records the inferred event.
func autoCompactDropThreshold(previous, current int) bool {
	return obs.AutoCompactDrop(previous, current)
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
