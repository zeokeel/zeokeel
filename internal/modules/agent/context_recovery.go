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
)

// contextRecoveryEventType is the durable context-overflow recovery event.
const contextRecoveryEventType = "context_recovery"

// contextRecoveryKeepLast is how many recent messages the recovery compaction
// keeps verbatim (the rest is summarized); it must keep the current user turn.
const contextRecoveryKeepLast = 6

// contextRecoveryHooks adapts the SDK's context-overflow recovery to this
// product: the raw history is archived fail-closed before the compacted window
// replaces it, and the outcome feeds the durable context_recovery event.
func (s *service) contextRecoveryHooks() *rig.ContextRecovery {
	return &rig.ContextRecovery{
		BeforeReplace: func(ctx context.Context, sessionID string, raw, compacted []rig.Message) error {
			before := rig.EstimateTokens(raw)
			after := rig.EstimateTokens(compacted)
			return s.archiveReplacedHistory(ctx, sessionID, "recovery", raw, before, after, len(raw), len(compacted))
		},
		OnRecovery: func(ctx context.Context, sessionID string, before, after int, err error) {
			outcome := "success"
			if err != nil {
				outcome = "failed"
			}
			s.recordContextRecovery(ctx, sessionID, before, after, outcome, err)
		},
	}
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
