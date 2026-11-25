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
