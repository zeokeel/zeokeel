package toolkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Regression form of AUDIT-2026-11-24 A-5: a workspace root that is a git-IGNORED
// subdirectory can be snapshotted, because the tracker falls back to the file
// backend instead of choosing the git backend whose `git add -A -- <ignored path>`
// exits 1 and made every turn's snapshot fail.
func TestUndoUsesFileBackendForGitIgnoredRoot(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) {
		command := exec.Command("git", args...)
		command.Dir = repo
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, output)
		}
	}
	run("init", "-q", ".")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("build/\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	ignoredRoot := filepath.Join(repo, "build")
	if err := os.MkdirAll(ignoredRoot, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	run("add", ".gitignore")
	run("commit", "-q", "-m", "init")

	dataDir := t.TempDir()
	tracker := newUndoTracker(ignoredRoot, dataDir, nil, nil)
	if tracker == nil {
		t.Fatal("newUndoTracker returned nil for a git-ignored workspace root")
	}
	if _, err := tracker.Begin(); err != nil {
		t.Fatalf("Begin (snapshot) on a git-ignored root: %v", err)
	}
}
