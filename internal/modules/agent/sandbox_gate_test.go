package agent

// Tests for the OS-sandbox shell auto-allow in the tool approval gate.
//
// The judgement itself belongs to the SDK: gate.AllowSandboxedCommands decides
// "this shell call is already confined" (shell family + a runner that reports
// non-none enforcement + a policy that is not danger-full-access). The product
// supplies the live runner, the policy the toolkit built for the run's
// workspace root, and the shell surface it wants judged (bash/pwsh). It keeps its
// own vetoes in front of it: plan/chat whitelists, session permission overrides
// and the deny-all "never" policy.
//
// Precedence is part of the contract: the plan/chat whitelists and the
// per-session read-only override keep their veto even when the OS sandbox is
// available, and the headless deny-all approvals policy ("never") is never
// bypassed by a prompt-skipping auto-allow.

import (
	"context"
	"strings"
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
	// confinement is what the read model reports ("" behaves like "none", the
	// conservative answer). A healthy host reports SandboxConfinementOS.
	confinement string
}

func (f fakeSandbox) Runner() rigsandbox.Runner { return f.runner }

func (f fakeSandbox) Available() bool { return f.available }

func (f fakeSandbox) Status() contracts.SandboxStatus {
	return contracts.SandboxStatus{Available: f.available, Platform: "test", Confinement: f.confinement}
}

// fakePolicySource is a contracts.ToolPolicy test double: it returns the policy
// the toolkit would have built for the requested root. onRoot (optional) sees
// every root the gate asks about, so a test can prove the gate judged the run's
// own workspace instead of whatever is current.
type fakePolicySource struct {
	policy *rigsandbox.Policy
	onRoot func(root string)
}

func (f fakePolicySource) PolicyFor(root string) *rigsandbox.Policy {
	if f.onRoot != nil {
		f.onRoot(root)
	}
	return f.policy
}

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
	return fakeSandbox{available: true, runner: reportingRunner{enforcement: rigsandbox.EnforcementFull},
		confinement: contracts.SandboxConfinementOS}
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
	svc, sessionID, _ := newLoggedGateService(t, sandbox, policy, settings, approvals)
	return svc, sessionID
}

// newLoggedGateService is newSandboxGateService that also hands back the logger,
// so a test can pin the "no OS confinement" record.
func newLoggedGateService(t *testing.T, sandbox contracts.SandboxService, policy contracts.ToolPolicy,
	settings map[string]string, approvals contracts.Approvals) (*service, string, *fakeLogger) {
	t.Helper()
	logger := &fakeLogger{}
	svc := newService(Config{}, rigtools.NewRegistry(),
		contracts.SettingsService(newFakeSettings(settings)), logger, newFakeStore(), approvals)
	svc.sandboxService = sandbox
	svc.policySource = policy
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return svc, sessionID, logger
}

// countLines counts the recorded lines containing fragment.
func countLines(lines []string, fragment string) int {
	count := 0
	for _, line := range lines {
		if strings.Contains(line, fragment) {
			count++
		}
	}
	return count
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

// TestUncertifiedShellFamilyStillSkipsThePrompt pins the second half of the
// command exemption (permission model 2026-11-26): when the SDK does not certify
// confinement — a runner that reports no enforcement, no runner at all, no
// policy source, or a danger-full-access policy — the command family still runs
// without an approval card. The approvals backend is a deny-all fake, so an
// accidental fall-through to approvals.Request fails loudly.
func TestUncertifiedShellFamilyStillSkipsThePrompt(t *testing.T) {
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
			name:    "no runner at all",
			sandbox: fakeSandbox{available: false},
			policy:  fakePolicySource{policy: sandboxPolicy(t, rigsandbox.ModeWorkspaceWrite)},
		},
		{
			name:    "danger-full-access policy",
			sandbox: healthyRunnerSandbox(),
			policy:  fakePolicySource{policy: sandboxPolicy(t, rigsandbox.ModeDangerFullAccess)},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			approvals := &recordingApprovals{decision: contracts.ApprovalDeny}
			svc, sessionID, logf := newLoggedGateService(t, testCase.sandbox, testCase.policy, nil, approvals)
			for _, name := range sandboxShellFamily {
				if got := gateDecision(t, svc, sessionID, name); got != rig.DecisionAllow {
					t.Fatalf("%s = %v, want allow without an approval", name, got)
				}
			}
			if asked := approvals.askedTools(); len(asked) != 0 {
				t.Fatalf("approval requests = %v, want none (commands never park)", asked)
			}
			// The difference from a certified call must be recorded, not hidden.
			for _, name := range sandboxShellFamily {
				if !logf.contains(name + " ran without an approval prompt") {
					t.Fatalf("no honest record for the unconfined %s call: %v", name, logf.snapshot())
				}
			}
		})
	}
}

