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

// GoalPauser is the optional half of the goal bridge that can pause and resume
// the active goal (hosts that only implement GoalTracker still work).
type GoalPauser interface {
	// Pause marks the active goal paused and reports whether one existed.
	Pause() bool
	// Resume marks a paused goal active and reports whether one existed.
	Resume() bool
}

// KeyGoalTracker is provided by the desktop host.
var KeyGoalTracker = cordis.NewServiceKey[GoalTracker]("zeokeel/goal")

// Goal lifecycle statuses. Active goals drive auto-continue; paused goals stay
// visible but are not advanced; completed/abandoned goals are terminal until
// cleared or edited back to active.
const (
	GoalStatusActive    = "active"
	GoalStatusPaused    = "paused"
	GoalStatusCompleted = "completed"
	GoalStatusAbandoned = "abandoned"
)

// GoalLifecycler is the optional half of the goal bridge that completes,
// abandons and edits the current goal (hosts that only implement GoalTracker
// still work).
type GoalLifecycler interface {
	// Complete marks the active/paused goal completed and reports whether one
	// was in a completable state.
	Complete() bool
	// Abandon marks the active/paused goal abandoned and reports whether one
	// was in an abandonable state.
	Abandon() bool
	// Edit replaces the goal text (same ID), resets the status to active and
	// returns the goal ID. Empty text or no goal is an error.
	Edit(text string) (string, error)
}
