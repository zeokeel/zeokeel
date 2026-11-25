package tasklease

// Per-task budget (G11). Of the three halves the plan asks for (token budget,
// time budget, per-Bot concurrency) only the first two belong to this layer, and
// of those two only the *stop* is genuinely this layer's job:
//
//   - the budget row is per task and sits beside the tool binding and the
//     approval policy in the same document (additive key, no version bump), so
//     one atomic write covers a claim and the budget it runs under;
//   - the measurement is NOT here: this layer never sees a model call. Whoever
//     owns the tokens reports them through Consume, and this layer adds them up;
//   - crossing a limit parks the live claim in LeaseStateBlocked and refuses
//     Submit (ErrLeaseBudgetExhausted). It does not cancel, retry, shrink the
//     task or ask for more budget: the plan is explicit that a stopped task waits
//     for the Lead to decide (追加还是收尾), and an automatic resume would turn
//     the stop into a suggestion;
//   - resuming is SetBudget with headroom — the Lead's decision, recorded as a
//     new row value — after which the parked claim goes back to claimed.
//
// Zero means unlimited on either axis. That is why the limits are plain ints: an
// omitted `maxTokens` and an explicit `maxTokens:0` mean the same thing, and a
// reader can tell "no ceiling" from "ceiling of zero" without a pointer dance.
// There is deliberately no "ceiling of zero": a task that may spend nothing is
// cancelled, not budgeted.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// SetBudget implements contracts.TaskLeaseService.
func (s *Service) SetBudget(ctx context.Context, taskID string, budget contracts.TaskBudget) (contracts.TaskBudget, error) {
	if err := ctx.Err(); err != nil {
		return contracts.TaskBudget{}, err
	}
	if strings.TrimSpace(taskID) == "" {
		return contracts.TaskBudget{}, contracts.ErrLeaseInvalid
	}
	if budget.MaxTokens < 0 || budget.MaxMinutes < 0 || budget.UsedTokens < 0 {
		return contracts.TaskBudget{}, fmt.Errorf("%w: budget limits and usage must not be negative", contracts.ErrLeaseInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.cfg.Now()

	stored := contracts.TaskBudget{TaskID: taskID, MaxTokens: budget.MaxTokens, MaxMinutes: budget.MaxMinutes}
	if existing, ok := s.budgets[taskID]; ok {
		// Raising a ceiling must not reset the meter: the usage that already
		// happened is the reason the Lead is looking at this row.
		stored.UsedTokens = existing.UsedTokens
		stored.StartedAt = existing.StartedAt
	}
	if budget.UsedTokens > 0 {
		stored.UsedTokens = budget.UsedTokens
	}
	if !budget.StartedAt.IsZero() {
		stored.StartedAt = budget.StartedAt
	}
	// Exhausted is never taken from the caller: it is derived from usage versus
	// limits, so a request cannot claim "not exhausted" about a row that is.
	stored.Exhausted = budgetExhausted(stored, now)

	s.budgets[taskID] = &stored
	// The Lead added budget: a claim parked by the stop may continue from where
	// it is (same owner, same deadline) instead of forcing a release/re-claim.
	// The resumed state comes from the ordinary gate, so re-budgeting a task that
	// still needs approval lands back in awaiting_approval — adding budget is not
	// an approval — and a budget still over its limit keeps the stop. Only a live
	// claim is resumed: an expired one is not resurrected.
	if lease, ok := s.leases[taskID]; ok &&
		lease.State == contracts.LeaseStateBlocked && now.Before(lease.Until) {
		lease.State = s.stateForClaimLocked(taskID)
	}
	if err := s.persistLocked(); err != nil {
		return contracts.TaskBudget{}, err
	}
	return stored, nil
}

// Budget implements contracts.TaskLeaseService.
func (s *Service) Budget(ctx context.Context, taskID string) (contracts.TaskBudget, bool, error) {
	if err := ctx.Err(); err != nil {
		return contracts.TaskBudget{}, false, err
	}
	if strings.TrimSpace(taskID) == "" {
		return contracts.TaskBudget{}, false, contracts.ErrLeaseInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.budgets[taskID]
	if !ok {
		return contracts.TaskBudget{}, false, nil
	}
	return *row, true, nil
}

// Consume implements contracts.TaskLeaseService.
func (s *Service) Consume(ctx context.Context, taskID string, tokens int) (contracts.TaskBudget, error) {
	if err := ctx.Err(); err != nil {
		return contracts.TaskBudget{}, err
	}
	if strings.TrimSpace(taskID) == "" {
		return contracts.TaskBudget{}, contracts.ErrLeaseInvalid
	}
	if tokens < 0 {
		// Usage is a measurement, not a correction: accepting a negative delta
		// would let a caller walk a task back under its ceiling after the stop.
		return contracts.TaskBudget{}, fmt.Errorf("%w: tokens must not be negative", contracts.ErrLeaseInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.cfg.Now()

	row, ok := s.budgets[taskID]
	if !ok {
		// Accounting may start before anybody sets a ceiling: the row exists so
		// "used so far" is visible, and with both limits at zero it never stops.
		row = &contracts.TaskBudget{TaskID: taskID}
		s.budgets[taskID] = row
	}
	if row.StartedAt.IsZero() {
		// The clock for MaxMinutes starts at the first reported usage, not at
		// claim time: time spent waiting for an approval is not work spent.
		row.StartedAt = now
	}
	row.UsedTokens += tokens
	// Sticky: once the stop fired, only SetBudget clears it. Recomputing alone
	// would let a minutes budget un-stop itself on the tick after it crossed,
	// because "how long since StartedAt" keeps growing but a token-only reader
	// would see headroom.
	row.Exhausted = row.Exhausted || budgetExhausted(*row, now)

	if row.Exhausted {
		// The claim is parked, not dropped: the owner still holds the task (so
		// nobody else starts burning on it) and the Lead still sees who to talk
		// to. Submit refuses from here; Heartbeat keeps the held claim alive.
		if lease, ok := s.leases[taskID]; ok && blockable(lease.State) && now.Before(lease.Until) {
			lease.State = contracts.LeaseStateBlocked
		}
	}
	if err := s.persistLocked(); err != nil {
		return contracts.TaskBudget{}, err
	}
	consumed := *row
	if consumed.Exhausted {
		return consumed, fmt.Errorf("%w: %s used %d/%s tokens, %s/%s minutes",
			contracts.ErrLeaseBudgetExhausted, taskID,
			consumed.UsedTokens, limitText(consumed.MaxTokens, "unlimited"),
			minutesText(now.Sub(consumed.StartedAt)), limitText(consumed.MaxMinutes, "unlimited"))
	}
	return consumed, nil
}

// budgetExhausted applies the two ceilings to a row. Tokens cross on `>`, not
// `>=`: a task with a ceiling of 1000 that has spent exactly 1000 is at its
// limit, not past it, and stopping it early would make the last useful token
// unusable.
func budgetExhausted(row contracts.TaskBudget, now time.Time) bool {
	if row.MaxTokens > 0 && row.UsedTokens > row.MaxTokens {
		return true
	}
	if row.MaxMinutes > 0 && !row.StartedAt.IsZero() &&
		now.Sub(row.StartedAt) > time.Duration(row.MaxMinutes)*time.Minute {
		return true
	}
	return false
}

// blockable reports whether the stop may park this claim state. A lease already
// in review (or done/rejected) has stopped working by itself; flipping it to
// blocked would hide an evidence submission that already happened.
func blockable(state string) bool {
	return state == contracts.LeaseStateClaimed || state == contracts.LeaseStateAwaitingApproval
}

func limitText(value int, unlimited string) string {
	if value <= 0 {
		return unlimited
	}
	return fmt.Sprintf("%d", value)
}

func minutesText(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%d", int(d.Minutes()))
}
