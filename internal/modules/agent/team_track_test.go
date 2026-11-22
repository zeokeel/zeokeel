package agent

// Regression test for follow-up tracking: a model-tool send_message to a
// coordinator-owned member arms the coordinator's watcher for that run.

import (
	"context"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	rigagentteam "github.com/zeokeel/zeokeel-rig/agent/team"
	rigteam "github.com/zeokeel/zeokeel-rig/tools/team"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestModelSendToolTracksFollowUpRun(t *testing.T) {
	provider := &captureProvider{}
	svc, _, _ := newTeamModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	spawn := registryTool(t, svc.registry, "spawn_teammate")
	result, err := spawn.Invoke(context.Background(), rig.ToolCall{
		ID: "track-1", Name: "spawn_teammate",
		Arguments: `{"name":"tracked-worker","prompt":"first task"}`,
	})
	if err != nil || result.IsError {
		t.Fatalf("spawn_teammate = %+v, %v", result, err)
	}
	manager := svc.teamManagerService()
	members := manager.Members()
	if len(members) != 1 {
		t.Fatalf("manager members = %+v", members)
	}
	memberID := members[0].ID
	tracker := svc.teamTrackerLocked()
	coordinator := tracker.coordinator()
	if coordinator == nil {
		t.Fatal("service has no coordinator")
	}
	// Wait for the adopted initial run to settle before sending a follow-up.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if teammate, ok := coordinator.Teammate(memberID); ok && teammate.Status != rigagentteam.StatusRunning {
			break
		}
		time.Sleep(time.Millisecond)
	}

	send := registryTool(t, svc.registry, "send_message")
	result, err = send.Invoke(context.Background(), rig.ToolCall{
		ID: "track-2", Name: "send_message",
		Arguments: `{"target":"tracked-worker","message":"follow up"}`,
	})
	if err != nil || result.IsError {
		t.Fatalf("send_message = %+v, %v", result, err)
	}

	teammate, ok := coordinator.Teammate(memberID)
	if !ok {
		t.Fatalf("coordinator lost member %q", memberID)
	}
	tracked := false
	for _, row := range teammate.Activity {
		if row.Kind == rigagentteam.ActivityWait {
			tracked = true
		}
		if row.Kind == rigagentteam.ActivityError {
			t.Fatalf("track follow-up recorded an error: %+v", row)
		}
	}
	if !tracked {
		t.Fatalf("send_message did not arm coordinator.Track: %+v", teammate.Activity)
	}
}

// forkNameTool mimics a subagent_fork tool: it starts a normal manager member
// but reports the fork tool name, so the wrapper's fork attribution is tested
// independently of backend fork support.
type forkNameTool struct{ manager *rigteam.Manager }

func (f *forkNameTool) Name() string           { return rigteam.ToolSubagentFork }
func (f *forkNameTool) Description() string    { return "fork" }
func (f *forkNameTool) Schema() map[string]any { return nil }
func (f *forkNameTool) Invoke(ctx context.Context, _ rig.ToolCall) (rig.ToolResult, error) {
	if _, err := f.manager.Spawn(ctx, rig.SubagentSpec{Name: "forked-worker", Prompt: "fork work"}); err != nil {
		return rig.ToolResult{}, err
	}
	return rig.ToolResult{Content: "started forked-worker"}, nil
}

func TestForkToolAdoptionCarriesForkIdentity(t *testing.T) {
	provider := &captureProvider{}
	svc, _, _ := newTeamModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	manager := svc.teamManagerService()
	wrapper := &trackedTeamTool{inner: &forkNameTool{manager: manager}, tracker: svc.teamTrackerLocked(), manager: manager}
	ctx := contracts.WithAgentSession(context.Background(), "session-fork")
	if _, err := wrapper.Invoke(ctx, rig.ToolCall{
		ID: "fork-1", Name: rigteam.ToolSubagentFork,
		Arguments: `{"description":"forked worker","prompt":"fork work"}`,
	}); err != nil {
		t.Fatalf("subagent_fork: %v", err)
	}
	members := manager.Members()
	if len(members) != 1 {
		t.Fatalf("manager members = %+v", members)
	}
	teammate, ok := svc.teamTrackerLocked().coordinator().Teammate(members[0].ID)
	if !ok {
		t.Fatal("forked member was not adopted")
	}
	if !teammate.Fork || teammate.ForkedFrom != "session-fork" {
		t.Fatalf("fork identity = fork=%v from=%q", teammate.Fork, teammate.ForkedFrom)
	}
}
