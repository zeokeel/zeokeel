package runtime

import (
	"context"
	"fmt"
	"strings"
)

// Compactor condenses a session's conversation before a turn when the
// approximate context size exceeds Config.ContextBudget.
//
// messages is a copy of the prior conversation in chronological order,
// including any system messages; budget is Config.ContextBudget, estimated in
// tokens (see EstimateTokens). The returned messages replace the prior
// conversation for the upcoming turn. Compact runs in the turn goroutine, so
// returning an error ends that turn with a state.Error and a failed terminal
// status; the turn itself never reaches the provider.
//
// Implementations must be safe for concurrent use across sessions. They must
// not retain or mutate messages.
type Compactor interface {
	Compact(ctx context.Context, sessionID string, messages []Message, budget int) ([]Message, error)
}

// DefaultCompactionKeepLast is the KeepLast value used by
// WindowCompactor.Compact when KeepLast is not positive (for example the zero
// value). It leaves a small recent tail so the model still sees the immediate
// conversational context.
const DefaultCompactionKeepLast = 8

// compactionSummaryPrefix marks the synthetic message that stands in for the
// compacted part of a conversation. It is deliberately stable so repeated
// compaction can replace the previous summary instead of accumulating them.
const compactionSummaryPrefix = "Earlier conversation compacted ("

// WindowCompactor is the default Compactor. It preserves every system message,
// keeps the last KeepLast non-system messages, and inserts one synthetic summary
// message between the two:
//
//	Earlier conversation compacted (N messages)
//
// where N is the number of non-system messages dropped by this compaction. A
// KeepLast that is not positive falls back to DefaultCompactionKeepLast. The
// compactor never calls a model; apps that want an LLM-written summary can
// implement Compactor themselves and plug it into Config.Compactor.
type WindowCompactor struct {
	// KeepLast is the number of most recent non-system messages to retain.
	// Zero or negative means DefaultCompactionKeepLast.
	KeepLast int
}

var _ Compactor = WindowCompactor{}

// Compact implements Compactor.
func (c WindowCompactor) Compact(ctx context.Context, sessionID string, messages []Message, budget int) ([]Message, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if len(messages) == 0 {
		return nil, nil
	}

	keepLast := c.KeepLast
	if keepLast <= 0 {
		keepLast = DefaultCompactionKeepLast
	}

	// Preserve system messages in their original relative order, and replace a
	// summary from an earlier compaction with the fresh one below.
	system := make([]Message, 0, len(messages))
	rest := make([]Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == RoleSystem {
			if !isCompactionSummary(msg) {
				system = append(system, msg)
			}
			continue
		}
		rest = append(rest, msg)
	}

	if len(rest) <= keepLast {
		// Nothing to drop: leave the conversation untouched (including any
		// previous summary).
		return cloneMessages(messages), nil
	}

	dropped := compactionBoundary(rest, keepLast)
	if dropped == 0 {
		// The requested window starts inside a tool-result block, so keeping
		// the whole block leaves nothing to compact. Do not emit a pointless
		// "0 messages" summary.
		return cloneMessages(messages), nil
	}
	// Clone the retained tail: the caller owns its output and must not be able
	// to mutate the conversation it passed in through the tail's ToolCalls.
	recent := cloneMessages(rest[dropped:])

	out := make([]Message, 0, len(system)+1+len(recent))
	out = append(out, system...)
	out = append(out, Message{
		Role:    RoleSystem,
		Content: fmt.Sprintf("%s%d messages)", compactionSummaryPrefix, dropped),
	})
	out = append(out, recent...)
	return out, nil
}

func isCompactionSummary(msg Message) bool {
	return msg.Role == RoleSystem && strings.HasPrefix(msg.Content, compactionSummaryPrefix)
}

// compactionBoundary returns the index into rest where the retained tail
// starts. It snaps the keep-last window backward out of a tool-result block:
// OpenAI-compatible APIs require every role=tool message to answer a preceding
// assistant tool_calls message, so a tail beginning with a retained tool result
// whose assistant call was dropped is an invalid request. Assistant tool-call
// messages precede all of their results, so walking back over the contiguous
// tool messages also retains every result of the assistant call that is kept.
func compactionBoundary(rest []Message, keepLast int) int {
	dropped := len(rest) - keepLast
	if dropped <= 0 {
		return 0
	}
	if dropped >= len(rest) {
		return len(rest)
	}
	for dropped > 0 && rest[dropped].Role == RoleTool {
		dropped--
	}
	return dropped
}

// estimateTokens approximates the prompt size of messages using the common
// chars/4 heuristic. Reasoning text and tool-call payloads count too, because
// providers send them back as part of the prompt.
func estimateTokens(messages []Message) int {
	chars := 0
	for _, msg := range messages {
		chars += len(msg.Content)
		chars += len(msg.Reasoning)
		chars += len(msg.ToolCallID)
		chars += len(msg.ToolName)
		for _, call := range msg.ToolCalls {
			chars += len(call.ID)
			chars += len(call.Name)
			chars += len(call.Arguments)
		}
	}
	return chars / 4
}

// cloneMessages returns a shallow copy whose tool-call slice is independent of
// the input, so a Compactor or CheckpointStore cannot mutate stored state.
func cloneMessages(messages []Message) []Message {
	if messages == nil {
		return nil
	}
	out := make([]Message, len(messages))
	copy(out, messages)
	for i := range out {
		if out[i].ToolCalls != nil {
			out[i].ToolCalls = append([]ToolCall(nil), out[i].ToolCalls...)
		}
	}
	return out
}
