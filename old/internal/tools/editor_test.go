package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStrReplaceEditor(t *testing.T) {
	root := t.TempDir()
	tool := NewStrReplaceEditorTool(EditorConfig{WorkspaceRoot: root})
	assertSchema(t, tool)
	if tool.Name() != "str_replace_editor" {
		t.Fatalf("name = %q", tool.Name())
	}

	out := mustInvoke(t, tool, map[string]any{
		"command":   "create",
		"path":      "dir/notes.txt",
		"file_text": "alpha\nbeta\ngamma\n",
	})
	if !strings.Contains(out, "New file created successfully") {
		t.Fatalf("create output = %q", out)
	}
	path := filepath.Join(root, "dir", "notes.txt")
	if data, err := os.ReadFile(path); err != nil || string(data) != "alpha\nbeta\ngamma\n" {
		t.Fatalf("created content = %q, err = %v", data, err)
	}
	if _, err := invokeTool(t, tool, map[string]any{
		"command": "create", "path": "dir/notes.txt", "file_text": "again",
	}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("create existing error = %v", err)
	}

	out = mustInvoke(t, tool, map[string]any{"command": "view", "path": "dir/notes.txt"})
	for _, want := range []string{"alpha", "beta", "gamma", "total of 4 lines"} {
		if !strings.Contains(out, want) {
			t.Fatalf("view output missing %q: %q", want, out)
		}
	}
	out = mustInvoke(t, tool, map[string]any{"command": "view", "path": "dir/notes.txt", "view_range": []int{2, 2}})
	if !strings.Contains(out, "beta") || strings.Contains(out, "alpha") || !strings.Contains(out, "view_range=[2, 2]") {
		t.Fatalf("ranged view = %q", out)
	}
	for _, badRange := range [][]int{{0, 1}, {1, 99}, {2, 1}, {1}} {
		if _, err := invokeTool(t, tool, map[string]any{"command": "view", "path": "dir/notes.txt", "view_range": badRange}); err == nil {
			t.Fatalf("view accepted invalid range %v", badRange)
		}
	}

	out = mustInvoke(t, tool, map[string]any{
		"command": "str_replace", "path": "dir/notes.txt", "old_str": "beta", "new_str": "BETA",
	})
	if !strings.Contains(out, "edited successfully") {
		t.Fatalf("replace output = %q", out)
	}
	readNote := func() string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read note: %v", err)
		}
		return string(data)
	}
	if got := readNote(); got != "alpha\nBETA\ngamma\n" {
		t.Fatalf("after replace = %q", got)
	}

	mustInvoke(t, tool, map[string]any{"command": "create", "path": "dup.txt", "file_text": "x x"})
	if _, err := invokeTool(t, tool, map[string]any{
		"command": "str_replace", "path": "dup.txt", "old_str": "x", "new_str": "y",
	}); err == nil || !strings.Contains(err.Error(), "must be unique") {
		t.Fatalf("multiple replacement error = %v", err)
	}
	if _, err := invokeTool(t, tool, map[string]any{
		"command": "str_replace", "path": "dup.txt", "old_str": "missing", "new_str": "y",
	}); err == nil || !strings.Contains(err.Error(), "did not appear") {
		t.Fatalf("missing replacement error = %v", err)
	}
	writeTestFile(t, filepath.Join(root, "single.txt"), "single")
	if _, err := invokeTool(t, tool, map[string]any{
		"command": "str_replace", "path": "single.txt", "old_str": "single", "new_str": "SINGLE", "view_range": []int{1, 1},
	}); err != nil {
		t.Fatalf("unknown/extra parameter should be ignored: %v", err)
	}

	out = mustInvoke(t, tool, map[string]any{
		"command": "insert", "path": "dir/notes.txt", "insert_line": 1, "new_str": "inserted",
	})
	if !strings.Contains(out, "edited successfully") {
		t.Fatalf("insert output = %q", out)
	}
	if got := readNote(); got != "alpha\ninserted\nBETA\ngamma\n" {
		t.Fatalf("after insert = %q", got)
	}
	if _, err := invokeTool(t, tool, map[string]any{
		"command": "insert", "path": "dir/notes.txt", "insert_line": 99, "new_str": "x",
	}); err == nil {
		t.Fatal("out-of-range insert should fail")
	}

	out = mustInvoke(t, tool, map[string]any{"command": "undo_edit", "path": "dir/notes.txt"})
	if !strings.Contains(out, "Undid the last edit") {
		t.Fatalf("undo output = %q", out)
	}
	if got := readNote(); got != "alpha\nBETA\ngamma\n" {
		t.Fatalf("after first undo = %q", got)
	}
	mustInvoke(t, tool, map[string]any{"command": "undo_edit", "path": "dir/notes.txt"})
	if got := readNote(); got != "alpha\nbeta\ngamma\n" {
		t.Fatalf("after second undo = %q", got)
	}
	mustInvoke(t, tool, map[string]any{"command": "undo_edit", "path": "dir/notes.txt"})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("undo of create did not remove the file: %v", err)
	}
	if _, err := invokeTool(t, tool, map[string]any{"command": "undo_edit", "path": "dir/notes.txt"}); err == nil {
		t.Fatal("undo with empty history should fail")
	}
}

