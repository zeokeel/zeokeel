package agent

// Tests for the OS-sandbox shell auto-allow in the tool approval gate: when the
// optional contracts.SandboxService reports a usable platform runner, the shell
// family (bash/pwsh/job_start/terminal_*) runs without an approval prompt,
// because the OS profile now enforces the file effects. When the service is
// missing or unavailable, the gate must keep the pre-sandbox behavior exactly.
//
// Precedence is part of the contract: the plan/chat whitelists and the
// per-session read-only override keep their veto even when the OS sandbox is
// available, and the headless deny-all approvals policy ("never") is never
// bypassed by a prompt-skipping auto-allow.

import (
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigsandbox "github.com/zeokeel/zeokeel-rig/sandbox"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// sandboxShellFamily is the shell-family surface the sandbox auto-allow covers:
// command execution (bash/pwsh/job_start) plus the interactive terminal session
// tools. terminal_list/terminal_read are already in the shared read-only
// allowlist and are intentionally not part of this family.
var sandboxShellFamily = []string{
	"bash", "pwsh", "job_start",
	"terminal_open", "terminal_send", "terminal_signal", "terminal_close",
}

// fakeSandbox is a contracts.SandboxService test double. Runner is nil: the gate
// only reads Available, never the runner itself.
type fakeSandbox struct {
	available bool
}

func (f fakeSandbox) Runner() rigsandbox.Runner { return nil }

func (f fakeSandbox) Available() bool { return f.available }

func (f fakeSandbox) Status() contracts.SandboxStatus {
	return contracts.SandboxStatus{Available: f.available, Platform: "test"}
}

// newSandboxGateService builds an agent service with an optional sandbox
// service and one new session. A nil sandbox reproduces assemblies that do not
// mount the sandbox module.
func newSandboxGateService(t *testing.T, sandbox contracts.SandboxService, settings map[string]string, approvals contracts.Approvals) (*service, string) {
	t.Helper()
	svc := newService(Config{}, rigtools.NewRegistry(),
		contracts.SettingsService(newFakeSettings(settings)), &fakeLogger{}, newFakeStore(), approvals)
	svc.sandboxService = sandbox
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return svc, sessionID
}

// TestSandboxShellAutoAllowedHelper unit-tests the helper itself: it needs the
// service, the availability flag and the exact shell family, and it trims the
// name like the other gate name matches.
func TestSandboxShellAutoAllowedHelper(t *testing.T) {
	cases := []struct {
		name    string
		sandbox contracts.SandboxService
		tool    string
		want    bool
	}{
		{name: "nil service bash", sandbox: nil, tool: "bash", want: false},
		{name: "unavailable bash", sandbox: fakeSandbox{available: false}, tool: "bash", want: false},
		{name: "available bash", sandbox: fakeSandbox{available: true}, tool: "bash", want: true},
		{name: "available pwsh", sandbox: fakeSandbox{available: true}, tool: "pwsh", want: true},
		{name: "available job_start", sandbox: fakeSandbox{available: true}, tool: "job_start", want: true},
		{name: "available terminal_open", sandbox: fakeSandbox{available: true}, tool: "terminal_open", want: true},
		{name: "available terminal_send", sandbox: fakeSandbox{available: true}, tool: "terminal_send", want: true},
		{name: "available terminal_signal", sandbox: fakeSandbox{available: true}, tool: "terminal_signal", want: true},
		{name: "available terminal_close", sandbox: fakeSandbox{available: true}, tool: "terminal_close", want: true},
		{name: "available trims name", sandbox: fakeSandbox{available: true}, tool: " bash ", want: true},
		{name: "available non-shell", sandbox: fakeSandbox{available: true}, tool: "web_fetch", want: false},
		{name: "available terminal_list untouched", sandbox: fakeSandbox{available: true}, tool: "terminal_list", want: false},
		{name: "unavailable terminal_list untouched", sandbox: fakeSandbox{available: false}, tool: "terminal_list", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &service{sandboxService: tc.sandbox}
			if got := svc.sandboxShellAutoAllowed(tc.tool); got != tc.want {
				t.Fatalf("sandboxShellAutoAllowed(%q) = %v, want %v", tc.tool, got, tc.want)
			}
		})
	}
}

