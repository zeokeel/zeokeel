package agent

// Product-side pre-run compaction (the PRIMARY automatic compaction).
//
// Rig's in-run compactor only reacts after a model call fails (or its heuristic
// hint fires); by then the first request may already overflow and the user sees a
// failed turn. The product therefore compacts once BEFORE starting a run when the
// estimated request (system prompt + durable history) exceeds the effective
// context budget:
//
//  1. archive the raw history about to be replaced (`compact_archive`, kind=auto,
//     bounded; same helper as /compact). If the archive cannot be written the
//     compaction is skipped and the run continues normally — it is an audit
//     improvement, never a reason to fail or lose data;
//  2. compact with the LLM compactor (keepLast 6, deterministic window fallback)
//     and keep the last user message;
//  3. replace the durable history, then write an EXACT durable `compact` event
//     (kind=auto, inferred=false, before/after/replaced/saved) that also folds
//     into the ContextInspector timeline. A failed/no-reduction attempt is
//     recorded with an outcome instead of blocking the run.
//
// Because this happens before Rig appends the new turn, the new user message is
// never duplicated and no tool side effect is replayed; a successful run then
// starts with the compacted context. Rig's in-run compaction stays installed as
// the fallback for mid-run growth, and its event remains inferred.

import (
	"context"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/session/compact"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// preRunCompactKeepLast is how many recent messages the pre-run compaction
	// keeps verbatim (the rest is summarized). It mirrors the context-overflow
	// recovery window, and always keeps the current user turn.
	preRunCompactKeepLast = 6
	// Compact failure outcomes recorded on the exact compact event.
	compactOutcomeFailed        = "failed"
	compactOutcomeNoReduction   = "no-reduction"
	compactOutcomeArchiveFailed = "archive-failed"
	compactOutcomeReplaceFailed = "replace-failed"
)

// preRunCompact performs the product-side automatic compaction before a run.
// It never fails the run: any problem is logged (and recorded as an outcome when
// a compact attempt started) and the normal run proceeds with the history as is.
func (s *service) preRunCompact(ctx context.Context, sessionID string, cfg contracts.ChatConfig, history rig.History, provider rig.Provider) {
	if s == nil || ctx == nil || len(sessionID) == 0 {
		return
	}
	if err := ctx.Err(); err != nil {
		return
	}
	if s.sessionDeleted(sessionID) {
		return
	}
	budget := s.effectiveContextBudget(cfg)
	if budget <= 0 {
		return // no known window: nothing to protect against
	}
	messages, err := history.Load(ctx, sessionID)
	if err != nil {
		s.logger.Printf("[agent] pre-run compaction: load session %s history: %v", sessionID, err)
		return
	}
	if len(messages) == 0 {
		return
	}
	systemTokens := rig.EstimateTextTokens(cfg.SystemPrompt)
	before := rig.EstimateTokens(messages)
	if before+systemTokens <= budget {
		return // within budget: do not touch the history
	}
	compactor := s.preRunCompactor(cfg, sessionID, provider)
	if compactor == nil {
		return
	}
	promptBudget := budget - systemTokens
	if promptBudget < 1 {
		// A system prompt alone exceeds the window: compacting the history can
		// still help, but ask for any positive budget so the compactor reduces as
		// much as it can.
		promptBudget = 1
	}
	compacted, compactErr := compactor.Compact(ctx, sessionID, messages, promptBudget)
	if compactErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return
		}
		s.logger.Printf("[agent] pre-run compaction: session %s: %v", sessionID, compactErr)
		s.recordCompactEvent(ctx, sessionID, "auto", before, before, len(messages), 0, false, compactOutcomeFailed)
		return
	}
	if lastUser, ok := lastUserMessageOf(messages); ok && !containsMessages(compacted, lastUser) {
		compacted = append(compacted, lastUser)
	}
	after := rig.EstimateTokens(compacted)
	if len(compacted) == 0 || (len(compacted) >= len(messages) && after >= before) {
		// Nothing was actually reduced (e.g. the compactor kept everything): do
		// not replace, and leave the trace so the Context tab can explain it.
		s.recordCompactEvent(ctx, sessionID, "auto", before, after, len(messages), len(compacted), false, compactOutcomeNoReduction)
		return
	}
	// Fail-closed but non-blocking: the archive is a prerequisite for replacing
	// the durable history, but a failed archive merely skips the compaction.
	if err := s.archiveReplacedHistory(ctx, sessionID, "auto", messages,
		before, after, len(messages), len(compacted)); err != nil {
		s.logger.Printf("[agent] pre-run compaction: session %s archive: %v", sessionID, err)
		s.recordCompactEvent(ctx, sessionID, "auto", before, after, len(messages), len(compacted), false, compactOutcomeArchiveFailed)
		return
	}
	if err := s.replaceCompactedHistory(ctx, sessionID, compacted); err != nil {
		s.logger.Printf("[agent] pre-run compaction: session %s replace: %v", sessionID, err)
		s.recordCompactEvent(ctx, sessionID, "auto", before, after, len(messages), len(compacted), false, compactOutcomeReplaceFailed)
		return
	}
	// The exact audit event: the Context tab and the durable log now explain the
	// automatic compaction without relying on the inferred prompt-token drop.
	s.recordCompactEvent(ctx, sessionID, "auto", before, after, len(messages), len(compacted), false, "")
	s.logger.Printf("[agent] pre-run compaction: session %s compacted %d -> %d tokens before the run", sessionID, before, after)
	s.notify(sessionID)
}

// preRunCompactor resolves the compactor for the pre-run path: the manual path's
// test seam when installed, otherwise the LLM compactor with the bounded recent
// window and the deterministic fallback (a failed summary must not fail the run).
func (s *service) preRunCompactor(cfg contracts.ChatConfig, sessionID string, provider rig.Provider) rig.Compactor {
	s.mu.Lock()
	factory := s.compactorFactory
	s.mu.Unlock()
	if factory != nil {
		compactor, err := factory(cfg)
		if err != nil || compactor == nil {
			s.logger.Printf("[agent] pre-run compaction: create compactor: %v", err)
			return nil
		}
		return compactor
	}
	if provider == nil {
		return nil
	}
	return compact.LLMCompactor{
		Provider: provider,
		Model:    cfg.Model,
		KeepLast: preRunCompactKeepLast,
		Fallback: compact.WindowCompactor{},
	}
}
