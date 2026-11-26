package tasklease

// The automatic audit loop's accounting (M1).
//
// A dispatched task normally lives: claim → submit → review. When the review
// rejects, the work goes back to the pool and — because the loop is automatic —
// is dispatched again. That makes an unbounded rejection loop possible: each
// round is a paid run, and a task whose brief is wrong will be rejected forever
// while nobody is watching. This file is the counter that ends that possibility
// and the two operations around it:
//
//   - Reject bumps the round count; at the ceiling the task stops being claimed
//     or submitted (ErrLeaseReworkExhausted) instead of quietly spending again.
//   - ResetRework is the human's decision to give it another try, so escalation
//     is a state a person can leave rather than a dead end.
//
// The count lives beside the other task-scoped rows and is persisted in the same
// document, so a restart cannot hand a task a fresh budget of automatic rounds.

import (
	"context"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Rework implements the rework read model of one task.
func (s *Service) Rework(ctx context.Context, taskID string) (contracts.TaskRework, error) {
	if err := ctx.Err(); err != nil {
		return contracts.TaskRework{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reworkLocked(taskID), nil
}

// ResetRework clears the counter for one task: the human decided the task gets
// another attempt (a rewritten brief, a new owner, a different approach).
//
// It is idempotent and never fails on an unknown task: "make sure this task can
// be dispatched again" is a state, not an event, and a double click from the UI
// must not be an error.
func (s *Service) ResetRework(ctx context.Context, taskID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rework[taskID]; !ok {
		return nil
	}
	delete(s.rework, taskID)
	return s.persistLocked()
}

// reworkLocked is Rework with the lock already held.
func (s *Service) reworkLocked(taskID string) contracts.TaskRework {
	rounds := s.rework[taskID]
	max := s.cfg.MaxRework
	if max <= 0 {
		max = contracts.DefaultMaxRework
	}
	return contracts.TaskRework{TaskID: taskID, Rounds: rounds, Max: max, Exhausted: rounds >= max}
}

// reworkExhaustedLocked reports whether the task used every automatic round.
func (s *Service) reworkExhaustedLocked(taskID string) bool {
	return s.reworkLocked(taskID).Exhausted
}

// bumpReworkLocked records one rejection. The caller holds s.mu and persists
// afterwards, so the review row and the counter land in the same atomic write.
func (s *Service) bumpReworkLocked(taskID string) {
	if s.rework == nil {
		s.rework = make(map[string]int)
	}
	s.rework[taskID]++
}

// clearReworkLocked drops the counter of a finished task. The caller holds s.mu
// and persists afterwards.
func (s *Service) clearReworkLocked(taskID string) {
	delete(s.rework, taskID)
}

// reviewerRoleIsExecutor reports whether a review comes from the execution role.
//
// The comparison is normalized (trimmed, case-insensitive) for the same reason
// sameIdentity is: "Executor" must not become a third, unaudited role by
// spelling. Only the built-in role id counts — a human named "executor-2" is a
// user identity, not the role.
func reviewerRoleIsExecutor(reviewer string) bool {
	return reviewRole(normalizeReviewer(reviewer)) == contracts.PresetExecutorID
}

// normalizeReviewer is the identity form used for role lookup: trimmed and
// lower-cased, so " Auditor " resolves to the auditor role.
func normalizeReviewer(reviewer string) string {
	out := make([]rune, 0, len(reviewer))
	for _, r := range reviewer {
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			continue
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
		default:
			out = append(out, r)
		}
	}
	return string(out)
}
