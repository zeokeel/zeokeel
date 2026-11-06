package session_test

import (
	"context"
	"testing"

	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/tools"
)

// TestGoalEditKeepsIdentityAndContinuationBudget is the session-level half of
// the /goal edit contract: editing the objective keeps the goal instance (id,
// phase, status) and is not treated as a new goal by the automatic
// continuation driver, so the already-spent round budget survives the edit.
func TestGoalEditKeepsIdentityAndContinuationBudget(t *testing.T) {
	provider := &goalDriverProvider{objective: "First objective"}
	h := newGoalDriverHarness(t, provider,
		session.NewGoalRoundDriver(session.GoalRoundDriverConfig{MaxRounds: 2}))
	ctx := context.Background()

	if err := h.ctrl.Submit(ctx, h.sessionID, "start"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitGoalRounds(t, h.ctrl, h.sessionID, 2)
	waitIdleAndDrain(t, h.ctrl)
	assertGoalRoundsStay(t, h.ctrl, h.sessionID, 2)

	first := h.ctrl.SessionGoal(h.sessionID)
	if first == nil {
		t.Fatal("SessionGoal = nil after the goal-driven turns")
	}

	// An active goal edited in place keeps its identity and does not reset the
	// exhausted continuation budget (a reset would allow surprise extra turns).
	edited, err := h.ctrl.GoalAction("edit", "Second objective", nil, nil)
	if err != nil {
		t.Fatalf("GoalAction(edit): %v", err)
	}
	if edited == nil || edited.ID != first.ID {
		t.Fatalf("edited goal = %+v, want the same id %q", edited, first.ID)
	}
	if edited.Objective != "Second objective" {
		t.Fatalf("edited objective = %q", edited.Objective)
	}
	if edited.Status != tools.GoalActive || edited.Phase != tools.GoalPhaseActive {
		t.Fatalf("edited status/phase = %v/%v, want active/active", edited.Status, edited.Phase)
	}
	if edited.Revision != first.Revision+1 {
		t.Fatalf("edited revision = %d, want %d", edited.Revision, first.Revision+1)
	}
	assertGoalRoundsStay(t, h.ctrl, h.sessionID, 2)

	// A paused goal stays paused across an edit: edit never re-arms
	// continuation.
	if _, err := h.ctrl.GoalAction("pause", "", nil, nil); err != nil {
		t.Fatalf("GoalAction(pause): %v", err)
	}
	paused := h.ctrl.SessionGoal(h.sessionID)
	edited, err = h.ctrl.GoalAction("edit", "Third objective", nil, nil)
	if err != nil {
		t.Fatalf("GoalAction(edit while paused): %v", err)
	}
	if edited == nil || edited.ID != first.ID || edited.Phase != tools.GoalPhasePaused {
		t.Fatalf("edited paused goal = %+v, want the same id still paused", edited)
	}
	if edited.Revision != paused.Revision+1 {
		t.Fatalf("edited paused revision = %d, want %d", edited.Revision, paused.Revision+1)
	}
	assertGoalRoundsStay(t, h.ctrl, h.sessionID, 2)

	// Editing without a current goal is a domain rejection, not a panic or a
	// silent create.
	if _, err := h.ctrl.GoalAction("clear", "", nil, nil); err != nil {
		t.Fatalf("GoalAction(clear): %v", err)
	}
	if _, err := h.ctrl.GoalAction("edit", "orphan", nil, nil); err == nil {
		t.Fatal("GoalAction(edit) without a goal was accepted")
	}
}
