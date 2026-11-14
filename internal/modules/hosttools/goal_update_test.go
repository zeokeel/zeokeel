package hosttools

// Tests for the model-facing goal lifecycle: the desktop goal bridge implements
// the optional contracts halves, and the adapter must map Rig's goal updates
// onto them (complete/pause/resume/edit) while refusing the DSH-only "blocked"
// phase instead of reporting a state change that did not happen.

import (
	"context"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeGoalBridge is a contracts.GoalTracker that also implements the optional
// pauser and lifecycler halves, like the desktop goal service.
type fakeGoalBridge struct {
	text    string
	status  string
	handled func(contracts.Goal, bool)
}

func (g *fakeGoalBridge) SetHandler(fn func(contracts.Goal, bool)) { g.handled = fn }

func (g *fakeGoalBridge) Set(_ context.Context, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", errEmptyGoal
	}
	g.text = text
	g.status = contracts.GoalStatusActive
	return "goal-1", nil
}

func (g *fakeGoalBridge) Get() (id, text, status string, ok bool) {
	if g.text == "" {
		return "", "", "", false
	}
	return "goal-1", g.text, g.status, true
}

func (g *fakeGoalBridge) Clear() bool {
	had := g.text != ""
	g.text, g.status = "", ""
	return had
}

func (g *fakeGoalBridge) Pause() bool {
	if g.status != contracts.GoalStatusActive {
		return false
	}
	g.status = contracts.GoalStatusPaused
	return true
}

func (g *fakeGoalBridge) Resume() bool {
	if g.status != contracts.GoalStatusPaused {
		return false
	}
	g.status = contracts.GoalStatusActive
	return true
}

func (g *fakeGoalBridge) Complete() bool {
	switch g.status {
	case contracts.GoalStatusActive, contracts.GoalStatusPaused:
		g.status = contracts.GoalStatusCompleted
		return true
	}
	return false
}

func (g *fakeGoalBridge) Abandon() bool {
	switch g.status {
	case contracts.GoalStatusActive, contracts.GoalStatusPaused:
		g.status = contracts.GoalStatusAbandoned
		return true
	}
	return false
}

func (g *fakeGoalBridge) Edit(text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", errEmptyGoal
	}
	g.text = text
	g.status = contracts.GoalStatusActive
	return "goal-1", nil
}

var _ contracts.GoalLifecycler = (*fakeGoalBridge)(nil)
var _ contracts.GoalPauser = (*fakeGoalBridge)(nil)

// errEmptyGoal mirrors the host goal service's validation error.
var errEmptyGoal = errEmpty("goal: text is required")

type errEmpty string

func (e errEmpty) Error() string { return string(e) }

func TestGoalStoreUpdateMapsLifecycle(t *testing.T) {
	store := uiGoalTrackerStore{tracker: &fakeGoalBridge{}}
	ctx := context.Background()
	// No goal: updates report it instead of inventing a state.
	if _, err := store.Update(ctx, rig.GoalPause, ""); err == nil {
		t.Fatal("pause without a goal should fail")
	}
	if _, err := store.Set("ship the release"); err != nil {
		t.Fatalf("set goal: %v", err)
	}

	cases := []struct {
		action rig.GoalUpdateAction
		text   string
		want   string
	}{
		{rig.GoalPause, "", contracts.GoalStatusPaused},
		{rig.GoalResume, "", contracts.GoalStatusActive},
		{rig.GoalEdit, "ship it earlier", contracts.GoalStatusActive},
		{rig.GoalComplete, "", contracts.GoalStatusCompleted},
	}
	for _, tc := range cases {
		status, err := store.Update(ctx, tc.action, tc.text)
		if err != nil {
			t.Fatalf("update %s: %v", tc.action, err)
		}
		if status != tc.want {
			t.Fatalf("update %s status = %q, want %q", tc.action, status, tc.want)
		}
	}
	if id, text, status, ok := store.Get(); !ok || id != "goal-1" ||
		text != "ship it earlier" || status != contracts.GoalStatusCompleted {
		t.Fatalf("goal after updates = %q %q %q %v", id, text, status, ok)
	}

	// The DSH-only blocked phase is refused with a readable error.
	if _, err := store.Update(ctx, rig.GoalBlocked, "upstream outage"); err == nil ||
		!strings.Contains(err.Error(), "blocked") {
		t.Fatalf("blocked update error = %v, want a readable refusal", err)
	}

	// A store without the optional lifecycle halves refuses explicitly.
	plain := uiGoalTrackerStore{tracker: goalTrackerOnly{}}
	if _, err := plain.Update(ctx, rig.GoalComplete, ""); err == nil ||
		!strings.Contains(err.Error(), "cannot complete") {
		t.Fatalf("plain store complete error = %v, want a readable refusal", err)
	}
}

// goalTrackerOnly implements contracts.GoalTracker but neither optional half.
type goalTrackerOnly struct{ text string }

func (g goalTrackerOnly) SetHandler(func(contracts.Goal, bool)) {}

func (g goalTrackerOnly) Set(_ context.Context, text string) (string, error) {
	return "goal-1", nil
}

func (g goalTrackerOnly) Get() (id, text, status string, ok bool) {
	return "goal-1", "goal", contracts.GoalStatusActive, true
}

func (g goalTrackerOnly) Clear() bool { return true }