func TestEditorDirectoryAndTraversal(t *testing.T) {
	root := t.TempDir()
	tool := NewStrReplaceEditorTool(EditorConfig{WorkspaceRoot: root})
	if err := os.MkdirAll(filepath.Join(root, "sub", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "sub", "nested", "file.txt"), "content")
	out := mustInvoke(t, tool, map[string]any{"command": "view", "path": "sub"})
	for _, want := range []string{"f\tsub/nested/file.txt", "d\tsub/nested"} {
		if !strings.Contains(out, want) {
			t.Fatalf("directory view missing %q:\n%s", want, out)
		}
	}
	if _, err := invokeTool(t, tool, map[string]any{"command": "view", "path": "sub", "view_range": []int{1, 1}}); err == nil {
		t.Fatal("view_range on a directory should fail")
	}

	for _, bad := range []string{"../secret.txt", "/etc/passwd", "a/../b.txt", ""} {
		for _, command := range []string{"view", "create", "str_replace", "insert", "undo_edit"} {
			if _, err := invokeTool(t, tool, map[string]any{
				"command": command, "path": bad, "file_text": "x", "old_str": "x", "new_str": "y", "insert_line": 0,
			}); err == nil {
				t.Fatalf("%s accepted path %q", command, bad)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(root, "b.txt")); !os.IsNotExist(err) {
		t.Fatalf("traversal created b.txt: %v", err)
	}

	outside := t.TempDir()
	writeTestFile(t, filepath.Join(outside, "secret.txt"), "top secret")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Logf("symlinks unavailable: %v", err)
	} else if _, err := invokeTool(t, tool, map[string]any{"command": "view", "path": "escape/secret.txt"}); err == nil {
		t.Fatal("view through escaping symlink should fail")
	}

	writeTestFile(t, filepath.Join(root, "binary.bin"), "abc\x00def")
	if _, err := invokeTool(t, tool, map[string]any{"command": "view", "path": "binary.bin"}); err == nil {
		t.Fatal("viewing a binary file should fail")
	}
	if _, err := invokeTool(t, tool, map[string]any{"command": "bogus", "path": "sub"}); err == nil {
		t.Fatal("unknown command should fail")
	}
	if _, err := invokeTool(t, tool, map[string]any{"command": "view"}); err == nil {
		t.Fatal("missing path should fail")
	}
}

func TestEditorCreateUndoRoundTrip(t *testing.T) {
	root := t.TempDir()
	tool := NewStrReplaceEditorTool(EditorConfig{WorkspaceRoot: root})
	mustInvoke(t, tool, map[string]any{"command": "create", "path": "one.txt", "file_text": "one"})
	mustInvoke(t, tool, map[string]any{"command": "create", "path": "two.txt", "file_text": "two"})
	mustInvoke(t, tool, map[string]any{"command": "undo_edit", "path": "one.txt"})
	if _, err := os.Stat(filepath.Join(root, "one.txt")); !os.IsNotExist(err) {
		t.Fatalf("one.txt not removed: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "two.txt")); err != nil || string(data) != "two" {
		t.Fatalf("undo touched the wrong path: %q %v", data, err)
	}
	bad := NewStrReplaceEditorTool(EditorConfig{})
	if _, err := invokeTool(t, bad, map[string]any{"command": "view", "path": "one.txt"}); err == nil {
		t.Fatal("missing workspace root should fail")
	}
}
