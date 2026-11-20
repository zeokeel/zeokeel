package toolkit

// The SDK undo tool reverts a turn snapshot. The product wires it because a
// coding harness must be able to undo what the model changed; the turn hook
// (agent side) takes the snapshot, the toolkit owns the tracker.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zeokeel/zeokeel-rig/sandbox"
)

// TestUndoToolRevertsTheTurnSnapshot pins the flow end to end: snapshot, edit,
// revert — the file goes back to the snapshot content.
func TestUndoToolRevertsTheTurnSnapshot(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "notes.md")
	writeFile(t, target, "before the turn\n")

	scoped := newSandboxBuild(t, Config{}, root, sandbox.ModeWorkspaceWrite, nil)
	if scoped.undo == nil {
		t.Fatal("workspace build has no undo tracker")
	}
	if _, err := scoped.undo.Begin(); err != nil {
		t.Fatalf("undo snapshot: %v", err)
	}
	writeFile(t, target, "after the turn\n")

	result := invokeScopedTool(t, scoped, "undo", `{}`)
	if result.IsError {
		t.Fatalf("undo failed: %q", result.Content)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read notes.md: %v", err)
	}
	if string(content) != "before the turn\n" {
		t.Fatalf("notes.md after undo = %q, want the snapshot content", content)
	}
}

// TestUndoToolAbsentInReadOnlyBuild pins the policy promise: a read-only build
// does not ship a tool that writes the workspace back.
func TestUndoToolAbsentInReadOnlyBuild(t *testing.T) {
	root := t.TempDir()
	scoped := newSandboxBuild(t, Config{}, root, sandbox.ModeReadOnly, nil)
	if _, ok := scoped.byName["undo"]; ok {
		t.Fatal("read-only build registered the undo tool")
	}
}
