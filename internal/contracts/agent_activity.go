package contracts

import "time"

// Synchronous subagent activity, as shown by the inspector's Agents pane.
//
// The async team roster (contracts.TeamService) and the synchronous delegation
// tools (contracts.Subagents: subagent / subagent_fork) are different backends:
// a teammate lives in the rig team manager and can be steered or interrupted,
// while a synchronous delegation is one blocking call in the parent's run. The
// inspector lists both, but only teammates have controls. This file is the
// optional read-only half the agent module exposes for the second kind, so the
// UI can show a delegation while it runs and after it finishes.

// Subagent activity statuses.
const (
	// SubagentStatusRunning is a delegation that has started but not returned.
	SubagentStatusRunning = "running"
	// SubagentStatusDone is a delegation that returned its final answer.
	SubagentStatusDone = "done"
	// SubagentStatusError is a delegation that returned an error or failed.
	SubagentStatusError = "error"
	// SubagentStatusCancelled is a delegation whose context was cancelled.
	SubagentStatusCancelled = "cancelled"
)

// SubagentActivity is one synchronous delegation snapshot: a private copy, safe
// to read from any goroutine and to keep after the call returns.
type SubagentActivity struct {
	// ID is a process-unique activity ID (stable row identity, not the model
	// facing subagent name). It is unique even when two delegations share a
	// display name.
	ID string
	// Name is the display name of the delegation (sub-<n> by default).
	Name string
	// Status is one of the SubagentStatus* values.
	Status string
	// Started is when the delegation was registered.
	Started time.Time
	// Finished is when it returned; zero while running.
	Finished time.Time
	// SessionID is the parent conversation the delegation belongs to ("" when
	// the service had no active session at start).
	SessionID string
	// Model is the model the child uses (the request override or the parent
	// session model).
	Model string
	// Prompt is a bounded, whitespace-collapsed summary of the task.
	Prompt string
	// Detail is the bounded final answer, or the error/cancellation text; ""
	// while running.
	Detail string
	// Fork reports whether the delegation started from a copy of the parent
	// conversation history (subagent_fork) instead of fresh context.
	Fork bool
}

// SubagentActivityLister is the optional read-only surface of the synchronous
// delegation backend. The agent module's service implements it next to
// contracts.Subagents; the inspector discovers it with a type assertion and
// must keep working when the service or the lister is absent (nil means the
// pane has no subagent source). SubagentActivities returns copies: the caller
// may keep or mutate the slice without touching the registry.
type SubagentActivityLister interface {
	SubagentActivities() []SubagentActivity
}
