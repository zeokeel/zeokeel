package workspace

// Tests for the git-backed WorkspaceDiffer. They need a real git executable
// (git 2.43+ in CI) and skip when it is absent; every repository is a
// t.TempDir and nothing outside it is touched.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// requireGit skips the test when git is not installed.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
}

// git runs one git command in dir and fails the test on a non-zero exit.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, output)
	}
	return strings.TrimSpace(string(output))
}

// newGitRepo initializes a repository with one commit and returns its path.
func newGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "workspace-tests@example.com")
	git(t, dir, "config", "user.name", "Workspace Tests")
	writeFile(t, filepath.Join(dir, "keep.txt"), []byte("keep\n"))
	writeFile(t, filepath.Join(dir, "modify.txt"), []byte("old line\n"))
	writeFile(t, filepath.Join(dir, "delete.txt"), []byte("to delete\n"))
	writeFile(t, filepath.Join(dir, "rename-old.txt"), []byte("rename me\n"))
	writeFile(t, filepath.Join(dir, "binary.bin"), []byte{0x00, 0x01, 0x02})
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// changesByPath indexes a Changes result by path.
func changesByPath(t *testing.T, changes []contracts.FileChange) map[string]contracts.FileChange {
	t.Helper()
	byPath := make(map[string]contracts.FileChange, len(changes))
	for _, change := range changes {
		byPath[change.Path] = change
	}
	return byPath
}

// TestChangesStatusesAndDiffs covers the porcelain parse and per-file diffs for
// modified, deleted, renamed, staged, untracked and binary files.
func TestChangesStatusesAndDiffs(t *testing.T) {
	requireGit(t)
	repo := newGitRepo(t)

	// Working tree changes: one of each porcelain kind.
	writeFile(t, filepath.Join(repo, "modify.txt"), []byte("changed line\n"))
	if err := os.Remove(filepath.Join(repo, "delete.txt")); err != nil {
		t.Fatalf("remove delete.txt: %v", err)
	}
	git(t, repo, "mv", "rename-old.txt", "rename-new.txt")
	writeFile(t, filepath.Join(repo, "staged.txt"), []byte("staged\n"))
	git(t, repo, "add", "staged.txt")
	writeFile(t, filepath.Join(repo, "untracked.txt"), []byte("untracked\n"))
	writeFile(t, filepath.Join(repo, "binary.bin"), []byte{0x00, 0xff, 0x02})

	service := newDirectService(t, t.TempDir(), Config{})
	mustCreate(t, service, "changes", repo)
	differ, ok := contracts.WorkspaceService(service).(contracts.WorkspaceDiffer)
	if !ok {
		t.Fatal("service does not implement contracts.WorkspaceDiffer")
	}
	changes, err := differ.Changes(context.Background())
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	byPath := changesByPath(t, changes)
	if len(changes) != 6 {
		t.Fatalf("changes = %+v, want six entries", changes)
	}
	wantStatus := map[string]string{
		"modify.txt":     "M",
		"delete.txt":     "D",
		"rename-new.txt": "R",
		"staged.txt":     "A",
		"untracked.txt":  "??",
		"binary.bin":     "M",
	}
	for path, status := range wantStatus {
		change, ok := byPath[path]
		if !ok {
			t.Fatalf("change %q missing from %+v", path, changes)
		}
		if change.Status != status {
			t.Fatalf("%q status = %q, want %q", path, change.Status, status)
		}
	}
	if _, ok := byPath["keep.txt"]; ok {
		t.Fatalf("unchanged file in changes: %+v", changes)
	}
	// Diffs carry the text hunks.
	if diff := byPath["modify.txt"].Diff; !strings.Contains(diff, "-old line") || !strings.Contains(diff, "+changed line") {
		t.Fatalf("modify diff = %q, want - and + lines", diff)
	}
	if diff := byPath["delete.txt"].Diff; !strings.Contains(diff, "-to delete") {
		t.Fatalf("delete diff = %q", diff)
	}
	if diff := byPath["rename-new.txt"].Diff; !strings.Contains(diff, "+rename me") {
		t.Fatalf("rename diff = %q", diff)
	}
	if diff := byPath["staged.txt"].Diff; !strings.Contains(diff, "+staged") {
		t.Fatalf("staged diff = %q", diff)
	}
	// Untracked and binary files do not carry a text diff.
	if diff := byPath["untracked.txt"].Diff; diff != "" {
		t.Fatalf("untracked diff = %q, want empty", diff)
	}
	if diff := byPath["binary.bin"].Diff; diff != "" {
		t.Fatalf("binary diff = %q, want empty", diff)
	}
}

