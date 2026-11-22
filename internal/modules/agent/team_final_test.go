package agent

// Finalization tests: failed runs reach the coordinator's host snapshot, and the
// team tool build no longer emits duplicate-registry skips for the synchronous
// subagent names.

import (
	"context"
	"errors"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigagentteam "github.com/zeokeel/zeokeel-rig/agent/team"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestCoordinatorSnapshotMapsRunError: a failed run retained by the rig manager
// becomes MemberSnapshot.Error, so Adopt records StatusFailed.
func TestCoordinatorSnapshotMapsRunError(t *testing.T) {
	svc, _, _ := newTeamModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) {
		return &failingSubagentProvider{err: errors.New("provider down")}, nil
	}
	manager := svc.teamManagerService()
	member, err := manager.Spawn(context.Background(), rig.SubagentSpec{Name: "failure", Prompt: "p"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := manager.Wait(context.Background(), member.ID); err == nil {
		t.Fatal("Wait on a failed run returned nil")
	}

	tracker := svc.teamTrackerLocked()
	host := &coordinatorHost{tracker: tracker}
	snapshot, ok := host.Snapshot(context.Background(), member.ID)
	if !ok {
		t.Fatalf("Snapshot(%q) not found", member.ID)
	}
	if !snapshot.Finished || snapshot.Running {
		t.Fatalf("failed snapshot = %+v, want finished and not running", snapshot)
	}
	if !strings.Contains(snapshot.Error, "provider down") {
		t.Fatalf("snapshot.Error = %q", snapshot.Error)
	}

	coordinator := tracker.coordinator()
	if coordinator == nil {
		t.Fatal("service has no coordinator")
	}
	adopted, err := coordinator.Adopt(context.Background(), member.ID, rigagentteam.SpawnSpec{Name: "failure", Prompt: "p"})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if adopted.Status != rigagentteam.StatusFailed {
		t.Fatalf("adopted status = %q, want failed", adopted.Status)
	}
	if !strings.Contains(adopted.Error, "provider down") {
		t.Fatalf("adopted error = %q", adopted.Error)
	}
}

// TestTeamToolBuildFiltersSynchronousSubagentDuplicates: when the shared
// registry already owns subagent/subagent_fork, newTeamTools drops the manager
// duplicates but keeps the rest of the model-facing team surface.
func TestTeamToolBuildFiltersSynchronousSubagentDuplicates(t *testing.T) {
	svc, _, _ := newTeamModuleService(t)
	manager := svc.teamManagerService()
	known := map[string]bool{"subagent": true, "subagent_fork": true}

	tools := newTeamTools(svc, manager, known)
	names := make(map[string]int, len(tools))
	for _, tool := range tools {
		names[tool.Name()]++
	}
	for _, name := range []string{"subagent", "subagent_fork"} {
		if names[name] != 0 {
			t.Fatalf("filtered build still contains %q: %v", name, names)
		}
	}
	for _, name := range []string{"spawn_teammate", "list_agents", "wait_agent", "send_message", "interrupt_agent"} {
		if names[name] != 1 {
			t.Fatalf("filtered build tool %q count = %d, want 1", name, names[name])
		}
	}

	// Without a skip set the manager tools are still returned (compatibility for
	// callers that register them elsewhere).
	unfiltered := newTeamTools(svc, manager)
	found := make(map[string]bool)
	for _, tool := range unfiltered {
		found[tool.Name()] = true
	}
	if !found["subagent"] || !found["subagent_fork"] {
		t.Fatalf("unfiltered build lost the subagent tools: %v", found)
	}
}

// TestStartupHasNoSubagentDuplicateSkipLogs: module activation registers exactly
// one subagent/subagent_fork and does not log duplicate skips for them.
func TestStartupHasNoSubagentDuplicateSkipLogs(t *testing.T) {
	svc, _, ctx := newTeamModuleService(t)
	loggerValue, err := ctx.Require(contracts.KeyLogger)
	if err != nil {
		t.Fatalf("require logger: %v", err)
	}
	logger, ok := loggerValue.(*fakeLogger)
	if !ok {
		t.Fatalf("logger = %T", loggerValue)
	}
	for _, name := range []string{"subagent", "subagent_fork"} {
		if logger.contains("[agent] skip team tool " + name) {
			t.Fatalf("startup logged a duplicate skip for %q: %v", name, logger.lines)
		}
	}
	for _, name := range []string{"subagent", "subagent_fork", "spawn_teammate", "list_agents"} {
		if tool := registryTool(t, svc.registry, name); tool == nil {
			t.Fatalf("%q is not registered", name)
		}
	}
}
