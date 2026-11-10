package rightpanel_test

// Frame tests for the right sidebar tab strip: the fixed pages (开始/文件/
// 终端), one tab per opened file, switching, closing and overflow, plus the
// removal of the old 8-pane inspector and its cycle commands.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestOldInspectorPanesAreGone: the 8-pane inspector (overview/tools/log/
// changes/team/tasks) is deleted, including its selector and cycle commands.
func TestOldInspectorPanesAreGone(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	h := newHarness(t, harnessOptions{Workspace: workspace})
	tester := h.tester()
	h.assertTexts(tester, "开始", "文件", "终端")
	h.assertNoTexts(tester, "概览", "工具", "日志", "变更", "团队", "任务", "切换面板", "检查器")

	ids := make(map[string]bool)
	for _, command := range h.uiRoot.Runtime().Commands() {
		ids[command.ID] = true
	}
	for _, removed := range []string{"rightpanel.pane.prev", "rightpanel.pane.next",
		"rightpanel.team.open", "rightpanel.tasks.open"} {
		if ids[removed] {
			t.Fatalf("removed pane command %q is still registered", removed)
		}
	}
}

// TestFileTabOpensFromWorkspaceTree: clicking a file in the Files page opens
// its own preview tab and keeps the fixed pages.
func TestFileTabOpensFromWorkspaceTree(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("README.md", false))
	workspace.setFile("README.md", []byte("# 项目说明\n"))
	h := newHarness(t, harnessOptions{Workspace: workspace})
	tester := h.tester()

	h.openPage(tester, "文件")
	h.assertTexts(tester, "README.md")
	h.click(tester, "README.md")
	h.assertTexts(tester, "关闭 README.md", "项目说明")
	h.assertTexts(tester, "开始", "文件", "终端")
}

// TestFileTabSwitchAndClose: each opened file is one tab; switching back shows
// the same file, closing removes the tab without touching the fixed pages.
func TestFileTabSwitchAndClose(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("a.md", false), fileEntry("b.md", false))
	workspace.setFile("a.md", []byte("# 甲文件\n"))
	workspace.setFile("b.md", []byte("# 乙文件\n"))
	h := newHarness(t, harnessOptions{Workspace: workspace})
	tester := h.tester()

	h.openPage(tester, "文件")
	h.click(tester, "a.md")
	h.assertTexts(tester, "甲文件", "关闭 a.md")
	h.openPage(tester, "文件")
	h.click(tester, "b.md")
	h.assertTexts(tester, "乙文件", "关闭 a.md", "关闭 b.md")

	// Switching back to the first tab keeps its content.
	h.click(tester, "a.md")
	h.assertTexts(tester, "甲文件")

	// Closing the active tab falls back to the previous tab and frees the row.
	h.closeFileTab(tester, "a.md")
	h.assertNoTexts(tester, "关闭 a.md")
	h.assertTexts(tester, "乙文件")
	h.closeFileTab(tester, "b.md")
	h.assertNoTexts(tester, "关闭 b.md")
	h.assertTexts(tester, "开始", "文件", "终端")
}

// TestFileTabsFocusExistingTab: opening an already open file focuses its tab
// instead of adding a duplicate.
func TestFileTabsFocusExistingTab(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("a.md", false), fileEntry("b.md", false))
	workspace.setFile("a.md", []byte("# 甲\n"))
	workspace.setFile("b.md", []byte("# 乙\n"))
	h := newHarness(t, harnessOptions{Workspace: workspace})
	tester := h.tester()

	h.openPage(tester, "文件")
	h.click(tester, "a.md")
	h.openPage(tester, "文件")
	h.click(tester, "b.md")
	h.click(tester, "a.md")
	if count := countOf(tester.Texts(), "关闭 a.md"); count != 1 {
		t.Fatalf("close label for a.md appears %d times, want one tab; texts = %v", count, tester.Texts())
	}
	h.assertTexts(tester, "甲", "关闭 b.md")
}

