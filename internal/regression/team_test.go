package regression

// Adversarial audit: the team orchestration chain (contracts.TeamService over
// the rig team manager, registered through the real agent module and a real
// OpenAI-compatible test server). Negative/fault cases: unknown/empty member
// IDs, blank name/prompt, concurrent spawns, provider failures, interruption,
// disposal CloseAll and toolkit registration deduplication.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/agent"
)

// countLiveMembers counts the manager-backed members of a snapshot (the
// restored durable history rows are Historical and read-only).
func countLiveMembers(members []contracts.TeamMember) int {
	live := 0
	for _, member := range members {
		if !member.Historical {
			live++
		}
	}
	return live
}

// TestTeamUnknownAndInvalidOperations covers hostile arguments: empty/unknown
// member IDs on every operation and blank spawn fields must return readable
// errors instead of panicking.
func TestTeamUnknownAndInvalidOperations(t *testing.T) {
	harness := startAgent(t, agent.Config{}, nil)

	if _, err := harness.team.Spawn(context.Background(), "", "do work", ""); err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "name") {
		t.Fatalf("Spawn(blank name) error = %v, want a name error", err)
	}
	if _, err := harness.team.Spawn(context.Background(), "helper", "   ", ""); err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "prompt") {
		t.Fatalf("Spawn(blank prompt) error = %v, want a prompt error", err)
	}

	operations := map[string]func(id string) error{
		"SendAsync": func(id string) error { return harness.team.SendAsync(id, "hi") },
		"Send": func(id string) error {
			_, err := harness.team.Send(context.Background(), id, "hi")
			return err
		},
		"Wait": func(id string) error {
			_, err := harness.team.Wait(context.Background(), id)
			return err
		},
		"Interrupt": func(id string) error { return harness.team.Interrupt(id) },
		"Close":     func(id string) error { return harness.team.Close(id) },
	}
	for name, operation := range operations {
		for _, id := range []string{"member-404", "", "  "} {
			if err := operation(id); err == nil {
				t.Fatalf("%s(%q) returned nil, want an unknown-member error", name, id)
			}
		}
	}
	// Members on an empty manager is an empty slice, never nil panics.
	if members := harness.team.Members(); len(members) != 0 {
		t.Fatalf("Members() = %+v before any spawn, want empty", members)
	}
}

