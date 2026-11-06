package workspacechanges

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fileNumber formats a test file index for a stable, sortable path.
func fileNumber(i int) string { return fmt.Sprintf("%02d", i) }

// noteToolEdit simulates the runtime forwarding a file tool's ToolStart event:
// the tool's path is attributed to the turn (the comparison's before side comes
// from the start-of-turn snapshot).
func noteToolEdit(t *testing.T, turn *Turn, path string) {
	t.Helper()
	args, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	turn.NoteToolStart("write_file", args)
}

// TestTurnWalkDiff pins the non-Git turn: file-tool edits are reported with a
// unified diff against their pre-edit content, while a change made only by a
// shell command (no file-tool intent) is not reported.
func TestTurnWalkDiff(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "keep.txt"), "unchanged\n")
	writeFile(t, filepath.Join(root, "modify.txt"), "one\ntwo\nthree\n")
	writeFile(t, filepath.Join(root, "shell-only.txt"), "old\n")
	writeFile(t, filepath.Join(root, "deleted.txt"), "gone\n")

	turn, err := StartTurn(context.Background(), root)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	// One file-tool edit: the pre-edit content is captured at intent time.
	noteToolEdit(t, turn, "modify.txt")
	writeFile(t, filepath.Join(root, "modify.txt"), "one\nTWO\nthree\n")

	// A new file created by a file tool.
	noteToolEdit(t, turn, "added.txt")
	writeFile(t, filepath.Join(root, "added.txt"), "brand new\n")

	// A file removed by a file tool.
	noteToolEdit(t, turn, "deleted.txt")
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	// A shell-only change: no file-tool intent, so it must not be listed.
	writeFile(t, filepath.Join(root, "shell-only.txt"), "new\n")

	changes := turn.Finish(context.Background())
	byPath := make(map[string]Change, len(changes))
	for _, change := range changes {
		byPath[change.Path] = change
	}
	if len(changes) != 3 {
		t.Fatalf("changes = %+v, want added/modified/deleted", changes)
	}
	if _, ok := byPath["shell-only.txt"]; ok {
		t.Fatalf("shell-only change was reported: %+v", changes)
	}
	if _, ok := byPath["keep.txt"]; ok {
		t.Fatalf("unchanged file was reported: %+v", changes)
	}

	modified := byPath["modify.txt"]
	if modified.Status != StatusModified {
		t.Fatalf("modify.txt status = %q, want modified", modified.Status)
	}
	if modified.Added != 1 || modified.Removed != 1 {
		t.Fatalf("modify.txt added/removed = %d/%d, want 1/1 (diff %q)", modified.Added, modified.Removed, modified.Diff)
	}
	for _, want := range []string{"@@ -1,3 +1,3 @@", " one", "-two", "+TWO", " three"} {
		if !strings.Contains(modified.Diff, want) {
			t.Fatalf("modify.txt diff missing %q:\n%s", want, modified.Diff)
		}
	}

	added := byPath["added.txt"]
	if added.Status != StatusAdded || added.Added != 1 || added.Removed != 0 {
		t.Fatalf("added.txt = %+v, want added with one added line", added)
	}
	if !strings.Contains(added.Diff, "+brand new") {
		t.Fatalf("added.txt diff missing the new line:\n%s", added.Diff)
	}

	deleted := byPath["deleted.txt"]
	if deleted.Status != StatusDeleted || deleted.Removed != 1 || deleted.Added != 0 {
		t.Fatalf("deleted.txt = %+v, want deleted with one removed line", deleted)
	}
	if !strings.Contains(deleted.Diff, "-gone") {
		t.Fatalf("deleted.txt diff missing the removed line:\n%s", deleted.Diff)
	}

	// The diff is sorted by path, the contract the event payload relies on.
	for i := 1; i < len(changes); i++ {
		if changes[i-1].Path > changes[i].Path {
			t.Fatalf("changes are not sorted: %+v", changes)
		}
	}
}

