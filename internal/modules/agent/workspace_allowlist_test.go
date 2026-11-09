package agent

// Tests for the shared workspace auto-allow list (contracts.WorkspaceAutoAllowed):
// the default "workspace" permission must not prompt for read-only/informational
// tools — only for tools that can leave the workspace, mutate app settings or are
// unknown. The same frozen set is read by the approvals service, so the agent
// gate and the backend policy cannot drift apart.

import (
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// workspaceAutoAllowedSample is a representative subset of the shared set: file
// tools plus the read-only/informational tools the audit found prompting.
var workspaceAutoAllowedSample = []string{
	"read_file", "write_file", "edit_file", "str_replace_editor", "list_dir", "glob", "grep",
	"read_image", "working_directory", "memory", "skill", "skills", "settings_get",
	"todo_write", "team_task_list", "team_task_get", "terminal_list",
	"job_list", "job_output", "job_wait", "session_search", "session_event_trace",
	"lsp_diagnostics",
}

// workspaceMustAskTools are the tools the workspace policy must still ask for.
var workspaceMustAskTools = []string{
	"bash", "pwsh", "web_fetch", "web_search", "settings_set",
	"mcp__server__run", "unknown_tool",
}

// TestSessionPermissionWorkspaceUsesSharedAutoAllowList covers the per-session
// workspace override: every shared tool is allowed without consulting the
// approvals backend, and the tools that can leave the workspace still go through
// it (a deny-all fake proves they do not skip it).
func TestSessionPermissionWorkspaceUsesSharedAutoAllowList(t *testing.T) {
	svc, _ := newSwitcherService(t)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPermission(sessionID, contracts.PermissionWorkspace); err != nil {
		t.Fatalf("SetSessionPermission: %v", err)
	}
	approvals := &recordingApprovals{decision: contracts.ApprovalDeny}
	svc.approvals = approvals

	for _, tool := range workspaceAutoAllowedSample {
		if got := gateDecision(t, svc, sessionID, tool); got != rig.DecisionAllow {
			t.Fatalf("workspace gate %q = %v, want allow without approval", tool, got)
		}
	}
	if asked := approvals.askedTools(); len(asked) != 0 {
		t.Fatalf("shared auto-allow tools requested approval: %v", asked)
	}

	for _, tool := range workspaceMustAskTools {
		if got := gateDecision(t, svc, sessionID, tool); got != rig.DecisionDeny {
			t.Fatalf("workspace gate %q = %v, want the approvals backend's deny", tool, got)
		}
	}
	asked := approvals.askedTools()
	if strings.Join(asked, ",") != strings.Join(workspaceMustAskTools, ",") {
		t.Fatalf("approval requests = %v, want %v", asked, workspaceMustAskTools)
	}
}

// TestSessionPermissionWorkspaceCoversWholeContractList prevents the gate from
// drifting from the frozen contracts.WorkspaceAutoAllowedTools set.
func TestSessionPermissionWorkspaceCoversWholeContractList(t *testing.T) {
	svc, _ := newSwitcherService(t)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPermission(sessionID, contracts.PermissionWorkspace); err != nil {
		t.Fatalf("SetSessionPermission: %v", err)
	}
	svc.approvals = denyApprovals{}
	for tool := range contracts.WorkspaceAutoAllowedTools {
		if got := gateDecision(t, svc, sessionID, tool); got != rig.DecisionAllow {
			t.Fatalf("contract auto-allowed tool %q = %v, want allow", tool, got)
		}
	}
}

// TestGateWorkspaceWorkspaceKeepsInteractionToolsAllowed guards the interaction
// tools (ask_user/present/...) which never prompt regardless of the list.
func TestGateWorkspaceKeepsInteractionToolsAllowed(t *testing.T) {
	svc, _ := newSwitcherService(t)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPermission(sessionID, contracts.PermissionWorkspace); err != nil {
		t.Fatalf("SetSessionPermission: %v", err)
	}
	svc.approvals = denyApprovals{}
	for _, tool := range []string{"ask_user", "present", "plan_review", "exit_plan_mode", "goal"} {
		if got := gateDecision(t, svc, sessionID, tool); got != rig.DecisionAllow {
			t.Fatalf("interaction tool %q = %v, want allow", tool, got)
		}
	}
}

// TestSessionPermissionReadOnlyUsesReadOnlyList covers the per-session
// read-only override: only contracts.ReadOnlyAutoAllowedTools are allowed
// without consulting the approvals backend; workspace write tools and
// leave-the-workspace tools fall through to it.
func TestSessionPermissionReadOnlyUsesReadOnlyList(t *testing.T) {
	svc, _ := newSwitcherService(t)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPermission(sessionID, contracts.PermissionReadOnly); err != nil {
		t.Fatalf("SetSessionPermission: %v", err)
	}
	approvals := &recordingApprovals{decision: contracts.ApprovalDeny}
	svc.approvals = approvals

	for _, tool := range []string{
		"read_file", "list_dir", "glob", "grep", "read_image", "working_directory",
		"settings_get", "job_list", "session_search", "lsp_diagnostics",
	} {
		if got := gateDecision(t, svc, sessionID, tool); got != rig.DecisionAllow {
			t.Fatalf("read-only gate %q = %v, want allow without approval", tool, got)
		}
	}
	if asked := approvals.askedTools(); len(asked) != 0 {
		t.Fatalf("read-only auto-allow tools requested approval: %v", asked)
	}

	wantAsked := []string{"write_file", "edit_file", "str_replace_editor", "bash", "web_fetch", "unknown_tool"}
	for _, tool := range wantAsked {
		if got := gateDecision(t, svc, sessionID, tool); got != rig.DecisionDeny {
			t.Fatalf("read-only gate %q = %v, want the approvals backend's deny", tool, got)
		}
	}
	if asked := approvals.askedTools(); strings.Join(asked, ",") != strings.Join(wantAsked, ",") {
		t.Fatalf("approval requests = %v, want %v", asked, wantAsked)
	}
}

// TestReadOnlyListIsWorkspaceSubset prevents the read-only set from drifting
// outside the workspace set: read-only must never allow a tool that the default
// workspace mode would ask about.
func TestReadOnlyListIsWorkspaceSubset(t *testing.T) {
	for tool := range contracts.ReadOnlyAutoAllowedTools {
		if !contracts.WorkspaceAutoAllowed(tool) {
			t.Fatalf("read-only allows %q but workspace-write does not", tool)
		}
	}
	for _, tool := range []string{"write_file", "edit_file", "str_replace_editor"} {
		if contracts.ReadOnlyAutoAllowed(tool) {
			t.Fatalf("read-only must not auto-allow the workspace write tool %q", tool)
		}
		if !contracts.WorkspaceAutoAllowed(tool) {
			t.Fatalf("workspace-write must auto-allow %q", tool)
		}
	}
}
