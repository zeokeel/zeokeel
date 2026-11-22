package agent

// Durable tool-timeline hydration: Rig's message history records WHICH tool
// calls an assistant turn made, but not WHERE in the reply each call arrived.
// The durable tool_call events carry that byte offset (TextOffset), and the
// tool_result events carry the outcome, so restore replays them onto the read
// model. A live turn whose model calls tools before writing any prose then
// keeps rendering cards-above-text after a restart instead of falling back to
// the legacy whole-text-then-cards order.
//
// The event folding, per-call-id queueing and count-match safety rule live in
// rig's session/timeline package; this file maps its host-agnostic Placement
// onto the product read model (contracts.ToolCall, ToolCallFile, timeline-known
// and the localized error-kind fallback).
//
// Only sessions whose history actually carries tool calls load events (the
// bounded cost is limited to tool transcripts), and events from old app
// versions without text_offset leave the message's timeline unknown so it
// keeps the legacy fallback.

import (
	"context"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/session/timeline"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// hydrateToolTimeline replays sessionID's durable tool events onto the restored
// message read model in state. It runs during restore, before the state is
// published into s.sessions, so it needs no locking. Failures are logged and
// leave every message with its unknown (legacy) timeline.
func (s *service) hydrateToolTimeline(ctx context.Context, sessionID string, state *sessionState) {
	if s.store == nil || state == nil {
		return
	}
	// timeline.Apply pairs placements with []rig.Message, while the restored
	// read model is []contracts.ChatMessage (tool-result messages are not part
	// of the transcript read model). Build a same-index rig view carrying only
	// the call ids Apply needs, so every returned message/tool index maps
	// straight back onto state.messages.
	view := make([]rig.Message, len(state.messages))
	callCount := 0
	for messageIndex := range state.messages {
		tools := state.messages[messageIndex].Tools
		if len(tools) == 0 {
			continue
		}
		calls := make([]rig.ToolCall, len(tools))
		for toolIndex := range tools {
			calls[toolIndex] = rig.ToolCall{ID: tools[toolIndex].ID, Name: tools[toolIndex].Name}
			if strings.TrimSpace(tools[toolIndex].ID) != "" {
				callCount++
			}
		}
		view[messageIndex] = rig.Message{Role: rig.RoleAssistant, ToolCalls: calls}
	}
	if callCount == 0 {
		// A session without tool calls has no timeline to hydrate: skip the
		// (possibly large) event log read.
		return
	}
	placements, err := timeline.Hydrate(ctx, s.store, sessionID)
	if err != nil {
		s.logger.Printf("[agent] restore: tool timeline of %s: %v", sessionID, err)
		return
	}
	for _, hydrated := range timeline.Apply(view, placements) {
		message := &state.messages[hydrated.Message]
		message.ToolTimelineKnown = hydrated.Known
		for _, placement := range hydrated.Tools {
			applyToolPlacement(&message.Tools[placement.Tool], placement.Placement)
		}
	}
}

// applyToolPlacement copies one durable placement onto the product read model.
// A placement that was not hydrated (unknown id count, legacy event) carries
// zero fields: each field is only written when the event actually supplied it,
// so the restored defaults survive. Summary is the one always-assigned result
// field, matching the live reducer (a result event's bounded content replaces
// it; an unhydrated placement leaves the restored empty summary unchanged).
func applyToolPlacement(tool *contracts.ToolCall, placement timeline.Placement) {
	if path := strings.TrimSpace(placement.Path); path != "" {
		tool.Path = path
	}
	if len(placement.Generated) > 0 {
		tool.Generated = make([]contracts.ToolCallFile, 0, len(placement.Generated))
		for _, file := range placement.Generated {
			tool.Generated = append(tool.Generated, contracts.ToolCallFile{
				Path:    file.Path,
				Added:   file.Added,
				Deleted: file.Deleted,
			})
		}
	}
	if placement.TextOffset != nil {
		tool.TextOffset = *placement.TextOffset
	}
	if status := strings.TrimSpace(placement.Status); status != "" {
		tool.Status = status
	}
	tool.Summary = placement.Summary
	// The failure classification survives the restart: the persisted kind
	// wins; older events classify their bounded content (RG-03). The
	// host-agnostic fold does not classify, so the product applies its own
	// localized contract here.
	switch {
	case strings.TrimSpace(placement.ErrorKind) != "":
		tool.ErrorKind = strings.TrimSpace(placement.ErrorKind)
	case tool.Status == "error":
		tool.ErrorKind = contracts.ClassifyError(nil, placement.Summary)
	}
	if !placement.StartedAt.IsZero() {
		tool.StartedAt = placement.StartedAt
	}
	if !placement.EndedAt.IsZero() {
		tool.EndedAt = placement.EndedAt
	}
}
