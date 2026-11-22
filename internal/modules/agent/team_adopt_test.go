package agent

// Regression test for the model-tool spawn lane: a teammate started by the
// model-facing spawn_teammate tool is adopted by the coordinator, so its run
// state and activity belong to the same state machine as TeamService.Spawn.

import (
	"context"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestModelSpawnToolAdoptsManagerMember(t *testing.T) {
	provider := &captureProvider{}
	svc, _, _ := newTeamModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	spawn := registryTool(t, svc.registry, "spawn_teammate")
	result, err := spawn.Invoke(context.Background(), rig.ToolCall{
		ID: "adopt-1", Name: "spawn_teammate",
		Arguments: `{"name":"adopted-teammate","prompt":"do the work","model":""}`,
	})
	if err != nil || result.IsError {
		t.Fatalf("spawn_teammate = %+v, %v", result, err)
	}

	tracker := svc.teamTrackerLocked()
	coordinator := tracker.coordinator()
	if coordinator == nil {
		t.Fatal("service has no coordinator")
	}
	manager := svc.teamManagerService()
	members := manager.Members()
	if len(members) != 1 {
		t.Fatalf("manager members = %+v, want one", members)
	}
	memberID := members[0].ID

	// The coordinator owns the manager member (the tool spawn was adopted).
	teammate, ok := coordinator.Teammate(memberID)
	if !ok {
		t.Fatalf("coordinator does not own manager member %q", memberID)
	}
	if teammate.ID != memberID || teammate.Name != "adopted-teammate" || teammate.Prompt != "do the work" {
		t.Fatalf("adopted teammate = %+v", teammate)
	}
	// The durable child identity is bound, so the UI can attribute the row.
	if childID := tracker.childIDForMember(memberID); childID == "" {
		t.Fatal("adopted member has no durable child-session identity")
	}
	// Tool activity is attributed to the member.
	attributed := false
	for _, row := range tracker.activityForMember(memberID) {
		if row.Kind == contracts.TeamActivitySpawn {
			attributed = true
			break
		}
	}
	if !attributed {
		t.Fatalf("no attributed spawn activity: %+v", tracker.activityForMember(memberID))
	}
}
