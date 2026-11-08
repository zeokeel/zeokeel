package agent

// Manual context compaction: the optional contracts.SessionCompactor half used
// by the session screen's /compact command. It reads the session history from
// the durable store, compacts it with the same LLM compactor the run loop uses
// (or a test seam), replaces the stored history and reports the size delta. It
// never changes the active session and never runs while a turn is running.

import (
	"context"
	"errors"
	"fmt"

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
	running := ok && state.running
	cfg := s.configForSessionLocked(sessionID)
	factory := s.compactorFactory
	s.mu.Unlock()
	if !ok {
		return contracts.CompactResult{}, fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	if running {
		return contracts.CompactResult{}, contracts.ErrCompactRunning
	}
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
		provider, err = s.newProvider(cfg, s.sessionEffort(sessionID))
		if err == nil {
			compactor = compact.LLMCompactor{Provider: provider, Model: cfg.Model}
		}
	}
	if err != nil || compactor == nil {
		if err == nil {
			err = errors.New("no compactor")
		}
		return contracts.CompactResult{}, fmt.Errorf("%w: %v", contracts.ErrCompactUnavailable, err)
	}
	compacted, err := compactor.Compact(ctx, sessionID, messages, cfg.ContextBudget)
	if err != nil {
		return contracts.CompactResult{}, fmt.Errorf("agent: compact session %q: %w", sessionID, err)
	}
	afterTokens := rig.EstimateTokens(compacted)
	if len(compacted) == 0 || (len(compacted) >= len(messages) && afterTokens >= beforeTokens) {
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

// replaceCompactedHistory persists the compacted window: the durable store is
// preferred; the in-memory history fallback is only replaceable when it
// implements rig.HistoryReplacer.
func (s *service) replaceCompactedHistory(ctx context.Context, sessionID string, messages []rig.Message) error {
	if s.store != nil {
		return s.store.ReplaceHistory(ctx, sessionID, messages)
	}
	history := s.historyView()
	if replacer, ok := history.(rig.HistoryReplacer); ok {
		return replacer.Replace(ctx, sessionID, messages)
	}
	return errors.New("agent: history store cannot replace messages")
}
