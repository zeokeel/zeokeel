package session_test

import (
	"context"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/tools"
)

// fakeGoalStore records what the /goal API persists.
type fakeGoalStore struct {
	goals map[string]*tools.Goal
	saves int
}

func (s *fakeGoalStore) LoadGoal(sessionID string) (*tools.Goal, error) {
	return s.goals[sessionID], nil
}

func (s *fakeGoalStore) SaveGoal(sessionID string, goal *tools.Goal) error {
	if s.goals == nil {
		s.goals = make(map[string]*tools.Goal)
	}
	s.saves++
	if goal == nil {
		delete(s.goals, sessionID)
		return nil
	}
	s.goals[sessionID] = goal
	return nil
}

// TestControllerGoalAction covers FP-04's /goal controller API: set creates an
// id/revision/phase, pause/block/resume/unblock transition, complete is
// terminal, and every change is persisted through the goal store.
func TestControllerGoalAction(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	ctx := context.Background()
	sessionID, err := h.ctrl.CreateSession(ctx, "goal api session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	store := &fakeGoalStore{}
	h.ctrl.SetGoalStore(store)

	goal, err := h.ctrl.GoalAction("set", "Ship the release", []string{"tests", "notes"}, nil)
	if err != nil {
		t.Fatalf("GoalAction(set): %v", err)
	}
	if goal == nil || goal.ID == "" || goal.Revision != 1 || goal.Phase != tools.GoalPhaseActive || goal.Status != tools.GoalActive {
		t.Fatalf("set goal = %+v", goal)
	}
	if goal.Objective != "Ship the release" || len(goal.Steps) != 2 {
		t.Fatalf("set goal fields = %+v", goal)
	}
	if got := store.goals[sessionID]; got == nil || got.ID != goal.ID {
		t.Fatalf("set goal was not persisted: %+v", store.goals)
	}
	if got := h.ctrl.Goal(); got == nil || got.ID != goal.ID {
		t.Fatalf("controller Goal() = %+v, want the set goal", got)
	}

	paused, err := h.ctrl.GoalAction("pause", "", nil, nil)
	if err != nil {
		t.Fatalf("GoalAction(pause): %v", err)
	}
	if paused.Phase != tools.GoalPhasePaused || paused.Revision != 2 || paused.ID != goal.ID {
		t.Fatalf("paused goal = %+v", paused)
	}
	if got := store.goals[sessionID]; got == nil || got.Phase != tools.GoalPhasePaused {
		t.Fatalf("paused phase was not persisted: %+v", got)
	}

	blocked, err := h.ctrl.GoalAction("block", "", nil, nil)
	if err != nil || blocked.Phase != tools.GoalPhaseBlocked {
		t.Fatalf("block = %+v, %v", blocked, err)
	}
	if resumed, err := h.ctrl.GoalAction("resume", "", nil, nil); err != nil || resumed.Phase != tools.GoalPhaseActive {
		t.Fatalf("resume = %+v, %v", resumed, err)
	}
	if unblocked, err := h.ctrl.GoalAction("unblock", "", nil, nil); err != nil || unblocked.Phase != tools.GoalPhaseActive {
		t.Fatalf("unblock = %+v, %v", unblocked, err)
	}

	completed, err := h.ctrl.GoalAction("complete", "", nil, nil)
	if err != nil || completed.Status != tools.GoalCompleted || completed.Phase != tools.GoalPhaseCompleted {
		t.Fatalf("complete = %+v, %v", completed, err)
	}
	if _, err := h.ctrl.GoalAction("pause", "", nil, nil); err == nil {
		t.Fatal("pausing a completed goal was accepted")
	}

	cleared, err := h.ctrl.GoalAction("clear", "", nil, nil)
	if err != nil || cleared != nil {
		t.Fatalf("clear = %+v, %v; want nil goal", cleared, err)
	}
	if h.ctrl.Goal() != nil {
		t.Fatalf("Goal() after clear = %+v", h.ctrl.Goal())
	}
	if _, ok := store.goals[sessionID]; ok {
		t.Fatal("cleared goal still persisted")
	}
	if store.saves == 0 {
		t.Fatal("goal store never saved")
	}

	// get with no goal is not an error and reports nil.
	if got, err := h.ctrl.GoalAction("get", "", nil, nil); err != nil || got != nil {
		t.Fatalf("get after clear = %+v, %v", got, err)
	}
	// Unknown actions are rejected.
	if _, err := h.ctrl.GoalAction("teleport", "", nil, nil); err == nil {
		t.Fatal("unknown goal action was accepted")
	}
}
