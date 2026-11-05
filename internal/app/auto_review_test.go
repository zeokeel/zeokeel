package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/session"
)

// TestAutoReviewAppWiring proves the app builds the model-backed reviewer only
// for the auto_review permission, while keeping the mutating tool set. Auto
// review keeps its own wider review pattern set (the workspace-confined file
// tools included), the human-approval modes use the converged default, Full
// access still bypasses approval, and Read Only keeps its restricted registry.
func TestAutoReviewAppWiring(t *testing.T) {
	if got := permissionMode(nil); got != session.PermissionWorkspace {
		t.Fatalf("permissionMode(nil) = %q, want %q", got, session.PermissionWorkspace)
	}

	cases := []struct {
		permission     string
		wantReviewer   bool
		wantBash       bool
		wantBashApprov bool
		wantFileApprov bool
	}{
		{permission: session.PermissionAsk, wantBash: false, wantBashApprov: true, wantFileApprov: false},
		{permission: session.PermissionWorkspace, wantBash: true, wantBashApprov: true, wantFileApprov: false},
		{permission: session.PermissionFull, wantBash: true, wantBashApprov: false, wantFileApprov: false},
		{permission: session.PermissionAutoReview, wantReviewer: true, wantBash: true, wantBashApprov: true, wantFileApprov: true},
	}
	for _, tc := range cases {
		t.Run(tc.permission, func(t *testing.T) {
			st, dir := newToolStore(t)
			configuredProvider(t, st)
			if err := st.SetSetting(session.SettingPermission, tc.permission); err != nil {
				t.Fatalf("SetSetting(permission): %v", err)
			}
			if got := permissionMode(st); got != tc.permission {
				t.Fatalf("permissionMode = %q, want %q", got, tc.permission)
			}
			bundle, err := buildAgentWithPermission(st, dir, "instruction", tc.permission)
			if err != nil {
				t.Fatalf("buildAgentWithPermission: %v", err)
			}
			if bundle == nil || bundle.Agent == nil {
				t.Fatal("no agent bundle")
			}
			t.Cleanup(func() {
				if bundle.Close != nil {
					_ = bundle.Close()
				}
			})
			if got := bundle.Agent.HasToolReviewer(); got != tc.wantReviewer {
				t.Fatalf("reviewer configured = %v, want %v", got, tc.wantReviewer)
			}
			if got := registryHas(bundle, "bash"); got != tc.wantBash {
				t.Fatalf("bash registered = %v, want %v", got, tc.wantBash)
			}
			if bundle.Gate == nil {
				t.Fatal("no approval gate")
			}
			if got := bundle.Gate.RequireApproval("bash", nil); got != tc.wantBashApprov {
				t.Fatalf("bash approval = %v, want %v", got, tc.wantBashApprov)
			}
			// Workspace-confined file writes no longer ask a human under the
			// converged Workspace default; Auto review keeps reviewing them.
			if got := bundle.Gate.RequireApproval("write_file", json.RawMessage(`{"path":"a.txt","content":"x"}`)); got != tc.wantFileApprov {
				t.Fatalf("write_file approval = %v, want %v", got, tc.wantFileApprov)
			}
			if got := bundle.Gate.RequireApproval("str_replace_editor", json.RawMessage(`{"command":"create","path":"a.txt","file_text":"x"}`)); got != tc.wantFileApprov {
				t.Fatalf("str_replace_editor create approval = %v, want %v", got, tc.wantFileApprov)
			}
			// A read-only view call never asks, even in Auto review's wider set
			// where str_replace_editor is a gated name.
			if bundle.Gate.RequireApproval("str_replace_editor", json.RawMessage(`{"command":"view","path":"."}`)) {
				t.Fatal("str_replace_editor view requires approval")
			}
			// Read-only tools never reach the reviewer: the gate's patterns
			// keep them ungated in every permission mode.
			if bundle.Gate.RequireApproval("read_file", nil) {
				t.Fatal("read_file requires approval")
			}
			// Auto review keeps the workspace-write policy: it is a reviewer
			// in front of the same mutating tool set, not a sandbox change.
			if tc.permission == session.PermissionAutoReview &&
				!strings.Contains(bundle.Agent.Instruction(), "workspace-write") {
				t.Fatalf("auto review prompt does not describe workspace-write: %q", bundle.Agent.Instruction())
			}
		})
	}
}
