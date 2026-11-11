package agent

// Tests for the team orchestration backend: TeamService methods over the rig
// team.Manager, the registered model-facing team tools (duplicates skipped) and
// disposal cleanup.

import (
	"context"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// newTeamModuleService activates the agent module and requires both the team
// service and the chat service from the kernel context.
func newTeamModuleService(t *testing.T) (*service, contracts.TeamService, cordis.Context) {
	t.Helper()
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	registry := rigtool.NewRegistry()
	provide(t, ctx, contracts.KeyToolkit, registry)
	provide(t, ctx, contracts.KeySettings, contracts.SettingsService(newFakeSettings(nil)))
	provide(t, ctx, contracts.KeyLogger, contracts.Logger(&fakeLogger{}))
	provide(t, ctx, contracts.KeyStore, contracts.Store(newFakeStore()))
	provide(t, ctx, contracts.KeyApprovals, contracts.Approvals(fakeApprovals{}))
	provide(t, ctx, contracts.KeyDataDir, t.TempDir())
	if err := Module().Factory(ctx, Config{}); err != nil {
		t.Fatalf("activate agent module: %v", err)
	}
	chat, err := ctx.Require(contracts.KeyChat)
	if err != nil {
		t.Fatalf("require KeyChat: %v", err)
	}
	svc, ok := chat.(*service)
	if !ok {
		t.Fatalf("KeyChat = %T, want *service", chat)
	}
	teamService, err := ctx.Require(contracts.KeyTeam)
	if err != nil {
		t.Fatalf("require KeyTeam: %v", err)
	}
	return svc, teamService, ctx
}

// liveTeamMembers filters the durable historical rows out of a Members()
// snapshot: a closed/finished member stays in the read model as history, so
// "the team is empty" now means "no live member".
func liveTeamMembers(members []contracts.TeamMember) []contracts.TeamMember {
	live := make([]contracts.TeamMember, 0, len(members))
	for _, member := range members {
		if !member.Historical {
			live = append(live, member)
		}
	}
	return live
}

func TestTeamSpawnWaitAndMembers(t *testing.T) {
	provider := &captureProvider{}
	svc, teamService, _ := newTeamModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	member, err := teamService.Spawn(context.Background(), "researcher", "  summarize the repo  ", "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if member.ID == "" || member.Name != "researcher" || member.Status != "active" || member.Created.IsZero() {
		t.Fatalf("spawned member = %+v", member)
	}
	answer, err := teamService.Wait(context.Background(), member.ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if answer != "ok" {
		t.Fatalf("Wait answer = %q, want ok", answer)
	}
	members := teamService.Members()
	if len(members) != 1 || members[0].ID != member.ID || members[0].Last != "ok" {
		t.Fatalf("Members = %+v, want one member with the finished answer", members)
	}
}

func TestTeamValidationAndUnknownMember(t *testing.T) {
	provider := &captureProvider{}
	svc, teamService, _ := newTeamModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	if _, err := teamService.Spawn(context.Background(), "blank", "   ", ""); err == nil ||
		!strings.Contains(err.Error(), "prompt") {
		t.Fatalf("Spawn(blank prompt) = %v, want prompt error", err)
	}
	for name, call := range map[string]func() error{
		"SendAsync": func() error { return teamService.SendAsync("missing", "hi") },
		"Send":      func() error { _, err := teamService.Send(context.Background(), "missing", "hi"); return err },
		"Wait":      func() error { _, err := teamService.Wait(context.Background(), "missing"); return err },
		"Interrupt": func() error { return teamService.Interrupt("missing") },
		"Close":     func() error { return teamService.Close("missing") },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("%s(unknown member) = %v, want unknown-member error", name, err)
		}
	}
	// A negative/empty name still gets the manager's readable error.
	if _, err := teamService.Spawn(context.Background(), "  ", "work", ""); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("Spawn(blank name) = %v, want name error", err)
	}
}

