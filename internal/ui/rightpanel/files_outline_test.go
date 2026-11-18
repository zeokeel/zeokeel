package rightpanel_test

// Frame tests for UD-14 (2026-11 wave D): the workspace file tree migrates from
// ui.Tree + a 400-entry cap to the virtualized ui.Outline, so a large directory
// is fully reachable (the 401st entry and beyond) and the tree keeps native
// keyboard semantics (arrows move, Right opens a directory, Enter opens a file).
// Lazy directory loading through the existing syncDir cache is preserved.

import (
	"fmt"
	"sort"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestFilesTreeLargeDirectoryReachesBeyondCap boots a root with 450 files: the
// virtualized outline must reach the last entry (the old 400 cap hid it), and a
// click on it opens its tab.
func TestFilesTreeLargeDirectoryReachesBeyondCap(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	entries := make([]contracts.FileEntry, 0, 450)
	for index := 0; index < 450; index++ {
		entries = append(entries, fileEntry(fmt.Sprintf("file-%03d.md", index), false))
	}
	workspace.setDir("", entries...)
	workspace.setFile("file-449.md", []byte("# 最后一项\n"))
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	tester.SetSize(1100, 500)
	h.openPage(tester, "文件")
	tester.Frame()

	// The first entry is visible; the last is not yet (virtualized viewport).
	h.assertTexts(tester, "file-000.md")

	// Focus the virtualized outline (Tab reaches its 工作区文件 focus stop) and
	// jump to its last row: End chooses the last item and scrolls it into view
	// (no file is clicked, so the page stays).
	focusedName := func() string {
		for _, info := range tester.AccessAll() {
			if info.Focused {
				return info.Name
			}
		}
		return ""
	}
	for i := 0; i < 10 && focusedName() != "工作区文件"; i++ {
		tester.Key(0, ui.KeyTab)
		tester.Frame()
	}
	if focusedName() != "工作区文件" {
		t.Fatalf("Tab did not reach the workspace files outline (focused %q); texts = %v", focusedName(), tester.Texts())
	}
	tester.Key(0, ui.KeyEnd)
	tester.Frame()
	tester.Frame()
	if !tester.HasText("file-449.md") {
		t.Fatalf("UD-14: the 450-entry directory's last file is unreachable; texts = %v", tester.Texts())
	}
	// The previously-capped 401st entry is reachable too: scroll back up to it.
	reached400 := tester.HasText("file-400.md")
	for i := 0; i < 12 && !reached400; i++ {
		tester.Key(0, ui.KeyPageUp)
		tester.Frame()
		reached400 = tester.HasText("file-400.md")
	}
	if !reached400 {
		t.Fatalf("UD-14: entry 401 (file-400.md) is unreachable; texts = %v", tester.Texts())
	}
	// Return to the last entry and open it: a beyond-cap file opens its tab.
	tester.Key(0, ui.KeyEnd)
	tester.Frame()
	h.click(tester, "file-449.md")
	h.assertTexts(tester, "关闭 file-449.md", "最后一项")
}

// TestFilesTreeKeyboardSemantics covers the native outline keyboard: Right opens
// the chosen directory (lazily loading its children) and Enter opens a file.
func TestFilesTreeKeyboardSemantics(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("docs", true), fileEntry("README.md", false))
	workspace.setDir("docs", contracts.FileEntry{Name: "guide.md", Path: "docs/guide.md"})
	workspace.setFile("docs/guide.md", []byte("# 指南\n"))
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	tester.Frame()

	// Focus the first row (docs), then Right opens it.
	h.click(tester, "docs")
	tester.Frame()
	// The click already toggles it open; collapse via Left, then reopen via Right
	// to prove the keyboard path drives the disclosure.
	tester.Key(0, ui.KeyLeft)
	tester.Frame()
	tester.Key(0, ui.KeyRight)
	tester.Frame()
	tester.Frame()
	if !tester.HasText("guide.md") {
		t.Fatalf("UD-14: Right did not open the directory via the keyboard; texts = %v", tester.Texts())
	}
	// Move down to the child and Enter opens its tab.
	tester.Key(0, ui.KeyDown)
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	h.assertTexts(tester, "关闭 guide.md", "指南")
}

// --- A4-I-11: the tree's current row and the preview area --------------------

// focusedName returns the accessible name of whatever holds the keyboard
// focus, so a test can walk Tab stops to the tree without guessing.
func focusedName(tester *ui.Tester) string {
	for _, info := range tester.AccessAll() {
		if info.Focused {
			return info.Name
		}
	}
	return ""
}

// focusTree walks the Tab stops until the workspace outline has the keyboard.
func focusTree(t *testing.T, tester *ui.Tester) {
	t.Helper()
	for i := 0; i < 20 && focusedName(tester) != "工作区文件"; i++ {
		tester.Key(0, ui.KeyTab)
		tester.Frame()
	}
	if got := focusedName(tester); got != "工作区文件" {
		t.Fatalf("Tab did not reach the workspace tree (focused %q); texts = %v", got, tester.Texts())
	}
}

// treeRowSelected reports whether the tree row named row is the current item.
func treeRowSelected(tester *ui.Tester, row string) bool {
	for _, info := range tester.AccessAll() {
		if info.Role == ui.RoleTreeItem && info.Name == row {
			return info.Selected
		}
	}
	return false
}

