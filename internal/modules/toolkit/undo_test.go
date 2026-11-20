package toolkit

// The SDK undo tool reverts a turn snapshot. The product wires it because a
// coding harness must be able to undo what the model changed; the turn hook
// (agent side) takes the snapshot, the toolkit owns the tracker.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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
	// The snapshot store must live outside the workspace: a mirror inside it is
	// readable through the file tools (hidden patterns are root-anchored) and
	// would double every glob/grep/search result.
	if _, err := os.Stat(filepath.Join(root, ".zeokeel", "undo")); err == nil {
		t.Fatal("undo snapshot store was created inside the workspace")
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
	if scoped.undo != nil {
		t.Fatal("read-only build created an undo tracker (it would snapshot into the workspace)")
	}
	if _, err := os.Stat(filepath.Join(root, ".zeokeel")); err == nil {
		t.Fatal("read-only build created workspace state for undo")
	}
}

// TestUndoIgnoreRules pins the hidden-glob conversion: the SDK's ignore syntax is
// element/prefix matching, so only patterns that reduce to a literal survive.
func TestUndoIgnoreRules(t *testing.T) {
	got := undoIgnoreRules([]string{
		"**/.env", "**/.env/**", "secrets/**", "secrets/", "**/*.pem", "docs/generated/**", "  ", ".env",
	})
	want := map[string]bool{"secrets": true, "docs/generated": true, ".env": true}
	if len(got) != len(want) {
		t.Fatalf("undoIgnoreRules = %v, want %v (wildcard basenames dropped)", got, want)
	}
	for _, rule := range got {
		if !want[rule] {
			t.Fatalf("undoIgnoreRules returned %q, want one of %v", rule, want)
		}
	}
}

// TestUndoGitSnapshotPersistsTurnNumbers pins Options.Dir: a rebuilt tracker on
// the same workspace continues the turn numbering instead of resetting to 1.
func TestUndoGitSnapshotPersistsTurnNumbers(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	root := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = root
	if err := cmd.Run(); err != nil {
		t.Skipf("git init failed: %v", err)
	}
	dataDir := t.TempDir()
	first := newUndoTracker(root, dataDir, nil, nil)
	if first == nil {
		t.Fatal("git workspace got no tracker")
	}
	if turn, err := first.Begin(); err != nil || turn != 1 {
		t.Fatalf("first Begin = %d/%v, want 1", turn, err)
	}
	second := newUndoTracker(root, dataDir, nil, nil)
	if second == nil {
		t.Fatal("rebuilt tracker is nil")
	}
	if turn, err := second.Begin(); err != nil || turn != 2 {
		t.Fatalf("rebuilt Begin = %d/%v, want 2 (persisted turn number)", turn, err)
	}
}

// TestReadOnlyBuildLeavesTheDataDirAlone pins the other half of the read-only
// promise: no tracker, no snapshot store, not even in the product data dir.
func TestReadOnlyBuildLeavesTheDataDirAlone(t *testing.T) {
	root, dataDir := t.TempDir(), t.TempDir()
	scoped := buildRootScopedWith(Config{}, root, 5*time.Second, sandbox.ModeReadOnly, nil, dataDir, nil, "linux",
		func(string) (string, error) { return "", os.ErrNotExist })
	t.Cleanup(scoped.close)
	if entries, err := os.ReadDir(dataDir); err == nil && len(entries) != 0 {
		t.Fatalf("read-only build wrote %d entries to the data dir", len(entries))
	}
}

// TestUndoIgnoreRulesExcludeHiddenPathsEndToEnd pins the wiring (not just the
// conversion): with denied globs configured, the tracker's own changes report
// only the files the user did not hide.
func TestUndoIgnoreRulesExcludeHiddenPathsEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	root := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		// Pinned config: a host with commit.gpgsign=true would fail the commit
		// (no gpg in CI) and a host excludes file naming .env would silently
		// change what this test snapshots.
		full := append([]string{"-c", "commit.gpgsign=false", "-c", "core.excludesFile="}, args...)
		cmd := exec.Command("git", full...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	for path, content := range map[string]string{
		".env":                  "A=1\n",
		"sub/.env":              "B=1\n",
		"secrets/token":         "T\n",
		"docs/generated/out.go": "package out\n",
		"other/secrets/x":       "X\n",
		"keep.txt":              "keep\n",
	} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(path)), content)
	}
	runGit("add", "-A")
	runGit("commit", "-qm", "init")

	patterns := hiddenPatterns([]string{"**/.env", "secrets/**", "docs/generated/**"})
	tracker := newUndoTracker(root, t.TempDir(), patterns, t.Logf)
	if tracker == nil {
		t.Fatal("git workspace got no tracker")
	}
	turn, err := tracker.Begin()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	for _, file := range []struct{ path, content string }{
		{".env", "A=2\n"},
		{"sub/.env", "B=2\n"},
		{"secrets/token", "T2\n"},
		{"docs/generated/out.go", "package out2\n"},
		{"other/secrets/x", "X2\n"},
		{"keep.txt", "changed\n"},
	} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(file.path)), file.content)
	}
	changes, err := tracker.Changes(turn, "")
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if len(changes) != 1 || changes[0].Path != "keep.txt" {
		t.Fatalf("changes = %+v, want only keep.txt (hidden paths excluded)", changes)
	}
}

// TestUndoBeginTurnRespectsCancelledContext pins the ctx contract: a cancelled
// run must not copy the workspace into the snapshot store.
func TestUndoBeginTurnRespectsCancelledContext(t *testing.T) {
	root, dataDir := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "v1\n")
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return root }, nil, nil)
	group.dataDir = dataDir
	t.Cleanup(group.close)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := group.BeginTurn(cancelled, root); err == nil {
		t.Fatal("BeginTurn accepted a cancelled context")
	}
	// The SDK consumes the turn number before taking the snapshot, so the
	// cancelled attempt still advances it; the next call must succeed.
	turn, err := group.BeginTurn(context.Background(), root)
	if err != nil || turn != 2 {
		t.Fatalf("BeginTurn = %d/%v, want 2 (number consumed by the cancelled take)", turn, err)
	}
}
