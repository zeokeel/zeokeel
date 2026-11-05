package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DefaultToolResultKeepLast is how many of the newest tool messages
// PruneToolResults leaves untouched when keepLast is not positive.
const DefaultToolResultKeepLast = 4

// DefaultLLMSummaryChars caps the size of the model-written summary that
// LLMCompactor stores in the conversation. A summary larger than the part it
// replaces would defeat compaction.
const DefaultLLMSummaryChars = 4000

// DefaultLLMSummaryTimeout bounds one summarization request. It is shorter
// than the provider's 60s response timeout so a stalled summary falls back to
// deterministic compaction instead of holding up the turn for a full minute.
const DefaultLLMSummaryTimeout = 20 * time.Second

// ToolResultPrunedMarker prefixes the placeholder that replaces an old tool
// result. It is stable so pruning is idempotent and tests can recognize an
// already-pruned message.
const ToolResultPrunedMarker = "[tool result pruned]"

// LLMCompactorSystemPrompt is the system instruction sent to the provider for
// the summarization request. It is exported so fake providers in tests can
// recognize a compaction request without guessing from the message list.
const LLMCompactorSystemPrompt = "You are a conversation summarizer. Summarize the provided conversation so another assistant can continue it. " +
	"Preserve decisions, user preferences, file paths, tool outcomes, unresolved questions and the current task. " +
	"Reply with the summary text only, without preamble."

// LLMCompactor is a Compactor that writes a real summary with the configured
// Provider. It keeps the same shape as WindowCompactor — every ordinary system
// message, one synthetic system summary, and the newest KeepLast non-system
// messages — but the summary text comes from the model instead of a message
// count. Old tool results among the retained tail are truncated to a marker so
// large outputs cannot keep the prompt over budget.
//
// A summarization failure (provider error, stream error, empty answer, timeout,
// or a cancelled-free unusable response) falls back to Fallback. When Fallback
// is nil, WindowCompactor{KeepLast: KeepLast} is used. A caller context
// cancellation is reported as an error instead of being hidden by the fallback.
type LLMCompactor struct {
	// Provider is the model backend used to write the summary. It must be
	// set for summarization to be attempted; a nil Provider always falls back.
	Provider Provider
	// Model is forwarded on the summarization request. Empty lets the
	// provider use its configured default model.
	Model string
	// Timeout bounds one summarization request. Zero means
	// DefaultLLMSummaryTimeout; a negative value disables the extra timeout
	// and relies on the caller's context and the provider client.
	Timeout time.Duration
	// KeepLast is the number of most recent non-system messages to retain.
	// Zero or negative means DefaultCompactionKeepLast.
	KeepLast int
	// ToolResultKeepLast is the number of most recent tool messages left
	// verbatim in the retained tail. Zero means DefaultToolResultKeepLast;
	// a negative value disables tool-result pruning.
	ToolResultKeepLast int
	// MaxSummaryChars caps the summary in characters. Zero or negative means
	// DefaultLLMSummaryChars.
	MaxSummaryChars int
	// Fallback takes over when summarization fails. Nil means
	// WindowCompactor{KeepLast: KeepLast}.
	Fallback Compactor
}

var _ Compactor = LLMCompactor{}

// Compact implements Compactor.
func (c LLMCompactor) Compact(ctx context.Context, sessionID string, messages []Message, budget int) ([]Message, error) {
	if ctx == nil {
		// The provider contract expects a live context; keep the public
		// Compactor surface as forgiving as WindowCompactor's.
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, nil
	}

	keepLast := c.keepLast()
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
		// previous summary), exactly like WindowCompactor.
		return cloneMessages(messages), nil
	}

	dropped := compactionBoundary(rest, keepLast)
	if dropped == 0 {
		// See WindowCompactor: retaining a complete tool-result block can
		// consume the whole budget, leaving nothing to summarize.
		return cloneMessages(messages), nil
	}
	summarizeCtx := ctx
	if timeout := c.summaryTimeout(); timeout > 0 {
		var cancel context.CancelFunc
		summarizeCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	summary, err := c.summarize(summarizeCtx, sessionID, rest[:dropped])
	if err != nil {
		// A caller cancellation is a stopped turn, not a summarizer failure;
		// report it instead of hiding it behind the fallback. A summarizer
		// timeout is different: it falls through to the deterministic fallback.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return c.fallback().Compact(ctx, sessionID, cloneMessages(messages), budget)
	}
	summary = c.truncateSummary(summary)
	if summary == "" {
		return c.fallback().Compact(ctx, sessionID, cloneMessages(messages), budget)
	}

	// Clone the retained tail before pruning or returning it: neither the
	// caller's conversation nor its ToolCalls may alias the compactor output.
	recent := cloneMessages(rest[dropped:])
	if c.ToolResultKeepLast >= 0 {
		recent = pruneToolResultsExact(recent, c.toolResultKeepLast())
	}

	out := make([]Message, 0, len(system)+1+len(recent))
	out = append(out, system...)
	out = append(out, Message{
		Role: RoleSystem,
		Content: fmt.Sprintf("%s%d messages)\n\n%s",
			compactionSummaryPrefix, dropped, summary),
	})
	out = append(out, recent...)
	return out, nil
}

