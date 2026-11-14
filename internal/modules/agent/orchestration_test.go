package agent

// Tests for the orchestration tools (workflow/ralph), the delegated-child round
// budget and the orchestration guidance/opt-out behavior.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// captureCallCount reports how many provider requests a captureProvider saw.
func captureCallCount(provider *captureProvider) int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return len(provider.requests)
}

// registryToolNames returns the model-visible tool names of one request.
func requestToolNames(request rig.Request) map[string]bool {
	names := make(map[string]bool, len(request.Tools))
	for _, spec := range request.Tools {
		names[strings.TrimSpace(spec.Name)] = true
	}
	return names
}

// TestOrchestrationToolsRegistered pins the model surface: workflow and ralph
// are registered by the agent module with readable descriptions.
func TestOrchestrationToolsRegistered(t *testing.T) {
	svc := newTestService(t, Config{}, newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"}), nil)
	names := make(map[string]string, len(svc.registry.Specs()))
	for _, spec := range svc.registry.Specs() {
		names[spec.Name] = spec.Description
	}
	for _, want := range []string{workflowToolName, ralphToolName} {
		description, ok := names[want]
		if !ok {
			t.Fatalf("registry lacks orchestration tool %q (have %v)", want, toolNameMapKeys(names))
		}
		if strings.TrimSpace(description) == "" {
			t.Fatalf("orchestration tool %q has an empty description", want)
		}
	}
}

// toolNameMapKeys returns the map keys sorted for readable failures.
func toolNameMapKeys(names map[string]string) []string {
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	return out
}

// TestWorkflowToolRunsThroughTheDynamicRunner drives the real workflow tool over
// the product's dynamic subagent runner with a scripted provider, and verifies
// the child surface excludes every orchestration tool (no recursion) while the
// step outputs are returned.
func TestWorkflowToolRunsThroughTheDynamicRunner(t *testing.T) {
	provider := &captureProvider{}
	svc := newTestService(t, Config{}, newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"}), nil)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	sessionID, err := svc.NewSession("workflow")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	ctx := contracts.WithAgentRunOrigin(contracts.WithAgentSession(context.Background(), sessionID), contracts.AgentRunOriginUser)
	result, err := svc.registry.Invoke(ctx, rig.ToolCall{
		ID:   "call-workflow",
		Name: workflowToolName,
		Arguments: `{"steps":[` +
			`{"name":"research","prompt":"research the bug"},` +
			`{"name":"fix","prompt":"fix it using {{prev}}"}]}`,
	})
	if err != nil {
		t.Fatalf("workflow invoke: %v", err)
	}
	if result.IsError {
		t.Fatalf("workflow returned a tool error: %s", result.Content)
	}
	for _, want := range []string{"research", "fix", "ok"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("workflow result lacks %q:\n%s", want, result.Content)
		}
	}
	if got := captureCallCount(provider); got != 2 {
		t.Fatalf("workflow ran %d child requests, want 2", got)
	}
	childTools := requestToolNames(provider.lastRequest())
	for _, forbidden := range []string{workflowToolName, ralphToolName, subagentToolName, subagentForkToolName, "spawn_teammate"} {
		if childTools[forbidden] {
			t.Fatalf("workflow child received orchestration tool %q (recursion is not excluded)", forbidden)
		}
	}
	// The child keeps its non-orchestration surface (this unit-test service has
	// the agent-registered session tools rather than the full toolkit catalog).
	if len(childTools) == 0 {
		t.Fatal("workflow child lost its whole tool surface")
	}
	if !childTools["session_search"] {
		t.Fatalf("workflow child lost its normal (non-orchestration) tool surface: %v", childTools)
	}
}

// TestRalphToolRunsBoundedRounds drives ralph with a provider that never reports
// completion: it must stop at max_rounds instead of looping.
func TestRalphToolRunsBoundedRounds(t *testing.T) {
	provider := &captureProvider{}
	svc := newTestService(t, Config{}, newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"}), nil)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	sessionID, err := svc.NewSession("ralph")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	ctx := contracts.WithAgentRunOrigin(contracts.WithAgentSession(context.Background(), sessionID), contracts.AgentRunOriginUser)
	result, err := svc.registry.Invoke(ctx, rig.ToolCall{
		ID: "call-ralph", Name: ralphToolName,
		Arguments: `{"prompt":"keep improving","max_rounds":2}`,
	})
	if err != nil {
		t.Fatalf("ralph invoke: %v", err)
	}
	// The provider never reports DONE, so ralph stops at its round cap with the
	// bounded-stop message instead of looping forever.
	if !result.IsError {
		t.Fatalf("ralph without a DONE marker returned success: %s", result.Content)
	}
	if !strings.Contains(result.Content, "max_rounds") {
		t.Fatalf("ralph error = %q, want the bounded-stop message", result.Content)
	}
	if got := captureCallCount(provider); got != 2 {
		t.Fatalf("ralph ran %d requests, want the 2-round cap", got)
	}
}

// TestDelegatedParentSpecUsesExplicitBudget pins the budget alignment: a
// delegated child gets the product's explicit budget (never Rig's 20-round
// default) and a configured chat.maxIterations applies to children too.
func TestDelegatedParentSpecUsesExplicitBudget(t *testing.T) {
	provider := &captureProvider{}
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	svc := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	spec := svc.delegatedParentSpec("sess-1", "", nil, svc.Config(), provider, contracts.AgentRunOriginSubagent)
	if spec.MaxIterations != agentIterationLimitUnbounded {
		t.Fatalf("default delegated budget = %d, want the unlimited sentinel %d (not rig's 20)",
			spec.MaxIterations, agentIterationLimitUnbounded)
	}
	if spec.MaxIterations == 20 {
		t.Fatal("delegated child falls back to rig's default 20 rounds")
	}

	configured := newService(Config{MaxIterations: 40}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
	configured.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	if got := configured.delegatedParentSpec("sess-1", "", nil, configured.Config(), provider,
		contracts.AgentRunOriginSubagent).MaxIterations; got != 40 {
		t.Fatalf("configured delegated budget = %d, want 40", got)
	}

	// The parent spec scopes every tool with the run session and origin.
	probe := &contextProbeTool{}
	scoped := svc.delegatedParentSpec("sess-9", "", []rig.Tool{probe}, svc.Config(), provider,
		contracts.AgentRunOriginSubagent)
	if len(scoped.Tools) != 1 {
		t.Fatalf("delegated tools = %d, want 1", len(scoped.Tools))
	}
	if _, err := scoped.Tools[0].Invoke(context.Background(), rig.ToolCall{Name: probe.Name()}); err != nil {
		t.Fatalf("invoke scoped tool: %v", err)
	}
	if probe.sessionID != "sess-9" || probe.origin != contracts.AgentRunOriginSubagent {
		t.Fatalf("scoped tool context = %q/%q, want sess-9/subagent", probe.sessionID, probe.origin)
	}
}

// TestOrchestrationGuidanceAndOptOut covers the model-facing guidance and the
// existing opt-out mechanisms (no invented setting): workflow is explicit-request
// only; ralph is experimental/opt-in and removable through tools.disabled, and
// neither is on the restricted-mode whitelists.
func TestOrchestrationGuidanceAndOptOut(t *testing.T) {
	lines := strings.Join(toolGuidanceLines(map[string]bool{workflowToolName: true, ralphToolName: true}), "\n")
	for _, want := range []string{
		"only when the user explicitly asks for a workflow",
		"never for a single delegation",
		"only when the user explicitly asks for Ralph",
		"experimental and opt-in",
	} {
		if !strings.Contains(lines, want) {
			t.Fatalf("orchestration guidance lacks %q:\n%s", want, lines)
		}
	}
	// The restricted runtime modes deny both tools (they are not whitelisted).
	for _, mode := range []string{contracts.AgentModePlan, contracts.ChatModeMinimal, contracts.ChatModeCreator} {
		whitelist := modeToolWhitelist(mode)
		for _, name := range []string{workflowToolName, ralphToolName} {
			if toolAllowed(name, whitelist) {
				t.Fatalf("%s is whitelisted in mode %q; orchestration must stay out of restricted modes", name, mode)
			}
		}
	}
	// tools.disabled is the product's existing opt-out: disabling ralph removes
	// it from the model surface while workflow stays available.
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:    "test-model",
		contracts.SettingToolsDisabled: `["ralph"]`,
	})
	registry := rigtools.NewRegistry(
		&gateTestTool{name: workflowToolName}, &gateTestTool{name: ralphToolName},
	)
	svc := newService(Config{}, registry, contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
	sessionID, err := svc.NewSession("opt-out")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	names := toolNameSet(svc.toolsForSession(sessionID))
	if !names[workflowToolName] {
		t.Fatal("disabling ralph removed workflow from the model surface")
	}
	if names[ralphToolName] {
		t.Fatal("tools.disabled did not remove ralph from the model surface")
	}
}

// TestDelegatedExclusionsCoverOrchestration pins the recursion guard for every
// delegated child kind.
func TestDelegatedExclusionsCoverOrchestration(t *testing.T) {
	for _, family := range [][]string{subagentExcludedTools(), teamExcludedTools()} {
		excluded := make(map[string]bool, len(family))
		for _, name := range family {
			excluded[strings.TrimSpace(name)] = true
		}
		for _, want := range []string{workflowToolName, ralphToolName, subagentToolName, subagentForkToolName, "spawn_teammate"} {
			if !excluded[want] {
				t.Fatalf("child exclusions lack orchestration tool %q: %v", want, family)
			}
		}
	}
}

// TestOrchestrationChildBudgetIsExplicit checks the budget reaches a real child
// run through the dynamic orchestration runner: the parent spec of the runner is
// the only place a child budget can come from, and it must be the explicit
// sentinel rather than an inherited zero.
func TestOrchestrationChildBudgetIsExplicit(t *testing.T) {
	provider := &captureProvider{}
	svc := newService(Config{}, rigtools.NewRegistry(),
		contracts.SettingsService(newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	runner := &dynamicOrchestrationRunner{service: svc}
	sub, err := runner.Start(context.Background(), rig.SubagentSpec{Name: "step", Prompt: "do it"})
	if err != nil {
		t.Fatalf("orchestration runner Start: %v", err)
	}
	defer func() { _ = sub.Stop() }()
	result, err := sub.Wait(context.Background())
	if err != nil {
		t.Fatalf("orchestration child Wait: %v", err)
	}
	if strings.TrimSpace(result.Output) == "" {
		t.Fatal("orchestration child returned no output")
	}
	if got := fmt.Sprint(requestToolNames(provider.lastRequest())); strings.Contains(got, workflowToolName) {
		t.Fatalf("orchestration child received the workflow tool: %s", got)
	}
}

// TestOrchestrationToolsAskForApproval pins the approval classification: like
// the existing subagent/team orchestration tools, workflow and ralph are not in
// the workspace auto-allow set, so the default workspace policy asks the user
// before a delegation starts.
func TestOrchestrationToolsAskForApproval(t *testing.T) {
	svc := newTestService(t, Config{}, newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"}), nil)
	approvals := &recordingApprovals{decision: contracts.ApprovalAllow, mode: "ask"}
	svc.approvals = approvals
	sessionID, err := svc.NewSession("approval")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for _, name := range []string{workflowToolName, ralphToolName} {
		if decision := gateDecision(t, svc, sessionID, name); decision != rig.DecisionAllow {
			t.Fatalf("gate %s = %v, want allow from the fake backend", name, decision)
		}
	}
	approvals.mu.Lock()
	requests := append([]contracts.ApprovalRequest(nil), approvals.requests...)
	approvals.mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("approval requests = %d, want one per orchestration tool", len(requests))
	}
	for index, want := range []string{workflowToolName, ralphToolName} {
		if requests[index].Tool != want {
			t.Fatalf("approval request %d = %q, want %q", index, requests[index].Tool, want)
		}
	}
}

// TestDelegationBudgetGuidance pins the model-facing budget note: the model must
// know delegated children share this session's explicit round budget instead of
// a hidden default.
func TestDelegationBudgetGuidance(t *testing.T) {
	lines := strings.Join(toolGuidanceLines(map[string]bool{"subagent": true, ralphToolName: true}), "\n")
	if !strings.Contains(lines, "explicit round budget rather than a hidden default limit") {
		t.Fatalf("delegation guidance lacks the budget note:\n%s", lines)
	}
	// A surface without any delegation tool must not mention the budget at all.
	solo := strings.Join(toolGuidanceLines(map[string]bool{"read_file": true}), "\n")
	if strings.Contains(solo, "round budget") {
		t.Fatalf("delegation budget guidance leaked to a non-orchestration surface:\n%s", solo)
	}
}
