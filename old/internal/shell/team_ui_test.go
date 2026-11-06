package shell

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
	"github.com/zeokeel/zeokeel/old/internal/tools"
)

// renderTeamPreview renders the goal banner and team panel. Both are composed
// in team_ui.go and are wired into the live session screen by its owner.
func renderTeamPreview(app *App) func(*ui.Context) {
	return func(c *ui.Context) {
		t := design.Light()
		c.SetTheme(t.Theme())
		ui.Column(c).Padding(design.Space4).Width(420).Gap(design.Space4).
			Children(func() {
				app.teamGoalBanner(c, t)
				app.teamPanel(c, t)
			})
	}
}

func TestTeamGoalBannerAndPanel(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})

	// Nothing recorded yet: both surfaces stay hidden.
	tt := ui.NewTester(renderTeamPreview(h.app), 900, 600)
	if tt.HasText("Goal") {
		t.Fatalf("goal banner rendered without a goal: %q", tt.Texts())
	}
	if tt.HasText("Team") {
		t.Fatalf("team panel rendered without teammates: %q", tt.Texts())
	}

	h.ctrl.SetGoal(&tools.Goal{
		Objective: "Ship the release",
		Steps:     []string{"Run the tests", "Write the notes"},
		Progress:  40,
		Status:    tools.GoalActive,
	})
	h.ctrl.SetTeam([]tools.Teammate{{
		CallID: "call-sub",
		Name:   "researcher",
		Task:   "check the failing tests",
		Status: tools.SubagentStatusCompleted,
		Output: "all tests pass",
	}})

	tt.Frame()
	if !tt.HasText("Goal") || !tt.HasText("Ship the release") {
		t.Fatalf("goal banner missing: %q", tt.Texts())
	}
	if !tt.HasText("40%") {
		t.Fatalf("goal progress missing: %q", tt.Texts())
	}
	if !tt.HasText("2 step(s)") {
		t.Fatalf("goal step count missing: %q", tt.Texts())
	}
	if !tt.HasText("Team") || !tt.HasText("researcher") || !tt.HasText("completed") {
		t.Fatalf("team panel missing: %q", tt.Texts())
	}
	if !tt.HasText("check the failing tests") {
		t.Fatalf("teammate task missing: %q", tt.Texts())
	}
	// The team header carries the current agent mode badge.
	if !tt.HasText("Standard") {
		t.Fatalf("team mode badge missing: %q", tt.Texts())
	}

	// A completed goal is announced as such and keeps its objective.
	h.ctrl.SetGoal(&tools.Goal{
		Objective: "Ship the release",
		Progress:  100,
		Status:    tools.GoalCompleted,
	})
	tt.Frame()
	if !tt.HasText("Completed") || !tt.HasText("100%") {
		t.Fatalf("completed goal missing: %q", tt.Texts())
	}

	// An abandoned goal hides the banner but keeps the team panel.
	h.ctrl.SetGoal(&tools.Goal{
		Objective: "Ship the release",
		Progress:  30,
		Status:    tools.GoalAbandoned,
	})
	tt.Frame()
	if tt.HasText("Ship the release") {
		t.Fatalf("abandoned goal banner still shown: %q", tt.Texts())
	}
	if !tt.HasText("researcher") {
		t.Fatalf("team panel disappeared: %q", tt.Texts())
	}

	// Out-of-range progress from a hand-written goal is clamped.
	h.ctrl.SetGoal(&tools.Goal{Objective: "Clamped", Progress: 250, Status: tools.GoalActive})
	tt.Frame()
	if !tt.HasText("100%") {
		t.Fatalf("progress was not clamped: %q", tt.Texts())
	}
}

// TestTeamPanelShowsLiveAsyncRoster proves the Team panel merges the app's
// live asynchronous team members (spawn_team) with the synchronous subagent
// calls, and that stopping a turn also asks the app to stop the session's
// asynchronous members.
func TestTeamPanelShowsLiveAsyncRoster(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.ctrl.SetTeam([]tools.Teammate{{
		CallID: "call-sub",
		Name:   "researcher",
		Task:   "check the failing tests",
		Status: tools.SubagentStatusCompleted,
	}})

	async := []tools.Teammate{
		{CallID: "team:agent-1", Name: "worker", Task: "background task", Status: "running"},
		{CallID: "team:agent-2", Name: "queued-worker", Task: "later task", Status: "queued"},
	}
	h.app.SetTeamRoster(func() []tools.Teammate { return async })

	stopped := make(chan string, 1)
	h.app.SetTeamStop(func(sessionID string) { stopped <- sessionID })

	tt := ui.NewTester(renderTeamPreview(h.app), 900, 600)
	tt.Frame()
	for _, want := range []string{"researcher", "worker", "running", "queued-worker", "queued"} {
		if !tt.HasText(want) {
			t.Fatalf("live team panel missing %q: %q", want, tt.Texts())
		}
	}
	if !tt.HasText("3") {
		t.Fatalf("team count does not include the asynchronous members: %q", tt.Texts())
	}

	// stopTurn stops the controller turn and hands the session id to the app's
	// team-stop hook off the view goroutine.
	id := h.ctrl.CurrentSessionID()
	h.app.stopTurn()
	select {
	case got := <-stopped:
		if got != id {
			t.Fatalf("team stop session = %q, want %q", got, id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stopTurn did not call the team stop hook")
	}
}
