package rightpanel_test

// Frame tests for UD-14 (2026-11 wave D): the workspace file tree migrates from
// ui.Tree + a 400-entry cap to the virtualized ui.Outline, so a large directory
// is fully reachable (the 401st entry and beyond) and the tree keeps native
// keyboard semantics (arrows move, Right opens a directory, Enter opens a file).
// Lazy directory loading through the existing syncDir cache is preserved.

import (
	"fmt"
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
