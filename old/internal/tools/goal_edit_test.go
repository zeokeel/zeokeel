package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

// TestGoalEditKeepsIdentityPhaseAndFields is the /goal edit contract: an edit
// replaces the objective of the current goal in place — same goal id, one
// revision bump, and the phase/status/steps/progress are untouched — so
// automatic continuation state and the UI banner's identity survive the edit.
func TestGoalEditKeepsIdentityPhaseAndFields(t *testing.T) {
	cases := []struct {
		name     string
		prepare  []map[string]any
		wantStat GoalStatus
		wantPhas GoalPhase
	}{
		{"active", nil, GoalActive, GoalPhaseActive},
		{"paused", []map[string]any{{"action": "pause"}}, GoalActive, GoalPhasePaused},
		{"blocked", []map[string]any{{"action": "block", "message": "waiting"}}, GoalActive, GoalPhaseBlocked},
		{"completed", []map[string]any{{"action": "complete"}}, GoalCompleted, GoalPhaseCompleted},
		{"abandoned", []map[string]any{{"action": "abandon"}}, GoalAbandoned, GoalPhaseAbandoned},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, ctx := goalToolWithSession(t, "goal-edit-"+tc.name)
			set := invokeGoal(t, tool, ctx, map[string]any{
				"action":    "set",
				"objective": "original objective",
				"steps":     []string{"one", "two"},
				"progress":  25,
			})
			if set.Goal == nil {
				t.Fatal("set returned no goal")
			}
			goalID := set.Goal.ID
			for _, args := range tc.prepare {
				invokeGoal(t, tool, ctx, args)
			}
			before := invokeGoal(t, tool, ctx, map[string]any{"action": "get"})
			if before.Goal == nil {
				t.Fatalf("get after prepare = %+v", before.Goal)
			}

			edited := invokeGoal(t, tool, ctx, map[string]any{
				"action":    "edit",
				"objective": "replacement objective",
			})
			if edited.Action != "edit" {
				t.Fatalf("result action = %q, want edit", edited.Action)
			}
			if edited.Goal == nil {
				t.Fatal("edit returned no goal")
			}
			if edited.Goal.ID != goalID || edited.Goal.ID != before.Goal.ID {
				t.Fatalf("edit goal id = %q, want the same id %q", edited.Goal.ID, goalID)
			}
			if edited.Goal.Objective != "replacement objective" {
				t.Fatalf("edit objective = %q", edited.Goal.Objective)
			}
			if edited.Goal.Revision != before.Goal.Revision+1 {
				t.Fatalf("edit revision = %d, want %d", edited.Goal.Revision, before.Goal.Revision+1)
			}
			if edited.Goal.Status != tc.wantStat || edited.Goal.Phase != tc.wantPhas {
				t.Fatalf("edit status/phase = %v/%v, want %v/%v",
					edited.Goal.Status, edited.Goal.Phase, tc.wantStat, tc.wantPhas)
			}
			if len(edited.Goal.Steps) != len(before.Goal.Steps) ||
				edited.Goal.Progress != before.Goal.Progress {
				t.Fatalf("edit changed steps/progress: %+v, want %+v", edited.Goal, before.Goal)
			}

			// A later get returns the edited goal (the edit is the current
			// state, not a transient response).
			got := invokeGoal(t, tool, ctx, map[string]any{"action": "get"})
			if got.Goal == nil || got.Goal.Objective != "replacement objective" || got.Goal.ID != goalID {
				t.Fatalf("get after edit = %+v", got.Goal)
			}
		})
	}
}

// TestGoalEditValidation pins the edit error paths: no current goal and a
// missing objective are domain rejections, not silent no-ops.
func TestGoalEditValidation(t *testing.T) {
	t.Run("no goal", func(t *testing.T) {
		tool, ctx := goalToolWithSession(t, "goal-edit-none")
		raw, err := json.Marshal(map[string]any{"action": "edit", "objective": "anything"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tool.Invoke(ctx, raw); err == nil || !strings.Contains(err.Error(), "no goal") {
			t.Fatalf("edit without a goal error = %v, want a no-goal rejection", err)
		}
	})
	t.Run("missing objective", func(t *testing.T) {
		tool, ctx := goalToolWithSession(t, "goal-edit-empty")
		invokeGoal(t, tool, ctx, map[string]any{"action": "set", "objective": "keep me"})
		raw, err := json.Marshal(map[string]any{"action": "edit"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tool.Invoke(ctx, raw); err == nil || !strings.Contains(err.Error(), "objective") {
			t.Fatalf("edit without an objective error = %v, want an objective rejection", err)
		}
		if got := invokeGoal(t, tool, ctx, map[string]any{"action": "get"}); got.Goal == nil || got.Goal.Objective != "keep me" {
			t.Fatalf("failed edit changed the goal: %+v", got.Goal)
		}
	})
}

// TestGoalEditPersists drives an edit through the goal store: the persisted
// goal keeps its id and gains the new objective.
func TestGoalEditPersists(t *testing.T) {
	store := &recordingGoalStore{}
	tool := NewGoalToolWithStore(store)
	ctx := runtime.WithSessionID(t.Context(), "goal-edit-persist")
	set := invokeGoal(t, tool, ctx, map[string]any{"action": "set", "objective": "before"})
	edited := invokeGoal(t, tool, ctx, map[string]any{"action": "edit", "objective": "after"})
	saved := store.last("goal-edit-persist")
	if saved == nil || saved.Objective != "after" || saved.ID != set.Goal.ID {
		t.Fatalf("persisted goal = %+v, want the edited objective on the same id", saved)
	}
	if saved.Revision != edited.Goal.Revision {
		t.Fatalf("persisted revision = %d, want %d", saved.Revision, edited.Goal.Revision)
	}
}
