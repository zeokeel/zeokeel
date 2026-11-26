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
	// SetBudget sets a task's budget (G11) and returns the stored row. The
	// limits come from the argument; the usage (UsedTokens/StartedAt) is kept
	// from the stored row unless the argument carries its own, so raising a
	// ceiling never resets the meter. Negative values are ErrLeaseInvalid and
	// zero means unlimited. Setting a budget is also how the Lead resumes a
	// stopped task: a blocked claim with headroom goes back to claimed, while a
	// budget that is still over the limit keeps the stop.
	SetBudget(ctx context.Context, taskID string, budget TaskBudget) (TaskBudget, error)
	// Budget reports a task's budget; false means none was ever set (or usage
	// was never reported, which behaves exactly like an unlimited budget).
	Budget(ctx context.Context, taskID string) (TaskBudget, bool, error)
	// Consume adds measured usage and applies the stop: when a limit is crossed
	// the row is marked Exhausted, the live lease moves to LeaseStateBlocked, and
	// the error is ErrLeaseBudgetExhausted — with the budget still returned, so
	// the caller can show used/limit. A task with no budget row accumulates
	// against an unlimited one: accounting may start before anyone sets a limit.
	Consume(ctx context.Context, taskID string, tokens int) (TaskBudget, error)
}

// TaskReviewLog is the optional half of the lease service that reports the
// recorded review decisions of a task, oldest first. It is a separate interface
// on purpose: reading the audit trail is a reporting capability, and a product
// that cannot report it still runs the full claim/review flow (the gateway
// answers the read as unavailable instead of pretending there are no reviews).
type TaskReviewLog interface {
	Reviews(ctx context.Context, taskID string) ([]TaskReview, error)
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
	// ErrLeaseReviewerIsOwner means the review came from the identity that
	// executed the task. In a single-user product the "reviewer ≠ executor"
	// rule cannot mean two natural persons; it means the *audit role* is not
	// the *execution role* (contracts.PresetAuditorID vs PresetExecutorID). The
	// lease owner is the executing identity, so reviewing your own work is
	// refused instead of being recorded as an audit.
	ErrLeaseReviewerIsOwner = errors.New("lease: reviewer is the executor")
)

// TaskReview is one recorded review decision (accept or reject). It outlives
// the lease: Accept/Reject drop the lease from the live table (the board keeps
// the outcome), and the historical write used to disappear with it — the
// rejection reason a teammate needs was gone the moment it was written. The
// review log is stored in the same document as the leases and is additive
// (older builds ignore the key), the same way TaskStartRejection is.
//
// Role is the reviewer's role (a preset id such as "lead" or "auditor", or ""
// when the reviewer is not role-shaped, e.g. the user's own name). Verdict is
// the machine-readable outcome — "pass" / "fail" — so an auditor can be checked
// independently of the free-form Findings text.
type TaskReview struct {
	TaskID string
	// Reviewer is who decided (an identity: the user, the lead, an auditor).
	Reviewer string
	// Role is the reviewer's role, when the identity has one.
	Role string
	// Decision is the review verb: "accept" or "reject".
	Decision string
	// Verdict is "pass" for an accept, "fail" for a reject, and "unaudited" for a
	// direct close that skipped the review (TaskReviewDirectClose).
	Verdict string
	// Findings is the note the reviewer gave (empty on an accept without one).
	Findings string
	// At is when the decision was recorded.
	At time.Time
}

// Review decisions and verdicts (TaskReview.Decision / TaskReview.Verdict).
const (
	TaskReviewAccept = "accept"
	TaskReviewReject = "reject"
	// TaskReviewDirectClose is the decision recorded when a task entered a
	// terminal state WITHOUT a lease review: `task.status` (the UI's
	// complete/stop chip) and `team_task_update` can move a task to completed
	// directly, and before this they left no audit row at all — so
	// "reviewer ≠ owner, every task leaves a record" could be bypassed by the
	// very button the UI offers (2026-11-26 audit, 高).
	//
	// The close is not refused (that would break the client): it is recorded as
	// what it is — a human/agent decision that skipped the review — and the UI
	// renders it as such.
	TaskReviewDirectClose = "direct_close"
	TaskVerdictPass       = "pass"
	TaskVerdictFail       = "fail"
	// TaskVerdictUnaudited marks a close nobody reviewed. It is deliberately not
	// "pass": a verdict is the machine-readable half of a review, and claiming a
	// pass for a task no reviewer looked at is exactly the dishonesty this row
	// exists to prevent.
	TaskVerdictUnaudited = "unaudited"
)

// TaskCloseAuditor is the write half of the "no unaudited terminal state" rule:
// every path that can move a task into completed records itself here.
type TaskCloseAuditor interface {
	// RecordDirectClose appends one unaudited-close row for taskID. actor is who
	// closed it (the user for the UI's chip, the agent for the model's tool) and
	// source names the entry point ("tasks.status", "team_task_update").
	RecordDirectClose(ctx context.Context, taskID, actor, source string) error
}

// TaskCloseObserverSetter is implemented by the task board so the lease module
// can install its auditor at activation. It is a setter rather than a required
// dependency because the board (toolkit) must not depend on the lease module:
// the lease requires the board, so the direction is one-way.
type TaskCloseObserverSetter interface {
	SetTaskCloseObserver(observer TaskCloseAuditor)
}

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

	// TaskApprovalBeforeSend means the task may start, but the message it wants
	// to send when it settles waits for a human: the text is held with the task
	// and only leaves the product after `tasks.send.approve`. It is the outbound
	// half of the same gate before_start puts at the door.
	TaskApprovalBeforeSend = "before_send"
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

// LeaseStateBlocked is the parked state of a claim that was stopped by its own
// budget (G11). It is a live claim like awaiting_approval — owner, deadline and
// heartbeat still hold the task — but the work may not be submitted: the task is
// waiting for the Lead to decide whether to add budget or wrap up. Resuming is
// SetBudget (the Lead's decision), never a retry by the owner.
const LeaseStateBlocked = "blocked"

// TaskBudget is the per-task cost ceiling (G11). The plan puts the shape of the
// limit here and the *measurement* elsewhere: whoever owns the model call reports
// usage through Consume, and this layer owns the only thing that cannot be
// delegated — the stop.
//
// Zero means unlimited on either axis. The two axes are deliberately joined in
// one row: "this task may spend 200k tokens or 30 minutes" is one decision, and
// splitting it would let a task pass one gate while failing the other.
type TaskBudget struct {
	TaskID string
	// MaxTokens is the token ceiling; 0 = unlimited.
	MaxTokens int
	// MaxMinutes is the wall-clock ceiling measured from StartedAt; 0 = unlimited.
	MaxMinutes int
	// UsedTokens is what the accounting source has reported so far. It is never
	// derived here: this layer does not see model calls, it only adds up what it
	// is told.
	UsedTokens int
	// StartedAt is when the first usage was reported — the clock MaxMinutes runs
	// on. A budget row with no usage yet has a zero StartedAt.
	StartedAt time.Time
	// Exhausted is the stop. It is sticky until somebody re-budgets the task
	// (SetBudget): a stop that clears itself on the next tick would be advice,
	// not a stop.
	Exhausted bool
}

// ErrLeaseBudgetExhausted means Consume crossed a task's budget. The budget is
// still returned with the error so the caller can show used/limit; the lease (if
// any) is already parked in LeaseStateBlocked by then.
var ErrLeaseBudgetExhausted = errors.New("lease: task budget exhausted")