// TestSandboxAvailableAutoAllowsShellFamily is the core behavior: every
// shell-family tool is allowed without calling approvals.Request. The fake
// backend would deny if consulted, so an accidental fall-through fails loudly.
func TestSandboxAvailableAutoAllowsShellFamily(t *testing.T) {
	approvals := &recordingApprovals{decision: contracts.ApprovalDeny}
	svc, sessionID := newSandboxGateService(t, fakeSandbox{available: true}, nil, approvals)

	for _, name := range sandboxShellFamily {
		if got := gateDecision(t, svc, sessionID, name); got != rig.DecisionAllow {
			t.Fatalf("sandbox-available gate %q = %v, want allow without approval", name, got)
		}
	}
	if asked := approvals.askedTools(); len(asked) != 0 {
		t.Fatalf("sandbox auto-allowed shell tools requested approval: %v", asked)
	}
}

// TestSandboxAvailableStillAsksForNonShellTool keeps the auto-allow narrow: a
// non-shell tool (network, team board, unknown) still goes through the
// approvals service.
func TestSandboxAvailableStillAsksForNonShellTool(t *testing.T) {
	for _, name := range []string{"web_fetch", "team_task_create", "settings_set", "unknown_tool"} {
		t.Run(name, func(t *testing.T) {
			approvals := &recordingApprovals{decision: contracts.ApprovalAllow}
			svc, sessionID := newSandboxGateService(t, fakeSandbox{available: true}, nil, approvals)
			if got := gateDecision(t, svc, sessionID, name); got != rig.DecisionAllow {
				t.Fatalf("non-shell gate %q = %v, want the approvals backend's allow", name, got)
			}
			if asked := approvals.askedTools(); len(asked) != 1 || asked[0] != name {
				t.Fatalf("approval requests = %v, want [%s]", asked, name)
			}
		})
	}
}

// TestSandboxUnavailableKeepsApprovalFlow proves the switch-off/no-runner path
// is byte-for-byte the old behavior: shell tools ask, and the backend decision
// decides allow vs deny.
func TestSandboxUnavailableKeepsApprovalFlow(t *testing.T) {
	t.Run("backend allows", func(t *testing.T) {
		approvals := &recordingApprovals{decision: contracts.ApprovalAllow}
		svc, sessionID := newSandboxGateService(t, fakeSandbox{available: false}, nil, approvals)
		if got := gateDecision(t, svc, sessionID, "bash"); got != rig.DecisionAllow {
			t.Fatalf("unavailable bash = %v, want the approvals backend's allow", got)
		}
		if asked := approvals.askedTools(); len(asked) != 1 || asked[0] != "bash" {
			t.Fatalf("approval requests = %v, want [bash]", asked)
		}
	})
	t.Run("backend denies", func(t *testing.T) {
		approvals := &recordingApprovals{decision: contracts.ApprovalDeny}
		svc, sessionID := newSandboxGateService(t, fakeSandbox{available: false}, nil, approvals)
		if got := gateDecision(t, svc, sessionID, "bash"); got != rig.DecisionDeny {
			t.Fatalf("unavailable bash = %v, want the approvals backend's deny", got)
		}
		if asked := approvals.askedTools(); len(asked) != 1 || asked[0] != "bash" {
			t.Fatalf("approval requests = %v, want [bash]", asked)
		}
	})
}

// TestSandboxNilServiceKeepsApprovalFlow covers assemblies without the sandbox
// module: the gate must stay nil-safe and ask exactly like before.
func TestSandboxNilServiceKeepsApprovalFlow(t *testing.T) {
	approvals := &recordingApprovals{decision: contracts.ApprovalAllow}
	svc, sessionID := newSandboxGateService(t, nil, nil, approvals)
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
	approvals := &recordingApprovals{decision: contracts.ApprovalAllow}
	svc, sessionID := newSandboxGateService(t, fakeSandbox{available: true},
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
	approvals := &recordingApprovals{decision: contracts.ApprovalAllow}
	svc, sessionID := newSandboxGateService(t, fakeSandbox{available: true}, nil, approvals)
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
	approvals := &recordingApprovals{mode: approvalModeNever, decision: contracts.ApprovalAllow}
	svc, sessionID := newSandboxGateService(t, fakeSandbox{available: true}, nil, approvals)

	for _, name := range sandboxShellFamily {
		if got := gateDecision(t, svc, sessionID, name); got != rig.DecisionDeny {
			t.Fatalf("deny-all policy gate %q = %v, want deny even with the sandbox available", name, got)
		}
	}
	if asked := approvals.askedTools(); len(asked) != 0 {
		t.Fatalf("deny-all policy requested approval: %v", asked)
	}
}
