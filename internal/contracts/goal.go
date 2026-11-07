package contracts

import (
	"context"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Goal is the active session goal set through the agent's goal tool (the old
// shell's goal tracking).
type Goal struct {
	// ID identifies the goal.
	ID string
	// Text is the goal description.
	Text string
	// Status is "active" (in-memory goals are always active until cleared).
	Status string
}

// GoalTracker bridges the agent's goal tool to the desktop UI: the host
// provides it, the session header displays the active goal and can clear it.
type GoalTracker interface {
	// SetHandler installs a change handler (nil clears it).
	SetHandler(fn func(goal Goal, ok bool))
	// Set creates or replaces the goal.
	Set(ctx context.Context, text string) (string, error)
	// Get returns the current goal (ok=false when none is set).
	Get() (id, text, status string, ok bool)
	// Clear removes the goal and reports whether one existed.
	Clear() bool
}

// KeyGoalTracker is provided by the desktop host.
var KeyGoalTracker = cordis.NewServiceKey[GoalTracker]("zeokeel/goal")
