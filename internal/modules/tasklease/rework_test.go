package tasklease_test

// The automatic audit loop (M1): who may decide a review, how many rework
// rounds a task may take, and what happens when the ceiling is reached.
//
// Why the role rule is here and not only in the tool surface: the product
// enforces "the lead cannot write" by removing the tools, but a review is a
// decision the *lease* records, and the lease is the thing an audit trail is
// checked against. A build that forgot to hide the review tool (a preset edited
// by hand, a new entry point) would otherwise let the executor accept its own
// work without a single line changing. The role check makes the executor
// unable, not merely unequipped.
//
// Why the ceiling exists: an automatic loop that can reject forever is an
// automatic loop that can spend forever. At the ceiling the task stops being
// dispatched and becomes the human's problem, explicitly (ErrLeaseReworkExhausted)
// rather than by silence.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/tasklease"
)

// claimAndSubmit drives one task to the review state under an arbitrary owner.
func claimAndSubmit(t *testing.T, svc *tasklease.Service, taskID, owner string) {
	t.Helper()
	ctx := context.Background()
	if _, err := svc.Claim(ctx, taskID, owner); err != nil {
		t.Fatalf("Claim(%s): %v", taskID, err)
	}
	if _, err := svc.Submit(ctx, taskID, owner, []contracts.Evidence{{Kind: "output", Ref: "go test ./..."}}); err != nil {
		t.Fatalf("Submit(%s): %v", taskID, err)
	}
}

// TestExecutorRoleCannotReviewEvenAsAnotherIdentity: the identity differs (so
// the existing self-review guard would pass) but the ROLE is the executor's.
// That is still not an audit.
func TestExecutorRoleCannotReviewEvenAsAnotherIdentity(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	claimAndSubmit(t, svc, "task-1", "bot-1")

	if _, err := svc.Accept(context.Background(), "task-1", contracts.PresetExecutorID, "fine"); !errors.Is(err, contracts.ErrLeaseReviewerRoleMismatch) {
		t.Fatalf("Accept by the executor role err = %v, want ErrLeaseReviewerRoleMismatch", err)
	}
	if _, err := svc.Reject(context.Background(), "task-1", contracts.PresetExecutorID, "fine"); !errors.Is(err, contracts.ErrLeaseReviewerRoleMismatch) {
		t.Fatalf("Reject by the executor role err = %v, want ErrLeaseReviewerRoleMismatch", err)
	}
	// A refused review changes nothing: still in review, no rows, no rework.
	if lease, ok, err := svc.Lease(context.Background(), "task-1"); err != nil || !ok || lease.State != contracts.LeaseStateReview {
		t.Fatalf("lease after refused review = (%+v, %v, %v), want still in review", lease, ok, err)
	}
	if reviews, err := svc.Reviews(context.Background(), "task-1"); err != nil || len(reviews) != 0 {
		t.Fatalf("reviews after refused review = (%v, %v), want none", reviews, err)
	}
	if rework, err := svc.Rework(context.Background(), "task-1"); err != nil || rework.Rounds != 0 {
		t.Fatalf("rework after refused review = (%+v, %v), want zero rounds", rework, err)
	}
}

// TestAuditorAndHumanMayReview: the auditor role and a plain user identity are
// the two reviewers the loop accepts (the user is the escalation path).
func TestAuditorAndHumanMayReview(t *testing.T) {
	for _, reviewer := range []string{contracts.PresetAuditorID, "alice"} {
		board := newFakeBoard("task-1")
		svc, _ := newService(t, board, nil)
		claimAndSubmit(t, svc, "task-1", "bot-1")

		if _, err := svc.Accept(context.Background(), "task-1", reviewer, "ok"); err != nil {
			t.Fatalf("Accept by %q: %v", reviewer, err)
		}
		reviews, err := svc.Reviews(context.Background(), "task-1")
		if err != nil || len(reviews) != 1 {
			t.Fatalf("reviews = (%v, %v), want one", reviews, err)
		}
		if reviews[0].Reviewer != reviewer {
			t.Fatalf("reviewer = %q, want %q", reviews[0].Reviewer, reviewer)
		}
	}
}

