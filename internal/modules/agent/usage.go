package agent

// Assistant-turn accounting: token usage reported by the provider is attached to
// the in-memory assistant message (the durable rig history has no usage field,
// so a restore loses it) and mirrored into a durable "usage" event for audit.

import (
	"context"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// recordRunUsage copies the run's cumulative provider usage to the current
// assistant message and appends the audit event. A provider that reported no
// tokens leaves both untouched (nil Usage, no event).
func (s *service) recordRunUsage(ctx context.Context, sessionID string, result rig.RunResult, durationMS int64) {
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
	})
}
