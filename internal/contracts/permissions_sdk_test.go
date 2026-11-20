package contracts

// The read-only approval set must be derived from the SDK's sandbox-safety
// judgement, not from a hand-maintained copy: rig ships the list of tools that
// are "safe without asking" with every release (gate.SandboxReadOnlyTools), and
// a private copy silently drifts when a new tool lands. The product keeps only
// explicit deltas — tools it deliberately treats differently.

import (
	"sort"
	"testing"

	"github.com/zeokeel/zeokeel-rig/gate"
)

// TestReadOnlySetTracksSDKList pins the derivation: every SDK entry is allowed
// except the documented product removals, and every allowed name is either an SDK
// entry or a documented product addition.
func TestReadOnlySetTracksSDKList(t *testing.T) {
	sdk := make(map[string]bool, len(gate.SandboxReadOnlyTools))
	for _, name := range gate.SandboxReadOnlyTools {
		sdk[name] = true
	}
	if len(sdk) == 0 {
		t.Fatal("SDK read-only list is empty")
	}

	for name := range sdk {
		if productReadOnlyRemovals[name] {
			if ReadOnlyAutoAllowed(name) {
				t.Errorf("SDK tool %q is a documented product removal but is auto-allowed", name)
			}
			continue
		}
		if !ReadOnlyAutoAllowed(name) {
			t.Errorf("SDK read-only tool %q is not auto-allowed (add it to the product additions or removals)", name)
		}
	}

	for name := range ReadOnlyAutoAllowedTools {
		if sdk[name] || productReadOnlyAdditions[name] {
			continue
		}
		t.Errorf("auto-allowed tool %q is neither an SDK read-only tool nor a documented product addition", name)
	}
}

// TestProductDeltasAreDocumented keeps the two delta sets small and explicit:
// a silent growth of either one is the drift this file exists to prevent.
func TestProductDeltasAreDocumented(t *testing.T) {
	removals := make([]string, 0, len(productReadOnlyRemovals))
	for name := range productReadOnlyRemovals {
		removals = append(removals, name)
	}
	sort.Strings(removals)
	if len(removals) != 1 || removals[0] != "memory" {
		t.Fatalf("product read-only removals = %v, want exactly [memory] (read-only must not mutate durable state)", removals)
	}

	additions := make([]string, 0, len(productReadOnlyAdditions))
	for name := range productReadOnlyAdditions {
		additions = append(additions, name)
	}
	sort.Strings(additions)
	want := []string{
		"lsp_definition", "lsp_diagnostics", "lsp_hover", "lsp_implementation",
		"lsp_references", "lsp_symbols", "team_task_get", "team_task_list",
	}
	if len(additions) != len(want) {
		t.Fatalf("product read-only additions = %v, want %v", additions, want)
	}
	for index, name := range want {
		if additions[index] != name {
			t.Fatalf("product read-only additions = %v, want %v", additions, want)
		}
	}
}

// TestWorkspaceWriteToolsAutoAllowed pins the write half of the presets: every
// workspace file writer runs without asking in the workspace preset and asks in
// the read-only preset.
func TestWorkspaceWriteToolsAutoAllowed(t *testing.T) {
	for _, name := range []string{"write_file", "edit_file", "str_replace_editor", "apply_patch", "undo"} {
		if !WorkspaceAutoAllowed(name) {
			t.Errorf("workspace preset does not auto-allow %q", name)
		}
		if ReadOnlyAutoAllowed(name) {
			t.Errorf("read-only preset auto-allows the writer %q", name)
		}
	}
}

// TestSandboxAffordancesApproval pins the sandbox tools' approval posture:
// sandbox_status is read-only and auto-allowed; request_sandbox_access widens
// the writable surface and must never be auto-allowed (the human decides).
func TestSandboxAffordancesApproval(t *testing.T) {
	if !ReadOnlyAutoAllowed("sandbox_status") || !WorkspaceAutoAllowed("sandbox_status") {
		t.Error("sandbox_status must be auto-allowed (read-only diagnostic)")
	}
	for _, name := range []string{"request_sandbox_access"} {
		if ReadOnlyAutoAllowed(name) || WorkspaceAutoAllowed(name) {
			t.Errorf("%q is auto-allowed, but it widens the writable surface", name)
		}
	}
}
