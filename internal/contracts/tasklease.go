package contracts

import (
	"context"
	"errors"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Lease states. A lease is the product-side answer to "who is working on this
// right now": the board keeps its three statuses, and the lease adds the
// time-bounded claim, the evidence and the review that a dispatched task needs.
const (
	LeaseStateClaimed  = "claimed"
	LeaseStateReview   = "review"
	LeaseStateDone     = "done"
	LeaseStateRejected = "rejected"
)

// Evidence is one proof a teammate attaches when submitting a task. It is what
// makes "done" reviewable instead of a claim.
type Evidence struct {
	// Kind is file / link / output / screenshot.
	Kind string
	// Ref is the path, URL or command output reference.
	Ref string
	// Note is one line of context.
	Note string
}

// TaskLease is the live claim on one board task.
type TaskLease struct {
	TaskID string
	Owner  string
	// Until is when the claim expires if no heartbeat arrives.
	Until time.Time
	// Heartbeat is the last time the owner proved it was alive.
	Heartbeat time.Time
	// State is one of the LeaseState values.
	State string
	// Evidence is what the owner submitted (kept through accept/reject).
	Evidence []Evidence
	// ReviewNote is the reviewer's reason on accept/reject.
	ReviewNote string
}

// TaskLeaseService is the time-bounded claim layer over the task board. It never
// replaces the board: every transition here also moves the board status, so the
// two views cannot disagree.
type TaskLeaseService interface {
	// Claim takes the task for owner and returns the lease (ErrLeaseHeld when a
	// live lease exists, ErrLeaseExpired when a stale one had to be dropped
	// first — in that case the caller may retry).
	Claim(ctx context.Context, taskID, owner string) (TaskLease, error)
	// Heartbeat extends a lease the caller still owns.
	Heartbeat(ctx context.Context, taskID, owner string) (TaskLease, error)
	// Release gives the task back (owner mismatch is an error).
	Release(ctx context.Context, taskID, owner string) error
	// Submit attaches evidence and moves the task to review.
	Submit(ctx context.Context, taskID, owner string, evidence []Evidence) (TaskLease, error)
	// Accept marks the task done.
	Accept(ctx context.Context, taskID, reviewer, note string) (TaskLease, error)
	// Reject sends the task back for another attempt.
	Reject(ctx context.Context, taskID, reviewer, reason string) (TaskLease, error)
	// Lease reports the current lease (false when there is none).
	Lease(ctx context.Context, taskID string) (TaskLease, bool, error)
	// Expire releases every lease whose deadline passed and returns their ids.
	Expire(ctx context.Context) ([]string, error)
}

// Lease errors are typed so the UI can say "someone else has it" instead of a
// generic failure.
var (
	// ErrLeaseHeld means a live lease already owns the task.
	ErrLeaseHeld = errors.New("lease: task already claimed")
	// ErrLeaseNotHeld means the caller does not own the task (or it expired).
	ErrLeaseNotHeld = errors.New("lease: not the lease owner")
	// ErrLeaseExpired means the previous lease had expired and was dropped.
	ErrLeaseExpired = errors.New("lease: previous lease expired")
	// ErrLeaseEvidenceRequired means Submit arrived without evidence.
	ErrLeaseEvidenceRequired = errors.New("lease: evidence required")
	// ErrLeaseInvalid means a required field was empty.
	ErrLeaseInvalid = errors.New("lease: invalid")
)

// DefaultLeaseTTL is the default claim lifetime; heartbeats extend it.
const DefaultLeaseTTL = 10 * time.Minute

// KeyTaskLease is provided by the task lease module.
var KeyTaskLease = cordis.NewServiceKey[TaskLeaseService]("zeokeel/tasklease")