// treeFixture is the standard tree: a dot-directory, two files with distinct
// bodies, and one subdirectory.
func treeFixture(t *testing.T) *fakeWorkspace {
	t.Helper()
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry(".git", true), fileEntry("main.go", false),
		fileEntry("README.md", false), fileEntry("notes.md", false))
	workspace.setDir(".git", contracts.FileEntry{Name: "config", Path: ".git/config"})
	workspace.setFile("main.go", []byte("package main\n\n// QA 预览标记 alpha\n"))
	workspace.setFile("README.md", []byte("# 自述\n\nQA 预览标记 beta\n"))
	// A Markdown file renders as text, so the preview area's CONTENT is
	// assertable; a .go file renders in the official code editor, whose content
	// is its own a11y value rather than frame text, so those rows only assert
	// the row/selection side.
	workspace.setFile("notes.md", []byte("# 笔记\n\nQA 预览标记 gamma\n"))
	return workspace
}

// treeRowOrder reads the tree's rows in display order (top to bottom) from the
// accessibility tree, so a test never hard-codes the sort the backend applies.
func treeRowOrder(tester *ui.Tester) []string {
	type row struct {
		name string
		y    float32
	}
	var rows []row
	for _, info := range tester.AccessAll() {
		if info.Role == ui.RoleTreeItem {
			rows = append(rows, row{info.Name, info.Bounds.Y})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].y < rows[j].y })
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.name)
	}
	return names
}

// TestFilesTreeKeyboardSelectShowsPreview covers A4-I-11: the arrows move the
// current row and the preview area follows in the same frame, without opening a
// tab; Home and End reach the first and last rows.
func TestFilesTreeKeyboardSelectShowsPreview(t *testing.T) {
	workspace := treeFixture(t)
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	tester.Frame()

	// A fresh panel has no current row: nothing is chosen and nothing previews.
	if treeRowSelected(tester, "main.go") || treeRowSelected(tester, "README.md") {
		t.Fatalf("the tree started with a chosen row; texts = %v", tester.Texts())
	}

	focusTree(t, tester)
	// Walk every row from the top, in the order the tree really shows them (the
	// backend sorts, so the test reads the order instead of assuming it).
	order := treeRowOrder(tester)
	if len(order) < 3 {
		t.Fatalf("the tree shows %d rows, want at least 3: %v", len(order), order)
	}
	markers := map[string]string{
		".git":      "目录",
		"README.md": "QA 预览标记 beta",
		"notes.md":  "QA 预览标记 gamma",
	}
	for step, want := range order {
		tester.Key(0, ui.KeyDown)
		tester.Frame()
		tester.Frame()
		if !treeRowSelected(tester, want) {
			t.Fatalf("step %d: the current row is not %q; texts = %v", step, want, tester.Texts())
		}
		marker, known := markers[want]
		if !known {
			continue
		}
		if !tester.HasText(marker) {
			t.Fatalf("step %d: the preview did not follow %q (want %q); texts = %v",
				step, want, marker, tester.Texts())
		}
	}
	// Browsing is not opening: no document tab appeared for any of them.
	h.assertNoTexts(tester, "关闭 main.go", "关闭 README.md")

	// End reaches the last row; Home returns to the first.
	tester.Key(0, ui.KeyEnd)
	tester.Frame()
	tester.Frame()
	if !treeRowSelected(tester, order[len(order)-1]) {
		t.Fatalf("End did not choose the last row %q; texts = %v", order[len(order)-1], tester.Texts())
	}
	tester.Key(0, ui.KeyHome)
	tester.Frame()
	tester.Frame()
	if !treeRowSelected(tester, order[0]) {
		t.Fatalf("Home did not choose the first row %q; texts = %v", order[0], tester.Texts())
	}
}

// TestFilesTreeEnterOpensCurrent covers the keyboard open: Enter on the current
// row opens that file as a document tab.
func TestFilesTreeEnterOpensCurrent(t *testing.T) {
	workspace := treeFixture(t)
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	tester.Frame()

	focusTree(t, tester)
	// Step to main.go wherever it sits in the sort order.
	var reached bool
	for i := 0; i < 6 && !reached; i++ {
		tester.Key(0, ui.KeyDown)
		tester.Frame()
		tester.Frame()
		reached = treeRowSelected(tester, "main.go")
	}
	if !reached {
		t.Fatalf("the cursor never reached main.go; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	tester.Frame()
	if _, ok := tester.AccessByRole(ui.RoleTab, "main.go"); !ok {
		t.Fatalf("Enter did not open main.go as a document tab; texts = %v", tester.Texts())
	}
	// The editor renders the .go source itself; the preview area still names the
	// file it is showing.
	h.assertTexts(tester, "main.go")
}

// TestFilesTreeSelectFollowsPointer covers the pointer path: a click opens the
// file AND makes its row current, so coming back to the tree continues from it.
func TestFilesTreeSelectFollowsPointer(t *testing.T) {
	workspace := treeFixture(t)
	h := workspaceHarness(t, workspace)
	tester := h.tester()
	h.openPage(tester, "文件")
	tester.Frame()

	h.click(tester, "README.md")
	h.assertTexts(tester, "QA 预览标记 beta")
	// Back to the file tree: the row the pointer opened is still the current one.
	h.click(tester, "文件")
	tester.Frame()
	tester.Frame()
	if !treeRowSelected(tester, "README.md") {
		t.Fatalf("the opened row is not the current row after returning; texts = %v", tester.Texts())
	}
}
