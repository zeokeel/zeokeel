package agent

// Tests for the OS-sandbox shell auto-allow in the tool approval gate.
//
// The judgement itself belongs to the SDK: gate.AllowSandboxedCommands decides
// "this shell call is already confined" (shell family + a runner that reports
// non-none enforcement + a policy that is not danger-full-access). The product
// supplies the live runner and the policy the toolkit built for the current
// workspace, and keeps its own vetoes in front of it (plan/chat whitelists,
// session permission overrides, the deny-all "never" policy, and the read-only
// preset's "no commands at all" rule).
//
// Precedence is part of the contract: the plan/chat whitelists and the
// per-session read-only override keep their veto even when the OS sandbox is
// available, and the headless deny-all approvals policy ("never") is never
// bypassed by a prompt-skipping auto-allow.

import (
	"context"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/gate"
	rigsandbox "github.com/zeokeel/zeokeel-rig/sandbox"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// sandboxShellFamily is the shell-family surface the auto-allow covers: command
// execution (bash/pwsh); background jobs use bash run_in_background.
var sandboxShellFamily = []string{"bash", "pwsh"}

// reportingRunner is a sandbox runner that also reports how completely it
// enforces the policy — the SDK's auto-allow criterion reads that report.
type reportingRunner struct {
	enforcement rigsandbox.Enforcement
}

func (r reportingRunner) Confine(argv []string, _ *rigsandbox.Policy) (rigsandbox.ConfinedCommand, error) {
	return rigsandbox.ConfinedCommand{Argv: argv, Enforcement: r.RunnerEnforcement()}, nil
}

func (r reportingRunner) RunnerName() string { return "fake-runner" }

func (r reportingRunner) RunnerEnforcement() rigsandbox.Enforcement {
	if r.enforcement == "" {
		return rigsandbox.EnforcementFull
	}
	return r.enforcement
}

func (r reportingRunner) RunnerLimits() string { return "" }

// plainRunner reports nothing at all: the SDK documents unreported runners as
// "unknown" and treats them as partial enforcement, so calls stay eligible.
type plainRunner struct{}

func (plainRunner) Confine(argv []string, _ *rigsandbox.Policy) (rigsandbox.ConfinedCommand, error) {
	return rigsandbox.ConfinedCommand{Argv: argv}, nil
}

// fakeSandbox is a contracts.SandboxService test double. runner is what the
// gate asks the SDK about; available is the settings-surface flag.
type fakeSandbox struct {
	available bool
	runner    rigsandbox.Runner
}

func (f fakeSandbox) Runner() rigsandbox.Runner { return f.runner }

func (f fakeSandbox) Available() bool { return f.available }

func (f fakeSandbox) Status() contracts.SandboxStatus {
	return contracts.SandboxStatus{Available: f.available, Platform: "test"}
}

// fakePolicySource is a contracts.ToolPolicy test double: it returns the policy
// the toolkit would have built for the current workspace.
type fakePolicySource struct {
	policy *rigsandbox.Policy
}

func (f fakePolicySource) CurrentPolicy() *rigsandbox.Policy { return f.policy }

// sandboxPolicy builds a policy with one root and the given mode.
func sandboxPolicy(t *testing.T, mode rigsandbox.Mode) *rigsandbox.Policy {
	t.Helper()
	return rigsandbox.New(
		rigsandbox.WithRoots(t.TempDir()),
		rigsandbox.WithMode(mode),
	)
}

// healthyRunnerSandbox is a service whose runner reports full enforcement.
func healthyRunnerSandbox() contracts.SandboxService {
	return fakeSandbox{available: true, runner: reportingRunner{enforcement: rigsandbox.EnforcementFull}}
}

// healthySandbox is the fully confining setup: a runner that reports full
// enforcement and a workspace-write policy.
func healthySandbox(t *testing.T) (contracts.SandboxService, contracts.ToolPolicy) {
	t.Helper()
	return healthyRunnerSandbox(),
		fakePolicySource{policy: sandboxPolicy(t, rigsandbox.ModeWorkspaceWrite)}
}

// newSandboxGateService builds an agent service with an optional sandbox
// service, policy source, settings and approvals, plus one new session.
func newSandboxGateService(t *testing.T, sandbox contracts.SandboxService, policy contracts.ToolPolicy,
	settings map[string]string, approvals contracts.Approvals) (*service, string) {
	t.Helper()
	svc := newService(Config{}, rigtools.NewRegistry(),
		contracts.SettingsService(newFakeSettings(settings)), &fakeLogger{}, newFakeStore(), approvals)
	svc.sandboxService = sandbox
	svc.policySource = policy
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return svc, sessionID
}

// TestSandboxAutoAllowsShellFamily is the core behavior: with a confining
// runner and a workspace-write policy, every shell-family tool is allowed
// without calling approvals.Request. The fake backend would deny if consulted,
// so an accidental fall-through fails loudly.
func TestSandboxAutoAllowsShellFamily(t *testing.T) {
	sandbox, policy := healthySandbox(t)
	approvals := &recordingApprovals{decision: contracts.ApprovalDeny}
	svc, sessionID := newSandboxGateService(t, sandbox, policy, nil, approvals)

	for _, name := range sandboxShellFamily {
		if got := gateDecision(t, svc, sessionID, name); got != rig.DecisionAllow {
			t.Fatalf("sandbox-available gate %q = %v, want allow without approval", name, got)
		}
	}
	if asked := approvals.askedTools(); len(asked) != 0 {
		t.Fatalf("sandbox auto-allowed shell tools requested approval: %v", asked)
	}
}

// TestSandboxAutoAllowFollowsSDKCriteria pins the SDK criteria the product used
// to ignore: a runner that reports no enforcement must not skip the prompt, and
// neither a missing policy nor a danger-full-access policy may.
func TestSandboxAutoAllowFollowsSDKCriteria(t *testing.T) {
	cases := []struct {
		name    string
		sandbox contracts.SandboxService
		policy  contracts.ToolPolicy
	}{
		{
			name:    "runner reports no enforcement",
			sandbox: fakeSandbox{available: true, runner: reportingRunner{enforcement: rigsandbox.EnforcementNone}},
			policy:  fakePolicySource{policy: sandboxPolicy(t, rigsandbox.ModeWorkspaceWrite)},
		},
		{
			name:    "no policy source",
			sandbox: healthyRunnerSandbox(),
			policy:  nil,
		},
		{
			name:    "policy source without a policy",
			sandbox: healthyRunnerSandbox(),
			policy:  fakePolicySource{},
		},
		{
			name:    "danger-full-access policy",
			sandbox: healthyRunnerSandbox(),
			policy:  fakePolicySource{policy: sandboxPolicy(t, rigsandbox.ModeDangerFullAccess)},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			approvals := &recordingApprovals{decision: contracts.ApprovalAllow}
			svc, sessionID := newSandboxGateService(t, testCase.sandbox, testCase.policy, nil, approvals)
			if got := gateDecision(t, svc, sessionID, "bash"); got != rig.DecisionAllow {
				t.Fatalf("bash = %v, want the approvals backend's allow", got)
			}
			if asked := approvals.askedTools(); len(asked) != 1 || asked[0] != "bash" {
				t.Fatalf("approval requests = %v, want [bash] (the SDK must not have allowed it)", asked)
			}
		})
	}
}

