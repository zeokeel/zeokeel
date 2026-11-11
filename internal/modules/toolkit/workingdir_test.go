package toolkit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/sandbox"
)

// TestRootScopedFileToolsFollowWorkingDirectory pins the end-to-end contract:
// working_directory switches the session cwd, and the root-scoped file tools
// (write_file, list_dir, str_replace_editor) resolve relative paths from it.
func TestRootScopedFileToolsFollowWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	scoped := buildRootScopedFor(Config{}, root, time.Second, sandbox.ModeWorkspaceWrite, nil,
		"linux", func(string) (string, error) { return "", os.ErrNotExist })
	defer scoped.closeNow()

	invoke := func(name, args string) rig.ToolResult {
		t.Helper()
		tool, ok := scoped.byName[name]
		if !ok {
			t.Fatalf("tool %q not registered", name)
		}
		result, err := tool.Invoke(context.Background(), rig.ToolCall{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return result
	}

	if result := invoke("working_directory", `{"path":"sub"}`); result.IsError || strings.TrimSpace(result.Content) != "sub" {
		t.Fatalf("working_directory = %q (error=%v), want sub", result.Content, result.IsError)
	}
	if result := invoke("write_file", `{"path":"nested/note.txt","content":"hi"}`); result.IsError {
		t.Fatalf("write_file = %q", result.Content)
	}
	if _, err := os.Stat(filepath.Join(root, "sub", "nested", "note.txt")); err != nil {
		t.Fatalf("write_file did not follow working_directory: %v", err)
	}
	if result := invoke("list_dir", `{"path":"."}`); result.IsError || !strings.Contains(result.Content, "nested") {
		t.Fatalf("list_dir = %q (error=%v), want nested", result.Content, result.IsError)
	}
	if result := invoke("str_replace_editor", `{"command":"create","path":"editor.txt","file_text":"x"}`); result.IsError {
		t.Fatalf("str_replace_editor = %q", result.Content)
	}
	if _, err := os.Stat(filepath.Join(root, "sub", "editor.txt")); err != nil {
		t.Fatalf("str_replace_editor did not follow working_directory: %v", err)
	}
}
