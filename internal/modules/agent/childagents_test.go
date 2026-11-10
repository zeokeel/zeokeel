package agent

// Tests for the unified child-agent read model (contracts.ChildAgentService):
// the session shell lists synchronous subagent delegations and team members
// through one interface, maps both backends to one status/kind vocabulary and
// routes follow-up messages only to continuable teammates.

import (
	"context"
	"strings"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// newChildAgentsModuleService activates the agent module and returns the
// concrete chat service, the team service and the child-agent service the
// session UI consumes. Requiring KeyChildAgents here also pins the module
// wiring next to KeySubagents/KeyTeam.
func newChildAgentsModuleService(t *testing.T) (*service, contracts.TeamService, contracts.ChildAgentService) {
	t.Helper()
	svc, teamService, ctx := newTeamModuleService(t)
	children, err := ctx.Require(contracts.KeyChildAgents)
	if err != nil {
		t.Fatalf("require KeyChildAgents: %v", err)
	}
	if children == nil {
		t.Fatal("KeyChildAgents is nil")
	}
	return svc, teamService, children
}

// findChildAgent returns the first child whose ID matches.
func findChildAgent(t *testing.T, agents []contracts.ChildAgent, id string) contracts.ChildAgent {
	t.Helper()
	for _, agent := range agents {
		if agent.ID == id {
			return agent
		}
	}
	t.Fatalf("child agent %q not in %+v", id, agents)
	return contracts.ChildAgent{}
}

// hasChildActivity reports whether one child timeline carries a matching entry.
func hasChildActivity(agent contracts.ChildAgent, want func(contracts.ChildAgentActivity) bool) bool {
	for _, activity := range agent.Activity {
		if want(activity) {
			return true
		}
	}
	return false
}

// TestChildAgentsProvidedAndEmpty pins the service wiring and the empty read
// model: a fresh service has no children and an unknown ID is not found.
func TestChildAgentsProvidedAndEmpty(t *testing.T) {
	svc, _, children := newChildAgentsModuleService(t)
	session, err := svc.NewSession("parent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if agents := children.ChildAgents(session); len(agents) != 0 {
		t.Fatalf("ChildAgents(fresh) = %+v, want none", agents)
	}
	if agents := children.ChildAgents(""); len(agents) != 0 {
		t.Fatalf("ChildAgents(active, fresh) = %+v, want none", agents)
	}
	for _, id := range []string{"", "subagent-404", "member-404"} {
		if agent, ok := children.ChildAgent(id); ok {
			t.Fatalf("ChildAgent(%q) = %+v, want not found", id, agent)
		}
	}
}

// TestChildAgentsSubagentMappingAndStatus pins the synchronous delegation half:
// every SubagentActivity status maps to one ChildAgent status, the fields are
// carried over, the list is scoped to the parent session (empty = active
// session) and the direct lookup ignores the scope.
func TestChildAgentsSubagentMappingAndStatus(t *testing.T) {
	svc, _, children := newChildAgentsModuleService(t)
	parent, err := svc.NewSession("parent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	other, err := svc.NewSession("other")
	if err != nil {
		t.Fatalf("NewSession(other): %v", err)
	}
	// The second NewSession makes "other" the active session.

	running := svc.subagentActivities.start(contracts.SubagentActivity{
		Name: "研究员", SessionID: parent, Model: "model-x", Prompt: "audit the repo",
	})
	done := svc.subagentActivities.start(contracts.SubagentActivity{
		Name: "sub-done", SessionID: parent, Model: "model-x", Prompt: "finish the task",
	})
	svc.subagentActivities.finish(done.ID, contracts.SubagentStatusDone, "the answer")
	failed := svc.subagentActivities.start(contracts.SubagentActivity{
		Name: "sub-failed", SessionID: parent, Prompt: "fail the task",
	})
	svc.subagentActivities.finish(failed.ID, contracts.SubagentStatusError, "provider exploded")
	cancelled := svc.subagentActivities.start(contracts.SubagentActivity{
		Name: "sub-cancelled", SessionID: parent, Prompt: "cancel the task",
	})
	svc.subagentActivities.finish(cancelled.ID, contracts.SubagentStatusCancelled, "")
	foreign := svc.subagentActivities.start(contracts.SubagentActivity{
		Name: "sub-foreign", SessionID: other, Prompt: "other session task",
	})

	agents := children.ChildAgents(parent)
	if len(agents) != 4 {
		t.Fatalf("ChildAgents(parent) = %+v, want the four parent delegations", agents)
	}
	// Oldest first, and every subagent carries the unified kind/mode.
	for index := 1; index < len(agents); index++ {
		if agents[index].Started.Before(agents[index-1].Started) {
			t.Fatalf("ChildAgents order = %+v, want oldest first", agents)
		}
	}
	for _, agent := range agents {
		if agent.Kind != contracts.ChildAgentSubagent {
			t.Fatalf("subagent child kind = %q, want %q", agent.Kind, contracts.ChildAgentSubagent)
		}
		if agent.Mode != contracts.ChildAgentOneShot {
			t.Fatalf("subagent child mode = %q, want one-shot", agent.Mode)
		}
		if agent.ParentSessionID != parent {
			t.Fatalf("subagent child parent = %q, want %q", agent.ParentSessionID, parent)
		}
		if len(agent.Activity) != 0 {
			t.Fatalf("subagent child activity = %+v, want none (the registry has no timeline)", agent.Activity)
		}
		if agent.UsageTokens != 0 || agent.Iterations != 0 {
			t.Fatalf("subagent child usage = %d/%d, want 0/0", agent.UsageTokens, agent.Iterations)
		}
	}

	runningChild := findChildAgent(t, agents, running.ID)
	if runningChild.Status != contracts.ChildAgentRunning {
		t.Fatalf("running child status = %q, want running", runningChild.Status)
	}
	if runningChild.Name != "研究员" || runningChild.Model != "model-x" || runningChild.Prompt != "audit the repo" {
		t.Fatalf("running child fields = %+v", runningChild)
	}
	if runningChild.Started.IsZero() || !runningChild.Finished.IsZero() ||
		runningChild.Output != "" || runningChild.Error != "" {
		t.Fatalf("running child timestamps/output = %+v", runningChild)
	}

	doneChild := findChildAgent(t, agents, done.ID)
	if doneChild.Status != contracts.ChildAgentCompleted || doneChild.Output != "the answer" || doneChild.Error != "" {
		t.Fatalf("completed child = %+v, want completed with the answer", doneChild)
	}
	if doneChild.Finished.IsZero() {
		t.Fatal("completed child has no finish time")
	}

	failedChild := findChildAgent(t, agents, failed.ID)
	if failedChild.Status != contracts.ChildAgentFailed || failedChild.Error != "provider exploded" || failedChild.Output != "" {
		t.Fatalf("failed child = %+v, want failed with the error", failedChild)
	}

	cancelledChild := findChildAgent(t, agents, cancelled.ID)
	if cancelledChild.Status != contracts.ChildAgentInterrupted {
		t.Fatalf("cancelled child status = %q, want interrupted", cancelledChild.Status)
	}
	if cancelledChild.Output != "" || cancelledChild.Error != "" {
		t.Fatalf("cancelled child output/error = %+v, want none", cancelledChild)
	}

	// Session scoping: an explicit parent keeps other sessions' delegations out,
	// an empty parent means the active session ("other" here).
	otherAgents := children.ChildAgents(other)
	if len(otherAgents) != 1 || otherAgents[0].ID != foreign.ID {
		t.Fatalf("ChildAgents(other) = %+v, want only %q", otherAgents, foreign.ID)
	}
	activeAgents := children.ChildAgents("")
	if len(activeAgents) != 1 || activeAgents[0].ID != foreign.ID {
		t.Fatalf("ChildAgents(empty) = %+v, want the active session's delegation %q", activeAgents, foreign.ID)
	}

	// The direct lookup is by ID and ignores the parent scope.
	if child, ok := children.ChildAgent(running.ID); !ok || child.ID != running.ID {
		t.Fatalf("ChildAgent(%q) = %+v, %v", running.ID, child, ok)
	}
}

// TestChildAgentsTeammateMappingAndActivity pins the teammate half: a member is
// continuable, process-wide (so it is listed for every parent scope with no
// parent session), its TeamRun* state maps to a ChildAgent status and its
// timeline is the team activity table filtered by member ID.
func TestChildAgentsTeammateMappingAndActivity(t *testing.T) {
	provider := &captureProvider{}
	svc, teamService, children := newChildAgentsModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	first, err := teamService.Spawn(context.Background(), "researcher", "summarize the repo", "")
	if err != nil {
		t.Fatalf("Spawn(first): %v", err)
	}
	second, err := teamService.Spawn(context.Background(), "reviewer", "review the diff", "")
	if err != nil {
		t.Fatalf("Spawn(second): %v", err)
	}
	for _, member := range []contracts.TeamMember{first, second} {
		if _, err := teamService.Wait(context.Background(), member.ID); err != nil {
			t.Fatalf("Wait(%s): %v", member.ID, err)
		}
	}

	agents := children.ChildAgents("")
	if len(agents) != 2 {
		t.Fatalf("ChildAgents = %+v, want the two teammates", agents)
	}
	for _, agent := range agents {
		if agent.Kind != contracts.ChildAgentTeammate {
			t.Fatalf("teammate child kind = %q, want %q", agent.Kind, contracts.ChildAgentTeammate)
		}
		if agent.Mode != contracts.ChildAgentContinuable {
			t.Fatalf("teammate child mode = %q, want continuable", agent.Mode)
		}
		if agent.ParentSessionID != "" {
			t.Fatalf("teammate child parent = %q, want none (the team is process-wide)", agent.ParentSessionID)
		}
		if agent.Status != contracts.ChildAgentCompleted {
			t.Fatalf("teammate child %q status = %q, want completed", agent.ID, agent.Status)
		}
		if agent.Output != "ok" || agent.Error != "" {
			t.Fatalf("teammate child output/error = %+v, want the finished answer", agent)
		}
		if agent.Started.IsZero() || agent.Finished.IsZero() {
			t.Fatalf("teammate child timestamps = %+v", agent)
		}
		if len(agent.Activity) == 0 {
			t.Fatalf("teammate child %q has no activity timeline", agent.ID)
		}
		for _, activity := range agent.Activity {
			if activity.ID == "" || activity.Kind == "" || activity.Text == "" || activity.Time.IsZero() {
				t.Fatalf("teammate child %q activity = %+v, want a full row", agent.ID, activity)
			}
		}
	}
	firstChild := findChildAgent(t, agents, first.ID)
	if firstChild.Name != "researcher" {
		t.Fatalf("teammate child name = %q, want researcher", firstChild.Name)
	}
	if !strings.Contains(firstChild.Prompt, "summarize the repo") {
		t.Fatalf("teammate child prompt = %q, want the spawn task", firstChild.Prompt)
	}
	if !hasChildActivity(firstChild, func(activity contracts.ChildAgentActivity) bool {
		return activity.Kind == contracts.TeamActivitySpawn
	}) {
		t.Fatalf("teammate child activity = %+v, want the spawn event first", firstChild.Activity)
	}

	// A follow-up run adds its send activity to the addressed member only, so
	// the timeline is filtered by member ID and never leaks across teammates.
	if _, err := teamService.Send(context.Background(), first.ID, "follow up alpha"); err != nil {
		t.Fatalf("Send(first): %v", err)
	}
	firstChild, ok := children.ChildAgent(first.ID)
	if !ok {
		t.Fatalf("ChildAgent(%q) = not found", first.ID)
	}
	if !hasChildActivity(firstChild, func(activity contracts.ChildAgentActivity) bool {
		return activity.Kind == contracts.TeamActivitySend && strings.Contains(activity.Text, "follow up alpha")
	}) {
		t.Fatalf("first child activity = %+v, want its send event", firstChild.Activity)
	}
	secondChild := findChildAgent(t, children.ChildAgents(""), second.ID)
	for _, activity := range secondChild.Activity {
		if strings.Contains(activity.Text, "follow up alpha") {
			t.Fatalf("second child timeline = %+v, leaked the first member's send", secondChild.Activity)
		}
	}

	// Teammates are process-wide: a scope that matches no delegation still
	// lists them (the active conversation is where the team is steered).
	scoped := children.ChildAgents("session-without-delegations")
	if len(scoped) != 2 {
		t.Fatalf("ChildAgents(other scope) = %+v, want the process-wide teammates", scoped)
	}
}

// TestChildAgentsTeammateRunningStatus covers the live run state: a teammate
// whose initial run is in flight maps to running (no output, no finish time)
// and to completed once the run returns.
func TestChildAgentsTeammateRunningStatus(t *testing.T) {
	provider := newGatedSubagentProvider()
	svc, teamService, children := newChildAgentsModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	member, err := teamService.Spawn(context.Background(), "slow", "long task", "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	provider.waitStarted(t)

	runningChild, ok := children.ChildAgent(member.ID)
	if !ok {
		t.Fatalf("ChildAgent(%q) = not found while running", member.ID)
	}
	if runningChild.Status != contracts.ChildAgentRunning {
		t.Fatalf("running teammate status = %q, want running", runningChild.Status)
	}
	if runningChild.Output != "" || runningChild.Error != "" || !runningChild.Finished.IsZero() {
		t.Fatalf("running teammate = %+v, want no output/finish", runningChild)
	}

	close(provider.release)
	if _, err := teamService.Wait(context.Background(), member.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	completed, ok := children.ChildAgent(member.ID)
	if !ok {
		t.Fatalf("ChildAgent(%q) = not found after the run", member.ID)
	}
	if completed.Status != contracts.ChildAgentCompleted || completed.Output != "ok" {
		t.Fatalf("completed teammate = %+v, want completed with the answer", completed)
	}
	if completed.Finished.IsZero() {
		t.Fatal("completed teammate has no finish time")
	}
}

// TestChildAgentsMergedOldestFirst pins the unified list: subagents and
// teammates are merged and ordered by their start time.
func TestChildAgentsMergedOldestFirst(t *testing.T) {
	provider := &captureProvider{}
	svc, teamService, children := newChildAgentsModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	parent, err := svc.NewSession("parent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	member, err := teamService.Spawn(context.Background(), "researcher", "team task", "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := teamService.Wait(context.Background(), member.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	activity := svc.subagentActivities.start(contracts.SubagentActivity{
		Name: "sub-1", SessionID: parent, Prompt: "sync task",
	})
	svc.subagentActivities.finish(activity.ID, contracts.SubagentStatusDone, "answer")

	agents := children.ChildAgents(parent)
	if len(agents) != 2 {
		t.Fatalf("ChildAgents(parent) = %+v, want the teammate and the delegation", agents)
	}
	if agents[0].Kind != contracts.ChildAgentTeammate || agents[0].ID != member.ID {
		t.Fatalf("first merged child = %+v, want the earlier teammate", agents[0])
	}
	if agents[1].Kind != contracts.ChildAgentSubagent || agents[1].ID != activity.ID {
		t.Fatalf("second merged child = %+v, want the later delegation", agents[1])
	}
	for index := 1; index < len(agents); index++ {
		if agents[index].Started.Before(agents[index-1].Started) {
			t.Fatalf("merged order = %+v, want oldest first", agents)
		}
	}
}

// TestSendChildAgentRoutesToTeamAndRejectsOneShot pins the control surface: a
// teammate receives the follow-up through the team Send path, a one-shot
// subagent is rejected with a readable error and unknown IDs are not found.
func TestSendChildAgentRoutesToTeamAndRejectsOneShot(t *testing.T) {
	provider := &captureProvider{}
	svc, teamService, children := newChildAgentsModuleService(t)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	member, err := teamService.Spawn(context.Background(), "worker", "first task", "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := teamService.Wait(context.Background(), member.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	answer, err := children.SendChildAgent(context.Background(), member.ID, "follow up please")
	if err != nil {
		t.Fatalf("SendChildAgent(teammate): %v", err)
	}
	if answer != "ok" {
		t.Fatalf("SendChildAgent answer = %q, want the teammate's answer", answer)
	}
	if got := lastUserMessage(t, provider.lastRequest()).Text(); !strings.Contains(got, "follow up please") {
		t.Fatalf("follow-up prompt = %q, want the message", got)
	}
	sent, ok := children.ChildAgent(member.ID)
	if !ok {
		t.Fatalf("ChildAgent(%q) = not found", member.ID)
	}
	if !hasChildActivity(sent, func(activity contracts.ChildAgentActivity) bool {
		return activity.Kind == contracts.TeamActivitySend && strings.Contains(activity.Text, "follow up please")
	}) {
		t.Fatalf("teammate activity = %+v, want the follow-up send event", sent.Activity)
	}

	delegation := svc.subagentActivities.start(contracts.SubagentActivity{Name: "sub", Prompt: "one shot"})
	svc.subagentActivities.finish(delegation.ID, contracts.SubagentStatusDone, "done")
	_, err = children.SendChildAgent(context.Background(), delegation.ID, "continue")
	if err == nil || !strings.Contains(err.Error(), "one-shot") {
		t.Fatalf("SendChildAgent(one-shot) = %v, want a one-shot error", err)
	}

	if _, err := children.SendChildAgent(context.Background(), "missing", "hi"); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Fatalf("SendChildAgent(unknown) = %v, want a not-found error", err)
	}
}
