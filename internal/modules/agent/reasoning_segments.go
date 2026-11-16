package agent

// Per-iteration reasoning segments (2026-11 audit US-01/US-02).
//
// A live run folds every model iteration into ONE assistant ChatMessage. The
// flat Reasoning string keeps the compatibility concatenation; the segments
// added here preserve the iteration boundaries and their position in the
// cumulative reply text (the same TextOffset timeline mechanism ToolCall uses),
// so the transcript can interleave thinking with prose and tool cards instead
// of drawing one aggregated thinking block. rig emits EventTurnStart /
// EventReasoningDelta / EventMessage / EventTurnEnd with the run-local
// Iteration; the turn-start text offset is recorded so a segment keeps the
// right position even when prose arrived before the iteration's thinking.
//
// The restore path (chatMessagesFromRig) fills one segment per persisted
// assistant message — rig history keeps one assistant message per iteration —
// so live and restored turns carry the same structure (US-02).

import (
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// reasoningSegmentOffset returns the TextOffset a new segment of iteration
// must carry: the text length recorded when TurnStart announced this iteration
// (prose may already have arrived within it), else the current cumulative text
// length (the reasoning-before-prose default).
func reasoningSegmentOffset(state *sessionState, message *contracts.ChatMessage, iteration int) int {
	if state.turnStarted && state.turnIteration == iteration {
		return state.turnTextOffset
	}
	return len(message.Text)
}

// appendReasoningDelta folds one reasoning delta into the message's segments:
// deltas of the current iteration append to its segment, a new iteration opens
// a new segment at its timeline offset.
func appendReasoningDelta(state *sessionState, message *contracts.ChatMessage, iteration int, text string) {
	if n := len(message.ReasoningSegments); n > 0 && message.ReasoningSegments[n-1].Iteration == iteration {
		message.ReasoningSegments[n-1].Text += text
		return
	}
	message.ReasoningSegments = append(message.ReasoningSegments, contracts.ReasoningSegment{
		Text:       text,
		Iteration:  iteration,
		TextOffset: reasoningSegmentOffset(state, message, iteration),
	})
}

// reconcileReasoningSegment makes one iteration's segment match the complete
// reasoning of rig's EventMessage (the authoritative per-iteration assistant
// message). It fills a segment no delta created and repairs a partial one; the
// flat Reasoning string is rebuilt from the segments so the two never drift.
func reconcileReasoningSegment(state *sessionState, message *contracts.ChatMessage, iteration int, reasoning string) {
	if reasoning == "" {
		return
	}
	for index := range message.ReasoningSegments {
		if message.ReasoningSegments[index].Iteration != iteration {
			continue
		}
		if message.ReasoningSegments[index].Text != reasoning {
			message.ReasoningSegments[index].Text = reasoning
			message.Reasoning = joinReasoningSegments(message.ReasoningSegments)
		}
		return
	}
	message.ReasoningSegments = append(message.ReasoningSegments, contracts.ReasoningSegment{
		Text:       reasoning,
		Iteration:  iteration,
		TextOffset: reasoningSegmentOffset(state, message, iteration),
	})
	message.Reasoning = joinReasoningSegments(message.ReasoningSegments)
}

// joinReasoningSegments renders the flat compatibility concatenation.
func joinReasoningSegments(segments []contracts.ReasoningSegment) string {
	var builder strings.Builder
	for _, segment := range segments {
		builder.WriteString(segment.Text)
	}
	return builder.String()
}

// cloneReasoningSegments copies a segment slice so snapshots and forks never
// share mutable segment state with the live read model.
func cloneReasoningSegments(segments []contracts.ReasoningSegment) []contracts.ReasoningSegment {
	return append([]contracts.ReasoningSegment(nil), segments...)
}

// restoredReasoningSegments fills the single segment of one restored assistant
// message: rig history persists one assistant message per model iteration, so
// the message's whole Reasoning is that iteration's thinking and its offset is
// 0 (the message's text belongs to the same iteration; history records no
// intra-iteration ordering). iteration is the 1-based assistant position within
// the current turn.
func restoredReasoningSegments(reasoning string, iteration int) []contracts.ReasoningSegment {
	if reasoning == "" {
		return nil
	}
	return []contracts.ReasoningSegment{{Text: reasoning, Iteration: iteration, TextOffset: 0}}
}
