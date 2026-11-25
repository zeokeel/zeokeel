package tasklease_test

// TDD for the per-task budget stop (G11). What matters here is not accounting —
// this layer never sees a model call — but the two decisions it owns:
//
//   - crossing a ceiling must stop the work, not warn about it: the claim parks
//     in blocked and Submit is refused until somebody re-budgets the task;
//   - the stop must survive a restart and must not be cleared by the owner's own
//     retry, because the plan says the Lead decides (追加还是收尾).
//
// The tempting shortcuts are covered too: a zero limit never blocks, exactly
// hitting the ceiling is not crossing it, a negative delta cannot walk the meter
// back, and adding budget keeps the usage that already happened.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/tasklease"
)

func TestBudgetSetAndGet(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	// Nothing was ever set: absent, not zeroed.
	if _, ok, err := svc.Budget(ctx, "task-1"); err != nil || ok {
		t.Fatalf("Budget(before set) = (ok=%v, err=%v), want absent", ok, err)
	}

	stored, err := svc.SetBudget(ctx, "task-1", contracts.TaskBudget{MaxTokens: 1000, MaxMinutes: 30})
	if err != nil {
		t.Fatalf("SetBudget: %v", err)
	}
	if stored.TaskID != "task-1" || stored.MaxTokens != 1000 || stored.MaxMinutes != 30 {
		t.Fatalf("stored = %+v, want task-1 1000/30", stored)
	}
	if stored.UsedTokens != 0 || stored.Exhausted {
		t.Fatalf("stored = %+v, want a fresh meter", stored)
	}

	read, ok, err := svc.Budget(ctx, "task-1")
	if err != nil || !ok {
		t.Fatalf("Budget = (ok=%v, err=%v), want the row", ok, err)
	}
	if read.MaxTokens != 1000 || read.MaxMinutes != 30 {
		t.Fatalf("Budget = %+v, want 1000/30", read)
	}

	// Replacing the ceiling keeps the meter: that is the whole point of a
	// per-task budget surviving a re-budget.
	if _, err := svc.Consume(ctx, "task-1", 400); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	raised, err := svc.SetBudget(ctx, "task-1", contracts.TaskBudget{MaxTokens: 5000})
	if err != nil {
		t.Fatalf("SetBudget(raise): %v", err)
	}
	if raised.UsedTokens != 400 {
		t.Fatalf("raised.UsedTokens = %d, want 400 (usage must not reset)", raised.UsedTokens)
	}
	if raised.MaxMinutes != 0 {
		t.Fatalf("raised.MaxMinutes = %d, want 0 (the ceiling is replaced, not merged)", raised.MaxMinutes)
	}
}

func TestConsumeAccumulates(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, clock := newService(t, board, nil)
	ctx := context.Background()

	start := clock.now()
	first, err := svc.Consume(ctx, "task-1", 120)
	if err != nil {
		t.Fatalf("Consume(first): %v", err)
	}
	if first.UsedTokens != 120 || first.Exhausted {
		t.Fatalf("first = %+v, want 120 used and not exhausted", first)
	}
	// StartedAt starts at the first reported usage, not at claim time.
	if !first.StartedAt.Equal(start) {
		t.Fatalf("StartedAt = %v, want %v", first.StartedAt, start)
	}
	clock.advance(5 * time.Minute)

	second, err := svc.Consume(ctx, "task-1", 80)
	if err != nil {
		t.Fatalf("Consume(second): %v", err)
	}
	if second.UsedTokens != 200 {
		t.Fatalf("UsedTokens = %d, want 200", second.UsedTokens)
	}
	if !second.StartedAt.Equal(start) {
		t.Fatalf("StartedAt moved to %v, want %v", second.StartedAt, start)
	}
	// A zero-token consume is a legal re-check, not a reset.
	third, err := svc.Consume(ctx, "task-1", 0)
	if err != nil {
		t.Fatalf("Consume(0): %v", err)
	}
	if third.UsedTokens != 200 {
		t.Fatalf("UsedTokens after 0 = %d, want 200", third.UsedTokens)
	}
}

