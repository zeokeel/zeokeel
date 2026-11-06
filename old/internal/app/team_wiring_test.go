package app

import (
	"context"
	"errors"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/agentteam"
)

// TestBuildAgentAdoptsAndClosesTeamManager proves the team manager created by
// a build is owned by the bundle: the app adopt hook sees it and bundle.Close
// closes it (canceling and waiting for its members) instead of leaking it when
// a rebuild replaces the agent.
func TestBuildAgentAdoptsAndClosesTeamManager(t *testing.T) {
	st, dir := newToolStore(t)
	configuredProvider(t, st)
	if err := st.SetSetting("plugin.agent_teams.enabled", "true"); err != nil {
		t.Fatalf("enable Agent Teams: %v", err)
	}

	var adopted *agentteam.Manager
	bundle, err := buildAgentWithOptions(st, dir, "instruction", registryOptions{
		AdoptTeam: func(m *agentteam.Manager) { adopted = m },
	})
	if err != nil {
		t.Fatalf("buildAgentWithOptions: %v", err)
	}
	if bundle == nil || bundle.Close == nil {
		t.Fatal("bundle or its Close is nil")
	}
	if adopted == nil {
		t.Fatal("AdoptTeam was not called for an Agent Teams build")
	}
	for _, name := range []string{"spawn_team", "wait_team", "close_team"} {
		if !registryHas(bundle, name) {
			t.Fatalf("%s is not model-visible with Agent Teams enabled: %v", name, bundle.Agent.ToolNames())
		}
	}

	if err := bundle.Close(); err != nil {
		t.Fatalf("bundle.Close: %v", err)
	}
	// A closed manager rejects new members, which proves it was closed rather
	// than leaked.
	if _, err := adopted.Spawn(context.Background(), agentteam.SpawnRequest{Task: "after close"}); !errors.Is(err, agentteam.ErrClosed) {
		t.Fatalf("Spawn after bundle.Close error = %v, want ErrClosed", err)
	}
	if stopped := adopted.StopSession("any-session"); stopped != 0 {
		t.Fatalf("StopSession after Close = %d, want 0", stopped)
	}
}

// TestBuildAgentWithoutAgentTeamsDoesNotAdoptManager keeps the hook nil for the
// default configuration: a build without Agent Teams reports one nil adoption
// (clearing any replaced roster) instead of a manager.
func TestBuildAgentWithoutAgentTeamsDoesNotAdoptManager(t *testing.T) {
	st, dir := newToolStore(t)
	configuredProvider(t, st)

	calls := 0
	var adopted *agentteam.Manager
	bundle, err := buildAgentWithOptions(st, dir, "instruction", registryOptions{
		AdoptTeam: func(m *agentteam.Manager) { calls++; adopted = m },
	})
	if err != nil {
		t.Fatalf("buildAgentWithOptions: %v", err)
	}
	t.Cleanup(func() {
		if bundle != nil && bundle.Close != nil {
			_ = bundle.Close()
		}
	})
	if calls != 1 || adopted != nil {
		t.Fatalf("AdoptTeam calls = %d (manager %v), want exactly one nil adoption for a build without Agent Teams", calls, adopted)
	}
	if bundle != nil && registryHas(bundle, "spawn_team") {
		t.Fatal("spawn_team is model-visible even though Agent Teams is off")
	}
}

// TestTeammatesForSessionMapsLiveMembers proves the shell roster adapter keeps
// only the current session's members and maps the manager's status/output into
// the panel row type.
func TestTeammatesForSessionMapsLiveMembers(t *testing.T) {
	members := []agentteam.Member{
		{
			ID: "agent-1", Name: "worker", SessionID: "session-a",
			Status: agentteam.StatusRunning, Task: "background task",
			Output: "", Turns: 1,
		},
		{
			ID: "agent-2", Name: "other", SessionID: "session-b",
			Status: agentteam.StatusCompleted, Task: "another task",
		},
	}
	got := teammatesForSession(members, "session-a")
	if len(got) != 1 {
		t.Fatalf("teammatesForSession = %+v, want the session-a member only", got)
	}
	if got[0].CallID != "team:agent-1" || got[0].Name != "worker" || got[0].Status != "running" || got[0].Task != "background task" {
		t.Fatalf("mapped teammate = %+v", got[0])
	}
	if got := teammatesForSession(members, ""); got != nil {
		t.Fatalf("empty session id returned %+v, want nil", got)
	}
	if got := teammatesForSession(nil, "session-a"); got != nil {
		t.Fatalf("no members returned %+v, want nil", got)
	}
}
