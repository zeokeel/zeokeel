package agent

// Assistant-turn accounting: token usage reported by the provider is attached to
// the in-memory assistant message (the durable rig history has no usage field,
// so a restore loses it) and mirrored into a durable "usage" event for audit.
//
// Rig's RunResult.Usage accumulates every model call of a turn (a billing total
// that can exceed the context window), while rig.EventUsage is emitted once per
// model call. The per-call prompt tokens of the LAST call are therefore tracked
// separately and exposed as MessageUsage.ContextTokens: the real size of the
// conversation context the model saw, which the "remaining context" display
// needs.

import (
	"context"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// turnUsage is the run-local tracker for per-model-call usage events. It is
// created by run and passed to handleEvent on the run goroutine, so no locking
// is needed beyond Rig's synchronous event emission.
type turnUsage struct {
	// lastPromptTokens is the prompt-token count of the most recent model call
	// that reported usage; 0 until an EventUsage arrives.
	lastPromptTokens int
}

// recordRunUsage copies the run's cumulative provider usage to the current
// assistant message and appends the audit event. contextTokens is the last
// per-model-call prompt count (0 when no usage event was reported). A provider
// that reported no tokens leaves both untouched (nil Usage, no event).
func (s *service) recordRunUsage(ctx context.Context, sessionID string, result rig.RunResult, durationMS int64, contextTokens int) {
	usage := result.Usage
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 &&
		usage.TotalTokens == 0 && usage.CachedTokens == 0 {
		return
	}
	record := &contracts.MessageUsage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
		CachedTokens:     usage.CachedTokens,
		Iterations:       result.Iterations,
		DurationMS:       durationMS,
		ContextTokens:    contextTokens,
	}
	s.mu.Lock()
	if state, ok := s.sessions[sessionID]; ok {
		if message := state.assistantMessage(); message != nil {
			message.Usage = record
		}
	}
	s.mu.Unlock()
	s.appendEvent(ctx, sessionID, "usage", map[string]any{
		"promptTokens":     record.PromptTokens,
		"completionTokens": record.CompletionTokens,
		"totalTokens":      record.TotalTokens,
		"cachedTokens":     record.CachedTokens,
		"iterations":       record.Iterations,
		"durationMs":       record.DurationMS,
		"contextTokens":    record.ContextTokens,
	})
}