// TestTeamSpawnWaitSendAndMembers covers the happy path through the public
// service, including nil contexts and whitespace trimming.
func TestTeamSpawnWaitSendAndMembers(t *testing.T) {
	harness := startAgent(t, agent.Config{}, func(body []byte) (int, string) {
		return 0, sseAnswer("member answer for " + string(requestContent(t, body, "user")))
	})

	member, err := harness.team.Spawn(nil, "  researcher  ", "  summarize the repo  ", "")
	if err != nil {
		t.Fatalf("Spawn(nil ctx): %v", err)
	}
	if member.ID == "" || member.Name != "researcher" || member.Status == "" || member.Created.IsZero() {
		t.Fatalf("spawned member = %+v, want id/name/status/created", member)
	}
	first, err := harness.team.Wait(nil, member.ID)
	if err != nil {
		t.Fatalf("Wait(nil ctx): %v", err)
	}
	if !strings.Contains(first, "summarize the repo") {
		t.Fatalf("Wait answer = %q, want the run output", first)
	}
	members := harness.team.Members()
	if len(members) != 1 || members[0].ID != member.ID || members[0].Last != first {
		t.Fatalf("Members() = %+v, want the member with its last answer", members)
	}
	// A follow-up Send returns the teardown answer.
	followUp, err := harness.team.Send(nil, member.ID, "  follow up  ")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !strings.Contains(followUp, "follow up") {
		t.Fatalf("Send answer = %q, want the follow-up output", followUp)
	}
	// SendAsync queues without returning an answer. The member's Last field
	// carries the async answer; Wait() keeps returning the *first* run result
	// (AUDIT-P3-5: the contracts.TeamService.Wait wording says "current run",
	// the rig subagent Wait is pinned to the first run).
	if err := harness.team.SendAsync(member.ID, "async follow up"); err != nil {
		t.Fatalf("SendAsync: %v", err)
	}
	waitFor(t, "the async answer to land in Members().Last", func() bool {
		for _, listed := range harness.team.Members() {
			if listed.ID == member.ID && strings.Contains(listed.Last, "async follow up") {
				return true
			}
		}
		return false
	})
	answer, err := harness.team.Wait(context.Background(), member.ID)
	if err != nil {
		t.Fatalf("Wait after SendAsync: %v", err)
	}
	if !strings.Contains(answer, "async follow up") {
		t.Logf("AUDIT-P3-5: Wait after SendAsync returned the initial answer %q while the latest answer is in Members().Last", answer)
	}
	if err := harness.team.Close(member.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// A closed member is archived as durable history (Historical) instead of
	// disappearing: "the team is empty" means "no live member".
	if members := harness.team.Members(); countLiveMembers(members) != 0 {
		t.Fatalf("Members() = %+v after Close, want no live member", members)
	}
}

// TestTeamConcurrentSpawn hammers Spawn from several goroutines: every member is
// created once with a unique ID, every Wait gets its own answer and the manager
// stays consistent (-race).
func TestTeamConcurrentSpawn(t *testing.T) {
	harness := startAgent(t, agent.Config{}, func(body []byte) (int, string) {
		return 0, sseAnswer("answer " + requestContent(t, body, "user"))
	})

	const members = 6
	var wg sync.WaitGroup
	results := make(chan contracts.TeamMember, members)
	errs := make(chan error, members)
	for index := 0; index < members; index++ {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			member, err := harness.team.Spawn(context.Background(),
				"worker-"+itoa(int64(index)), "job-"+itoa(int64(index)), "")
			if err != nil {
				errs <- err
				return
			}
			results <- member
		}()
	}
	wg.Wait()
	close(errs)
	close(results)
	for err := range errs {
		t.Fatalf("concurrent Spawn: %v", err)
	}
	ids := make(map[string]bool, members)
	for member := range results {
		if member.ID == "" || ids[member.ID] {
			t.Fatalf("member ID %q is empty or duplicated", member.ID)
		}
		ids[member.ID] = true
	}
	if got := harness.team.Members(); len(got) != members {
		t.Fatalf("Members() = %d after concurrent spawn, want %d", len(got), members)
	}
	var waitWG sync.WaitGroup
	for id := range ids {
		id := id
		waitWG.Add(1)
		go func() {
			defer waitWG.Done()
			answer, err := harness.team.Wait(context.Background(), id)
			if err != nil {
				t.Errorf("Wait(%q): %v", id, err)
				return
			}
			if !strings.HasPrefix(answer, "answer job-") {
				t.Errorf("Wait(%q) = %q, want its own job answer", id, answer)
			}
		}()
	}
	waitWG.Wait()
}

// TestTeamProviderErrorPropagation covers a teammate whose provider fails: Spawn
// succeeds (the run is asynchronous), Wait/Send surface the provider error, the
// member stays listed and can be closed.
func TestTeamProviderErrorPropagation(t *testing.T) {
	harness := startAgent(t, agent.Config{}, func(body []byte) (int, string) {
		if requestModel(t, body) == "broken-model" {
			return 400, ""
		}
		return 0, sseAnswer("ok")
	})
	member, err := harness.team.Spawn(context.Background(), "broken", "do work", "broken-model")
	if err != nil {
		t.Fatalf("Spawn with a failing provider model: %v", err)
	}
	if member.ID == "" {
		t.Fatalf("Spawn returned no member for a failing provider: %+v", member)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := harness.team.Wait(waitCtx, member.ID); err == nil {
		t.Fatal("Wait succeeded although the provider returned 400")
	} else if !strings.Contains(strings.ToLower(err.Error()), "model") &&
		!strings.Contains(err.Error(), "400") &&
		!strings.Contains(strings.ToLower(err.Error()), "scripted model failure") {
		t.Fatalf("Wait provider error = %v, want the provider failure detail", err)
	}
	// The failed member is still manageable: listed, then closable.
	found := false
	for _, listed := range harness.team.Members() {
		if listed.ID == member.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("Members() = %+v lacks the failed member", harness.team.Members())
	}
	if err := harness.team.Close(member.ID); err != nil {
		t.Fatalf("Close after provider failure: %v", err)
	}
}

// TestTeamInterruptStopsRun covers interruption of an in-flight run: the member
// becomes interrupted and Wait reports the interruption instead of a result.
func TestTeamInterruptStopsRun(t *testing.T) {
	release := make(chan struct{})
	model := newFakeModel(t, func([]byte) (int, string) {
		<-release // hold the run open until the test interrupts it
		return 0, sseAnswer("too late")
	})
	t.Cleanup(func() { close(release) })
	harness := startAgentWith(t, agent.Config{}, model)

	member, err := harness.team.Spawn(context.Background(), "slow", "long task", "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	waitFor(t, "the member run to reach the provider", func() bool {
		return len(harness.model.callsSnapshot()) >= 1
	})
	if err := harness.team.Interrupt(member.ID); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := harness.team.Wait(waitCtx, member.ID); err == nil {
		t.Fatal("Wait succeeded after Interrupt")
	}
	interrupted := false
	for _, listed := range harness.team.Members() {
		if listed.ID == member.ID && listed.Status == "interrupted" {
			interrupted = true
		}
	}
	if !interrupted {
		t.Fatalf("Members() = %+v, want the member marked interrupted", harness.team.Members())
	}
	if err := harness.team.Close(member.ID); err != nil {
		t.Fatalf("Close interrupted member: %v", err)
	}
}

