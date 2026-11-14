package rightpanel_test

// Frame tests for the keyboard path to a workspace file's context menu: with
// the workspace tree focused on a row, the menu key or Shift+F10 opens that
// row's menu (mygo docs/ui/context-menu.md: the menu key, or Shift+F10, opens
// the menu of the element with the focus, or the chosen row of a focused list).

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestWorkspaceFileRowMenuKeyOpensRowMenu: the file rows carry the 打开/复制路径
// menu that a right-click opens; the keyboard must reach the same menu when the
// tree's focused row is the file.
func TestWorkspaceFileRowMenuKeyOpensRowMenu(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("README.md", false), fileEntry("notes.txt", false))
	workspace.setFile("README.md", []byte("# 项目说明\n"))
	workspace.setFile("notes.txt", []byte("notes\n"))
	h := newHarness(t, harnessOptions{Workspace: workspace})
	tester := h.tester()
	h.openPage(tester, "文件")
	h.assertTexts(tester, "README.md", "notes.txt")

	focusedName := func() string {
		for _, info := range tester.AccessAll() {
			if info.Focused {
				return info.Name
			}
		}
		return ""
	}
	focusFileRow := func() {
		t.Helper()
		for i := 0; i < 6 && focusedName() != "README.md"; i++ {
			tester.Key(0, ui.KeyTab)
			tester.Frame()
		}
		if got := focusedName(); got != "README.md" {
			t.Fatalf("Tab did not reach the README.md tree row (focused %q); texts = %v", got, tester.Texts())
		}
	}

	assertRowMenu := func(key ui.Key, mods ui.Modifiers) {
		t.Helper()
		focusFileRow()
		tester.Key(mods, key)
		tester.Frame()
		menu := tester.Menu()
		if !slices.Contains(menu, "打开") || !slices.Contains(menu, "复制路径") {
			t.Fatalf("%v opened %q, want the file row's 打开/复制路径 menu; texts = %v",
				key, menu, tester.Texts())
		}
		tester.CloseMenu()
		tester.Frame()
	}

	assertRowMenu(ui.KeyContextMenu, 0)
	assertRowMenu(ui.KeyF10, ui.Shift)
}
