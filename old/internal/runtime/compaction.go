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

// DefaultImageKeepLast is how many of the newest image-bearing messages keep
// their image parts after compaction. Older images are replaced by a text
// placeholder: a base64 data URL costs a large, fixed amount of prompt context
// and an image that is many turns old is rarely still needed (DSH's
// compaction-image-offload behaviour). The most recent images are preserved so
// the model can still see what the user last showed it.
const DefaultImageKeepLast = 2

// ImageOffloadPlaceholder prefixes the text that replaces an offloaded image
// part. It is stable so replay and tests can recognize the substitution.
const ImageOffloadPlaceholder = "[image omitted to recover context]"

// estimatedImageTokens is the rough prompt cost charged for one image part.
// The provider sends a data URL whose size dwarfs its information content, so
// counting the URL bytes would wildly overestimate; one vision input is
// charged a flat, conservative amount instead.
const estimatedImageTokens = 1024

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

	// Old images are offloaded first: they are the largest prompt cost that a
	// window change can reclaim without dropping whole messages.
	messages = offloadOldImages(messages, DefaultImageKeepLast)

	keepLast := c.KeepLast
	if keepLast <= 0 {
		keepLast = DefaultCompactionKeepLast
	}

	// Preserve system messages in their original relative order, and replace a
	// summary from an earlier compaction (either role it was stored with) with
	// the fresh one below.
	system, rest := splitCompactionConversation(messages)

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

// isCompactionSummary reports whether msg is a summary produced by an earlier
// compaction. A compacted conversation's summary is a synthetic system message
// at the runtime level, but a manually compacted session persists its summary
// as an assistant row (the app's storeHistory maps only user/assistant rows),
// so the recognizer keys on the stable content marker and accepts both roles.
// Recognition is what lets a later compaction replace the previous summary
// instead of treating it as an ordinary answer, including after a reopen.
func isCompactionSummary(msg Message) bool {
	if msg.Role != RoleSystem && msg.Role != RoleAssistant {
		return false
	}
	return strings.HasPrefix(msg.Content, compactionSummaryPrefix)
}

// splitCompactionConversation partitions a conversation for compaction: system
// messages are preserved in order, and a previous compaction summary (whatever
// role it was persisted with) is dropped so the fresh summary replaces it
// instead of competing with it. The remaining messages are the compaction
// window candidates.
func splitCompactionConversation(messages []Message) (system, rest []Message) {
	system = make([]Message, 0, len(messages))
	rest = make([]Message, 0, len(messages))
	for _, msg := range messages {
		if isCompactionSummary(msg) {
			continue
		}
		if msg.Role == RoleSystem {
			system = append(system, msg)
			continue
		}
		rest = append(rest, msg)
	}
	return system, rest
}

// CompactionSummaryContent renders the stable content of a persisted compaction
// summary. The shell's manual /compact stores the summary as an assistant row
// (see internal/session controller_compact.go), so the content must carry the
// marker isCompactionSummary recognizes even when a custom Compactor's summary
// text does not: an already framed summary passes through unchanged.
func CompactionSummaryContent(dropped int, summary string) string {
	summary = strings.TrimSpace(summary)
	if strings.HasPrefix(summary, compactionSummaryPrefix) {
		return summary
	}
	frame := fmt.Sprintf("%s%d messages)", compactionSummaryPrefix, dropped)
	if summary == "" {
		return frame
	}
	return frame + "\n\n" + summary
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

// estimateTokens approximates the prompt size in tokens. It is rune-aware:
// ASCII text averages the common ~4 characters per token, while a CJK rune is
// charged about one token (the chars/4 heuristic under-counted Chinese and
// Japanese prompts by roughly 4x on their CJK portion). Image parts are charged
// a flat per-image amount instead of their base64 URL length. Reasoning text
// and tool-call payloads count too, because providers send them back as part of
// the prompt.
func estimateTokens(messages []Message) int {
	tokens := 0
	for _, msg := range messages {
		tokens += estimateTextTokens(msg.Content)
		tokens += estimateTextTokens(msg.Reasoning)
		tokens += (len(msg.ToolCallID) + len(msg.ToolName) + 3) / 4
		for _, call := range msg.ToolCalls {
			tokens += (len(call.ID) + len(call.Name) + len(call.Arguments) + 3) / 4
		}
		tokens += len(msg.Images) * estimatedImageTokens
	}
	return tokens
}

// estimateTextTokens approximates one string's token count. Pure ASCII keeps
// the fast chars/4 path; mixed text counts CJK runes as one token each and the
// remaining runes at four characters per token.
func estimateTextTokens(s string) int {
	if s == "" {
		return 0
	}
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return (len(s) + 3) / 4
	}
	cjk, other := 0, 0
	for _, r := range s {
		if isCJKRune(r) {
			cjk++
		} else {
			other++
		}
	}
	return cjk + (other+3)/4
}

// isCJKRune reports whether r belongs to a CJK/CJK-adjacent script whose
// characters tokenize at roughly one token each.
func isCJKRune(r rune) bool {
	switch {
	case r >= 0x2E80 && r <= 0x303F: // CJK radicals, Kangxi, CJK punctuation
		return true
	case r >= 0x3040 && r <= 0x30FF: // Hiragana, Katakana
		return true
	case r >= 0x3100 && r <= 0x31FF: // Bopomofo, Hangul compatibility Jamo
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK Unified Ideographs Extension A
		return true
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
		return true
	case r >= 0xAC00 && r <= 0xD7AF: // Hangul Syllables
		return true
	case r >= 0xF900 && r <= 0xFAFF: // CJK Compatibility Ideographs
		return true
	case r >= 0xFF00 && r <= 0xFFEF: // Halfwidth/Fullwidth Forms
		return true
	case r >= 0x20000 && r <= 0x3FFFF: // CJK Extension B+ (and supplement)
		return true
	default:
		return false
	}
}

// offloadOldImages returns a copy of messages where every image part outside
// the newest keepLast image-bearing messages is replaced by an
// ImageOffloadPlaceholder line. A non-positive keepLast means
// DefaultImageKeepLast. Messages without images and the kept images are
// returned unchanged (the input slice is never mutated).
func offloadOldImages(messages []Message, keepLast int) []Message {
	if len(messages) == 0 {
		return messages
	}
	if keepLast <= 0 {
		keepLast = DefaultImageKeepLast
	}
	keep := make(map[int]bool, keepLast)
	for i := len(messages) - 1; i >= 0 && len(keep) < keepLast; i-- {
		if len(messages[i].Images) > 0 {
			keep[i] = true
		}
	}
	if len(keep) == 0 {
		return messages
	}
	out := make([]Message, len(messages))
	copy(out, messages)
	changed := false
	for i := range out {
		if len(out[i].Images) == 0 || keep[i] {
			continue
		}
		dropped := len(out[i].Images)
		out[i].Images = nil
		note := fmt.Sprintf("%s (%d image(s) removed)", ImageOffloadPlaceholder, dropped)
		if content := strings.TrimRight(out[i].Content, "\n"); content != "" {
			out[i].Content = content + "\n" + note
		} else {
			out[i].Content = note
		}
		changed = true
	}
	if !changed {
		return messages
	}
	return out
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
