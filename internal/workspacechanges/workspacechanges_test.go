package workspacechanges

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestCaptureWalkDiff pins the non-Git backend: a turn that adds, modifies and
// deletes files is reported with the right statuses, and unchanged files are
// omitted.
func TestCaptureWalkDiff(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "keep.txt"), "unchanged")
	writeFile(t, filepath.Join(root, "modify.txt"), "before")
	writeFile(t, filepath.Join(root, "delete.txt"), "gone soon")
	writeFile(t, filepath.Join(root, "sub", "nested.txt"), "nested")

	before, err := Capture(context.Background(), root)
	if err != nil {
		t.Fatalf("Capture before: %v", err)
	}
	if before.Git {
		t.Fatal("plain temp dir was detected as a Git repository")
	}
	if len(before.Files) != 4 {
		t.Fatalf("before files = %d, want 4", len(before.Files))
	}

	writeFile(t, filepath.Join(root, "modify.txt"), "after!")
	writeFile(t, filepath.Join(root, "added.txt"), "new file")
	if err := os.Remove(filepath.Join(root, "delete.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	after, err := Capture(context.Background(), root)
	if err != nil {
		t.Fatalf("Capture after: %v", err)
	}
	changes := Diff(before, after)
	want := []Change{
		{Path: "added.txt", Status: StatusAdded},
		{Path: "delete.txt", Status: StatusDeleted},
		{Path: "modify.txt", Status: StatusModified},
	}
	if len(changes) != len(want) {
		t.Fatalf("changes = %+v, want %+v", changes, want)
	}
	for i := range want {
		if changes[i] != want[i] {
			t.Fatalf("change %d = %+v, want %+v (all: %+v)", i, changes[i], want[i], changes)
		}
	}

	// An unchanged snapshot diffs to nothing.
	if again := Diff(after, after); len(again) != 0 {
		t.Fatalf("diff of identical snapshots = %+v, want none", again)
	}
}

// TestCaptureWalkIgnoresBuildDirs pins that the walk backend skips noise
// directories (a .git/.cache/node_modules hit would otherwise flood the diff).
func TestCaptureWalkIgnoresBuildDirs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "src", "main.go"), "package main")
	writeFile(t, filepath.Join(root, "node_modules", "lib", "index.js"), "noise")
	writeFile(t, filepath.Join(root, ".cache", "blob.bin"), "noise")

	snap, err := Capture(context.Background(), root)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	for path := range snap.Files {
		for _, prefix := range []string{"node_modules/", ".cache/"} {
			if strings.HasPrefix(path, prefix) {
				t.Fatalf("snapshot included ignored path %q", path)
			}
		}
	}
	if _, ok := snap.Files["src/main.go"]; !ok {
		t.Fatalf("snapshot missing src/main.go: %v", snap.Files)
	}
}

// TestCaptureLargeFileUsesModTime pins the fallback fingerprint: a large file
// is not hashed, but a size/mtime change is still detected.
func TestCaptureLargeFileUsesModTime(t *testing.T) {
	root := t.TempDir()
	big := filepath.Join(root, "big.bin")
	if err := os.WriteFile(big, make([]byte, maxHashedFileBytes+1), 0o644); err != nil {
		t.Fatalf("write big file: %v", err)
	}
	before, err := Capture(context.Background(), root)
	if err != nil {
		t.Fatalf("Capture before: %v", err)
	}
	state := before.Files["big.bin"]
	if state.Hash != "" {
		t.Fatal("large file was hashed; want the size+mtime fallback")
	}
	if err := os.WriteFile(big, make([]byte, maxHashedFileBytes+2), 0o644); err != nil {
		t.Fatalf("rewrite big file: %v", err)
	}
	after, err := Capture(context.Background(), root)
	if err != nil {
		t.Fatalf("Capture after: %v", err)
	}
	changes := Diff(before, after)
	if len(changes) != 1 || changes[0].Path != "big.bin" || changes[0].Status != StatusModified {
		t.Fatalf("large-file changes = %+v, want modified big.bin", changes)
	}
}

// TestCaptureGitBackend pins the Git backend when git is available: tracked
// modifications, deletions and untracked additions are reported.
func TestCaptureGitBackend(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	_ = gitPath
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	writeFile(t, filepath.Join(root, "tracked.txt"), "v1")
	run("add", ".")
	run("commit", "-qm", "init")

	before, err := Capture(context.Background(), root)
	if err != nil {
		t.Fatalf("Capture before: %v", err)
	}
	if !before.Git {
		t.Fatal("git repository was not detected")
	}
	writeFile(t, filepath.Join(root, "tracked.txt"), "v2 changed")
	writeFile(t, filepath.Join(root, "untracked.txt"), "new")
	after, err := Capture(context.Background(), root)
	if err != nil {
		t.Fatalf("Capture after: %v", err)
	}
	changes := Diff(before, after)
	status := make(map[string]string, len(changes))
	for _, change := range changes {
		status[change.Path] = change.Status
	}
	if status["tracked.txt"] != StatusModified {
		t.Fatalf("tracked.txt status = %q (all: %+v)", status["tracked.txt"], changes)
	}
	if status["untracked.txt"] != StatusAdded {
		t.Fatalf("untracked.txt status = %q (all: %+v)", status["untracked.txt"], changes)
	}
	if err := os.Remove(filepath.Join(root, "tracked.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	final, err := Capture(context.Background(), root)
	if err != nil {
		t.Fatalf("Capture final: %v", err)
	}
	deleted := Diff(after, final)
	found := false
	for _, change := range deleted {
		if change.Path == "tracked.txt" && change.Status == StatusDeleted {
			found = true
		}
	}
	if !found {
		t.Fatalf("tracked.txt deletion not detected: %+v", deleted)
	}
}

// TestToStateChanges pins the JSON payload the session event persistence
// accepts (turn id + path/status pairs).
func TestToStateChanges(t *testing.T) {
	payload := ToStateChanges("turn-1", []Change{
		{Path: "a/b.go", Status: StatusModified},
		{Path: `quote"and\slash`, Status: StatusAdded},
	})
	var decoded struct {
		TurnID  string `json:"turn_id"`
		Changes []struct {
			Path   string `json:"path"`
			Status string `json:"status"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("payload is not JSON: %v\n%s", err, payload)
	}
	if decoded.TurnID != "turn-1" || len(decoded.Changes) != 2 {
		t.Fatalf("decoded = %+v", decoded)
	}
	if decoded.Changes[1].Path != `quote"and\slash` || decoded.Changes[1].Status != StatusAdded {
		t.Fatalf("escaped path round-trip = %+v", decoded.Changes[1])
	}
}

// TestIsGit pins the cheap repo detection helper.
func TestIsGit(t *testing.T) {
	if IsGit(t.TempDir()) {
		t.Fatal("plain temp dir reported as Git")
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if !IsGit(root) {
		t.Fatal("directory with .git reported as non-Git")
	}
}
