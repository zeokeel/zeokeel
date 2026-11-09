package approvals

// Tests for the shared workspace auto-allow list (contracts.WorkspaceAutoAllowed):
// the default "workspace" policy must allow the workspace file tools plus the
// read-only/informational tools without queueing an approval, and must keep
// queueing tools that can leave the workspace, mutate app settings or are
// unknown. This list is shared with the agent gate, so the two layers cannot
// drift apart.

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestWorkspaceModeAutoAllowsSharedList covers every tool of the frozen shared
// set: the request is answered allow immediately, nothing queues and no
// subscriber is notified.
func TestWorkspaceModeAutoAllowsSharedList(t *testing.T) {
	settings := newFakeSettings(nil)
	settings.setPermission(contracts.PermissionWorkspace)
	svc, _ := newPermissionService(t, ModeAsk, settings)
	notifications := atomic.Int32{}
	if _, err := svc.Subscribe(func() { notifications.Add(1) }); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	for tool := range contracts.WorkspaceAutoAllowedTools {
		decision, err := svc.Request(context.Background(), contracts.ApprovalRequest{Tool: tool})
		if err != nil || decision != contracts.ApprovalAllow {
			t.Fatalf("workspace %q decision = %q/%v, want allow", tool, decision, err)
		}
	}
	if pending := svc.Pending(); len(pending) != 0 {
		t.Fatalf("pending = %v, want none for the shared auto-allow set", pending)
	}
	if got := notifications.Load(); got != 0 {
		t.Fatalf("notifications = %d, want 0 for the shared auto-allow set", got)
	}

	// The helper trims whitespace, so a padded name is still recognized.
	if decision, err := svc.Request(context.Background(),
		contracts.ApprovalRequest{Tool: "  working_directory  "}); err != nil || decision != contracts.ApprovalAllow {
		t.Fatalf("padded working_directory = %q/%v, want allow", decision, err)
	}
}

// TestWorkspaceModeKeepsLeavingToolsAsking covers the complement of the shared
// list: shell, network, MCP, settings writes and unknown tools still queue.
func TestWorkspaceModeKeepsLeavingToolsAsking(t *testing.T) {
	settings := newFakeSettings(nil)
	settings.setPermission(contracts.PermissionWorkspace)
	svc, _ := newPermissionService(t, ModeAsk, settings)

	for _, tool := range []string{
		"bash", "pwsh", "web_fetch", "web_search", "settings_set",
		"mcp__server__run", "unknown_tool", "",
	} {
		results := startRequest(context.Background(), svc, contracts.ApprovalRequest{Tool: tool})
		waitFor(t, tool+" pending", func() bool { return len(svc.Pending()) == 1 })
		pending := svc.Pending()
		if pending[0].Tool != tool {
			t.Fatalf("pending tool = %q, want %q", pending[0].Tool, tool)
		}
		if err := svc.Resolve(pending[0].ID, contracts.ApprovalAllow); err != nil {
			t.Fatalf("Resolve(%q): %v", tool, err)
		}
		if result := waitResult(t, results); result.decision != contracts.ApprovalAllow {
			t.Fatalf("%q decision = %q, want the resolved allow", tool, result.decision)
		}
	}
}

// TestWorkspaceModeUnknownPermissionKeepsSharedList proves the unknown-value
// fallback mode (workspace) and the shared list agree.
func TestWorkspaceModeUnknownPermissionKeepsSharedList(t *testing.T) {
	settings := newFakeSettings(map[string]string{contracts.SettingAgentPermission: "bogus"})
	svc, _ := newPermissionService(t, ModeAsk, settings)
	if mode := snapshotMode(t, svc); mode != contracts.PermissionWorkspace {
		t.Fatalf("Mode = %q, want workspace for an unknown setting", mode)
	}
	if decision, err := svc.Request(context.Background(),
		contracts.ApprovalRequest{Tool: "working_directory"}); err != nil || decision != contracts.ApprovalAllow {
		t.Fatalf("working_directory = %q/%v, want allow", decision, err)
	}
	results := startRequest(context.Background(), svc, contracts.ApprovalRequest{Tool: "bash"})
	waitFor(t, "bash pending", func() bool { return len(svc.Pending()) == 1 })
	if err := svc.Resolve(svc.Pending()[0].ID, contracts.ApprovalDeny); err != nil {
		t.Fatalf("Resolve bash: %v", err)
	}
	if result := waitResult(t, results); result.decision != contracts.ApprovalDeny {
		t.Fatalf("bash decision = %q, want deny", result.decision)
	}
}
