package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func workdirResult(t *testing.T, tool *WorkingDirectoryTool, args string) workingDirectoryResult {
	t.Helper()
	out, err := tool.Invoke(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("working_directory %s: %v", args, err)
	}
	var result workingDirectoryResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode working_directory: %v\n%s", err, out)
	}
	return result
}

func TestWorkingDirectoryGetSetWithinRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src", "deep"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	tool := NewWorkingDirectoryTool(WorkingDirectoryConfig{WorkspaceRoot: root})

	initial := workdirResult(t, tool, `{}`)
	if initial.Kind != "working_directory" || initial.Relative != "." || initial.Cwd != root || initial.Changed {
		t.Fatalf("initial = %+v", initial)
	}

	set := workdirResult(t, tool, `{"subdir":"src"}`)
	if !set.Changed || set.Relative != "src" || set.Cwd != filepath.Join(root, "src") {
		t.Fatalf("set src = %+v", set)
	}
	deep := workdirResult(t, tool, `{"subdir":"deep"}`)
	if deep.Relative != filepath.Join("src", "deep") {
		t.Fatalf("set deep = %+v", deep)
	}
	current := workdirResult(t, tool, `{}`)
	if current.Relative != deep.Relative || current.Cwd != deep.Cwd {
		t.Fatalf("get after set = %+v, want %+v", current, deep)
	}
	dot := workdirResult(t, tool, `{"subdir":"."}`)
	if dot.Relative != deep.Relative {
		t.Fatalf(`set "." = %+v, want current directory %q`, dot, deep.Relative)
	}
	up := workdirResult(t, tool, `{"cd":"."}`)
	if up.Relative != deep.Relative {
		t.Fatalf("cd alias = %+v", up)
	}
}

func TestWorkingDirectoryRejectsTraversalAndInvalidDirs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	tool := NewWorkingDirectoryTool(WorkingDirectoryConfig{WorkspaceRoot: root})

	for _, args := range []string{
		`{"subdir":".."}`,
		`{"subdir":"../outside"}`,
		`{"subdir":"src/../../outside"}`,
		`{"subdir":"/tmp"}`,
		`{"subdir":"file.txt"}`,
		`{"subdir":"missing"}`,
		`{"subdir":"escape"}`,
	} {
		if _, err := tool.Invoke(context.Background(), json.RawMessage(args)); err == nil {
			t.Fatalf("working_directory %s = nil error", args)
		}
	}
}

func TestWorkingDirectorySharedAtomicState(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	state, err := NewWorkingDirectoryState(root)
	if err != nil {
		t.Fatalf("NewWorkingDirectoryState: %v", err)
	}
	first := NewWorkingDirectoryTool(WorkingDirectoryConfig{State: state})
	second := NewWorkingDirectoryTool(WorkingDirectoryConfig{State: state})

	workdirResult(t, first, `{"subdir":"a"}`)
	got := workdirResult(t, second, `{}`)
	if got.Relative != "a" || got.Cwd != filepath.Join(root, "a") {
		t.Fatalf("shared state = %+v", got)
	}
	abs, rel := state.Cwd()
	if abs != got.Cwd || rel != "a" {
		t.Fatalf("state.Cwd = %q, %q", abs, rel)
	}
	if state.Root() != root {
		t.Fatalf("state.Root = %q, want %q", state.Root(), root)
	}
}

func TestWorkingDirectoryWithoutRoot(t *testing.T) {
	tool := NewWorkingDirectoryTool(WorkingDirectoryConfig{})
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("working_directory without a root = nil error")
	}
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	bad := NewWorkingDirectoryTool(WorkingDirectoryConfig{WorkspaceRoot: file})
	if _, err := bad.Invoke(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("working_directory with a file root = nil error")
	}
}

// TestToolDescriptionsMatchSharedWorkingDirectory pins the description text
// to the behavior: paths are relative to the shared current working
// directory (workspace root by default), which working_directory can change.
func TestToolDescriptionsMatchSharedWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	state, err := NewWorkingDirectoryState(root)
	if err != nil {
		t.Fatal(err)
	}
	setter := NewWorkingDirectoryTool(WorkingDirectoryConfig{WorkspaceRoot: root, State: state})
	if _, err := setter.Invoke(context.Background(), json.RawMessage(`{"subdir":"src"}`)); err != nil {
		t.Fatalf("set working directory: %v", err)
	}
	if got := state.Rel(); got != "src" {
		t.Fatalf("state.Rel() = %q, want src", got)
	}
	builtins, err := Builtins(Config{WorkspaceRoot: root, AllowShell: true, DirState: state})
	if err != nil {
		t.Fatalf("Builtins: %v", err)
	}
	want := map[string]bool{
		"read_file": true, "write_file": true, "edit_file": true,
		"glob": true, "grep": true, "bash": true,
	}
	seen := make(map[string]bool, len(want))
	for _, tool := range builtins {
		if !want[tool.Name()] {
			continue
		}
		seen[tool.Name()] = true
		desc := tool.Description()
		if !strings.Contains(desc, "current working directory") {
			t.Errorf("%s description does not mention the current working directory:\n%s", tool.Name(), desc)
		}
		if !strings.Contains(desc, "working_directory") {
			t.Errorf("%s description does not mention the working_directory tool:\n%s", tool.Name(), desc)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("%s not registered by Builtins", name)
		}
	}
}
