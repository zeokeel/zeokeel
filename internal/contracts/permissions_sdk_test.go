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
		"lsp_references", "lsp_symbols", "skills", "team_task_get", "team_task_list",
		"todo", "todos",
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