func TestConsumeCrossingLimitBlocksLeaseAndIsTyped(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	if _, err := svc.SetBudget(ctx, "task-1", contracts.TaskBudget{MaxTokens: 100}); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}
	if _, err := svc.Claim(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	// Exactly at the ceiling is not over it.
	at, err := svc.Consume(ctx, "task-1", 100)
	if err != nil {
		t.Fatalf("Consume(at limit): %v", err)
	}
	if at.Exhausted {
		t.Fatalf("at = %+v, want exactly-at-limit to be allowed", at)
	}

	over, err := svc.Consume(ctx, "task-1", 1)
	if !errors.Is(err, contracts.ErrLeaseBudgetExhausted) {
		t.Fatalf("Consume(over) error = %v, want ErrLeaseBudgetExhausted", err)
	}
	// The budget still comes back with the error: the caller must be able to show
	// used/limit without a second read.
	if over.UsedTokens != 101 || !over.Exhausted || over.MaxTokens != 100 {
		t.Fatalf("over = %+v, want 101/100 exhausted", over)
	}

	lease, ok, err := svc.Lease(ctx, "task-1")
	if err != nil || !ok {
		t.Fatalf("Lease = (ok=%v, err=%v), want the held claim", ok, err)
	}
	if lease.State != contracts.LeaseStateBlocked {
		t.Fatalf("lease.State = %q, want blocked", lease.State)
	}
	if lease.Owner != "bot-1" {
		t.Fatalf("lease.Owner = %q, want bot-1 (a stop keeps the owner)", lease.Owner)
	}
	// The board is untouched: blocked is a lease state; the plan's board statuses
	// do not grow a fourth meaning here.
	if status, owner := board.get("task-1"); status != contracts.TaskStatusInProgress || owner != "bot-1" {
		t.Fatalf("board = (%q,%q), want in_progress/bot-1", status, owner)
	}

	// The stop is not advisory: the owner cannot submit work as if nothing
	// happened, and the error is the typed budget one.
	_, err = svc.Submit(ctx, "task-1", "bot-1", []contracts.Evidence{{Kind: "link", Ref: "https://x"}})
	if !errors.Is(err, contracts.ErrLeaseBudgetExhausted) {
		t.Fatalf("Submit on blocked lease error = %v, want ErrLeaseBudgetExhausted", err)
	}

	// Heartbeat still works: the claim is held, not dropped, so nobody else
	// starts spending on a task that is waiting for a decision.
	if _, err := svc.Heartbeat(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("Heartbeat on blocked lease: %v", err)
	}
}

func TestUnlimitedBudgetNeverBlocks(t *testing.T) {
	board := newFakeBoard("task-1", "task-2")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	// A task with no budget row at all accumulates and never stops.
	unset, err := svc.Consume(ctx, "task-1", 5_000_000)
	if err != nil {
		t.Fatalf("Consume(no budget): %v", err)
	}
	if unset.Exhausted || unset.UsedTokens != 5_000_000 {
		t.Fatalf("unset = %+v, want unlimited accounting", unset)
	}

	// Explicit zeros mean unlimited too — the two spellings agree.
	if _, err := svc.SetBudget(ctx, "task-2", contracts.TaskBudget{}); err != nil {
		t.Fatalf("SetBudget(zero): %v", err)
	}
	if _, err := svc.Claim(ctx, "task-2", "bot-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := svc.Consume(ctx, "task-2", 9_000_000); err != nil {
		t.Fatalf("Consume(zero ceiling): %v", err)
	}
	lease, ok, err := svc.Lease(ctx, "task-2")
	if err != nil || !ok {
		t.Fatalf("Lease = (ok=%v, err=%v)", ok, err)
	}
	if lease.State != contracts.LeaseStateClaimed {
		t.Fatalf("lease.State = %q, want claimed (zero limit is unlimited)", lease.State)
	}
}