// TestUncertifiedShellRecordIsOncePerSession keeps the record from becoming
// per-call noise: one line per session+tool, however many commands run.
func TestUncertifiedShellRecordIsOncePerSession(t *testing.T) {
	approvals := &recordingApprovals{decision: contracts.ApprovalDeny}
	svc, sessionID, logf := newLoggedGateService(t,
		fakeSandbox{available: false},
		fakePolicySource{policy: sandboxPolicy(t, rigsandbox.ModeWorkspaceWrite)}, nil, approvals)
	for i := 0; i < 3; i++ {
		if got := gateDecision(t, svc, sessionID, "bash"); got != rig.DecisionAllow {
			t.Fatalf("bash = %v, want allow", got)
		}
	}
	if got := countLines(logf.snapshot(), "bash ran without an approval prompt"); got != 1 {
		t.Fatalf("records = %d, want exactly 1 per session+tool: %v", got, logf.snapshot())
	}
	// A different session is a different record.
	otherSession, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if got := gateDecision(t, svc, otherSession, "bash"); got != rig.DecisionAllow {
		t.Fatalf("bash in the second session = %v, want allow", got)
	}
	if got := countLines(logf.snapshot(), "bash ran without an approval prompt"); got != 2 {
		t.Fatalf("records = %d, want one per session: %v", got, logf.snapshot())
	}
}

// TestMCPFamilyNeverParks is requirement 3 of the permission model: an MCP tool
// does not enter the approval queue. The deny-all fake proves the gate answered
// on its own. Every approvals policy spelling is checked because the two
// products configure different ones (serve pins workspace, headless pins auto):
// the exemption must not depend on that, or the products would fork.
func TestMCPFamilyNeverParks(t *testing.T) {
	for _, mode := range []string{"", "ask", contracts.PermissionWorkspace, "auto", contracts.PermissionReadOnly} {
		t.Run("approvals mode "+mode, func(t *testing.T) {
			approvals := &recordingApprovals{mode: mode, decision: contracts.ApprovalDeny}
			svc, sessionID := newSandboxGateService(t, nil, nil, nil, approvals)
			for _, name := range []string{"mcp__server__run", "mcp__zk-test-mcp__mcp_echo_marker"} {
				if got := gateDecision(t, svc, sessionID, name); got != rig.DecisionAllow {
					t.Fatalf("MCP tool %q = %v, want allow without an approval", name, got)
				}
			}
			if asked := approvals.askedTools(); len(asked) != 0 {
				t.Fatalf("approval requests = %v, want none for MCP tools", asked)
			}
		})
	}
}

// TestExemptionsFailClosedWithoutApprovals is the 2026-11-26 audit fix (severe
// #2). The command and MCP exemption branches sit BEFORE the "approvals == nil →
// deny" branch, so each of them must carry that rule itself; otherwise an
// assembly with a sandbox module (so shellCommandExempt is true) and no approvals
// service allows both families silently — the exact inverse of the fail-closed
// promise printed further down the chain.
func TestExemptionsFailClosedWithoutApprovals(t *testing.T) {
	// No runner, workspace-write policy: the uncertified-command branch, which is
	// the one that used to assume "sandbox module present ⇒ somebody is
	// accountable".
	sandbox := fakeSandbox{available: false}
	policy := fakePolicySource{policy: sandboxPolicy(t, rigsandbox.ModeWorkspaceWrite)}
	for _, name := range append(append([]string{}, sandboxShellFamily...), "mcp__server__run") {
		t.Run(name, func(t *testing.T) {
			svc, sessionID := newSandboxGateService(t, sandbox, policy, nil, nil)
			// The SDK's funcGate turns "error" into the empty decision (a caller
			// cannot execute on an error), so the contract here is: error, and no
			// allow. Anything else (allow, or a silent deny without a reason) is the
			// bug the audit found.
			decision, err := svc.gate(sessionID).Approve(context.Background(),
				&gateTestTool{name: name}, rig.ToolCall{Name: name})
			if decision == rig.DecisionAllow {
				t.Fatalf("%s was allowed with no approvals service", name)
			}
			if err == nil || !strings.Contains(err.Error(), "requires an approvals service") {
				t.Fatalf("%s error = %v (decision %q), want the readable fail-closed error", name, err, decision)
			}
		})
	}
	// The certified-confinement branch keeps its own behavior: an OS-confined call
	// never needed an approval, so a missing approvals service is not a reason to
	// refuse it. This contrast is the point of the test above — "no approvals
	// service" alone is not treated as "deny everything".
	t.Run("certified confinement still runs", func(t *testing.T) {
		svc, sessionID := newSandboxGateService(t, healthyRunnerSandbox(),
			fakePolicySource{policy: sandboxPolicy(t, rigsandbox.ModeWorkspaceWrite)}, nil, nil)
		if got := gateDecision(t, svc, sessionID, "bash"); got != rig.DecisionAllow {
			t.Fatalf("OS-confined bash = %v, want allow (no approval was ever needed)", got)
		}
	})
}

