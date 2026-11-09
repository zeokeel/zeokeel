package agent

// Tests for the session workspace/permission switcher halves used by
// automation runs.

import (
	"context"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func newSwitcherService(t *testing.T) (*service, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	svc := newService(Config{}, rigtools.NewRegistry(), newFakeSettings(nil), &fakeLogger{}, store, fakeApprovals{})
	return svc, store
}

// denyApprovals is an approval backend that always denies.
type denyApprovals struct{ fakeApprovals }

func (denyApprovals) Request(context.Context, contracts.ApprovalRequest) (contracts.ApprovalDecision, error) {
	return contracts.ApprovalDeny, nil
}

func gateDecision(t *testing.T, svc *service, sessionID, toolName string) rig.Decision {
	t.Helper()
	decision, err := svc.gate(sessionID).Approve(context.Background(), &gateTestTool{name: toolName}, rig.ToolCall{Name: toolName})
	if err != nil {
		t.Fatalf("gate approve %s: %v", toolName, err)
	}
	return decision
}

func TestSetSessionWorkspace(t *testing.T) {
	svc, store := newSwitcherService(t)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionWorkspace(sessionID, "  ws-1  "); err != nil {
		t.Fatalf("SetSessionWorkspace: %v", err)
	}
	if got := store.workspaceOf(sessionID); got != "ws-1" {
		t.Fatalf("store workspace = %q, want ws-1", got)
	}
	if info := sessionInfoOf(t, svc, sessionID); info.WorkspaceID != "ws-1" {
		t.Fatalf("read model workspace = %q", info.WorkspaceID)
	}
	// Empty clears both the durable row and the read model.
	if err := svc.SetSessionWorkspace(sessionID, "  "); err != nil {
		t.Fatalf("clear workspace: %v", err)
	}
	if got := store.workspaceOf(sessionID); got != "" {
		t.Fatalf("store workspace after clear = %q", got)
	}
	if info := sessionInfoOf(t, svc, sessionID); info.WorkspaceID != "" {
		t.Fatalf("read model after clear = %q", info.WorkspaceID)
	}
	if err := svc.SetSessionWorkspace("missing", "ws"); err == nil {
		t.Fatal("SetSessionWorkspace(unknown session) = nil error")
	}
}

func TestSetSessionPermission(t *testing.T) {
	svc, _ := newSwitcherService(t)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for _, mode := range []string{contracts.PermissionAsk, contracts.PermissionWorkspace, contracts.PermissionFull, contracts.PermissionNever} {
		if err := svc.SetSessionPermission(sessionID, " "+mode+" "); err != nil {
			t.Fatalf("SetSessionPermission(%q): %v", mode, err)
		}
	}
	for _, mode := range []string{"", "bogus", "AUTO"} {
		if err := svc.SetSessionPermission(sessionID, mode); err == nil {
			t.Fatalf("SetSessionPermission(%q) = nil error", mode)
		}
	}
	if err := svc.SetSessionPermission("missing", contracts.PermissionFull); err == nil {
		t.Fatal("SetSessionPermission(unknown session) = nil error")
	}
}

func TestSessionPermissionOverrideGate(t *testing.T) {
	svc, _ := newSwitcherService(t)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPermission(sessionID, contracts.PermissionNever); err != nil {
		t.Fatalf("never: %v", err)
	}
	if got := gateDecision(t, svc, sessionID, "bash"); got != rig.DecisionDeny {
		t.Fatalf("never gate = %v, want deny", got)
	}
	if err := svc.SetSessionPermission(sessionID, contracts.PermissionFull); err != nil {
		t.Fatalf("full: %v", err)
	}
	if got := gateDecision(t, svc, sessionID, "bash"); got != rig.DecisionAllow {
		t.Fatalf("full gate = %v, want allow", got)
	}
	// No override falls back to the global approvals policy.
	other, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession other: %v", err)
	}
	svc.approvals = denyApprovals{}
	if got := gateDecision(t, svc, other, "bash"); got != rig.DecisionDeny {
		t.Fatalf("global fallback gate = %v, want deny", got)
	}
	if got := gateDecision(t, svc, sessionID, "bash"); got != rig.DecisionAllow {
		t.Fatalf("override should survive global deny: %v", got)
	}
}

// TestSessionPermissionWorkspaceAllowsPathTools covers the per-session workspace
// override: normal in-workspace file editing (write/edit as well as reads) is
// allowed without approval, while tools that can leave the workspace fall
// through to the global policy.
func TestSessionPermissionWorkspaceAllowsPathTools(t *testing.T) {
	svc, _ := newSwitcherService(t)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPermission(sessionID, contracts.PermissionWorkspace); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	// A deny-everything global policy proves the allowed tools never consult it.
	svc.approvals = denyApprovals{}
	for _, tool := range []string{
		"read_file", "list_dir", "glob", "grep",
		"write_file", "edit_file", "str_replace_editor",
	} {
		if got := gateDecision(t, svc, sessionID, tool); got != rig.DecisionAllow {
			t.Fatalf("workspace gate %q = %v, want allow", tool, got)
		}
	}
	for _, tool := range []string{"bash", "web_fetch", "mcp__server__run", "unknown_tool"} {
		if got := gateDecision(t, svc, sessionID, tool); got != rig.DecisionDeny {
			t.Fatalf("workspace gate %q = %v, want the global deny", tool, got)
		}
	}
}

func TestSessionPermissionPlanModeWins(t *testing.T) {
	settings := newFakeSettings(map[string]string{contracts.SettingAgentMode: contracts.AgentModePlan})
	svc := newService(Config{}, rigtools.NewRegistry(), settings, &fakeLogger{}, newFakeStore(), fakeApprovals{})
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPermission(sessionID, contracts.PermissionFull); err != nil {
		t.Fatalf("SetSessionPermission: %v", err)
	}
	if got := gateDecision(t, svc, sessionID, "bash"); got != rig.DecisionDeny {
		t.Fatalf("plan-mode gate = %v, want deny even with full override", got)
	}
}

func TestSessionPermissionClearedOnDelete(t *testing.T) {
	svc, _ := newSwitcherService(t)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPermission(sessionID, contracts.PermissionFull); err != nil {
		t.Fatalf("SetSessionPermission: %v", err)
	}
	svc.approvals = denyApprovals{}
	if err := svc.DeleteSession(sessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	svc.mu.Lock()
	_, present := svc.sessionPermissions[sessionID]
	svc.mu.Unlock()
	if present {
		t.Fatal("permission override survived session deletion")
	}
}
