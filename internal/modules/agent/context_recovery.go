package agent

// Context overflow recovery (DSH: one maximal auto-compaction + one retry of the
// same turn).
//
// A provider rejects a request when the prompt exceeds the model window
// ("context length exceeded" and friends). Rig returns that as a run error, and
// the turn would simply fail. The backend instead:
//
//  1. compacts the session's durable history once with the LLM compactor (the
//     same compactor the automatic/`/compact` paths use);
//  2. retries the SAME turn once with no new input — the user message is already
//     in the durable history, so it is never appended twice and executed tools
//     are not replayed (their recorded results are part of the retried context);
//  3. records a durable `context_recovery` event (before/after tokens, retry
//     count, outcome) so the UI/log can explain what happened.
//
// A failed recovery keeps the run failed: goal continuation only runs after a
// successful run, so an overflow never silently resumes the goal.

import (
	"context"

	rig "github.com/zeokeel/zeokeel-rig"
	rigagent "github.com/zeokeel/zeokeel-rig/agent"
	"github.com/zeokeel/zeokeel-rig/session/compact"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// contextRecoveryEventType is the durable context-overflow recovery event.
const contextRecoveryEventType = "context_recovery"

// contextRecoveryKeepLast is how many recent messages the recovery compaction
// keeps verbatim (the rest is summarized); it must keep the current user turn.
const contextRecoveryKeepLast = 6

// isContextOverflowError reports whether a run failed because the request
// exceeded the model context window.
func isContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	return contracts.ClassifyError(err, err.Error()) == contracts.MessageErrorKindContext
}

// retryContextOverflow compacts the history once and retries the same turn. It
// returns retried=false when the recovery could not even attempt the retry (the
// caller then keeps the original failure); retried=true means result/err are the
// retry's outcome (a second overflow still fails, exactly once).
func (s *service) retryContextOverflow(ctx context.Context, sessionID string, cfg contracts.ChatConfig,
	spec rig.AgentSpec, provider rig.Provider, emit func(rig.Event), usage *turnUsage,
	firstResult rig.RunResult) (rig.RunResult, error, bool) {
	if s.sessionDeleted(sessionID) {
		return rig.RunResult{}, nil, false
	}
	history := s.historyView()
	messages, err := history.Load(ctx, sessionID)
	if err != nil {
		s.recordContextRecovery(ctx, sessionID, 0, 0, "failed", err)
		return rig.RunResult{}, nil, false
	}
	before := rig.EstimateTokens(messages)
	budget := s.effectiveContextBudget(cfg) - rig.EstimateTextTokens(spec.SystemPrompt)
	if budget <= 0 {
		if effective := s.effectiveContextBudget(cfg); effective > 1 {
			budget = effective / 2
		} else {
			budget = 1024
		}
	}
	compactor := compact.LLMCompactor{
		Provider: provider,
		Model:    cfg.Model,
		KeepLast: contextRecoveryKeepLast,
		// A failed/empty summary falls back to deterministic window compaction:
		// recovery must never depend on one more model call succeeding.
		Fallback: compact.WindowCompactor{},
	}
	compacted, compactErr := compactor.Compact(ctx, sessionID, messages, budget)
	if compactErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return rig.RunResult{}, nil, false
		}
		s.recordContextRecovery(ctx, sessionID, before, before, "compact-failed", compactErr)
		return rig.RunResult{}, nil, false
	}
	// The retried turn must still see the current user message. The compactor
	// keeps the recent window (which includes it), but never rely on that for
	// correctness: put the last user message back if compaction dropped it.
	if lastUser, ok := lastUserMessageOf(messages); ok && !containsMessages(compacted, lastUser) {
		compacted = append(compacted, lastUser)
	}
	after := rig.EstimateTokens(compacted)
	// Fail-closed archive: recovery replaces history too, so preserve the raw
	// messages first (manual /compact has the same guarantee).
	if err := s.archiveReplacedHistory(ctx, sessionID, "recovery", messages, before, after, len(messages), len(compacted)); err != nil {
		s.recordContextRecovery(ctx, sessionID, before, after, "archive-failed", err)
		return rig.RunResult{}, nil, false
	}
	if err := s.replaceCompactedHistory(ctx, sessionID, compacted); err != nil {
		s.recordContextRecovery(ctx, sessionID, before, after, "failed", err)
		return rig.RunResult{}, nil, false
	}
	// Same turn, no new input: the durable history already carries the user
	// message and every executed tool result.
	retrySpec := spec
	// Keep the session tombstone guard: a deleted session must not accept the
	// retried turn's history write.
	retrySpec.History = s.guardedHistory(s.historyView())
	// The retry is a new runner: rebind it for tool_search and give it the
	// activations the session already carries (the failed attempt may have
	// activated tools before the overflow).
	retrySpec.InitialActiveTools = s.initialActiveTools(sessionID, retrySpec.Tools)
	runner, err := rigagent.New(retrySpec)
	if err != nil {
		s.recordContextRecovery(ctx, sessionID, before, after, "failed", err)
		return rig.RunResult{}, nil, false
	}
	s.bindSessionRunner(sessionID, runner)
	retryResult, retryErr := runner.Run(ctx, rig.RunRequest{SessionID: sessionID, Emit: emit})
	if retryErr != nil {
		s.recordContextRecovery(ctx, sessionID, before, after, "failed", retryErr)
		return retryResult, retryErr, true
	}
	// Keep the failed attempt's billed usage in the turn totals.
	retryResult.Usage.Add(firstResult.Usage)
	retryResult.Iterations += firstResult.Iterations
	s.recordContextRecovery(ctx, sessionID, before, after, "success", nil)
	return retryResult, nil, true
}

// recordContextRecovery writes the durable recovery audit event (and folds it
// into the context timeline).
func (s *service) recordContextRecovery(ctx context.Context, sessionID string, before, after int, outcome string, runErr error) {
	payload := compactPayload{
		Kind:   "context-recovery",
		Before: before,
		After:  after,
		Saved:  1, // retry count
	}
	if runErr != nil {
		payload.Kind = "context-recovery-failed:" + outcome
	}
	s.appendObservedEvent(ctx, sessionID, contextRecoveryEventType, payload)
	if runErr != nil {
		s.logger.Printf("[agent] session %s: context recovery %s (%d -> %d tokens): %v", sessionID, outcome, before, after, runErr)
	} else {
		s.logger.Printf("[agent] session %s: context recovery compacted %d -> %d tokens and retried the turn", sessionID, before, after)
	}
}

// lastUserMessageOf returns the last user-role message of a history.
func lastUserMessageOf(messages []rig.Message) (rig.Message, bool) {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == rig.RoleUser {
			return messages[index], true
		}
	}
	return rig.Message{}, false
}

// containsMessages reports whether candidate already appears in messages (a
// tool-call id and text comparison is enough for the recovery guard).
func containsMessages(messages []rig.Message, candidate rig.Message) bool {
	for _, message := range messages {
		if message.Role != candidate.Role || message.ToolCallID != candidate.ToolCallID {
			continue
		}
		if messageText(message) == messageText(candidate) {
			return true
		}
	}
	return false
}