// TestFileTabsAreClosableButPagesAreNot: only the opened-file tabs carry a
// close control; the fixed pages stay.
func TestFileTabsAreClosableButPagesAreNot(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("a.md", false))
	workspace.setFile("a.md", []byte("# 甲\n"))
	h := newHarness(t, harnessOptions{Workspace: workspace})
	tester := h.tester()

	h.openPage(tester, "文件")
	h.click(tester, "a.md")
	tester.Frame()
	for _, fixed := range []string{"关闭 开始", "关闭 文件", "关闭 终端"} {
		if tester.HasText(fixed) {
			t.Fatalf("fixed page has a close control (%q); texts = %v", fixed, tester.Texts())
		}
	}
	if !tester.HasText("关闭 a.md") {
		t.Fatalf("file tab has no close control; texts = %v", tester.Texts())
	}
}

// TestTabStripOverflowsSafely: a long strip keeps every tab reachable in its
// horizontal scroll instead of overflowing the narrow sidebar.
func TestTabStripOverflowsSafely(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	entries := make([]contracts.FileEntry, 0, 14)
	for index := 0; index < 14; index++ {
		name := fmt.Sprintf("file-%02d.md", index)
		entries = append(entries, fileEntry(name, false))
		workspace.setFile(name, []byte("# "+name+"\n"))
	}
	workspace.setDir("", entries...)
	h := newHarness(t, harnessOptions{Workspace: workspace})
	tester := h.tester()

	for _, entry := range entries {
		h.openPage(tester, "文件")
		h.click(tester, entry.Name)
	}
	for _, entry := range entries {
		if !tester.HasText("关闭 " + entry.Name) {
			t.Fatalf("tab %q missing after opening 14 files; texts = %v", entry.Name, tester.Texts())
		}
	}
}

// TestPageCommands: the file/terminal pages have stable command ids the host
// palette and shortcuts can target; they open the panel and focus the page.
func TestPageCommands(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("README.md", false))
	h := newHarness(t, harnessOptions{Workspace: workspace})
	tester := h.tester()

	files := h.command(tester, "rightpanel.files.open")
	if files.Shortcut != "Ctrl+Alt+P" {
		t.Fatalf("files command shortcut = %q, want Ctrl+Alt+P", files.Shortcut)
	}
	if !strings.Contains(files.Hint, "文件") || !strings.Contains(files.Title, "文件") {
		t.Fatalf("files command = %+v, want 文件 in title/hint", files)
	}
	terminal := h.command(tester, "rightpanel.terminal.open")
	if terminal.Shortcut != "Ctrl+`" {
		t.Fatalf("terminal command shortcut = %q, want Ctrl+`", terminal.Shortcut)
	}
	if !strings.Contains(terminal.Hint, "终端") || !strings.Contains(terminal.Title, "终端") {
		t.Fatalf("terminal command = %+v, want 终端 in title/hint", terminal)
	}
	if files.Run == nil || terminal.Run == nil {
		t.Fatal("page commands have no Run")
	}

	if err := files.Run(); err != nil {
		t.Fatalf("run files command: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "工作区", "README.md")
	if !h.uiRoot.RightPanelOpen() {
		t.Fatal("running the files command did not open the host panel")
	}

	if err := terminal.Run(); err != nil {
		t.Fatalf("run terminal command: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "终端未启用")
}

// TestTabStripKeyboard: the native tab list keeps the arrows/Home/End
// semantics (the official TabsBase owns them).
func TestTabStripKeyboard(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tester := h.tester()
	tester.Frame()
	if err := tester.Click("文件"); err != nil {
		t.Fatalf("click files tab: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "工作区未启用")
	tester.Key(0, ui.KeyLeft)
	tester.Frame()
	h.assertTexts(tester, "工作区文件", "新建终端")
}
