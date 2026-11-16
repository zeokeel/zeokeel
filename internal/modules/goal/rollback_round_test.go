package goal

// BE-10 (2026-11 audit): AdmitRound persists RoundsStarted+1 BEFORE the driver
// starts the next run; when that send failed (a concurrent human Send, a model
// unconfigured mid-flight) the round stayed consumed — the goal burned a round
// on a turn that never ran, with no rollback anywhere. RollbackRound refunds
// exactly the admitted round when nothing else advanced the goal, and persists
// the refund so a restart agrees.

import (
	"context"
	"errors"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestRollbackRoundRefundsAdmittedRound(t *testing.T) {
	store := newFakeEventStore()
	service := newTestService(store)
	mustCreate(t, service, "sess-1", "objective", 3)
	admitted, err := service.AdmitRound(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("AdmitRound: %v", err)
	}
	if admitted.RoundsStarted != 1 {
		t.Fatalf("admitted rounds = %d, want 1", admitted.RoundsStarted)
	}
	if err := service.RollbackRound(context.Background(), "sess-1", admitted.RoundsStarted); err != nil {
		t.Fatalf("RollbackRound: %v", err)
	}
	current, ok := service.Goal("sess-1")
	if !ok || current.RoundsStarted != 0 {
		t.Fatalf("goal after rollback = %+v, want rounds 0", current)
	}
	if current.Phase != contracts.GoalPhaseActive {
		t.Fatalf("phase after rollback = %q, want active", current.Phase)
	}
	// The refund is durable: a fresh service over the same store replays it.
	fresh := newTestService(store)
	replayed, ok := fresh.Goal("sess-1")
	if !ok || replayed.RoundsStarted != 0 {
		t.Fatalf("replayed goal = %+v, want rounds 0 (the refund must persist)", replayed)
	}
	// A refunded round can be admitted again.
	again, err := service.AdmitRound(context.Background(), "sess-1")
	if err != nil || again.RoundsStarted != 1 {
		t.Fatalf("AdmitRound after rollback = %+v/%v, want rounds 1", again, err)
	}
}

// A stale rollback (another round admitted meanwhile) must not refund the
// newer round.
func TestRollbackRoundStaleRoundIsNoOp(t *testing.T) {
	store := newFakeEventStore()
	service := newTestService(store)
	mustCreate(t, service, "sess-1", "objective", 3)
	first, _ := service.AdmitRound(context.Background(), "sess-1")
	if _, err := service.AdmitRound(context.Background(), "sess-1"); err != nil {
		t.Fatalf("AdmitRound 2: %v", err)
	}
	if err := service.RollbackRound(context.Background(), "sess-1", first.RoundsStarted); err != nil {
		t.Fatalf("RollbackRound(stale): %v", err)
	}
	current, _ := service.Goal("sess-1")
	if current.RoundsStarted != 2 {
		t.Fatalf("rounds after stale rollback = %d, want 2 (no-op)", current.RoundsStarted)
	}
}

// A goal that moved on (blocked at the cap) is never refunded: the blocked
// transition is the durable truth.
func TestRollbackRoundBlockedGoalIsNoOp(t *testing.T) {
	store := newFakeEventStore()
	service := newTestService(store)
	mustCreate(t, service, "sess-1", "objective", 1)
	admitted, _ := service.AdmitRound(context.Background(), "sess-1")
	if _, err := service.AdmitRound(context.Background(), "sess-1"); !errors.Is(err, contracts.ErrGoalRoundLimit) {
		t.Fatalf("AdmitRound over cap = %v, want ErrGoalRoundLimit", err)
	}
	if err := service.RollbackRound(context.Background(), "sess-1", admitted.RoundsStarted); err != nil {
		t.Fatalf("RollbackRound(blocked): %v", err)
	}
	current, _ := service.Goal("sess-1")
	if current.Phase != contracts.GoalPhaseBlocked || current.RoundsStarted != 1 {
		t.Fatalf("goal after rollback on blocked = %+v, want blocked rounds 1", current)
	}
}

func TestRollbackRoundUnknownGoal(t *testing.T) {
	service := newTestService(newFakeEventStore())
	if err := service.RollbackRound(context.Background(), "sess-none", 1); !errors.Is(err, contracts.ErrGoalNotFound) {
		t.Fatalf("RollbackRound(no goal) = %v, want ErrGoalNotFound", err)
	}
}