// TestTurnIgnoresUnrelatedToolCalls pins the tool classification: a tool that
// does not edit files, an editor "view" command and a path outside the
// workspace never capture content or mark a path edited.
func TestTurnIgnoresUnrelatedToolCalls(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "a\n")

	turn, err := StartTurn(context.Background(), root)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	turn.NoteToolStart("read_file", json.RawMessage(`{"path":"a.txt"}`))
	turn.NoteToolStart("str_replace_editor", json.RawMessage(`{"command":"view","path":"a.txt"}`))
	turn.NoteToolStart("write_file", json.RawMessage(`{"path":"../escape.txt"}`))
	turn.NoteToolStart("write_file", json.RawMessage(`not json`))
	writeFile(t, filepath.Join(root, "a.txt"), "changed by shell\n")
	if changes := turn.Finish(context.Background()); len(changes) != 0 {
		t.Fatalf("unrelated tool calls produced changes: %+v", changes)
	}
}

// TestTurnBinaryOversized pins the content limits: a NUL byte marks the file
// binary and a file above the per-file limit is oversized; neither carries a
// text diff.
func TestTurnBinaryOversized(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "binary.bin")
	if err := os.WriteFile(binaryPath, []byte("head\x00tail"), 0o644); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	oversizedPath := filepath.Join(root, "oversized.txt")
	if err := os.WriteFile(oversizedPath, make([]byte, MaxContentFileBytes+1), 0o644); err != nil {
		t.Fatalf("write oversized: %v", err)
	}

	turn, err := StartTurn(context.Background(), root)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	noteToolEdit(t, turn, "binary.bin")
	if err := os.WriteFile(binaryPath, []byte("new\x00bytes"), 0o644); err != nil {
		t.Fatalf("rewrite binary: %v", err)
	}
	noteToolEdit(t, turn, "oversized.txt")
	if err := os.WriteFile(oversizedPath, make([]byte, MaxContentFileBytes+2), 0o644); err != nil {
		t.Fatalf("rewrite oversized: %v", err)
	}

	changes := turn.Finish(context.Background())
	byPath := make(map[string]Change, len(changes))
	for _, change := range changes {
		byPath[change.Path] = change
	}
	binary := byPath["binary.bin"]
	if !binary.Binary || binary.Diff != "" {
		t.Fatalf("binary.bin = %+v, want binary with no diff", binary)
	}
	oversized := byPath["oversized.txt"]
	if !oversized.Oversized || oversized.Diff != "" {
		t.Fatalf("oversized.txt = %+v, want oversized with no diff", oversized)
	}
}

// TestTurnDiffBudget pins the per-turn payload budget: the summed diff text
// stays within MaxTurnDiffBytes and the changes whose text did not fit are
// marked truncated instead of overflowing the persisted event.
func TestTurnDiffBudget(t *testing.T) {
	root := t.TempDir()
	const files = 20
	const lines = 100
	long := strings.Repeat("x", 400)
	before := make([]string, lines)
	for i := range before {
		before[i] = "old " + long
	}
	after := make([]string, lines)
	for i := range after {
		after[i] = "new " + long
	}
	beforeText := strings.Join(before, "\n") + "\n"
	afterText := strings.Join(after, "\n") + "\n"
	for i := 0; i < files; i++ {
		writeFile(t, filepath.Join(root, fileNumber(i)+".txt"), beforeText)
	}

	turn, err := StartTurn(context.Background(), root)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	for i := 0; i < files; i++ {
		noteToolEdit(t, turn, fileNumber(i)+".txt")
		writeFile(t, filepath.Join(root, fileNumber(i)+".txt"), afterText)
	}
	changes := turn.Finish(context.Background())
	if len(changes) != files {
		t.Fatalf("changes = %d, want %d", len(changes), files)
	}
	total := 0
	truncated := 0
	for _, change := range changes {
		total += len(change.Diff)
		if change.Truncated {
			truncated++
		}
		if change.Added != lines || change.Removed != lines {
			t.Fatalf("%s added/removed = %d/%d, want %d/%d", change.Path, change.Added, change.Removed, lines, lines)
		}
	}
	if total > MaxTurnDiffBytes {
		t.Fatalf("diff payload = %d bytes, want <= %d", total, MaxTurnDiffBytes)
	}
	if truncated == 0 {
		t.Fatalf("no change marked truncated; payload %d bytes", total)
	}
}

