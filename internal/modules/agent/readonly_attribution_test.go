package agent

// Tests for the session-level read-only override (a hard restriction that must
// never fall through to the global workspace policy) and for the session
// attribution carried by approval requests.

import (
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// FR-03: a session whose permission was set to read-only (automation tasks do
// this) must deny every non-read-only tool without consulting the global
// (workspace) approval policy — otherwise write_file is auto-allowed by the
// workspace set and the "read-only" label is a lie.
func TestReadOnlyOverrideFailsClosedWithoutGlobalAutoAllow(t *testing.T) {
	svc := newService(Config{}, rigtools.NewRegistry(&gateTestTool{name: "bash"}),
		contracts.SettingsService(newFakeSettings(nil)), &fakeLogger{}, newFakeStore(),
		// The global policy is workspace-write: Request would auto-allow
		// write_file if the gate fell through to it.
		fakeApprovals{mode: "workspace"})
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPermission(sessionID, contracts.PermissionReadOnly); err != nil {
		t.Fatalf("SetSessionPermission: %v", err)
	}

	for _, tool := range []string{"write_file", "edit_file", "str_replace_editor", "bash", "web_fetch", "settings_set", "unknown_tool"} {
		if got := gateDecision(t, svc, sessionID, tool); got != rig.DecisionDeny {
			t.Fatalf("read-only override gate %q = %v, want deny (hard restriction)", tool, got)
		}
	}
	for _, tool := range []string{"read_file", "list_dir", "grep", "session_search", "lsp_diagnostics"} {
		if got := gateDecision(t, svc, sessionID, tool); got != rig.DecisionAllow {
			t.Fatalf("read-only auto-allowed tool %q = %v, want allow", tool, got)
		}
	}
	// Interaction host tools own their overlay and never prompt.
	for _, tool := range []string{"ask_user", "present", "plan_review", "exit_plan_mode", "goal"} {
		if got := gateDecision(t, svc, sessionID, tool); got != rig.DecisionAllow {
			t.Fatalf("interaction tool %q = %v, want allow", tool, got)
		}
	}
}

// FR-29: the approval dialog must know which session/source asked, otherwise a
// background session's request is rendered in whatever conversation is open.
func TestApprovalRequestCarriesSessionAttribution(t *testing.T) {
	settings := newFakeSettings(nil)
	svc := newService(Config{}, rigtools.NewRegistry(&gateTestTool{name: "bash"}),
		contracts.SettingsService(settings), &fakeLogger{}, newFakeStore(), fakeApprovals{})
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := settings.Set(contracts.SessionOriginSettingKey(sessionID), contracts.SessionOriginAutomation); err != nil {
		t.Fatalf("set origin: %v", err)
	}
	approvals := &recordingApprovals{decision: contracts.ApprovalAllow}
	svc.approvals = approvals

	if got := gateDecision(t, svc, sessionID, "bash"); got != rig.DecisionAllow {
		t.Fatalf("gate bash = %v, want allow from the fake backend", got)
	}
	if len(approvals.requests) != 1 {
		t.Fatalf("approval requests = %d, want 1", len(approvals.requests))
	}
	request := approvals.requests[0]
	if request.SessionID != sessionID {
		t.Fatalf("approval SessionID = %q, want %q", request.SessionID, sessionID)
	}
	if request.Source != contracts.SessionOriginAutomation {
		t.Fatalf("approval Source = %q, want %q", request.Source, contracts.SessionOriginAutomation)
	}
}