func TestTeamSendModelOverrideInterruptAndClose(t *testing.T) {
	provider := &captureProvider{}
	svc, teamService, _ := newTeamModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	member, err := teamService.Spawn(context.Background(), "worker", "first task", "team-model")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := teamService.Wait(context.Background(), member.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if request := provider.lastRequest(); request.Model != "team-model" {
		t.Fatalf("member model = %q, want team-model", request.Model)
	}
	answer, err := teamService.Send(context.Background(), member.ID, "follow up")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if answer != "ok" {
		t.Fatalf("Send answer = %q, want ok", answer)
	}
	if err := teamService.SendAsync(member.ID, "async follow up"); err != nil {
		t.Fatalf("SendAsync: %v", err)
	}
	if err := teamService.Interrupt(member.ID); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	members := teamService.Members()
	if len(members) != 1 || members[0].Status != "interrupted" {
		t.Fatalf("members after interrupt = %+v", members)
	}
	if err := teamService.Close(member.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if members := liveTeamMembers(teamService.Members()); len(members) != 0 {
		t.Fatalf("members after close = %+v, want no live member", members)
	}
	if err := teamService.Interrupt(member.ID); err == nil {
		t.Fatal("Interrupt after close returned nil error")
	}
}

func TestTeamConcurrentSpawn(t *testing.T) {
	provider := &captureProvider{}
	svc, teamService, _ := newTeamModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, name := range []string{"one", "two"} {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := teamService.Spawn(context.Background(), name, "work "+name, ""); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Spawn: %v", err)
	}
	members := teamService.Members()
	if len(members) != 2 {
		t.Fatalf("Members = %+v, want two", members)
	}
	names := map[string]bool{}
	for _, member := range members {
		names[member.Name] = true
	}
	if !names["one"] || !names["two"] {
		t.Fatalf("member names = %v", names)
	}
}

func TestTeamToolsRegisteredAndDeduplicated(t *testing.T) {
	svc, _, _ := newTeamModuleService(t)
	seen := make(map[string]int)
	for _, spec := range svc.registry.Specs() {
		seen[spec.Name]++
	}
	for _, name := range []string{
		"team_spawn", "team_list", "team_send", "team_wait", "team_last", "team_close", "team_interrupt",
		"spawn_team", "list_teams", "team_status", "close_team", "send_message", "interrupt_agent",
		"spawn_teammate", "list_agents", "wait_agent",
	} {
		if seen[name] != 1 {
			t.Fatalf("team tool %q count = %d (have %v)", name, seen[name], seen)
		}
	}
	// The DSH tools repeat subagent/subagent_fork names; the synchronous tools
	// already registered by RUN-1 must win and never be duplicated.
	if seen["subagent"] != 1 || seen["subagent_fork"] != 1 {
		t.Fatalf("subagent tools = %d/%d, want exactly one each", seen["subagent"], seen["subagent_fork"])
	}
}

func TestTeamToolsUnregisterAndCloseAllOnDispose(t *testing.T) {
	provider := &captureProvider{}
	svc, teamService, ctx := newTeamModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	member, err := teamService.Spawn(context.Background(), "cleanup", "work", "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(teamService.Members()) != 1 {
		t.Fatalf("members before dispose = %+v", teamService.Members())
	}
	if err := ctx.DisposeAll(); err != nil {
		t.Fatalf("DisposeAll: %v", err)
	}
	for _, spec := range svc.registry.Specs() {
		if spec.Name == "team_spawn" || spec.Name == "spawn_teammate" || spec.Name == "spawn_team" {
			t.Fatalf("team tool %q survived disposal", spec.Name)
		}
	}
	if members := liveTeamMembers(teamService.Members()); len(members) != 0 {
		t.Fatalf("members after CloseAll = %+v, want no live member", members)
	}
	if err := teamService.Close(member.ID); err == nil {
		t.Fatal("Close after manager disposal returned nil error")
	}
}

func TestTeamExcludedTools(t *testing.T) {
	names := teamExcludedTools()
	want := []string{"team_spawn", "team_list", "team_send", "team_wait", "team_last", "team_close",
		"team_interrupt", "spawn_teammate", "list_agents", "wait_agent", "subagent", "subagent_fork"}
	for _, name := range want {
		found := false
		for _, excluded := range names {
			if excluded == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("team excluded tools %v lack %q", names, name)
		}
	}
}

func TestTeamToolsDriveTheManager(t *testing.T) {
	provider := &captureProvider{}
	svc, teamService, _ := newTeamModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	spawn := registryTool(t, svc.registry, "spawn_teammate")
	result, err := spawn.Invoke(context.Background(), rig.ToolCall{
		ID: "spawn-1", Name: "spawn_teammate", Arguments: `{"name":"researcher","prompt":"work"}`,
	})
	if err != nil || result.IsError {
		t.Fatalf("spawn_teammate = %+v, %v", result, err)
	}
	members := teamService.Members()
	if len(members) != 1 || members[0].Name != "researcher" {
		t.Fatalf("members after spawn_teammate = %+v", members)
	}
	if answer, err := teamService.Wait(context.Background(), members[0].ID); err != nil || answer != "ok" {
		t.Fatalf("Wait after spawn_teammate = %q, %v", answer, err)
	}
	// The synchronous subagent registration wins the duplicate-name race: the
	// registry's subagent is the synchronous tool, not the manager's async DSH
	// variant.
	syncTool := registryTool(t, svc.registry, "subagent")
	result, err = syncTool.Invoke(context.Background(), rig.ToolCall{
		ID: "sync-1", Name: "subagent", Arguments: `{"prompt":"independent"}`,
	})
	if err != nil || result.IsError || !strings.Contains(result.Content, "subagent") {
		t.Fatalf("synchronous subagent = %+v, %v", result, err)
	}
}
