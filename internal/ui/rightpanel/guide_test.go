package rightpanel_test

// Frame tests for the right sidebar's 开始 guide (the no-page-open landing):
// the single workspace-files card, its shortcut, no explanatory copy, and a
// scrollable body. The sidebar used to offer a second "命令运行" card; the
// command page is gone, so the guide has exactly one entry point.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestGuideIsTheDefaultPage: with no page opened the sidebar shows the
// workspace-files card and its shortcut.
func TestGuideIsTheDefaultPage(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tester := h.tester()
	h.assertTexts(tester, "开始", "文件", "工作区文件", "Ctrl+Alt+P")
	h.assertNoTexts(tester, "命令", "命令运行", "Ctrl+`")
}

// TestGuideHasNoDescriptionCopy: the user-approved guide is title + shortcut
// only. The explanatory paragraphs of the mockup are gone.
func TestGuideHasNoDescriptionCopy(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tester := h.tester()
	h.assertTexts(tester, "工作区文件")
	h.assertNoTexts(tester,
		"浏览会话工作区的文件", "在会话工作区运行命令",
		"只保留两个按需页面", "点开才会出现，两个可以并排查看",
		"其它能力不再放进右侧栏",
		"Browse the files of the session workspace",
		"Run commands in the session workspace")
}

// TestGuideHasOneWorkspaceFilesCard pins the new layout: a single accessible
// card, not a two-up row with an empty second slot.
func TestGuideHasOneWorkspaceFilesCard(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tester := h.tester()
	tester.Frame()
	card, ok := tester.AccessByRole(ui.RoleButton, "工作区文件")
	if !ok {
		t.Fatalf("工作区文件 is not an accessible button; texts = %v", tester.Texts())
	}
	if card.Bounds.W <= 0 || card.Bounds.H <= 0 {
		t.Fatalf("workspace-files card has no visible box: %+v", card.Bounds)
	}
	if _, ok := tester.AccessByRole(ui.RoleButton, "命令运行"); ok {
		t.Fatalf("the removed command card is still an accessible button; texts = %v", tester.Texts())
	}
}

// TestGuideCardsOpenTheirPages: the card is the guide's entry point.
func TestGuideCardsOpenTheirPages(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("README.md", false))
	workspace.setFile("README.md", []byte("# 项目\n"))
	h := newHarness(t, harnessOptions{Workspace: workspace})
	tester := h.tester()

	h.click(tester, "工作区文件")
	h.assertTexts(tester, "工作区", "README.md")

	h.click(tester, "开始")
	h.assertTexts(tester, "工作区文件")
	h.assertNoTexts(tester, "命令运行")
}

// TestGuideBodyScrolls: the guide body is a native scroll, so a short window
// keeps the card reachable instead of clipping the content.
func TestGuideBodyScrolls(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tester := h.tester()
	tester.SetSize(1100, 320)
	tester.Frame()
	h.assertTexts(tester, "工作区文件")
	tester.SetSize(1200, 800)
	tester.Frame()
	h.assertTexts(tester, "工作区文件")
}

// TestShortcutLabelsMatchCommands keeps the guide copy and the palette command
// shortcuts in sync (the guide is the shortcut's discoverability surface).
func TestShortcutLabelsMatchCommands(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tester := h.tester()
	tester.Frame()
	texts := strings.Join(tester.Texts(), "\n")
	files := h.command(tester, "rightpanel.files.open")
	if !strings.Contains(texts, files.Shortcut) {
		t.Fatalf("guide does not show the files shortcut %q; texts = %v", files.Shortcut, tester.Texts())
	}
}

// TestGuideHasNoDuplicatePaneHeading pins B7/P3-8: the pane's name lives on its
// view tab only — the body no longer repeats it as an H2. The tab panel carries
// the same name as its tab for assistive technology (mygo RoleTabPanel, as
// ARIA's Tabs pattern asks), so one non-panel name is allowed.
func TestGuideHasNoDuplicatePaneHeading(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tester := h.tester()
	tester.Frame()
	count := 0
	for _, text := range tester.Texts() {
		if text == "开始" {
			count++
		}
	}
	if _, ok := tester.AccessByRole(ui.RoleTabPanel, "开始"); ok {
		// The tab panel's accessible name is not a painted heading.
		count--
	}
	if count != 1 {
		t.Fatalf("guide pane shows its name %d times, want one (the tab); texts = %v", count, tester.Texts())
	}
}
