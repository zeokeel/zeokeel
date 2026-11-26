package tasklease

// The review log (accept/reject history).
//
// Why this exists: Accept and Reject both drop the lease from the live table —
// that is correct, the board keeps the outcome and the claim is over — and the
// review note was stored *on the lease*. So the moment a reviewer wrote "缺来源，
// 请补出处" the string it lived in was deleted. A task that came back to the
// pool carried no explanation, and a task accepted months ago carried no audit
// at all. This file keeps the decision in its own row, in the same document, so
// it survives the lease and the restart.
//
// The row is additive in the store (older builds ignore the key, like the tool
// bindings and start refusals), and it is bounded per task: the log is an audit
// trail, not a transcript.

import (
	"context"
	"fmt"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

var _ contracts.TaskReviewLog = (*Service)(nil)

// maxReviewsPerTask bounds one task's review log. A task reviewed more than
// this many times is a review loop, and the oldest rows are the ones nobody
// reads any more; dropping them keeps the document from growing without a
// bound the way every other per-task row would.
const maxReviewsPerTask = 50

// Reviews implements contracts.TaskLeaseService: the recorded decisions of one
// task, oldest first.
func (s *Service) Reviews(ctx context.Context, taskID string) ([]contracts.TaskReview, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := s.reviews[strings.TrimSpace(taskID)]
	if len(rows) == 0 {
		return nil, nil
	}
	return append([]contracts.TaskReview(nil), rows...), nil
}

// recordReviewLocked appends one decision to the task's log. The caller holds
// s.mu; the caller persists afterwards, so the log and the lease state change
// in the same atomic write.
func (s *Service) recordReviewLocked(taskID, reviewer, decision, findings string) {
	verdict := contracts.TaskVerdictPass
	switch decision {
	case contracts.TaskReviewReject:
		verdict = contracts.TaskVerdictFail
	case contracts.TaskReviewDirectClose:
		verdict = contracts.TaskVerdictUnaudited
	}
	s.appendReviewLocked(taskID, reviewer, decision, verdict, findings)
}

// appendReviewLocked is recordReviewLocked with an explicit verdict: a direct
// close is not a review, so its verdict is "unaudited" rather than derived from
// the decision verb.
func (s *Service) appendReviewLocked(taskID, reviewer, decision, verdict, findings string) {
	reviewer = strings.TrimSpace(reviewer)
	if s.reviews == nil {
		s.reviews = make(map[string][]contracts.TaskReview)
	}
	log := append(s.reviews[taskID], contracts.TaskReview{
		TaskID:   taskID,
		Reviewer: reviewer,
		Role:     reviewRole(reviewer),
		Decision: decision,
		Verdict:  verdict,
		Findings: findings,
		At:       s.cfg.Now(),
	})
	if len(log) > maxReviewsPerTask {
		log = log[len(log)-maxReviewsPerTask:]
	}
	s.reviews[taskID] = log
}

var _ contracts.TaskCloseAuditor = (*Service)(nil)

// RecordDirectClose implements contracts.TaskCloseAuditor: it records that a task
// entered a terminal state without a lease review.
//
// The product does not refuse the write (the UI's complete/stop chip is a
// legitimate action), but it also must not let the action be invisible: before
// this, `task.status=completed` and `team_task_update` skipped TaskReview
// entirely, so "reviewer ≠ owner + a record for every task" could be bypassed by
// the button the client offers (2026-11-26 audit, 高). The row says what
// happened — who closed it, through which entry point, and which lease state was
// skipped — and carries Verdict "unaudited" so a reader cannot mistake it for a
// pass.
//
// Repeating the same close (a double click, or the same close arriving through
// two paths) does not append a second identical row: the newest row is compared
// first, and only a different actor/source/context adds another.
func (s *Service) RecordDirectClose(ctx context.Context, taskID, actor, source string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return contracts.ErrLeaseInvalid
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		// A close always has an author in practice (the chip is the user, the tool
		// is the model); an empty one is recorded as the product rather than
		// silently promoted to a role.
		actor = "user"
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "unknown"
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	findings := fmt.Sprintf("closed via %s, no lease review", source)
	if lease, ok := s.leases[taskID]; ok && strings.TrimSpace(lease.State) != "" {
		// The lease is the thing that was skipped: say so, including the case
		// where evidence was already submitted and waiting for a reviewer.
		findings += fmt.Sprintf(" (lease state: %s, evidence: %d)", lease.State, len(lease.Evidence))
	}
	if last := s.lastReviewLocked(taskID); last != nil &&
		last.Decision == contracts.TaskReviewDirectClose &&
		last.Reviewer == actor && last.Findings == findings {
		return nil
	}
	s.appendReviewLocked(taskID, actor, contracts.TaskReviewDirectClose, contracts.TaskVerdictUnaudited, findings)
	if err := s.persistLocked(); err != nil {
		return err
	}
	if s.cfg.OnDirectClose != nil {
		s.cfg.OnDirectClose(taskID, actor, source)
	}
	return nil
}

// lastReviewLocked returns the newest review row of a task (nil when none).
func (s *Service) lastReviewLocked(taskID string) *contracts.TaskReview {
	rows := s.reviews[taskID]
	if len(rows) == 0 {
		return nil
	}
	return &rows[len(rows)-1]
}

// reviewRole maps a reviewer identity to the role it acts as. Only the built-in
// role ids are roles; any other reviewer (the user's name, a bot slug) is an
// identity without a role, and "" says so instead of guessing.
func reviewRole(reviewer string) string {
	switch reviewer {
	case contracts.PresetLeadID, contracts.PresetExecutorID, contracts.PresetAuditorID:
		return reviewer
	}
	return ""
}

// sameIdentity reports whether two identities are the same actor. Comparison is
// case-insensitive and trimmed: "Lead" and "lead" are one role, and letting the
// difference through would quietly re-open self-review.
func sameIdentity(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