// summarize asks the provider for a summary of old. The old tool results are
// pruned before they are sent so the summarizer itself stays within budget.
func (c LLMCompactor) summarize(ctx context.Context, sessionID string, old []Message) (string, error) {
	if c.Provider == nil {
		return "", errors.New("runtime: LLMCompactor has no provider")
	}
	request := make([]Message, 0, len(old)+2)
	request = append(request, Message{Role: RoleSystem, Content: LLMCompactorSystemPrompt})
	request = append(request, pruneToolResultsExact(old, 0)...)
	request = append(request, Message{
		Role: RoleUser,
		Content: fmt.Sprintf(
			"Summarize the conversation above in at most %d characters so it can replace that part of the context.",
			c.maxSummaryChars(),
		),
	})

	ch, err := c.Provider.Stream(ctx, Request{
		SessionID: sessionID,
		Model:     c.Model,
		Messages:  request,
	})
	if err != nil {
		return "", err
	}
	if ch == nil {
		return "", errors.New("runtime: LLMCompactor provider returned no stream")
	}

	var b strings.Builder
	for {
		var (
			chunk Chunk
			ok    bool
		)
		if ctx == nil {
			chunk, ok = <-ch
		} else {
			select {
			case chunk, ok = <-ch:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		if !ok {
			break
		}
		if chunk.Err != nil {
			return "", chunk.Err
		}
		if chunk.Text != "" {
			b.WriteString(chunk.Text)
		}
		if chunk.Done {
			break
		}
	}
	return strings.TrimSpace(b.String()), nil
}

func (c LLMCompactor) keepLast() int {
	if c.KeepLast <= 0 {
		return DefaultCompactionKeepLast
	}
	return c.KeepLast
}

func (c LLMCompactor) toolResultKeepLast() int {
	if c.ToolResultKeepLast <= 0 {
		return DefaultToolResultKeepLast
	}
	return c.ToolResultKeepLast
}

func (c LLMCompactor) summaryTimeout() time.Duration {
	if c.Timeout == 0 {
		return DefaultLLMSummaryTimeout
	}
	return c.Timeout
}

func (c LLMCompactor) maxSummaryChars() int {
	if c.MaxSummaryChars <= 0 {
		return DefaultLLMSummaryChars
	}
	return c.MaxSummaryChars
}

func (c LLMCompactor) truncateSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	runes := []rune(summary)
	max := c.maxSummaryChars()
	if len(runes) <= max {
		return summary
	}
	// Count the ellipsis against MaxSummaryChars: reserving one rune keeps the
	// stored summary within the documented cap instead of exceeding it by one.
	if max <= 1 {
		return "…"
	}
	return strings.TrimSpace(string(runes[:max-1])) + "…"
}

func (c LLMCompactor) fallback() Compactor {
	if c.Fallback != nil {
		return c.Fallback
	}
	return WindowCompactor{KeepLast: c.keepLast()}
}

// PruneToolResults returns a copy of messages where all but the newest keepLast
// tool messages have their payload replaced by ToolResultPrunedMarker and the
// original byte count. Every other field (role, call id, tool name, tool calls,
// images) is preserved, and non-tool messages are untouched. keepLast is not
// positive means DefaultToolResultKeepLast. The input is never mutated.
func PruneToolResults(messages []Message, keepLast int) []Message {
	if keepLast <= 0 {
		keepLast = DefaultToolResultKeepLast
	}
	return pruneToolResultsExact(messages, keepLast)
}

// pruneToolResultsExact is PruneToolResults with an explicit keep count; 0
// prunes every tool message, which is what the summarization prompt wants.
func pruneToolResultsExact(messages []Message, keepLast int) []Message {
	if len(messages) == 0 {
		return cloneMessages(messages)
	}
	out := cloneMessages(messages)
	remaining := keepLast
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Role != RoleTool {
			continue
		}
		if remaining > 0 {
			remaining--
			continue
		}
		if strings.HasPrefix(out[i].Content, ToolResultPrunedMarker) {
			continue
		}
		marker := fmt.Sprintf("%s %d bytes omitted", ToolResultPrunedMarker, len(out[i].Content))
		if len(marker) >= len(out[i].Content) {
			// Pruning must never grow a result: a tiny or empty payload is
			// already smaller than the placeholder that would replace it.
			continue
		}
		out[i].Content = marker
	}
	return out
}
