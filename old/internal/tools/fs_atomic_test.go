package tools

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

// TestEditFileRequiresPriorRead is the FP-08 read-before-write regression: an
// in-turn edit of a file the model has not read fails, and reading it with
// read_file unlocks the edit.
func TestEditFileRequiresPriorRead(t *testing.T) {
	root := t.TempDir()
	ts := testTools(t, Config{WorkspaceRoot: root})
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := runtime.WithSessionID(context.Background(), "read-before-write-requires")
	writeArgs := mustArgs(t, map[string]string{"path": "note.txt", "old": "world", "new": "gopher"})

	if _, err := ts["edit_file"].Invoke(ctx, writeArgs); err == nil || !strings.Contains(err.Error(), "before editing") {
		t.Fatalf("edit without a prior read error = %v, want the read_file requirement", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "hello world\n" {
		t.Fatalf("rejected edit changed the file: %q, %v", got, err)
	}

	// read_file records the exact content for this session.
	if _, err := ts["read_file"].Invoke(ctx, mustArgs(t, map[string]string{"path": "note.txt"})); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if _, err := ts["edit_file"].Invoke(ctx, writeArgs); err != nil {
		t.Fatalf("edit after read: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "hello gopher\n" {
		t.Fatalf("edited file = %q, %v", got, err)
	}
}

// TestEditFileRejectsChangedSinceRead proves the read must still be current: an
// external change between read_file and edit_file is rejected instead of
// silently replacing text the model never saw.
func TestEditFileRejectsChangedSinceRead(t *testing.T) {
	root := t.TempDir()
	ts := testTools(t, Config{WorkspaceRoot: root})
	path := filepath.Join(root, "changing.txt")
	if err := os.WriteFile(path, []byte("original text"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := runtime.WithSessionID(context.Background(), "read-before-write-stale")
	if _, err := ts["read_file"].Invoke(ctx, mustArgs(t, map[string]string{"path": "changing.txt"})); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if err := os.WriteFile(path, []byte("changed behind the model"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := mustArgs(t, map[string]string{"path": "changing.txt", "old": "original", "new": "replaced"})
	if _, err := ts["edit_file"].Invoke(ctx, args); err == nil || !strings.Contains(err.Error(), "changed since it was read") {
		t.Fatalf("stale edit error = %v, want the changed-since-read rejection", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "changed behind the model" {
		t.Fatalf("stale edit changed the file: %q", got)
	}

	// A fresh read makes the edit valid again.
	if _, err := ts["read_file"].Invoke(ctx, mustArgs(t, map[string]string{"path": "changing.txt"})); err != nil {
		t.Fatalf("re-read: %v", err)
	}
	args = mustArgs(t, map[string]string{"path": "changing.txt", "old": "changed", "new": "replaced"})
	if _, err := ts["edit_file"].Invoke(ctx, args); err != nil {
		t.Fatalf("edit after re-read: %v", err)
	}
}

// TestWriteFileThenEditNeedsNoReread keeps the common authoring flow smooth: a
// write counts as having seen the content, so a follow-up edit is allowed.
func TestWriteFileThenEditNeedsNoReread(t *testing.T) {
	root := t.TempDir()
	ts := testTools(t, Config{WorkspaceRoot: root})
	ctx := runtime.WithSessionID(context.Background(), "read-before-write-write-then-edit")
	if _, err := ts["write_file"].Invoke(ctx, mustArgs(t, map[string]string{"path": "new.txt", "content": "alpha beta"})); err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if _, err := ts["edit_file"].Invoke(ctx, mustArgs(t, map[string]string{"path": "new.txt", "old": "beta", "new": "gamma"})); err != nil {
		t.Fatalf("edit after write: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "new.txt")); err != nil || string(got) != "alpha gamma" {
		t.Fatalf("content = %q, %v", got, err)
	}
}

// TestWriteFileIsAtomicAndPreservesMode covers the write half of FP-08: the
// content is replaced completely through a same-directory temp file (no temp
// left behind), and the existing file mode survives the rename.
func TestWriteFileIsAtomicAndPreservesMode(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("Unix file modes are not reported on Windows")
	}
	root := t.TempDir()
	ts := testTools(t, Config{WorkspaceRoot: root})
	path := filepath.Join(root, "private.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ts["write_file"].Invoke(context.Background(), mustArgs(t, map[string]string{"path": "private.txt", "content": "new content"})); err != nil {
		t.Fatalf("write_file: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new content" {
		t.Fatalf("content = %q, %v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %o, want the existing 600 preserved", perm)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".zeokeel-write-") {
			t.Fatalf("atomic write left temporary file %q", entry.Name())
		}
	}
}
