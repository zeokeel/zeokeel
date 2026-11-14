package rightpanel_test

// Frame tests for the right sidebar's 开始 guide (the no-page-open landing):
// two equal side-by-side cards, their shortcuts, no explanatory copy, and a
// scrollable body.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestGuideIsTheDefaultPage: with no page opened the sidebar shows the two
// onboarding cards and their shortcuts.
func TestGuideIsTheDefaultPage(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tester := h.tester()
	h.assertTexts(tester, "开始", "文件", "命令", "工作区文件", "命令运行", "Ctrl+Alt+P", "Ctrl+`")
}

// TestGuideHasNoDescriptionCopy: the user-approved guide is title + shortcut
// only. The explanatory paragraphs of the mockup are gone.
func TestGuideHasNoDescriptionCopy(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tester := h.tester()
	h.assertTexts(tester, "工作区文件", "命令运行")
	h.assertNoTexts(tester,
		"浏览会话工作区的文件", "在会话工作区运行命令",
		"只保留两个按需页面", "点开才会出现，两个可以并排查看",
		"其它能力不再放进右侧栏",
		"Browse the files of the session workspace",
		"Run commands in the session workspace")
}

// TestGuideCardsAreEqualSideBySide pins the approved layout: two equal cards
// on one row.
func TestGuideCardsAreEqualSideBySide(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tester := h.tester()
	tester.Frame()
	first, ok := tester.AccessByRole(ui.RoleButton, "工作区文件")
	if !ok {
		t.Fatalf("工作区文件 is not an accessible button; texts = %v", tester.Texts())
	}
	second, ok := tester.AccessByRole(ui.RoleButton, "命令运行")
	if !ok {
		t.Fatalf("命令运行 is not an accessible button; texts = %v", tester.Texts())
	}
	if first.Bounds.W <= 0 || second.Bounds.W <= 0 {
		t.Fatalf("card widths = %v / %v, want visible cards", first.Bounds, second.Bounds)
	}
	if first.Bounds.W != second.Bounds.W {
		t.Fatalf("guide card widths differ: %v vs %v, want equal side-by-side cards",
			first.Bounds, second.Bounds)
	}
	if first.Bounds.Y != second.Bounds.Y {
		t.Fatalf("guide cards are not on one row: %v vs %v", first.Bounds, second.Bounds)
	}
	if first.Bounds.X >= second.Bounds.X {
		t.Fatalf("guide cards are not side by side: %v then %v", first.Bounds, second.Bounds)
	}
}

// TestGuideCardsOpenTheirPages: the cards are the guide's entry points.
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
	h.assertTexts(tester, "工作区文件", "命令运行")
	h.click(tester, "命令运行")
	h.assertTexts(tester, "命令未启用")
}

// TestGuideBodyScrolls: the guide body is a native scroll, so a short window
// keeps both cards reachable instead of clipping the content.
func TestGuideBodyScrolls(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tester := h.tester()
	tester.SetSize(1100, 320)
	tester.Frame()
	h.assertTexts(tester, "工作区文件", "命令运行")
	tester.SetSize(1200, 800)
	tester.Frame()
	h.assertTexts(tester, "工作区文件", "命令运行")
}

// TestShortcutLabelsMatchCommands keeps the guide copy and the palette command
// shortcuts in sync (the guide is the shortcut's discoverability surface).
func TestShortcutLabelsMatchCommands(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tester := h.tester()
	tester.Frame()
	texts := strings.Join(tester.Texts(), "\n")
	files := h.command(tester, "rightpanel.files.open")
	terminal := h.command(tester, "rightpanel.terminal.open")
	if !strings.Contains(texts, files.Shortcut) {
		t.Fatalf("guide does not show the files shortcut %q; texts = %v", files.Shortcut, tester.Texts())
	}
	if !strings.Contains(texts, terminal.Shortcut) {
		t.Fatalf("guide does not show the terminal shortcut %q; texts = %v", terminal.Shortcut, tester.Texts())
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
