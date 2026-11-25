package tasklease

// The pre-start approval gate (G2). A task may declare that its owner must be
// confirmed before the work begins; the gate is deliberately just a *state* on
// top of the lease layer, not a workflow engine:
//
//   - the policy is per task and sits beside the tool binding in the same
//     document (additive key, no version bump);
//   - a claim under `before_start` still claims — owner recorded, deadline set,
//     heartbeat accepted — it only parks in awaiting_approval instead of
//     claimed, so the task is held rather than lost;
//   - Submit is refused with ErrLeaseApprovalRequired while parked;
//   - Approve flips the same claim to claimed (no re-claim, no lost owner);
//   - RejectStart gives the work back (board pending, owner cleared) and
//     records the note, because a task that silently reappears in the pool looks
//     like a bug to the next owner.
//
// What this layer deliberately does not do: notify, escalate, expire an
// approval separately, or remember a decision. Expiry is the ordinary lease
// expiry (a parked claim with a dead owner must not pin the task forever), and
// the policy takes effect at the next claim rather than retroactively gating a
// lease that is already running.

import (
	"context"
	"fmt"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// SetApproval implements contracts.TaskLeaseService.
func (s *Service) SetApproval(ctx context.Context, taskID, policy string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(taskID) == "" {
		return "", contracts.ErrLeaseInvalid
	}
	normalized, ok := approvalPolicy(policy)
	if !ok {
		return "", fmt.Errorf("%w: approval policy %q (want %q or %q)",
			contracts.ErrLeaseInvalid, policy, contracts.TaskApprovalNone, contracts.TaskApprovalBeforeStart)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// The row is written even for an explicit `none`: "configured, needs
	// nothing" is a different answer from "never configured", and the reader
	// (tasks.approval.get) reports that difference as `set`.
	stored := &contracts.TaskApproval{TaskID: taskID, Policy: normalized, UpdatedAt: s.cfg.Now()}
	s.approvals[taskID] = stored
	if err := s.persistLocked(); err != nil {
		return "", err
	}
	return normalized, nil
}

// Approval implements contracts.TaskLeaseService.
func (s *Service) Approval(ctx context.Context, taskID string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if strings.TrimSpace(taskID) == "" {
		return "", false, contracts.ErrLeaseInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.approvals[taskID]
	if !ok {
		return contracts.TaskApprovalNone, false, nil
	}
	return row.Policy, true, nil
}

// Approve implements contracts.TaskLeaseService.
func (s *Service) Approve(ctx context.Context, taskID, approver string) (contracts.TaskLease, error) {
	if err := ctx.Err(); err != nil {
		return contracts.TaskLease{}, err
	}
	if strings.TrimSpace(taskID) == "" || strings.TrimSpace(approver) == "" {
		return contracts.TaskLease{}, contracts.ErrLeaseInvalid
	}
	now := s.cfg.Now()

	s.mu.Lock()
	lease, ok := s.leases[taskID]
	if !ok {
		s.mu.Unlock()
		return contracts.TaskLease{}, contracts.ErrLeaseNotHeld
	}
	if !now.Before(lease.Until) {
		// The parked claim outlived its window: drop it and hand the task back
		// instead of approving work nobody is holding any more.
		delete(s.leases, taskID)
		persistErr := s.persistLocked()
		s.mu.Unlock()
		s.releaseBoard(ctx, taskID)
		if persistErr != nil {
			return contracts.TaskLease{}, persistErr
		}
		return contracts.TaskLease{}, contracts.ErrLeaseNotHeld
	}
	if lease.State != contracts.LeaseStateAwaitingApproval {
		state := lease.State
		s.mu.Unlock()
		return contracts.TaskLease{}, fmt.Errorf("%w: lease is %s, not awaiting approval", contracts.ErrLeaseNotHeld, state)
	}
	// The owner gets a full window from the approval, not the remainder of the
	// window it may have spent waiting for the approver.
	lease.State = contracts.LeaseStateClaimed
	lease.Heartbeat = now
	lease.Until = now.Add(s.cfg.TTL)
	if err := s.persistLocked(); err != nil {
		s.mu.Unlock()
		return contracts.TaskLease{}, err
	}
	approved := cloneLease(lease)
	s.mu.Unlock()
	return approved, nil
}

// RejectStart implements contracts.TaskLeaseService.
func (s *Service) RejectStart(ctx context.Context, taskID, approver, note string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(taskID) == "" || strings.TrimSpace(approver) == "" || strings.TrimSpace(note) == "" {
		return contracts.ErrLeaseInvalid
	}
	now := s.cfg.Now()

	s.mu.Lock()
	lease, ok := s.leases[taskID]
	if !ok {
		s.mu.Unlock()
		return contracts.ErrLeaseNotHeld
	}
	if !now.Before(lease.Until) {
		delete(s.leases, taskID)
		persistErr := s.persistLocked()
		s.mu.Unlock()
		s.releaseBoard(ctx, taskID)
		if persistErr != nil {
			return persistErr
		}
		return contracts.ErrLeaseNotHeld
	}
	if lease.State != contracts.LeaseStateAwaitingApproval {
		state := lease.State
		s.mu.Unlock()
		return fmt.Errorf("%w: lease is %s, not awaiting approval", contracts.ErrLeaseNotHeld, state)
	}
	// One refusal row per task: the last refusal is what the next owner (and the
	// task screen) needs to know why the work came back.
	s.startRejections[taskID] = &contracts.TaskStartRejection{
		TaskID: taskID, Approver: approver, Note: note, At: now,
	}
	delete(s.leases, taskID)
	persistErr := s.persistLocked()
	s.mu.Unlock()

	// The board goes back to pending even when the document could not be
	// rewritten: a stale row is pruned on the next load, hidden work is not.
	s.releaseBoard(ctx, taskID)
	return persistErr
}

// approvalPolicy normalizes a requested policy. The alphabet is exact — no case
// folding, no aliases — because a policy that was silently reinterpreted is how
// an approval gate stops being a gate. Surrounding whitespace is tolerated
// (a JSON client that sends " before_start " means the same thing).
func approvalPolicy(value string) (string, bool) {
	switch strings.TrimSpace(value) {
	case contracts.TaskApprovalNone:
		return contracts.TaskApprovalNone, true
	case contracts.TaskApprovalBeforeStart:
		return contracts.TaskApprovalBeforeStart, true
	}
	return "", false
}

// stateForClaimLocked is where the gate lives: the one place a claim asks
// whether this task needs confirmation before it may start. Callers hold s.mu.
func (s *Service) stateForClaimLocked(taskID string) string {
	if row, ok := s.approvals[taskID]; ok && row.Policy == contracts.TaskApprovalBeforeStart {
		return contracts.LeaseStateAwaitingApproval
	}
	return contracts.LeaseStateClaimed
}
