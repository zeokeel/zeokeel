package contracts

import (
	"context"
	"errors"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// NewTask is the work the Lead asks the task board to create from one inbox
// item. It carries the fields the board already understands (subject, detail,
// dependencies, write scopes) plus the owner to assign.
type NewTask struct {
	Subject     string
	Detail      string
	BlockedBy   []string
	WriteScopes []string
	// Assignee is the teammate the Lead dispatched the task to ("" = unassigned).
	Assignee string
	// KnownAssignees is the set of teammate names the planner was allowed to
	// choose Assignee from — the assignees of the pass's own rules plus the role
	// names the product ships. The planner is the only party that knows them
	// (the rules live with the Lead), so it declares the envelope and the triage
	// service enforces it: an assignee outside the envelope is a name nobody can
	// act as, and it is refused instead of written to the board as an owner
	// (2026-11-26 audit: the model's assignee was never validated).
	//
	// An empty envelope means "role names only": the triage service still accepts
	// the built-in roles, so a caller that cannot enumerate its teammates does not
	// accidentally reject every dispatch.
	KnownAssignees []string
}

// TriageDecision is the Lead's decision about one inbox item. Action uses the
// inbox vocabulary; Task is required only for InboxActionTask.
type TriageDecision struct {
	Action string
	Task   *NewTask
	// Note is a short human-readable reason kept for the audit trail.
	Note string
}

// TriageResult reports what happened. Created is false when the decision was
// already applied (triage to the same task twice never creates a second task).
type TriageResult struct {
	Inbox   InboxItem
	TaskIDs []string
	Created bool
	// Dispatch reports the run the dispatcher started for this task, if any.
	// nil means "no run was expected here": the task was not assigned to the
	// lead role, or this assembly has no chat service to wake. A non-nil record
	// with a non-empty Error means the task was created and stored but the run
	// could not be started — the two outcomes must be distinguishable, because
	// "the work is on the board" stays true in both.
	Dispatch *TriageDispatch
}

// TriageDispatch is the wake-up record of one dispatched task.
type TriageDispatch struct {
	TaskID string
	// SessionID is the chat session the run was started in ("" when the session
	// could not be created).
	SessionID string
	// PresetID is the agent role the session runs as (the lead role).
	PresetID string
	// Error is why the run did not start ("" = it did).
	Error string
	// Skipped is why no run was started even though the decision asked for one,
	// when the reason is a policy decision rather than a failure: no budget to run
	// under, the dispatch concurrency ceiling, or an assignee nobody can act as.
	// A non-empty Skipped is not an error — the task exists and the board is
	// consistent — but it must be visible: "the task has an owner and nobody is
	// working on it" is exactly the state a user mistakes for progress.
	Skipped string
}

// TriageService is the only path from "something arrived" to "someone is
// working on it": it couples the inbox and the task board so the two facts can
// never disagree.
type TriageService interface {
	Apply(ctx context.Context, inboxID string, decision TriageDecision) (TriageResult, error)
}

// ErrTriageInvalid is returned when the decision is malformed (for example a
// task action without a task body).
var ErrTriageInvalid = errors.New("triage: invalid decision")

// KeyTriage is provided by the triage module.
var KeyTriage = cordis.NewServiceKey[TriageService]("zeokeel/triage")
