package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

// goalToolWithSession returns a goal tool and a context carrying sessionID.
func goalToolWithSession(t *testing.T, sessionID string) (*GoalTool, context.Context) {
	t.Helper()
	return NewGoalTool(), runtime.WithSessionID(context.Background(), sessionID)
}

// invokeGoal calls the goal tool and decodes its result.
func invokeGoal(t *testing.T, tool *GoalTool, ctx context.Context, args map[string]any) GoalResult {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Invoke(ctx, raw)
	if err != nil {
		t.Fatalf("goal %v: %v", args, err)
	}
	var result GoalResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode goal result: %v", err)
	}
	return result
}

// TestGoalLifecyclePhases is the FP-04 regression: a goal carries id/revision/
// phase, pause/block stop continuation without abandoning it, resume/unblock
// re-arm, and clear deletes.
func TestGoalLifecyclePhases(t *testing.T) {
	tool, ctx := goalToolWithSession(t, "goal-session-1")

	set := invokeGoal(t, tool, ctx, map[string]any{"action": "set", "objective": "Ship the release"})
	if set.Goal == nil || set.Goal.ID == "" || set.Goal.Revision != 1 || set.Goal.Phase != GoalPhaseActive {
		t.Fatalf("set goal = %+v, want id/revision 1/phase active", set.Goal)
	}
	firstID := set.Goal.ID

	paused := invokeGoal(t, tool, ctx, map[string]any{"action": "pause"})
	if paused.Goal == nil || paused.Goal.Phase != GoalPhasePaused || paused.Goal.Status != GoalActive {
		t.Fatalf("pause goal = %+v, want paused phase with an active status", paused.Goal)
	}
	if paused.Goal.Revision != 2 || paused.Goal.ID != firstID {
		t.Fatalf("pause revision/id = %d/%q, want 2 and the same id", paused.Goal.Revision, paused.Goal.ID)
	}

	resumed := invokeGoal(t, tool, ctx, map[string]any{"action": "resume"})
	if resumed.Goal.Phase != GoalPhaseActive || resumed.Goal.Revision != 3 {
		t.Fatalf("resume goal = %+v", resumed.Goal)
	}

	blocked := invokeGoal(t, tool, ctx, map[string]any{"action": "block", "message": "waiting for CI"})
	if blocked.Goal.Phase != GoalPhaseBlocked || blocked.Message != "waiting for CI" {
		t.Fatalf("block goal = %+v, message %q", blocked.Goal, blocked.Message)
	}
	unblocked := invokeGoal(t, tool, ctx, map[string]any{"action": "unblock"})
	if unblocked.Goal.Phase != GoalPhaseActive {
		t.Fatalf("unblock goal = %+v", unblocked.Goal)
	}

	completed := invokeGoal(t, tool, ctx, map[string]any{"action": "complete"})
	if completed.Goal.Status != GoalCompleted || completed.Goal.Phase != GoalPhaseCompleted || completed.Goal.Progress != 100 {
		t.Fatalf("complete goal = %+v", completed.Goal)
	}
	// Pausing a completed goal is an invalid transition.
	if _, err := tool.Invoke(ctx, json.RawMessage(`{"action":"pause"}`)); err == nil || !strings.Contains(err.Error(), "completed") {
		t.Fatalf("pause completed goal error = %v", err)
	}

	cleared := invokeGoal(t, tool, ctx, map[string]any{"action": "clear"})
	if cleared.Goal != nil {
		t.Fatalf("clear goal = %+v, want nil", cleared.Goal)
	}
	if got := invokeGoal(t, tool, ctx, map[string]any{"action": "get"}); got.Goal != nil {
		t.Fatalf("get after clear = %+v, want no goal", got.Goal)
	}
}

// TestGoalNewObjectiveGetsNewID proves the id identifies a goal instance: same
// objective updates keep it, a new objective replaces it.
func TestGoalNewObjectiveGetsNewID(t *testing.T) {
	tool, ctx := goalToolWithSession(t, "goal-session-2")
	first := invokeGoal(t, tool, ctx, map[string]any{"action": "set", "objective": "first"})
	updated := invokeGoal(t, tool, ctx, map[string]any{"action": "set", "progress": 50})
	if updated.Goal.ID != first.Goal.ID || updated.Goal.Revision != 2 {
		t.Fatalf("progress update = %+v, want the same goal at revision 2", updated.Goal)
	}
	replaced := invokeGoal(t, tool, ctx, map[string]any{"action": "set", "objective": "second"})
	if replaced.Goal.ID == first.Goal.ID {
		t.Fatalf("new objective kept the old id %q", replaced.Goal.ID)
	}
	if replaced.Goal.Revision != 1 {
		t.Fatalf("new goal revision = %d, want 1", replaced.Goal.Revision)
	}
}

// TestGoalActionsPersistPhase proves the goal store sees the phase changes, so
// a rebuild or restart restores a paused/blocked goal as paused/blocked.
func TestGoalActionsPersistPhase(t *testing.T) {
	store := &recordingGoalStore{}
	tool := NewGoalToolWithStore(store)
	ctx := runtime.WithSessionID(context.Background(), "goal-session-3")
	invokeGoal(t, tool, ctx, map[string]any{"action": "set", "objective": "persist me"})
	invokeGoal(t, tool, ctx, map[string]any{"action": "block"})
	saved := store.last("goal-session-3")
	if saved == nil || saved.Phase != GoalPhaseBlocked || saved.Revision != 2 {
		t.Fatalf("saved goal = %+v, want blocked at revision 2", saved)
	}

	// A rebuilt tool hydrates the paused/blocked phase from the store.
	rebuilt := NewGoalToolWithStore(store)
	got := invokeGoal(t, rebuilt, ctx, map[string]any{"action": "get"})
	if got.Goal == nil || got.Goal.Phase != GoalPhaseBlocked {
		t.Fatalf("rehydrated goal = %+v, want the blocked phase", got.Goal)
	}
}

// recordingGoalStore is an in-memory GoalStateStore capturing the last save.
type recordingGoalStore struct {
	goals map[string]*Goal
}

func (s *recordingGoalStore) LoadGoal(sessionID string) (*Goal, error) {
	if s.goals == nil {
		return nil, nil
	}
	return cloneGoal(s.goals[sessionID]), nil
}

func (s *recordingGoalStore) SaveGoal(sessionID string, goal *Goal) error {
	if s.goals == nil {
		s.goals = make(map[string]*Goal)
	}
	if goal == nil {
		delete(s.goals, sessionID)
		return nil
	}
	s.goals[sessionID] = cloneGoal(goal)
	return nil
}

func (s *recordingGoalStore) last(sessionID string) *Goal {
	return cloneGoal(s.goals[sessionID])
}