// TestMCPFamilyKeepsExplicitVetoes keeps the two hard restrictions in front of
// the MCP exemption: a session-level read-only override denies, and the headless
// deny-all policy denies.
func TestMCPFamilyKeepsExplicitVetoes(t *testing.T) {
	t.Run("session read-only override", func(t *testing.T) {
		approvals := &recordingApprovals{decision: contracts.ApprovalAllow}
		svc, sessionID := newSandboxGateService(t, nil, nil, nil, approvals)
		if err := svc.SetSessionPermission(sessionID, contracts.PermissionReadOnly); err != nil {
			t.Fatalf("SetSessionPermission: %v", err)
		}
		if got := gateDecision(t, svc, sessionID, "mcp__server__run"); got != rig.DecisionDeny {
			t.Fatalf("read-only override MCP call = %v, want deny", got)
		}
		if asked := approvals.askedTools(); len(asked) != 0 {
			t.Fatalf("a hard read-only restriction requested approval: %v", asked)
		}
	})
	t.Run("headless deny-all policy", func(t *testing.T) {
		approvals := &recordingApprovals{mode: approvalModeNever, decision: contracts.ApprovalAllow}
		svc, sessionID := newSandboxGateService(t, healthyRunnerSandbox(),
			fakePolicySource{policy: sandboxPolicy(t, rigsandbox.ModeWorkspaceWrite)}, nil, approvals)
		if got := gateDecision(t, svc, sessionID, "mcp__server__run"); got != rig.DecisionDeny {
			t.Fatalf("deny-all MCP call = %v, want deny", got)
		}
		if asked := approvals.askedTools(); len(asked) != 0 {
			t.Fatalf("deny-all policy requested approval: %v", asked)
		}
	})
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
		fakePolicySource{policy: sandboxPolicy(t, rigsandbox.ModeReadOnly)},
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
		fakePolicySource{policy: sandboxPolicy(t, rigsandbox.ModeReadOnly)},
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

// TestSandboxGateJudgesTheRunWorkspacePolicy pins the wiring: the decision asks
// the toolkit for the policy of the run's own workspace root, not for whichever
// workspace happens to be current.
func TestSandboxGateJudgesTheRunWorkspacePolicy(t *testing.T) {
	sandbox, _ := healthySandbox(t)
	var asked []string
	policy := fakePolicySource{
		policy: sandboxPolicy(t, rigsandbox.ModeWorkspaceWrite),
		onRoot: func(root string) { asked = append(asked, root) },
	}
	approvals := &recordingApprovals{decision: contracts.ApprovalDeny}
	svc, sessionID := newSandboxGateService(t, sandbox, policy, nil, approvals)
	if got := gateDecision(t, svc, sessionID, "bash"); got != rig.DecisionAllow {
		t.Fatalf("bash = %v, want allow", got)
	}
	want := svc.sessionWorkspaceRoot(sessionID)
	if len(asked) == 0 {
		t.Fatal("gate never asked for a policy root")
	}
	for _, root := range asked {
		if root != want {
			t.Fatalf("gate asked for policy of %q, want the run root %q", root, want)
		}
	}
}