// TestTurnDiffCoarse pins the large-change fallback: a rewrite too expensive
// to align line by line degrades to a whole-file replacement marked coarse.
func TestTurnDiffCoarse(t *testing.T) {
	root := t.TempDir()
	const lines = 3000
	var before, after strings.Builder
	for i := 0; i < lines; i++ {
		before.WriteString("before line \n")
		after.WriteString("after line \n")
	}
	writeFile(t, filepath.Join(root, "big.txt"), before.String())

	turn, err := StartTurn(context.Background(), root)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	noteToolEdit(t, turn, "big.txt")
	writeFile(t, filepath.Join(root, "big.txt"), after.String())

	changes := turn.Finish(context.Background())
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want one", changes)
	}
	change := changes[0]
	if !change.Coarse {
		t.Fatalf("big.txt change was not marked coarse (%d added, %d removed)", change.Added, change.Removed)
	}
	if change.Added != lines || change.Removed != lines {
		t.Fatalf("coarse added/removed = %d/%d, want %d/%d", change.Added, change.Removed, lines, lines)
	}
}

// gitTestRepo creates a committed Git repository with the given files.
func gitTestRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	for path, content := range files {
		writeFile(t, filepath.Join(root, path), content)
	}
	run("add", ".")
	run("commit", "-qm", "init")
	return root
}

// TestTurnGitReadOnlyDiff pins the Git backend: shell-visible changes are
// reported with diffs reconstructed from HEAD read-only, and the repository's
// index and object store are never written by the capture or the diff.
func TestTurnGitReadOnlyDiff(t *testing.T) {
	root := gitTestRepo(t, map[string]string{
		"tracked.txt": "v1\nline2\n",
		"deleted.txt": "gone soon\n",
	})
	gitDir := filepath.Join(root, ".git")
	readIndex := func() []byte {
		t.Helper()
		index, err := os.ReadFile(filepath.Join(gitDir, "index"))
		if err != nil {
			t.Fatalf("read index: %v", err)
		}
		return index
	}
	objectsBefore, err := os.ReadDir(filepath.Join(gitDir, "objects"))
	if err != nil {
		t.Fatalf("read objects: %v", err)
	}

	turn, err := StartTurn(context.Background(), root)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	indexBefore := readIndex()

	// Shell-visible mutations: no file-tool intent, so the Git snapshot is
	// what reports them.
	writeFile(t, filepath.Join(root, "tracked.txt"), "v1\nLINE2\n")
	writeFile(t, filepath.Join(root, "new.txt"), "hello\n")
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	changes := turn.Finish(context.Background())
	byPath := make(map[string]Change, len(changes))
	for _, change := range changes {
		byPath[change.Path] = change
	}
	if len(changes) != 3 {
		t.Fatalf("changes = %+v, want modified/added/deleted", changes)
	}
	modified := byPath["tracked.txt"]
	if modified.Status != StatusModified {
		t.Fatalf("tracked.txt status = %q", modified.Status)
	}
	for _, want := range []string{"-line2", "+LINE2"} {
		if !strings.Contains(modified.Diff, want) {
			t.Fatalf("tracked.txt diff missing %q:\n%s", want, modified.Diff)
		}
	}
	added := byPath["new.txt"]
	if added.Status != StatusAdded || !strings.Contains(added.Diff, "+hello") {
		t.Fatalf("new.txt = %+v, want added with a diff", added)
	}
	deleted := byPath["deleted.txt"]
	if deleted.Status != StatusDeleted || !strings.Contains(deleted.Diff, "-gone soon") {
		t.Fatalf("deleted.txt = %+v, want deleted with a diff", deleted)
	}

	if after := readIndex(); string(after) != string(indexBefore) {
		t.Fatal("the capture or diff wrote the repository index")
	}
	objectsAfter, err := os.ReadDir(filepath.Join(gitDir, "objects"))
	if err != nil {
		t.Fatalf("read objects after: %v", err)
	}
	if len(objectsAfter) != len(objectsBefore) {
		t.Fatalf("repository objects changed: %d -> %d entries", len(objectsBefore), len(objectsAfter))
	}
}