func TestMinutesBudgetBlocks(t *testing.T) {
	board := newFakeBoard("task-1")
	// A long TTL keeps the claim alive across the simulated 11 minutes: this test
	// is about the time budget, not about lease expiry.
	svc, clock := newService(t, board, func(cfg *tasklease.Config) { cfg.TTL = time.Hour })
	ctx := context.Background()

	if _, err := svc.SetBudget(ctx, "task-1", contracts.TaskBudget{MaxMinutes: 10}); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}
	if _, err := svc.Claim(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := svc.Consume(ctx, "task-1", 10); err != nil {
		t.Fatalf("Consume: %v", err)
	}

	clock.advance(9 * time.Minute)
	if row, err := svc.Consume(ctx, "task-1", 10); err != nil {
		t.Fatalf("Consume(inside window): %v", err)
	} else if row.Exhausted {
		t.Fatalf("row = %+v, want inside the window", row)
	}

	clock.advance(2 * time.Minute) // 11 minutes since first usage
	row, err := svc.Consume(ctx, "task-1", 0)
	if !errors.Is(err, contracts.ErrLeaseBudgetExhausted) {
		t.Fatalf("Consume(over minutes) error = %v, want ErrLeaseBudgetExhausted", err)
	}
	if !row.Exhausted {
		t.Fatalf("row = %+v, want exhausted", row)
	}
	lease, _, _ := svc.Lease(ctx, "task-1")
	if lease.State != contracts.LeaseStateBlocked {
		t.Fatalf("lease.State = %q, want blocked", lease.State)
	}
}

func TestBudgetSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	board := newFakeBoard("task-1")
	clock := newClock()
	cfg := tasklease.Config{Now: clock.now, TTL: time.Hour}
	ctx := context.Background()

	first := openLeaseStore(t, dir, board, cfg)
	if _, err := first.SetBudget(ctx, "task-1", contracts.TaskBudget{MaxTokens: 50}); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}
	if _, err := first.Claim(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := first.Consume(ctx, "task-1", 60); !errors.Is(err, contracts.ErrLeaseBudgetExhausted) {
		t.Fatalf("Consume: %v, want the typed stop", err)
	}

	// Reopen: the meter and the stop must both come back. A restart that forgets
	// "already over budget" would hand the same task a fresh allowance.
	second := openLeaseStore(t, dir, board, cfg)
	row, ok, err := second.Budget(ctx, "task-1")
	if err != nil || !ok {
		t.Fatalf("Budget after reopen = (ok=%v, err=%v)", ok, err)
	}
	if row.UsedTokens != 60 || row.MaxTokens != 50 || !row.Exhausted {
		t.Fatalf("row after reopen = %+v, want 60/50 exhausted", row)
	}
	lease, ok, err := second.Lease(ctx, "task-1")
	if err != nil || !ok {
		t.Fatalf("Lease after reopen = (ok=%v, err=%v)", ok, err)
	}
	if lease.State != contracts.LeaseStateBlocked {
		t.Fatalf("lease.State after reopen = %q, want blocked", lease.State)
	}
	// And the reopened service still refuses the submit.
	if _, err := second.Submit(ctx, "task-1", "bot-1", []contracts.Evidence{{Kind: "link", Ref: "https://x"}}); !errors.Is(err, contracts.ErrLeaseBudgetExhausted) {
		t.Fatalf("Submit after reopen = %v, want ErrLeaseBudgetExhausted", err)
	}
}