// TestSandboxReadOnlyPolicyAutoAllowsShellWithRunner pins the SDK's read-only
// semantic, which this personal client now follows exactly: with a confining
// runner attached a read-only policy executes commands (every write is confined
// by the runner), so the prompt is skipped. Without a runner the policy itself
// rejects the command and the tool reports "shell commands are disabled in
// read-only mode".
func TestSandboxReadOnlyPolicyAutoAllowsShellWithRunner(t *testing.T) {
	approvals := &recordingApprovals{mode: contracts.PermissionReadOnly, decision: contracts.ApprovalDeny}
	svc, sessionID := newSandboxGateService(t,
		fakeSandbox{available: true, runner: reportingRunner{enforcement: rigsandbox.EnforcementFull}},
		fakePolicySource{policy: sandboxPolicy(t, rigsandbox.ModeWorkspaceWrite)},
		map[string]string{contracts.SettingAgentPermission: contracts.PermissionReadOnly},
		approvals)

	for _, name := range sandboxShellFamily {
		if got := gateDecision(t, svc, sessionID, name); got != rig.DecisionAllow {
			t.Fatalf("read-only gate %q = %v, want allow (the runner confines the writes)", name, got)
		}
	}
	if asked := approvals.askedTools(); len(asked) != 0 {
		t.Fatalf("read-only prompted although the runner confines: %v", asked)
	}
}

