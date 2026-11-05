package session

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/tools"
)

// TestGoalContinuableHonorsPhase pins the FP-04 driver rule: paused and blocked
// goals never schedule automatic continuation turns, while an active goal does.
func TestGoalContinuableHonorsPhase(t *testing.T) {
	active := &tools.Goal{Status: tools.GoalActive, Phase: tools.GoalPhaseActive, Objective: "ship it"}
	if !goalContinuable(active) {
		t.Error("active goal is not continuable")
	}
	// Older stored goals have no phase: the active status still makes them
	// continuable (backward compatibility).
	if !goalContinuable(&tools.Goal{Status: tools.GoalActive, Objective: "ship it"}) {
		t.Error("phase-less active goal is not continuable")
	}
	for _, phase := range []tools.GoalPhase{tools.GoalPhasePaused, tools.GoalPhaseBlocked} {
		if goalContinuable(&tools.Goal{Status: tools.GoalActive, Phase: phase, Objective: "ship it"}) {
			t.Errorf("goal in phase %q is continuable, want no automatic turns", phase)
		}
	}
	for _, status := range []tools.GoalStatus{tools.GoalCompleted, tools.GoalAbandoned} {
		if goalContinuable(&tools.Goal{Status: status, Objective: "ship it"}) {
			t.Errorf("goal with status %q is continuable", status)
		}
	}
	if goalContinuable(nil) {
		t.Error("nil goal is continuable")
	}
}
