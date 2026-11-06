// Package hosttools defines the process-side capabilities that the product's
// host tools need, plus in-memory implementations of them.
//
// Not every agent tool is a pure function of its arguments: asking the user a
// question, presenting an artifact, reviewing a plan, tracking a goal and
// managing team members all need process state or a UI channel. Those
// capabilities belong to the product (they depend on the UI, the store and the
// process lifecycle), not to the ZeoKeel Rig toolset. This package is the seam:
//
//   - the interfaces describe what a host tool may require;
//   - the in-memory implementations make the tools useful — and testable —
//     without a window, a database or a real subagent process.
//
// A product with a UI replaces the memory capabilities with real ones; the
// host-tools kernel module only depends on the interfaces.
package hosttools

import (
	"context"
	"errors"
)

// ErrNoInteractive is returned by an Asker that has no interactive channel
// connected (headless products, tests, or a memory asker with no queued
// answers). Host tools turn it into a readable model-facing message.
var ErrNoInteractive = errors.New("no interactive UI")

// ErrUnknownMember is returned by a TeamManager when the member id is unknown.
var ErrUnknownMember = errors.New("unknown team member")

// Asker asks the user a question and waits for one answer. Implementations
// must respect ctx; a cancelled context returns ctx.Err().
//
// options, when non-empty, is a set of suggested answers; the returned answer
// is free text and need not be one of them.
type Asker interface {
	Ask(ctx context.Context, question string, options []string) (answer string, err error)
}

// AskerFunc adapts a function to Asker. A nil AskerFunc reports
// ErrNoInteractive.
type AskerFunc func(ctx context.Context, question string, options []string) (answer string, err error)

// Ask implements Asker.
func (f AskerFunc) Ask(ctx context.Context, question string, options []string) (string, error) {
	if f == nil {
		return "", ErrNoInteractive
	}
	return f(ctx, question, options)
}

// Presenter shows a finished artifact to the user and returns its artifact ID.
// The body is Markdown or plain text; the presenter decides how to render it.
type Presenter interface {
	Present(ctx context.Context, title, body string) (artifactID string, err error)
}

// PresenterFunc adapts a function to Presenter.
type PresenterFunc func(ctx context.Context, title, body string) (artifactID string, err error)

// Present implements Presenter.
func (f PresenterFunc) Present(ctx context.Context, title, body string) (string, error) {
	if f == nil {
		return "", errors.New("no presenter is connected")
	}
	return f(ctx, title, body)
}

// Plan mode states.
const (
	PlanIdle      = "idle"
	PlanReviewing = "reviewing"
	PlanApproved  = "approved"
	PlanCancelled = "cancelled"
)

// PlanController owns the session's plan-review state: the model submits a plan
// for review and the user (or the host on the user's behalf) approves or
// cancels it.
type PlanController interface {
	// State reports the current state (idle, reviewing, approved, cancelled).
	State() string
	// Review records content as the plan under review and returns the verdict
	// state (normally "reviewing").
	Review(content string) (verdict string, err error)
	// Exit applies one user decision: approve, cancel or status. It returns
	// the resulting (or current, for status) state.
	Exit(action string) (string, error)
}

// Goal status values. In-memory goals are always active until cleared.
const (
	GoalActive = "active"
)

// GoalStore holds the session's single goal (objective text).
type GoalStore interface {
	// Set creates the goal or replaces its text, returning the goal id.
	Set(text string) (id string, err error)
	// Get returns the goal id, text and status; ok is false when none is set.
	Get() (id, text, status string, ok bool)
	// Clear removes the goal and reports whether one existed.
	Clear() bool
}

// Team member status values.
const (
	TeamActive      = "active"
	TeamClosed      = "closed"
	TeamInterrupted = "interrupted"
)

// TeamMember is one spawned team member's read-only state.
type TeamMember struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// TeamManager tracks the session's team members. The in-memory implementation
// does not start subagents; a real product wires it to the agent runtime.
type TeamManager interface {
	// Spawn records a new member and returns its id.
	Spawn(name string) (id string, err error)
	// List returns a snapshot of the members in spawn order.
	List() []TeamMember
	// Close marks the member closed; unknown ids wrap ErrUnknownMember.
	Close(id string) error
	// Interrupt marks the member interrupted; unknown ids wrap ErrUnknownMember.
	Interrupt(id string) error
}

// Host bundles the process capabilities the host tools use. A nil member means
// that capability is unavailable in this product: the corresponding tool
// returns a readable model-facing error instead of failing activation.
type Host struct {
	Asker     Asker
	Presenter Presenter
	Plan      PlanController
	Goals     GoalStore
	Team      TeamManager
}

// NewMemoryHost returns a host whose capabilities are all in-memory: useful
// for tests and headless products, where state lives for the process lifetime
// and is never shown to a user.
func NewMemoryHost() *Host {
	return &Host{
		Asker:     NewMemoryAsker(),
		Presenter: NewMemoryPresenter(),
		Plan:      NewMemoryPlan(),
		Goals:     NewMemoryGoals(),
		Team:      NewMemoryTeam(),
	}
}