// TestChangesLimits covers the maxChanges and maxDiffBytes caps.
func TestChangesLimits(t *testing.T) {
	requireGit(t)
	repo := newGitRepo(t)
	for _, name := range []string{"modify.txt", "keep.txt"} {
		writeFile(t, filepath.Join(repo, name), []byte(strings.Repeat("changed "+name+"\n", 20)))
	}
	writeFile(t, filepath.Join(repo, "another.txt"), []byte("another\n"))
	git(t, repo, "add", "another.txt")

	service := newDirectService(t, t.TempDir(), Config{MaxChanges: 2, MaxDiffBytes: 16})
	mustCreate(t, service, "limits", repo)
	changes, err := service.Changes(context.Background())
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if len(changes) > 2 {
		t.Fatalf("changes = %d, want at most maxChanges 2", len(changes))
	}
	totalDiff := 0
	for _, change := range changes {
		totalDiff += len(change.Diff)
	}
	if totalDiff > 16 {
		t.Fatalf("total diff = %d bytes, want at most maxDiffBytes 16", totalDiff)
	}
}

// TestChangesErrors covers no-current, non-git and missing-git failures.
func TestChangesErrors(t *testing.T) {
	requireGit(t)
	service := newDirectService(t, t.TempDir(), Config{})
	if _, err := service.Changes(context.Background()); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("Changes without a workspace = %v, want ErrNoWorkspace", err)
	}

	plain := t.TempDir()
	mustCreate(t, service, "plain", plain)
	if _, err := service.Changes(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "git repository") {
		t.Fatalf("Changes(non-git workspace) = %v, want a readable git error", err)
	}

	// git missing from PATH is a readable error, not a panic.
	changedPath := t.TempDir()
	repo := newGitRepo(t)
	service = newDirectService(t, t.TempDir(), Config{})
	mustCreate(t, service, "repo", repo)
	t.Setenv("PATH", changedPath)
	if _, err := service.Changes(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "git executable not found") {
		t.Fatalf("Changes(no git) = %v, want a git-not-found error", err)
	}
}

// TestChangesNestedWorkspaceRoot covers a workspace root inside a repository:
// paths are translated to the workspace root and changes outside it skipped.
func TestChangesNestedWorkspaceRoot(t *testing.T) {
	requireGit(t)
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	git(t, repo, "config", "user.email", "workspace-tests@example.com")
	git(t, repo, "config", "user.name", "Workspace Tests")
	sub := makeDir(t, repo, "sub")
	writeFile(t, filepath.Join(repo, "outside.txt"), []byte("outside\n"))
	writeFile(t, filepath.Join(sub, "inside.txt"), []byte("inside\n"))
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "init")
	writeFile(t, filepath.Join(repo, "outside.txt"), []byte("outside changed\n"))
	writeFile(t, filepath.Join(sub, "inside.txt"), []byte("inside changed\n"))

	service := newDirectService(t, t.TempDir(), Config{})
	mustCreate(t, service, "nested", sub)
	changes, err := service.Changes(context.Background())
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if len(changes) != 1 || changes[0].Path != "inside.txt" {
		t.Fatalf("nested changes = %+v, want just inside.txt", changes)
	}
	if !strings.Contains(changes[0].Diff, "+inside changed") {
		t.Fatalf("nested diff = %q", changes[0].Diff)
	}
}

// TestChangesContextCancel covers context cancellation.
func TestChangesContextCancel(t *testing.T) {
	requireGit(t)
	repo := newGitRepo(t)
	service := newDirectService(t, t.TempDir(), Config{})
	mustCreate(t, service, "cancel", repo)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Changes(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Changes(cancelled) = %v, want context.Canceled", err)
	}
}

// TestChangesConcurrent covers concurrent read-only calls.
func TestChangesConcurrent(t *testing.T) {
	requireGit(t)
	repo := newGitRepo(t)
	writeFile(t, filepath.Join(repo, "modify.txt"), []byte("concurrent change\n"))
	service := newDirectService(t, t.TempDir(), Config{})
	mustCreate(t, service, "concurrent", repo)

	const workers = 4
	var waitGroup sync.WaitGroup
	errs := make(chan error, workers)
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			changes, err := service.Changes(context.Background())
			if err != nil {
				errs <- err
				return
			}
			if len(changes) == 0 || changes[0].Status != "M" {
				errs <- errors.New("concurrent Changes returned no modification")
			}
		}()
	}
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// TestChangesConfigValidation covers the new limit config fields.
func TestChangesConfigValidation(t *testing.T) {
	descriptor := Module()
	if descriptor.Validate == nil {
		t.Fatal("module has no config validation hook")
	}
	for _, cfg := range []Config{
		{MaxChanges: 1, MaxDiffBytes: 1},
		{MaxChanges: 0, MaxDiffBytes: 0},
	} {
		if err := descriptor.Validate(cfg); err != nil {
			t.Fatalf("Validate(%+v) = %v, want nil", cfg, err)
		}
	}
	if err := descriptor.Validate(Config{MaxChanges: -1}); err == nil {
		t.Fatal("Validate accepted a negative maxChanges")
	}
	if err := descriptor.Validate(Config{MaxDiffBytes: -1}); err == nil {
		t.Fatal("Validate accepted a negative maxDiffBytes")
	}
}