// TestSandboxReadOnlyWithoutRunnerAsks pins the other half of the SDK rule:
// without a runner the read-only policy denies the command, and the gate must
// not skip the prompt (the SDK's allowance requires a runner).
func TestSandboxReadOnlyWithoutRunnerAsks(t *testing.T) {
	approvals := &recordingApprovals{mode: contracts.PermissionReadOnly, decision: contracts.ApprovalDeny}
	svc, sessionID := newSandboxGateService(t, nil,
		fakePolicySource{policy: sandboxPolicy(t, rigsandbox.ModeWorkspaceWrite)},
		map[string]string{contracts.SettingAgentPermission: contracts.PermissionReadOnly},
		approvals)
	if got := gateDecision(t, svc, sessionID, "bash"); got != rig.DecisionDeny {
		t.Fatalf("read-only without a runner = %v, want the approvals backend's deny", got)
	}
	if asked := approvals.askedTools(); len(asked) != 1 {
		t.Fatalf("approval requests = %v, want exactly one", asked)
	}
}

// TestSandboxAvailableStillAsksForNonShellTool keeps the auto-allow narrow: a
// non-shell tool (network, team board, unknown) still goes through the
// approvals service.
func TestSandboxAvailableStillAsksForNonShellTool(t *testing.T) {
	sandbox, policy := healthySandbox(t)
	for _, name := range []string{"web_fetch", "team_task_create", "settings_set", "unknown_tool"} {
		t.Run(name, func(t *testing.T) {
			approvals := &recordingApprovals{decision: contracts.ApprovalAllow}
			svc, sessionID := newSandboxGateService(t, sandbox, policy, nil, approvals)
			if got := gateDecision(t, svc, sessionID, name); got != rig.DecisionAllow {
				t.Fatalf("non-shell gate %q = %v, want the approvals backend's allow", name, got)
			}
			if asked := approvals.askedTools(); len(asked) != 1 || asked[0] != name {
				t.Fatalf("approval requests = %v, want [%s]", asked, name)
			}
		})
	}
}

// TestSandboxUnavailableKeepsApprovalFlow proves the host-without-a-runner path
// still asks: a runner that reports no enforcement is never "already sandboxed".
func TestSandboxUnavailableKeepsApprovalFlow(t *testing.T) {
	unavailable := fakeSandbox{available: false, runner: reportingRunner{enforcement: rigsandbox.EnforcementNone}}
	for _, decision := range []struct {
		name  string
		value contracts.ApprovalDecision
		want  rig.Decision
	}{
		{"backend allows", contracts.ApprovalAllow, rig.DecisionAllow},
		{"backend denies", contracts.ApprovalDeny, rig.DecisionDeny},
	} {
		t.Run(decision.name, func(t *testing.T) {
			approvals := &recordingApprovals{decision: decision.value}
			svc, sessionID := newSandboxGateService(t, unavailable,
				fakePolicySource{policy: sandboxPolicy(t, rigsandbox.ModeWorkspaceWrite)}, nil, approvals)
			if got := gateDecision(t, svc, sessionID, "bash"); got != decision.want {
				t.Fatalf("unavailable bash = %v, want %v", got, decision.want)
			}
			if asked := approvals.askedTools(); len(asked) != 1 || asked[0] != "bash" {
				t.Fatalf("approval requests = %v, want [bash]", asked)
			}
		})
	}
}

// TestSandboxNilServiceKeepsApprovalFlow covers assemblies without the sandbox
// module: the gate must stay nil-safe and ask exactly like before.
func TestSandboxNilServiceKeepsApprovalFlow(t *testing.T) {
	approvals := &recordingApprovals{decision: contracts.ApprovalAllow}
	svc, sessionID := newSandboxGateService(t, nil, nil, nil, approvals)
	if got := gateDecision(t, svc, sessionID, "bash"); got != rig.DecisionAllow {
		t.Fatalf("nil-service bash = %v, want the approvals backend's allow", got)
	}
	if asked := approvals.askedTools(); len(asked) != 1 || asked[0] != "bash" {
		t.Fatalf("approval requests = %v, want [bash]", asked)
	}
}