// TestRejectCountsOneReworkRound: each rejection is one round of rework, and
// the policy ceiling defaults to contracts.DefaultMaxRework.
func TestRejectCountsOneReworkRound(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	claimAndSubmit(t, svc, "task-1", "bot-1")
	if _, err := svc.Reject(ctx, "task-1", contracts.PresetAuditorID, "缺来源"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	rework, err := svc.Rework(ctx, "task-1")
	if err != nil {
		t.Fatalf("Rework: %v", err)
	}
	if rework.Rounds != 1 || rework.Max != contracts.DefaultMaxRework || rework.Exhausted {
		t.Fatalf("rework = %+v, want 1/%d not exhausted", rework, contracts.DefaultMaxRework)
	}
	if got := board.statusOf("task-1"); got != contracts.TaskStatusPending {
		t.Fatalf("board status = %q, want pending after a rejection", got)
	}

	// The next claim is the same task, not a new one: the count must not reset.
	claimAndSubmit(t, svc, "task-1", "bot-1")
	if _, err := svc.Reject(ctx, "task-1", contracts.PresetAuditorID, "还是缺来源"); err != nil {
		t.Fatalf("second Reject: %v", err)
	}
	if rework, err = svc.Rework(ctx, "task-1"); err != nil || rework.Rounds != 2 {
		t.Fatalf("rework after two rejections = (%+v, %v), want 2", rework, err)
	}
}

// TestReworkCeilingStopsDispatchAndSubmit: at the ceiling the task is the
// human's, and both ways into the work refuse with the typed error.
func TestReworkCeilingStopsDispatchAndSubmit(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, func(c *tasklease.Config) { c.MaxRework = 2 })
	ctx := context.Background()

	for round := 0; round < 2; round++ {
		claimAndSubmit(t, svc, "task-1", "bot-1")
		if _, err := svc.Reject(ctx, "task-1", contracts.PresetAuditorID, "返工"); err != nil {
			t.Fatalf("Reject round %d: %v", round+1, err)
		}
	}
	rework, err := svc.Rework(ctx, "task-1")
	if err != nil || !rework.Exhausted || rework.Rounds != 2 {
		t.Fatalf("rework = (%+v, %v), want exhausted at 2", rework, err)
	}
	if _, err := svc.Claim(ctx, "task-1", "bot-1"); !errors.Is(err, contracts.ErrLeaseReworkExhausted) {
		t.Fatalf("Claim after the ceiling err = %v, want ErrLeaseReworkExhausted", err)
	}
	if _, err := svc.Submit(ctx, "task-1", "bot-1", []contracts.Evidence{{Kind: "link", Ref: "x"}}); !errors.Is(err, contracts.ErrLeaseReworkExhausted) {
		t.Fatalf("Submit after the ceiling err = %v, want ErrLeaseReworkExhausted", err)
	}
	if got := board.statusOf("task-1"); got != contracts.TaskStatusPending {
		t.Fatalf("board status = %q, want pending (the work stays open, it is just not automatic)", got)
	}
}

// TestResetReworkReleasesTheTask: escalation is a human decision, so a human
// must be able to make it — and only then does dispatch work again.
func TestResetReworkReleasesTheTask(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, func(c *tasklease.Config) { c.MaxRework = 1 })
	ctx := context.Background()

	claimAndSubmit(t, svc, "task-1", "bot-1")
	if _, err := svc.Reject(ctx, "task-1", contracts.PresetAuditorID, "返工"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if _, err := svc.Claim(ctx, "task-1", "bot-1"); !errors.Is(err, contracts.ErrLeaseReworkExhausted) {
		t.Fatalf("Claim err = %v, want ErrLeaseReworkExhausted", err)
	}
	if err := svc.ResetRework(ctx, "task-1"); err != nil {
		t.Fatalf("ResetRework: %v", err)
	}
	rework, err := svc.Rework(ctx, "task-1")
	if err != nil || rework.Rounds != 0 || rework.Exhausted {
		t.Fatalf("rework after reset = (%+v, %v), want 0 not exhausted", rework, err)
	}
	claimAndSubmit(t, svc, "task-1", "bot-1")
	if _, err := svc.Accept(ctx, "task-1", contracts.PresetAuditorID, "这次可以"); err != nil {
		t.Fatalf("Accept after reset: %v", err)
	}
}

// TestAcceptClearsRework: a finished task carries no debt into its next life.
func TestAcceptClearsRework(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	claimAndSubmit(t, svc, "task-1", "bot-1")
	if _, err := svc.Reject(ctx, "task-1", contracts.PresetAuditorID, "返工"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	claimAndSubmit(t, svc, "task-1", "bot-1")
	if _, err := svc.Accept(ctx, "task-1", contracts.PresetAuditorID, "验收通过"); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	rework, err := svc.Rework(ctx, "task-1")
	if err != nil || rework.Rounds != 0 {
		t.Fatalf("rework after accept = (%+v, %v), want 0", rework, err)
	}
}

// TestReworkSurvivesRestart: the count is what stops the loop; a restart must
// not hand a task a fresh budget of automatic rounds.
func TestReworkSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	board := newFakeBoard("task-1")
	clock := newClock()
	cfg := tasklease.Config{Now: clock.now, TTL: time.Minute}
	ctx := context.Background()

	first, err := tasklease.Open(dir, board, cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	claimAndSubmit(t, first, "task-1", "bot-1")
	if _, err := first.Reject(ctx, "task-1", contracts.PresetAuditorID, "返工"); err != nil {
		t.Fatalf("Reject: %v", err)
	}

	second, err := tasklease.Open(dir, board, cfg)
	if err != nil {
		t.Fatalf("Open after reject: %v", err)
	}
	rework, err := second.Rework(ctx, "task-1")
	if err != nil || rework.Rounds != 1 {
		t.Fatalf("rework after reopen = (%+v, %v), want 1", rework, err)
	}
}

// statusOf is the board row's status for one task (the fake board is shared with
// the package's other tests).
func (b *fakeBoard) statusOf(taskID string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status[taskID]
}
