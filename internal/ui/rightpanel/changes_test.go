package rightpanel_test

// Tests for the inspector's Changes pane: the optional differ, per-file status
// badges/stats, native collapsible diffs, truncation, refresh and the error and
// empty states.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/rightpanel"
)

// differWorkspace is a fake workspace that also implements
// contracts.WorkspaceDiffer with scripted changes and an error.
type differWorkspace struct {
	*fakeWorkspace
	mu      sync.Mutex
	changes []contracts.FileChange
	err     error
	calls   int
}

func newDifferWorkspace(changes ...contracts.FileChange) *differWorkspace {
	base := newFakeWorkspace()
	base.setCurrent(contracts.Workspace{ID: "ws-1", Name: "proj", Root: "/work/proj"})
	return &differWorkspace{fakeWorkspace: base, changes: changes}
}

func newDifferWorkspaceWithoutCurrent() *differWorkspace {
	return &differWorkspace{fakeWorkspace: newFakeWorkspace()}
}

func (d *differWorkspace) Changes(context.Context) ([]contracts.FileChange, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	if d.err != nil {
		return nil, d.err
	}
	return append([]contracts.FileChange(nil), d.changes...), nil
}

func (d *differWorkspace) setChanges(changes ...contracts.FileChange) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.changes = changes
}

func (d *differWorkspace) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

// --- tests ------------------------------------------------------------------

func TestChangesPaneWithoutBackend(t *testing.T) {
	h := newHarness(t, newFakeChat(), rightpanel.Config{})
	tester := h.tester()
	h.selectPane(tester, "变更")
	h.assertTexts(tester, "工作区未启用")
}

func TestChangesPaneWithoutDiffer(t *testing.T) {
	// A plain WorkspaceService (filesTreeWorkspace) does not implement the
	// optional WorkspaceDiffer.
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, filesTreeWorkspace())
	tester := h.tester()
	h.selectPane(tester, "变更")
	h.assertTexts(tester, "当前工作区不支持变更视图")
}

func TestChangesPaneWithoutWorkspace(t *testing.T) {
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, newDifferWorkspaceWithoutCurrent())
	tester := h.tester()
	h.selectPane(tester, "变更")
	h.assertTexts(tester, "还没有打开工作区")
}