func TestBudgetRejectsNegativeInputs(t *testing.T) {
	board := newFakeBoard("task-1")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	for name, call := range map[string]func() error{
		"maxTokens": func() error {
			_, err := svc.SetBudget(ctx, "task-1", contracts.TaskBudget{MaxTokens: -1})
			return err
		},
		"maxMinutes": func() error {
			_, err := svc.SetBudget(ctx, "task-1", contracts.TaskBudget{MaxMinutes: -1})
			return err
		},
		"usedTokens": func() error {
			_, err := svc.SetBudget(ctx, "task-1", contracts.TaskBudget{UsedTokens: -1})
			return err
		},
		"consume": func() error {
			_, err := svc.Consume(ctx, "task-1", -5)
			return err
		},
		"empty task id": func() error {
			_, err := svc.SetBudget(ctx, "  ", contracts.TaskBudget{MaxTokens: 1})
			return err
		},
	} {
		if err := call(); !errors.Is(err, contracts.ErrLeaseInvalid) {
			t.Errorf("%s error = %v, want ErrLeaseInvalid", name, err)
		}
	}
	// Nothing was written by the rejected calls.
	if _, ok, _ := svc.Budget(ctx, "task-1"); ok {
		t.Fatal("a rejected SetBudget wrote a row")
	}
}

// TestSetBudgetResumesBlockedLease pins the Lead's half of the loop: the plan
// says the task waits for a decision, so adding budget must be what continues it
// — same claim, same meter, no release/re-claim dance — and a budget that is
// still too small must not unblock anything.
func TestSetBudgetResumesBlockedLease(t *testing.T) {
	board := newFakeBoard("task-1", "task-2")
	svc, _ := newService(t, board, nil)
	ctx := context.Background()

	if _, err := svc.SetBudget(ctx, "task-1", contracts.TaskBudget{MaxTokens: 100}); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}
	if _, err := svc.Claim(ctx, "task-1", "bot-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := svc.Consume(ctx, "task-1", 150); !errors.Is(err, contracts.ErrLeaseBudgetExhausted) {
		t.Fatalf("Consume: %v, want the stop", err)
	}

	// A ceiling that is still below the usage keeps the stop.
	kept, err := svc.SetBudget(ctx, "task-1", contracts.TaskBudget{MaxTokens: 120})
	if err != nil {
		t.Fatalf("SetBudget(still over): %v", err)
	}
	if !kept.Exhausted {
		t.Fatalf("still-over budget = %+v, want exhausted", kept)
	}
	lease, _, _ := svc.Lease(ctx, "task-1")
	if lease.State != contracts.LeaseStateBlocked {
		t.Fatalf("lease.State = %q, want still blocked", lease.State)
	}

	// Raising it above the usage resumes the same claim.
	raised, err := svc.SetBudget(ctx, "task-1", contracts.TaskBudget{MaxTokens: 400})
	if err != nil {
		t.Fatalf("SetBudget(raise): %v", err)
	}
	if raised.Exhausted || raised.UsedTokens != 150 {
		t.Fatalf("raised = %+v, want 150 used and not exhausted", raised)
	}
	lease, _, _ = svc.Lease(ctx, "task-1")
	if lease.State != contracts.LeaseStateClaimed || lease.Owner != "bot-1" {
		t.Fatalf("lease = %+v, want the same owner back to claimed", lease)
	}
	if _, err := svc.Submit(ctx, "task-1", "bot-1", []contracts.Evidence{{Kind: "link", Ref: "https://x"}}); err != nil {
		t.Fatalf("Submit after the Lead raised the budget: %v", err)
	}

	// A fresh claim on an exhausted task parks in blocked instead of starting a
	// new owner at full speed on a task the Lead has not re-budgeted.
	if _, err := svc.SetBudget(ctx, "task-2", contracts.TaskBudget{MaxTokens: 10}); err != nil {
		t.Fatalf("SetBudget(task-2): %v", err)
	}
	if _, err := svc.Consume(ctx, "task-2", 11); !errors.Is(err, contracts.ErrLeaseBudgetExhausted) {
		t.Fatalf("Consume(task-2) = %v, want the stop", err)
	}
	fresh, err := svc.Claim(ctx, "task-2", "bot-2")
	if err != nil {
		t.Fatalf("Claim(stopped task): %v", err)
	}
	if fresh.State != contracts.LeaseStateBlocked {
		t.Fatalf("fresh claim state = %q, want blocked", fresh.State)
	}
}
