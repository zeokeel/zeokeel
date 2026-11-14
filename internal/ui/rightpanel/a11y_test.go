package rightpanel_test

// Frame tests for the right sidebar's v0.9.0 accessibility semantics: the page
// under the tab strip is the chosen tab's tab panel (mygo RoleTabPanel, as
// ARIA's Tabs pattern asks), named after the tab and pointed to by the tab's
// Controls.

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestTabPanelIsNamedAndLinkedToItsTab: switching tabs renames the panel and
// moves the tab's Controls reference with it, for the fixed view pages and for
// an opened file's document tab.
func TestTabPanelIsNamedAndLinkedToItsTab(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("README.md", false))
	workspace.setFile("README.md", []byte("# 项目说明\n"))
	h := newHarness(t, harnessOptions{Workspace: workspace})
	tester := h.tester()
	tester.Frame()

	assertPanel := func(tabName string) {
		t.Helper()
		tab, ok := tester.AccessByRole(ui.RoleTab, tabName)
		if !ok {
			t.Fatalf("no tab named %q; texts = %v", tabName, tester.Texts())
		}
		if _, ok := tester.AccessByRole(ui.RoleTabPanel, tabName); !ok {
			t.Fatalf("no tab panel named %q (the content under the strip); texts = %v", tabName, tester.Texts())
		}
		if len(tab.Controls) != 1 || tab.Controls[0] != tabName {
			t.Fatalf("tab %q Controls = %v, want the tab panel named %q", tabName, tab.Controls, tabName)
		}
	}

	assertPanel("开始")
	h.click(tester, "文件")
	assertPanel("文件")

	// Opening a file gives it a document tab; the panel follows that tab.
	h.click(tester, "README.md")
	tester.Frame()
	assertPanel("README.md")
}