func TestChangesListStatsAndExpand(t *testing.T) {
	mainDiff := strings.Join([]string{
		"diff --git a/main.go b/main.go",
		"--- a/main.go",
		"+++ b/main.go",
		"@@ -1,2 +1,2 @@",
		"-old line",
		"+new line",
		" context line",
	}, "\n")
	workspace := newDifferWorkspace(
		contracts.FileChange{Path: "main.go", Status: "M", Diff: mainDiff},
		contracts.FileChange{Path: "added.txt", Status: "A", Diff: ""},
		contracts.FileChange{Path: "gone.go", Status: "D", Diff: "@@ -1 +0,0 @@\n-removed line"},
		contracts.FileChange{Path: "renamed.go", Status: "R", Diff: ""},
		contracts.FileChange{Path: "untracked.md", Status: "??", Diff: ""},
	)
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
	tester := h.tester()
	h.selectPane(tester, "变更")

	h.assertTexts(tester,
		"变更", "main.go", "added.txt", "gone.go", "renamed.go", "untracked.md",
		"修改", "新增", "删除", "重命名", "未跟踪",
		"+1", "-1", "查看 diff", "无文本 diff")
	h.assertNoTexts(tester, "@@ -1,2 +1,2 @@") // collapsed by default

	// Expanding the first card's native collapsible shows the unified diff
	// through the official editor.DiffView. The viewer paints the changed lines
	// on the canvas, so the text layer carries the hunk headers and the lines
	// are asserted through the viewer's accessibility value (mygo
	// docs/ui/diff-viewer.md; Tester.Access), not through Tester.Texts.
	if err := tester.Click("查看 diff"); err != nil {
		t.Fatalf("expand diff: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Advance(400 * time.Millisecond)
	tester.Frame()
	assertContains(t, tester, "@@ -1,2 +1,2 @@")
	newSide, ok := tester.Access("New text")
	if !ok {
		t.Fatalf("the diff viewer's new side is not exposed to assistive technology; texts = %v", tester.Texts())
	}
	for _, want := range []string{"new line", "context line"} {
		if !strings.Contains(newSide.Value, want) {
			t.Fatalf("diff viewer's new side value = %q, want %q", newSide.Value, want)
		}
	}
	if strings.Contains(newSide.Value, "old line") {
		t.Fatalf("diff viewer's new side value unexpectedly holds the deleted line: %q", newSide.Value)
	}

	// Collapsing hides it again.
	if err := tester.Click("查看 diff"); err != nil {
		t.Fatalf("collapse diff: %v", err)
	}
	tester.Frame()
	tester.Advance(400 * time.Millisecond)
	tester.Frame()
	h.assertNoTexts(tester, "@@ -1,2 +1,2 @@")
}

func TestChangesEmpty(t *testing.T) {
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, newDifferWorkspace())
	tester := h.tester()
	h.selectPane(tester, "变更")
	h.assertTexts(tester, "工作区没有未提交的变更")
}

func TestChangesError(t *testing.T) {
	workspace := newDifferWorkspace()
	workspace.err = errors.New("not a git repository")
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
	tester := h.tester()
	h.selectPane(tester, "变更")
	h.assertTexts(tester, "读取变更失败：not a git repository")
}

func TestChangesRefreshReloads(t *testing.T) {
	workspace := newDifferWorkspace(contracts.FileChange{Path: "first.go", Status: "M", Diff: ""})
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
	tester := h.tester()
	h.selectPane(tester, "变更")
	// The read runs off the UI thread: wait for the call instead of asserting on
	// the frame right after the pane switch (CI runners can be slower than the
	// goroutine's Post).
	if !pumpFrames(tester, func() bool { return workspace.callCount() == 1 }, 3*time.Second) {
		t.Fatalf("Changes calls = %d, want 1 after opening the pane", workspace.callCount())
	}
	// Frames reuse the cache; only the native Refresh re-reads.
	tester.Frame()
	if calls := workspace.callCount(); calls != 1 {
		t.Fatalf("Changes calls = %d after an extra frame, want 1", calls)
	}

	workspace.setChanges(contracts.FileChange{Path: "second.go", Status: "A", Diff: ""})
	h.click(tester, "刷新")
	if !pumpFrames(tester, func() bool { return workspace.callCount() == 2 }, 3*time.Second) {
		t.Fatalf("Changes calls = %d after refresh, want 2", workspace.callCount())
	}
	h.assertTexts(tester, "second.go")
	h.assertNoTexts(tester, "first.go")
}

func TestChangesDiffTruncation(t *testing.T) {
	var builder strings.Builder
	for index := 1; index <= 450; index++ {
		fmt.Fprintf(&builder, "+line%d\n", index)
	}
	workspace := newDifferWorkspace(contracts.FileChange{Path: "big.go", Status: "M", Diff: builder.String()})
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{}, workspace)
	tester := h.tester()
	h.selectPane(tester, "变更")
	h.assertTexts(tester, "+450") // the stat counter still counts every line
	if err := tester.Click("查看 diff"); err != nil {
		t.Fatalf("expand diff: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "+line1", "+line400", "diff 已截断")
	h.assertNoTexts(tester, "+line401", "+line450")
}

func TestChangesCustomTabLabel(t *testing.T) {
	workspace := newDifferWorkspace()
	h := newHarnessWithWorkspace(t, newFakeChat(), rightpanel.Config{ChangesTab: "Changes"}, workspace)
	tester := h.tester()
	h.selectPane(tester, "Changes")
	// The compact pane picker names the pane that shows.
	h.assertTexts(tester, "Changes", "工作区没有未提交的变更")
}