// TestTurnGitDirtyBaseline pins the pre-turn content retention: a file already
// dirty at turn start keeps its start-of-turn content, so a shell change during
// the turn diffs against the turn's baseline, not HEAD.
func TestTurnGitDirtyBaseline(t *testing.T) {
	root := gitTestRepo(t, map[string]string{"dirty.txt": "base\n"})
	writeFile(t, filepath.Join(root, "dirty.txt"), "dirty\n")

	turn, err := StartTurn(context.Background(), root)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	writeFile(t, filepath.Join(root, "dirty.txt"), "after turn\n")
	changes := turn.Finish(context.Background())
	if len(changes) != 1 || changes[0].Path != "dirty.txt" {
		t.Fatalf("changes = %+v, want dirty.txt", changes)
	}
	change := changes[0]
	for _, want := range []string{"-dirty", "+after turn"} {
		if !strings.Contains(change.Diff, want) {
			t.Fatalf("dirty.txt diff missing %q:\n%s", want, change.Diff)
		}
	}
	if strings.Contains(change.Diff, "-base") {
		t.Fatalf("dirty.txt diff compared against HEAD instead of the turn baseline:\n%s", change.Diff)
	}
}

// TestTurnDiffContext pins the three lines of context around a change.
func TestTurnDiffContext(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = "line " + fileNumber(i)
	}
	writeFile(t, filepath.Join(root, "ctx.txt"), strings.Join(lines, "\n")+"\n")

	turn, err := StartTurn(context.Background(), root)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	noteToolEdit(t, turn, "ctx.txt")
	lines[5] = "changed line"
	writeFile(t, filepath.Join(root, "ctx.txt"), strings.Join(lines, "\n")+"\n")

	changes := turn.Finish(context.Background())
	if len(changes) != 1 {
		t.Fatalf("changes = %+v", changes)
	}
	diff := changes[0].Diff
	if !strings.Contains(diff, "@@ -3,7 +3,7 @@") {
		t.Fatalf("diff hunk header has the wrong context:\n%s", diff)
	}
	// Exactly three lines of context around the changed line.
	for i := 2; i <= 8; i++ {
		want := " line " + fileNumber(i)
		switch i {
		case 5:
			want = "-line " + fileNumber(i) + "\n+changed line"
		}
		if !strings.Contains(diff, want) {
			t.Fatalf("diff missing expected line %q:\n%s", want, diff)
		}
	}
	for _, outside := range []string{" line 01", " line 10"} {
		if strings.Contains(diff, outside) {
			t.Fatalf("diff included more than three context lines (%q):\n%s", outside, diff)
		}
	}
}

// TestTurnDiffLineLimitCoarse pins the hunk-line cap: a change that aligns but
// would render more than MaxDiffLines lines degrades to a coarse whole-file
// comparison instead of persisting an unbounded diff.
func TestTurnDiffLineLimitCoarse(t *testing.T) {
	root := t.TempDir()
	const lines = 1000
	var before, after strings.Builder
	for i := 0; i < lines; i++ {
		before.WriteString("old line " + fileNumber(i) + "\n")
		after.WriteString("new line " + fileNumber(i) + "\n")
	}
	writeFile(t, filepath.Join(root, "lines.txt"), before.String())

	turn, err := StartTurn(context.Background(), root)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	noteToolEdit(t, turn, "lines.txt")
	writeFile(t, filepath.Join(root, "lines.txt"), after.String())

	changes := turn.Finish(context.Background())
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want one", changes)
	}
	change := changes[0]
	if !change.Coarse {
		t.Fatalf("change with %d lines was not coarse (added %d, removed %d, diff %d bytes)",
			lines, change.Added, change.Removed, len(change.Diff))
	}
	if change.Added != lines || change.Removed != lines {
		t.Fatalf("coarse added/removed = %d/%d, want %d/%d", change.Added, change.Removed, lines, lines)
	}
}
