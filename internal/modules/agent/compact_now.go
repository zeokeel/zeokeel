package agent

// Manual context compaction: the optional contracts.SessionCompactor half used
// by the session screen's /compact command. It reads the session history from
// the durable store, compacts it with the same LLM compactor the run loop uses
// (or a test seam), replaces the stored history and reports the size delta. It
// never changes the active session or the transcript read model, and never runs
// while a turn is running.
//
// Compaction is a multi-second model call, so it marks the session "compacting":
// Send and Regenerate are rejected for that window (a turn appended halfway
// through would be erased by the write-back). The history is re-validated right
// before the replacement so a writer that bypasses the chat service (a run that
// was already in flight) is preserved instead of erased by the stale snapshot.

import (
	"context"
	"errors"
	"fmt"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/session/compact"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

var _ contracts.SessionCompactor = (*service)(nil)

// CompactNow implements contracts.SessionCompactor.
func (s *service) CompactNow(ctx context.Context, sessionID string) (contracts.CompactResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return contracts.CompactResult{}, err
	}
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return contracts.CompactResult{}, fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	if state.running || state.compacting {
		s.mu.Unlock()
		return contracts.CompactResult{}, contracts.ErrCompactRunning
	}
	state.compacting = true
	state.updatedAt = time.Now()
	cfg := s.configForSessionLocked(sessionID)
	factory := s.compactorFactory
	s.mu.Unlock()
	defer s.endCompaction(sessionID)

	if cfg.ContextBudget <= 0 {
		return contracts.CompactResult{}, contracts.ErrCompactUnavailable
	}
	history := s.historyView()
	messages, err := history.Load(ctx, sessionID)
	if err != nil {
		return contracts.CompactResult{}, fmt.Errorf("agent: load session %q for compaction: %w", sessionID, err)
	}
	if len(messages) == 0 {
		return contracts.CompactResult{}, contracts.ErrCompactNothing
	}
	beforeTokens := rig.EstimateTokens(messages)
	compactor := rig.Compactor(nil)
	if factory != nil {
		compactor, err = factory(cfg)
	} else {
		var provider rig.Provider
		provider, err = s.newProvider(cfg, s.thinkingParamsForSession(sessionID, cfg.Model))
		if err == nil {
			compactor = compact.LLMCompactor{
				Provider: provider,
				Model:    cfg.Model,
				// Same fallback as the automatic path: a failed/empty summary
				// falls back to deterministic window compaction instead of
				// failing the command.
				Fallback: compact.WindowCompactor{},
			}
		}
	}
	if err != nil || compactor == nil {
		if err == nil {
			err = errors.New("no compactor")
		}
		return contracts.CompactResult{}, fmt.Errorf("%w: %v", contracts.ErrCompactUnavailable, err)
	}
	// Charge the system prompt against the same context budget the automatic
	// path uses (rig/agent/agent.go prepareContext: budget-systemPromptTokens).
	// The manual path does not rebuild the full run prompt, so this deducts the
	// configured system prompt only.
	budget := cfg.ContextBudget - rig.EstimateTextTokens(cfg.SystemPrompt)
	compacted, err := compactor.Compact(ctx, sessionID, messages, budget)
	if err != nil {
		return contracts.CompactResult{}, fmt.Errorf("agent: compact session %q: %w", sessionID, err)
	}

	// Re-validate against the live history before replacing it. Send/Regenerate
	// are rejected while compacting, so this catches only writers that bypass
	// the chat service; their messages must be preserved, never erased by the
	// stale snapshot.
	current, relErr := history.Load(ctx, sessionID)
	if relErr != nil {
		return contracts.CompactResult{}, fmt.Errorf("agent: reload session %q for compaction: %w", sessionID, relErr)
	}
	if !sameHistoryPrefix(messages, current) {
		return contracts.CompactResult{}, fmt.Errorf("%w: session %q", contracts.ErrCompactConflict, sessionID)
	}
	if len(current) > len(messages) {
		compacted = append(append([]rig.Message(nil), compacted...), current[len(messages):]...)
	}

	afterTokens := rig.EstimateTokens(compacted)
	if len(compacted) == 0 || (len(compacted) >= len(current) && afterTokens >= beforeTokens) {
		return contracts.CompactResult{}, contracts.ErrCompactNothing
	}
	if err := s.replaceCompactedHistory(ctx, sessionID, compacted); err != nil {
		return contracts.CompactResult{}, fmt.Errorf("%w: %v", contracts.ErrCompactSave, err)
	}
	s.notify(sessionID)
	// Replaced/Saved carry the before/after message counts (the UI renders
	// "N → M messages"); Before/AfterTokens carry the estimated token counts.
	return contracts.CompactResult{
		Replaced:     len(messages),
		Saved:        len(compacted),
		BeforeTokens: beforeTokens,
		AfterTokens:  afterTokens,
	}, nil
}

// endCompaction clears the in-flight compaction flag of a session.
func (s *service) endCompaction(sessionID string) {
	s.mu.Lock()
	if state, ok := s.sessions[sessionID]; ok {
		state.compacting = false
		state.updatedAt = time.Now()
	}
	s.mu.Unlock()
}

// sameHistoryPrefix reports whether current starts with the same messages as
// prefix: the compaction snapshot must still be the history's head before the
// replacement drops it.
func sameHistoryPrefix(prefix, current []rig.Message) bool {
	if len(current) < len(prefix) {
		return false
	}
	for index := range prefix {
		before, after := prefix[index], current[index]
		if before.Role != after.Role || before.Text() != after.Text() ||
			before.Reasoning != after.Reasoning || before.ToolCallID != after.ToolCallID ||
			len(before.ToolCalls) != len(after.ToolCalls) {
			return false
		}
	}
	return true
}

// replaceCompactedHistory persists the compacted window: the durable store is
// preferred; the in-memory history fallback is only replaceable when it
// implements rig.HistoryReplacer.
func (s *service) replaceCompactedHistory(ctx context.Context, sessionID string, messages []rig.Message) error {
	if s.store != nil {
		return s.replaceSessionHistory(ctx, sessionID, messages)
	}
	history := s.historyView()
	if replacer, ok := history.(rig.HistoryReplacer); ok {
		return replacer.Replace(ctx, sessionID, messages)
	}
	return errors.New("agent: history store cannot replace messages")
}