// TestSandboxAvailablePlanModeStillDeniesShell covers whitelist precedence: plan
// mode denies bash without asking even when the OS sandbox is available.
func TestSandboxAvailablePlanModeStillDeniesShell(t *testing.T) {
	sandbox, policy := healthySandbox(t)
	approvals := &recordingApprovals{decision: contracts.ApprovalAllow}
	svc, sessionID := newSandboxGateService(t, sandbox, policy,
		map[string]string{contracts.SettingAgentMode: contracts.AgentModePlan}, approvals)

	for _, name := range append(append([]string{}, sandboxShellFamily...), "job_run") {
		if got := gateDecision(t, svc, sessionID, name); got != rig.DecisionDeny {
			t.Fatalf("plan-mode gate %q = %v, want deny even with the sandbox available", name, got)
		}
	}
	if got := gateDecision(t, svc, sessionID, "read_file"); got != rig.DecisionAllow {
		t.Fatalf("plan-mode read_file = %v, want allow", got)
	}
	if asked := approvals.askedTools(); len(asked) != 0 {
		t.Fatalf("plan mode requested approval: %v", asked)
	}
}

// TestSandboxAvailableReadOnlyOverrideStillDeniesShell covers override
// precedence: a session-level read-only override is a hard restriction, so the
// shell family is denied without consulting approvals even with the sandbox on.
func TestSandboxAvailableReadOnlyOverrideStillDeniesShell(t *testing.T) {
	sandbox, policy := healthySandbox(t)
	approvals := &recordingApprovals{decision: contracts.ApprovalAllow}
	svc, sessionID := newSandboxGateService(t, sandbox, policy, nil, approvals)
	if err := svc.SetSessionPermission(sessionID, contracts.PermissionReadOnly); err != nil {
		t.Fatalf("SetSessionPermission: %v", err)
	}
	for _, name := range sandboxShellFamily {
		if got := gateDecision(t, svc, sessionID, name); got != rig.DecisionDeny {
			t.Fatalf("read-only override gate %q = %v, want deny even with the sandbox available", name, got)
		}
	}
	if got := gateDecision(t, svc, sessionID, "read_file"); got != rig.DecisionAllow {
		t.Fatalf("read-only override read_file = %v, want allow", got)
	}
	if asked := approvals.askedTools(); len(asked) != 0 {
		t.Fatalf("read-only override requested approval: %v", asked)
	}
}

// TestSandboxAvailableNeverPolicyStillDeniesShell preserves the headless
// deny-all invariant: skipping the approval prompt must never bypass an
// explicit approvals "never" policy.
func TestSandboxAvailableNeverPolicyStillDeniesShell(t *testing.T) {
	sandbox, policy := healthySandbox(t)
	approvals := &recordingApprovals{mode: approvalModeNever, decision: contracts.ApprovalAllow}
	svc, sessionID := newSandboxGateService(t, sandbox, policy, nil, approvals)

	for _, name := range sandboxShellFamily {
		if got := gateDecision(t, svc, sessionID, name); got != rig.DecisionDeny {
			t.Fatalf("deny-all policy gate %q = %v, want deny even with the sandbox available", name, got)
		}
	}
	if asked := approvals.askedTools(); len(asked) != 0 {
		t.Fatalf("deny-all policy requested approval: %v", asked)
	}
}

// TestSandboxedAllowanceUsesSDKGate guards against re-growing a private copy of
// the SDK criterion: the product's helper must agree with the SDK for the shell
// surface it claims to cover.
func TestSandboxedAllowanceUsesSDKGate(t *testing.T) {
	allowance := gate.AllowSandboxedCommands(gate.SandboxedOptions{
		Runner: reportingRunner{enforcement: rigsandbox.EnforcementFull},
		Policy: sandboxPolicy(t, rigsandbox.ModeWorkspaceWrite),
	}, nil)
	for _, name := range sandboxShellFamily {
		decision, err := allowance.Approve(context.Background(), &gateTestTool{name: name}, rig.ToolCall{Name: name})
		if err != nil || decision != rig.DecisionAllow {
			t.Fatalf("SDK allowance(%s) = %v/%v, want allow", name, decision, err)
		}
	}
}
