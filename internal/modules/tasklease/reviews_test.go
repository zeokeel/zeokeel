package tasklease_test

// The review log (contracts.TaskReview): accept and reject BOTH record a row,
// the row outlives the lease the decision deletes, and the reviewer may not be
// the executor.
//
// Why the rule is role-shaped: this product has one human, so "somebody else
// reviewed it" can only mean "a different role reviewed it". The lease owner is
// the executing identity; a review from that identity would be a self-audit
// recorded as an audit — worse than no audit, because it looks like one.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/tasklease"
)

// reviewedLease drives one task to the review state.
func reviewedLease(t *testing.T, svc *tasklease.Service, owner string) {
	t.Helper()
	ctx := context.Background()
	if _, err := svc.Claim(ctx, "task-1", owner); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := svc.Submit(ctx, "task-1", owner, []contracts.Evidence{{Kind: "link", Ref: "https://x"}}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
}

func TestAcceptRecordsAPassingReview(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, clock := newService(t, board, nil)
	reviewedLease(t, svc, "executor")

	if _, err := svc.Accept(context.Background(), "task-1", contracts.PresetAuditorID, "证据齐全"); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	reviews, err := svc.Reviews(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("Reviews: %v", err)
	}
	if len(reviews) != 1 {
		t.Fatalf("reviews = %v, want one", reviews)
	}
	got := reviews[0]
	if got.TaskID != "task-1" || got.Reviewer != contracts.PresetAuditorID ||
		got.Role != contracts.PresetAuditorID || got.Decision != contracts.TaskReviewAccept ||
		got.Verdict != contracts.TaskVerdictPass || got.Findings != "证据齐全" {
		t.Fatalf("review = %+v, want a pass by the auditor", got)
	}
	if !got.At.Equal(clock.now()) {
		t.Fatalf("review At = %v, want the service clock", got.At)
	}
}

func TestRejectRecordsAFailingReviewWithItsReason(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	reviewedLease(t, svc, "executor")

	if _, err := svc.Reject(context.Background(), "task-1", "lead", "缺来源"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	reviews, err := svc.Reviews(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("Reviews: %v", err)
	}
	if len(reviews) != 1 {
		t.Fatalf("reviews = %v, want one", reviews)
	}
	got := reviews[0]
	// A lead is a role too, and the reason is kept: a task that came back to the
	// pool with no explanation looks like a bug to the next owner.
	if got.Decision != contracts.TaskReviewReject || got.Verdict != contracts.TaskVerdictFail ||
		got.Findings != "缺来源" || got.Role != contracts.PresetLeadID {
		t.Fatalf("review = %+v, want a fail by the lead with the reason", got)
	}
}

// TestReviewerMayNotBeTheExecutor is the constraint, including its case: "Lead"
// and "lead" are one role, and letting the difference through would re-open
// self-review by spelling.
func TestReviewerMayNotBeTheExecutor(t *testing.T) {
	for _, reviewer := range []string{"executor", "EXECUTOR", " executor "} {
		board := newFakeBoard("task-1")
		svc, _ := newService(t, board, nil)
		reviewedLease(t, svc, contracts.PresetExecutorID)

		if _, err := svc.Accept(context.Background(), "task-1", reviewer, "self review"); !errors.Is(err, contracts.ErrLeaseReviewerIsOwner) {
			t.Fatalf("Accept as %q err = %v, want ErrLeaseReviewerIsOwner", reviewer, err)
		}
		if _, err := svc.Reject(context.Background(), "task-1", reviewer, "self review"); !errors.Is(err, contracts.ErrLeaseReviewerIsOwner) {
			t.Fatalf("Reject as %q err = %v, want ErrLeaseReviewerIsOwner", reviewer, err)
		}
		// A refused review changes nothing: the claim is still in review.
		if lease, ok, err := svc.Lease(context.Background(), "task-1"); err != nil || !ok || lease.State != contracts.LeaseStateReview {
			t.Fatalf("lease after refused review = (%+v, %v, %v), want still in review", lease, ok, err)
		}
		if reviews, err := svc.Reviews(context.Background(), "task-1"); err != nil || len(reviews) != 0 {
			t.Fatalf("reviews after refused review = (%v, %v), want none", reviews, err)
		}
	}
}

// TestReviewsSurviveRestart: the whole point of the log. The lease is dropped by
// the decision, so a restart is exactly when the trail has to still be there.
func TestReviewsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	board := newFakeBoard("task-1")
	clock := newClock()
	cfg := tasklease.Config{Now: clock.now, TTL: time.Minute}
	ctx := context.Background()

	first, err := tasklease.Open(dir, board, cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	reviewedLease(t, first, contracts.PresetExecutorID)
	if _, err := first.Reject(ctx, "task-1", contracts.PresetAuditorID, "缺来源"); err != nil {
		t.Fatalf("Reject: %v", err)
	}

	second, err := tasklease.Open(dir, board, cfg)
	if err != nil {
		t.Fatalf("Open after reject: %v", err)
	}
	if lease, ok, err := second.Lease(ctx, "task-1"); err != nil || ok {
		t.Fatalf("lease after reopen = (%+v, %v, %v), want gone", lease, ok, err)
	}
	reviews, err := second.Reviews(ctx, "task-1")
	if err != nil {
		t.Fatalf("Reviews after reopen: %v", err)
	}
	if len(reviews) != 1 || reviews[0].Verdict != contracts.TaskVerdictFail || reviews[0].Findings != "缺来源" {
		t.Fatalf("reviews after reopen = %+v, want the recorded rejection", reviews)
	}
}

// TestReviewsOfAnUnknownTaskAreEmpty: "never reviewed" and "no such task" are
// both "nothing to show", and neither is an error the UI must handle.
func TestReviewsOfAnUnknownTaskAreEmpty(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	reviews, err := svc.Reviews(context.Background(), "missing")
	if err != nil || len(reviews) != 0 {
		t.Fatalf("Reviews(missing) = (%v, %v), want empty", reviews, err)
	}
}

// TestDirectCloseRecordsAnUnauditedRow is the 2026-11-26 audit's 高 finding: a task
// could be completed by `task.status` (or the model's team_task_update) with no
// TaskReview at all, so "reviewer ≠ owner, every task leaves a record" was
// bypassable by the very chip the client offers. The close is still allowed — it
// is recorded as what it is: unaudited, with the lease state it skipped.
func TestDirectCloseRecordsAnUnauditedRow(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, clock := newService(t, board, nil)
	reviewedLease(t, svc, contracts.PresetExecutorID)

	if err := svc.RecordDirectClose(context.Background(), "task-1", "user", "tasks.status"); err != nil {
		t.Fatalf("RecordDirectClose: %v", err)
	}
	reviews, err := svc.Reviews(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("Reviews: %v", err)
	}
	if len(reviews) != 1 {
		t.Fatalf("reviews = %v, want one", reviews)
	}
	got := reviews[0]
	if got.Decision != contracts.TaskReviewDirectClose {
		t.Fatalf("decision = %q, want %q", got.Decision, contracts.TaskReviewDirectClose)
	}
	// The verdict must not claim a pass: nobody reviewed this.
	if got.Verdict != contracts.TaskVerdictUnaudited {
		t.Fatalf("verdict = %q, want %q (an unaudited close is not a pass)", got.Verdict, contracts.TaskVerdictUnaudited)
	}
	for _, want := range []string{"no lease review", "tasks.status", "review"} {
		if !strings.Contains(got.Findings, want) {
			t.Fatalf("findings %q is missing %q", got.Findings, want)
		}
	}
	if got.Reviewer != "user" || got.TaskID != "task-1" {
		t.Fatalf("row = %+v, want the user's close of task-1", got)
	}
	if !got.At.Equal(clock.now()) {
		t.Fatalf("review At = %v, want the service clock", got.At)
	}

	// The same close arriving twice (a double click, or the same close through two
	// paths) does not double the log.
	if err := svc.RecordDirectClose(context.Background(), "task-1", "user", "tasks.status"); err != nil {
		t.Fatalf("RecordDirectClose again: %v", err)
	}
	if reviews, _ := svc.Reviews(context.Background(), "task-1"); len(reviews) != 1 {
		t.Fatalf("reviews after a repeated close = %v, want the log to stay one row", reviews)
	}
	// A different entry point IS a different fact and is recorded.
	if err := svc.RecordDirectClose(context.Background(), "task-1", "agent", "team_task_update"); err != nil {
		t.Fatalf("RecordDirectClose from the tool: %v", err)
	}
	if reviews, _ := svc.Reviews(context.Background(), "task-1"); len(reviews) != 2 {
		t.Fatalf("reviews after a second entry point = %v, want two rows", reviews)
	}
}

// TestDirectCloseNeedsATaskID: an empty id is a caller bug, not a global record.
func TestDirectCloseNeedsATaskID(t *testing.T) {
	svc, _ := newService(t, newFakeBoard("task-1"), nil)
	if err := svc.RecordDirectClose(context.Background(), "  ", "user", "tasks.status"); !errors.Is(err, contracts.ErrLeaseInvalid) {
		t.Fatalf("empty id error = %v, want ErrLeaseInvalid", err)
	}
}