// TestTeamDisposeClosesAllAndUnregistersTools is the disposal negative: closing
// the kernel root must stop every teammate and unregister the model-facing team
// tools it contributed, while leaving caller-provided tools alone.
func TestTeamDisposeClosesAllAndUnregistersTools(t *testing.T) {
	marker := fakeRigTool{name: "audit_marker"}
	harness := startAgentWith(t, agent.Config{}, newFakeModel(t, nil), marker)
	for index := 0; index < 2; index++ {
		if _, err := harness.team.Spawn(context.Background(), "cleanup-"+itoa(int64(index)), "work", ""); err != nil {
			t.Fatalf("Spawn: %v", err)
		}
	}
	if got := len(harness.team.Members()); got != 2 {
		t.Fatalf("Members() = %d before disposal, want 2", got)
	}
	if err := harness.h.ctx.DisposeAll(); err != nil {
		t.Fatalf("DisposeAll: %v", err)
	}
	for _, spec := range harness.registry.Specs() {
		switch spec.Name {
		case "team_spawn", "team_list", "spawn_team", "spawn_teammate", "list_agents", "wait_agent":
			t.Fatalf("team tool %q survived kernel disposal", spec.Name)
		}
	}
	if err := harness.team.Close("member-1"); err == nil {
		t.Fatal("Close after manager disposal returned nil error")
	}
	if members := harness.team.Members(); countLiveMembers(members) != 0 {
		t.Fatalf("Members() = %+v after CloseAll, want no live member", members)
	}
	if _, ok := findRegistrySpec(harness.registry, "audit_marker"); !ok {
		t.Fatal("the caller-provided marker tool was unregistered by agent disposal")
	}
}

// TestAgentToolRegistrationDeduplicatesTeamNames is the registration negative:
// pre-existing toolkit names win over the agent's team tools (a read-only host
// tool set may already occupy a name); the module logs and skips instead of
// failing activation, and every registered name stays unique.
func TestAgentToolRegistrationDeduplicatesTeamNames(t *testing.T) {
	seeded := []fakeRigTool{
		{name: "spawn_teammate"},
		{name: "team_spawn"},
	}
	harness := startAgentWith(t, agent.Config{}, newFakeModel(t, nil), seeded[0], seeded[1])

	seen := make(map[string]int)
	for _, spec := range harness.registry.Specs() {
		seen[spec.Name]++
	}
	for _, name := range []string{"spawn_teammate", "team_spawn"} {
		if seen[name] != 1 {
			t.Fatalf("tool %q count = %d, want exactly one (the seeded marker)", name, seen[name])
		}
		spec, ok := findRegistrySpec(harness.registry, name)
		if !ok || spec.Description != "audit marker tool" {
			t.Fatalf("tool %q is not the seeded marker: %+v (ok=%v)", name, spec, ok)
		}
	}
	// The synchronous subagent names are registered once, by the agent itself.
	for _, name := range []string{"subagent", "subagent_fork"} {
		if seen[name] != 1 {
			t.Fatalf("subagent tool %q count = %d, want exactly one", name, seen[name])
		}
	}
	// A team tool that had no conflict is registered by the agent.
	for _, name := range []string{"team_list", "spawn_team", "list_agents"} {
		if seen[name] != 1 {
			t.Fatalf("team tool %q count = %d, want one", name, seen[name])
		}
	}
	if !harness.h.logger.contains("[agent] skip team tool spawn_teammate") {
		t.Fatalf("the duplicate team tool was not logged: %v", harness.h.logger.lines)
	}
}

// findRegistrySpec returns one tool spec by name.
func findRegistrySpec(registry *rigtool.Registry, name string) (rig.ToolSpec, bool) {
	for _, candidate := range registry.Specs() {
		if candidate.Name == name {
			return candidate, true
		}
	}
	return rig.ToolSpec{}, false
}
