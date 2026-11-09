package agent

// Durable tool-timeline hydration: Rig's message history records WHICH tool
// calls an assistant turn made, but not WHERE in the reply each call arrived.
// The durable tool_call events carry that byte offset (TextOffset), and the
// tool_result events carry the outcome, so restore replays them onto the read
// model. A live turn whose model calls tools before writing any prose then
// keeps rendering cards-above-text after a restart instead of falling back to
// the legacy whole-text-then-cards order.
//
// Only sessions whose history actually carries tool calls load events (the
// bounded cost is limited to tool transcripts), and events from old app
// versions without text_offset leave the message's timeline unknown so it
// keeps the legacy fallback.

import (
	"context"
	"encoding/json"
	"strings"
)

// restoredToolRef locates one restored tool call in the message read model.
type restoredToolRef struct {
	message int
	tool    int
}

// toolCallEvent is the payload of a durable "tool_call" session event.
type toolCallEvent struct {
	ID string `json:"id"`
	// TextOffset is the assistant text length when the call arrived. It is a
	// pointer: old events have no such field and must not be mistaken for a
	// recorded offset of 0.
	TextOffset *int `json:"text_offset"`
}

// toolResultEvent is the payload of a durable "tool_result" session event.
type toolResultEvent struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Content string `json:"content"`
}

// hydrateToolTimeline replays sessionID's durable tool events onto the restored
// message read model in state. It runs during restore, before the state is
// published into s.sessions, so it needs no locking. Failures are logged and
// leave every message with its unknown (legacy) timeline.
func (s *service) hydrateToolTimeline(ctx context.Context, sessionID string, state *sessionState) {
	if s.store == nil || state == nil {
		return
	}
	// Restored tool calls in chronological order. Some providers number their
	// calls per turn ("call_0", "call_1"), so the same id can repeat across
	// turns: each id keeps a queue of the restored rows it can hydrate and the
	// events are consumed in sequence order, which keeps every event with its
	// own turn instead of overwriting an earlier call.
	refs := make([]restoredToolRef, 0)
	queues := make(map[string][]int)
	for messageIndex := range state.messages {
		for toolIndex := range state.messages[messageIndex].Tools {
			callID := strings.TrimSpace(state.messages[messageIndex].Tools[toolIndex].ID)
			if callID == "" {
				continue
			}
			refs = append(refs, restoredToolRef{message: messageIndex, tool: toolIndex})
			queues[callID] = append(queues[callID], len(refs)-1)
		}
	}
	if len(refs) == 0 {
		// A session without tool calls has no timeline to hydrate: skip the
		// (possibly large) event log read.
		return
	}
	events, err := s.store.Events(ctx, sessionID, 0, 0)
	if err != nil {
		s.logger.Printf("[agent] restore: events of %s: %v", sessionID, err)
		return
	}
	// Only hydrate call ids whose durable tool_call count matches the restored
	// history count. A mismatch means the log covers work the current history
	// no longer has (a rewound/regenerated or forked turn, a compacted-away
	// turn) and matching by id could attach another run's offset; those ids
	// stay unknown and keep the legacy order. This also handles providers that
	// reuse per-round ids like "call_0": when both turns are in the history the
	// counts match and the queues pair them in order.
	eventCalls := make(map[string]int, len(queues))
	for _, event := range events {
		if event.Type != "tool_call" {
			continue
		}
		var payload toolCallEvent
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			continue
		}
		eventCalls[strings.TrimSpace(payload.ID)]++
	}
	eligible := make(map[string]bool, len(queues))
	for callID, queue := range queues {
		if eventCalls[callID] == len(queue) {
			eligible[callID] = true
		}
	}
	if len(eligible) == 0 {
		return
	}
	// tool_call events and tool_result events consume their own queues: both
	// are chronological and a result belongs to the call that preceded it.
	callQueues := eligibleQueues(queues, eligible)
	resultQueues := eligibleQueues(queues, eligible)
	nextRef := func(queues map[string][]int, id string) (restoredToolRef, bool) {
		queue := queues[id]
		if len(queue) == 0 {
			return restoredToolRef{}, false
		}
		queues[id] = queue[1:]
		return refs[queue[0]], true
	}
	// timedTools marks the calls whose durable event carried an explicit
	// offset; a message is only "timeline known" when every call did.
	timedTools := make(map[restoredToolRef]bool, len(refs))
	for _, event := range events {
		switch event.Type {
		case "tool_call":
			var payload toolCallEvent
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				continue
			}
			callID := strings.TrimSpace(payload.ID)
			if !eligible[callID] {
				continue
			}
			ref, ok := nextRef(callQueues, callID)
			if !ok || payload.TextOffset == nil {
				continue
			}
			state.messages[ref.message].Tools[ref.tool].TextOffset = *payload.TextOffset
			timedTools[ref] = true
		case "tool_result":
			var payload toolResultEvent
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				continue
			}
			callID := strings.TrimSpace(payload.ID)
			if !eligible[callID] {
				continue
			}
			ref, ok := nextRef(resultQueues, callID)
			if !ok {
				continue
			}
			tool := &state.messages[ref.message].Tools[ref.tool]
			if status := strings.TrimSpace(payload.Status); status != "" {
				tool.Status = status
			}
			tool.Summary = payload.Content
		}
	}
	timedPerMessage := make(map[int]int, len(refs))
	for ref, timed := range timedTools {
		if !timed {
			continue
		}
		timedPerMessage[ref.message]++
		if timedPerMessage[ref.message] == len(state.messages[ref.message].Tools) {
			state.messages[ref.message].ToolTimelineKnown = true
		}
	}
}

// eligibleQueues copies the per-call-id reference queues for the ids whose
// durable event count matched, so the call and result scans consume independent
// positions without touching ineligible ids.
func eligibleQueues(source map[string][]int, eligible map[string]bool) map[string][]int {
	out := make(map[string][]int, len(eligible))
	for id, queue := range source {
		if !eligible[id] {
			continue
		}
		out[id] = append([]int(nil), queue...)
	}
	return out
}
