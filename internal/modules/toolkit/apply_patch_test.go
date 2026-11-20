package toolkit

// apply_patch is an SDK tool the product never registered: one atomic call can
// add, update, delete and move several files, and every path goes through the
// sandbox policy. A coding harness needs it — the product previously only had
// single-file editors.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// patchCall builds one apply_patch tool call from a patch body.
func patchCall(t *testing.T, patch string) rig.ToolCall {
	t.Helper()
	encoded, err := json.Marshal(map[string]string{"patch": patch})
	if err != nil {
		t.Fatalf("encode patch: %v", err)
	}
	return rig.ToolCall{Name: "apply_patch", Arguments: string(encoded)}
}

// TestApplyPatchToolWired pins the happy path: one call updates a file and
// creates another, atomically.
func TestApplyPatchToolWired(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	_, registry, _, _ := activateMCPToolkitWithRoot(t, map[string]any{"root": root}, newMutableSettings(nil))

	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: main.go",
		"@@",
		" package main",
		"-",
		"-func main() {}",
		"+",
		"+func main() { println(\"hi\") }",
		"*** Add File: helper.go",
		"+package main",
		"*** End Patch",
	}, "\n") + "\n"
	result, err := registry.Invoke(context.Background(), patchCall(t, patch))
	if err != nil {
		t.Fatalf("apply_patch: %v", err)
	}
	if result.IsError {
		t.Fatalf("apply_patch failed: %q", result.Content)
	}
	updated, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !strings.Contains(string(updated), `println("hi")`) {
		t.Fatalf("main.go was not patched: %q", updated)
	}
	added, err := os.ReadFile(filepath.Join(root, "helper.go"))
	if err != nil {
		t.Fatalf("read helper.go: %v", err)
	}
	if string(added) != "package main\n" {
		t.Fatalf("helper.go = %q", added)
	}
}

// TestApplyPatchRespectsPolicy pins the two policy halves a personal harness
// still relies on: a path outside the workspace is refused, and a read-only
// preset does not write anything.
func TestApplyPatchRespectsPolicy(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	escape := "*** Begin Patch\n*** Add File: ../escape.txt\n+owned\n*** End Patch\n"

	_, registry, _, _ := activateMCPToolkitWithRoot(t, map[string]any{"root": root}, newMutableSettings(nil))
	blocked, err := registry.Invoke(context.Background(), patchCall(t, escape))
	if err != nil {
		t.Fatalf("apply_patch(escape): %v", err)
	}
	if !blocked.IsError {
		t.Fatalf("apply_patch wrote outside the workspace: %q", blocked.Content)
	}
	if _, statErr := os.Stat(filepath.Join(root, "..", "escape.txt")); statErr == nil {
		t.Fatal("escape.txt was created outside the workspace")
	}

	readOnly := "*** Begin Patch\n*** Update File: main.go\n@@\n-package main\n+package hacked\n*** End Patch\n"
	_, readOnlyRegistry, _, _ := activateMCPToolkitWithRoot(t, map[string]any{"root": root},
		newMutableSettings(map[string]string{contracts.SettingAgentPermission: contracts.PermissionReadOnly}))
	denied, err := readOnlyRegistry.Invoke(context.Background(), patchCall(t, readOnly))
	if err != nil {
		t.Fatalf("apply_patch(read-only): %v", err)
	}
	if !denied.IsError {
		t.Fatalf("apply_patch wrote under the read-only preset: %q", denied.Content)
	}
	content, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if string(content) != "package main\n" {
		t.Fatalf("read-only apply_patch modified main.go: %q", content)
	}
}
