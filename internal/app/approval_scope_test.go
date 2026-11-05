package app

import (
	"encoding/json"
	"testing"

	"github.com/zeokeel/zeokeel/internal/session"
)

// TestToolNeedsApprovalWithArgs covers the argument-aware gate predicate. The
// only argument-level exemption is str_replace_editor's read-only "view"
// command; calls the predicate cannot classify (invalid JSON, missing command,
// nil args) keep the name-based decision, i.e. stay gated when a pattern
// matches (fail-closed).
func TestToolNeedsApprovalWithArgs(t *testing.T) {
	editor := []string{"str_replace_editor"}
	everything := []string{"*"}
	workspace := splitApprovalPatterns(defaultApprovalTools)
	review := splitApprovalPatterns(autoReviewApprovalTools)

	cases := []struct {
		name     string
		tool     string
		args     string // raw JSON; "" means nil args
		patterns []string
		want     bool
	}{
		// str_replace_editor: view is read-only and never asks.
		{"editor view under editor pattern", "str_replace_editor", `{"command":"view","path":"."}`, editor, false},
		{"editor view under wildcard", "str_replace_editor", `{"command":"view"}`, everything, false},
		{"editor view under auto review", "str_replace_editor", `{"command":"view","path":"."}`, review, false},
		// Every mutating command keeps the name-based decision.
		{"editor create under editor pattern", "str_replace_editor", `{"command":"create","path":"a"}`, editor, true},
		{"editor str_replace under editor pattern", "str_replace_editor", `{"command":"str_replace","path":"a"}`, editor, true},
		{"editor insert under wildcard", "str_replace_editor", `{"command":"insert","path":"a"}`, everything, true},
		{"editor undo_edit", "str_replace_editor", `{"command":"undo_edit","path":"a"}`, editor, true},
		// Fail-closed: unparseable or missing command stays gated.
		{"editor missing command", "str_replace_editor", `{"path":"a"}`, editor, true},
		{"editor invalid JSON under editor pattern", "str_replace_editor", `{"command":`, editor, true},
		{"editor invalid JSON under wildcard", "str_replace_editor", `{"command":`, everything, true},
		{"editor nil args stays gated", "str_replace_editor", "", editor, true},
		{"editor null payload stays gated", "str_replace_editor", `null`, editor, true},
		// Other tools keep the name-based decision; a "view" command in
		// another tool's args must not exempt it.
		{"bash under workspace default", "bash", `{"cmd":"ls"}`, workspace, true},
		{"bash under wildcard", "bash", `{"cmd":"ls"}`, everything, true},
		{"other tool with view command not exempt", "mcp__demo__run", `{"command":"view"}`, []string{"mcp__"}, true},
		// Converged default does not gate workspace-confined file writes; Auto
		// review keeps reviewing them.
		{"write_file un-gated by workspace default", "write_file", `{"path":"a"}`, workspace, false},
		{"write_file reviewed by auto review", "write_file", `{"path":"a"}`, review, true},
		{"read_file never gated", "read_file", `{"path":"a"}`, review, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var args json.RawMessage
			if tc.args != "" {
				args = json.RawMessage(tc.args)
			}
			if got := toolNeedsApprovalWithArgs(tc.tool, args, tc.patterns); got != tc.want {
				t.Fatalf("toolNeedsApprovalWithArgs(%q, %s, %v) = %v, want %v",
					tc.tool, tc.args, tc.patterns, got, tc.want)
			}
		})
	}
}

// TestApprovalPatternsForPermission covers the converged Workspace Write
// default, Auto review's wider review set, and the approval.tools override
// semantics in both modes.
func TestApprovalPatternsForPermission(t *testing.T) {
	st, _ := newToolStore(t)

	workspace := approvalPatternsForPermission(st, session.PermissionWorkspace)
	for _, name := range []string{"write_file", "edit_file", "str_replace_editor"} {
		if toolNeedsApproval(name, workspace) {
			t.Errorf("Workspace default gates sandbox-confined tool %q", name)
		}
	}
	for _, name := range []string{
		"bash", "pwsh", "terminal_open", "terminal_close", "job_start",
		"job_kill", "enter_worktree", "exit_worktree", "web_search",
		"web_fetch", "mcp__demo__echo",
	} {
		if !toolNeedsApproval(name, workspace) {
			t.Errorf("Workspace default does not gate %q", name)
		}
	}

	// Auto review keeps the wider pre-convergence coverage so its reviewer
	// still sees file mutations.
	autoReview := approvalPatternsForPermission(st, session.PermissionAutoReview)
	for _, name := range []string{
		"write_file", "edit_file", "str_replace_editor",
		"bash", "pwsh", "terminal_open", "web_search", "mcp__demo__echo",
	} {
		if !toolNeedsApproval(name, autoReview) {
			t.Errorf("Auto review default does not gate %q", name)
		}
	}

	// Ask shares the human-approval default; Full bypasses in the gate predicate
	// itself, not in the pattern list.
	ask := approvalPatternsForPermission(st, session.PermissionAsk)
	if !toolNeedsApproval("bash", ask) {
		t.Error("Ask mode does not gate bash")
	}
	full := approvalPatternsForPermission(st, session.PermissionFull)
	if !toolNeedsApproval("bash", full) {
		t.Error("Full mode pattern list should be the default; bypass happens in the gate")
	}

	// An explicit approval.tools setting still wins in both modes.
	if err := st.SetSetting("approval.tools", "bash,write_file"); err != nil {
		t.Fatalf("SetSetting(approval.tools): %v", err)
	}
	for _, permission := range []string{session.PermissionWorkspace, session.PermissionAutoReview} {
		patterns := approvalPatternsForPermission(st, permission)
		if len(patterns) != 2 || patterns[0] != "bash" || patterns[1] != "write_file" {
			t.Fatalf("%s patterns = %v, want [bash write_file]", permission, patterns)
		}
		if toolNeedsApproval("edit_file", patterns) {
			t.Errorf("%s: custom approval.tools still gates edit_file", permission)
		}
	}
}
