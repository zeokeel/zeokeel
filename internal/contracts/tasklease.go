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

// TaskTools is the execution policy of one board task: the named tools its
// owner may run while working on it (M5). Names are the ones the tool surface
// already uses — `browser_open`, `mcp__server__tool` — so the binding is a
// subset of the tool catalog, not a second vocabulary.
//
// The binding belongs to the task, not to a lease: it is set when the task is
// dispatched, survives a release/re-claim (the policy of the work does not
// change because the owner did), and is cleared explicitly. A name that no
// longer exists in the catalog is still returned: the UI must be able to show
// what was promised instead of silently narrowing it.
type TaskTools struct {
	TaskID string
	// Tools is the allowed set, sorted by insertion order as the caller gave it;
	// empty (and nil) means "no binding", which is not the same as "no tools".
	Tools []string
	// UpdatedAt is when the binding last changed.
	UpdatedAt time.Time
}

// MaxTaskTools bounds one binding. An execution policy longer than this is a
// caller bug (a copy of the whole catalog) rather than a policy.
const MaxTaskTools = 32

// TaskToolNameMaxLen is the longest accepted tool name.
const TaskToolNameMaxLen = 120

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
	// SetTools binds the tools the task's owner may run (rebinding replaces the
	// previous set; an empty slice clears the binding). The task must exist on
	// the board — ErrTaskToolsUnknownTask otherwise — and the names must be a
	// valid tool list (ErrTaskToolsInvalid).
	SetTools(ctx context.Context, taskID string, tools []string) (TaskTools, error)
	// Tools reports a task's binding; false means the task has none.
	Tools(ctx context.Context, taskID string) (TaskTools, bool, error)
	// SetApproval sets the task's pre-start approval policy (G2) and returns
	// the stored value. Only TaskApprovalNone and TaskApprovalBeforeStart are
	// accepted; anything else is ErrLeaseInvalid. The policy takes effect at
	// the next claim — a lease already claimed is never retroactively gated.
	SetApproval(ctx context.Context, taskID, policy string) (string, error)
	// Approval reports a task's policy; false means the task carries no
	// explicit policy (which behaves exactly like TaskApprovalNone).
	Approval(ctx context.Context, taskID string) (string, bool, error)
	// Approve lets a parked claim start: awaiting_approval → claimed, keeping
	// the owner. A task with no parked claim is ErrLeaseNotHeld.
	Approve(ctx context.Context, taskID, approver string) (TaskLease, error)
	// RejectStart refuses a start: the claim is released (board pending, owner
	// cleared) and the refusal is recorded so the task can re-enter the pool.
	RejectStart(ctx context.Context, taskID, approver, note string) error
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
	// ErrTaskToolsInvalid means the requested tool binding is not a valid list:
	// an empty name, a duplicate, a name outside [a-z0-9_.:-], or more than
	// MaxTaskTools entries.
	ErrTaskToolsInvalid = errors.New("lease: invalid tool binding")
	// ErrTaskToolsUnknownTask means tools were bound to a task that is not on
	// the board (a dispatch bug, never a future task).
	ErrTaskToolsUnknownTask = errors.New("lease: unknown task for tool binding")
)

// DefaultLeaseTTL is the default claim lifetime; heartbeats extend it.
const DefaultLeaseTTL = 10 * time.Minute

// KeyTaskLease is provided by the task lease module.
var KeyTaskLease = cordis.NewServiceKey[TaskLeaseService]("zeokeel/tasklease")

// Pre-start approval policies (G2). A task may require a human/Lead
// confirmation before its owner is allowed to start. The gate is a state, not a
// workflow engine: `before_start` only changes where a claim parks (see
// LeaseStateAwaitingApproval) and refuses Submit until somebody approves.
const (
	// TaskApprovalNone means the task may start as soon as it is claimed. It is
	// also the default for a task that carries no policy at all.
	TaskApprovalNone = "none"
	// TaskApprovalBeforeStart parks a claim in awaiting_approval: the owner is
	// recorded and the heartbeat keeps the claim alive, but Submit is refused
	// with ErrLeaseApprovalRequired until Approve moves it to claimed.
	TaskApprovalBeforeStart = "before_start"
)

// LeaseStateAwaitingApproval is the parked state of a claim whose task requires
// approval before start. It is a live claim (owner, deadline, heartbeat), not a
// separate record: the work is held, it just may not begin.
const LeaseStateAwaitingApproval = "awaiting_approval"

// TaskApproval is the persisted pre-start approval policy of one board task.
// Like TaskTools it belongs to the task, not to a lease: releasing or refusing a
// claim does not erase the policy, because the policy is a property of the work.
type TaskApproval struct {
	TaskID string
	// Policy is one of the TaskApproval values. An unrecognized stored value is
	// read back as TaskApprovalBeforeStart (fail closed): a gate this build
	// cannot interpret must not be mistaken for "no gate".
	Policy string
	// UpdatedAt is when the policy last changed.
	UpdatedAt time.Time
}

// TaskStartRejection records why a claimed task was refused at the gate. The
// note is durable because a task that reappears in the pool with no explanation
// looks like a bug to the next owner.
type TaskStartRejection struct {
	TaskID   string
	Approver string
	Note     string
	At       time.Time
}

// ErrLeaseApprovalRequired means Submit arrived while the claim is still parked
// in awaiting_approval: the work has not been approved to start.
var ErrLeaseApprovalRequired = errors.New("lease: approval required before start")
