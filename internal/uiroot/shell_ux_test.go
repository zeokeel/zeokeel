package uiroot

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestPaletteKeyboard covers the command palette's keyboard path: Up/Down move
// the choice while the search field keeps the keyboard, and Enter runs it.
func TestPaletteKeyboard(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	var runs [2]int
	mustCommand(t, r, contracts.CommandSpec{ID: "a", Title: "命令A", Order: 10,
		Run: func() error { runs[0]++; return nil }})
	mustCommand(t, r, contracts.CommandSpec{ID: "b", Title: "命令B", Order: 20,
		Run: func() error { runs[1]++; return nil }})

	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	for _, want := range []string{"命令面板", "命令A", "命令B"} {
		requireHasText(t, tt, want)
	}

	// Down moves the choice from the first command to the second, Enter runs
	// the second one.
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	requireNoText(t, tt, "命令面板")
	if runs[1] != 1 || runs[0] != 0 {
		t.Fatalf("runs after Down+Enter = %v, want command B once", runs)
	}

	// Escape closes the palette without running anything more.
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	requireNoText(t, tt, "命令面板")
	if runs[0]+runs[1] != 1 {
		t.Fatalf("runs after Escape = %v, want unchanged", runs)
	}
}

// TestSettingsScrollReachesBottomAction covers the audit's P0: a long settings
// page scrolls inside the dialog, so its last action is reachable instead of
// painting outside the panel.
func TestSettingsScrollReachesBottomAction(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustPage(t, r, contracts.SettingsPageSpec{
		ID: "tall", Section: "general", Title: "长页", Order: 10,
		Build: func(c *ui.Context) {
			ui.Column(c).FillWidth().Gap(12).Children(func() {
				for i := 0; i < 40; i++ {
					ui.Text(c, "filler-line").Height(20)
				}
				ui.Button(c, "底部操作").Label("底部操作")
			})
		},
	})

	tt := newTestTester(t, r, 1280, 800)
	tt.Frame()
	r.Navigator().ToggleSettings()
	tt.Frame()
	requireHasText(t, tt, "长页")
	requireHasText(t, tt, "关闭设置")

	// The tall page body scrolls: wheel over the body and the bottom action
	// becomes clickable.
	tt.Scroll(700, 500, 0, 4000)
	tt.Frame()
	rect, ok := tt.Find("底部操作")
	if !ok {
		t.Fatalf("bottom action not built")
	}
	// The modal panel is at most the window minus the theme's Space(10)
	// margin, centered: its inner content must end above that panel's bottom
	// edge, not only inside the window (the audit's P0 was a button painted on
	// the backdrop below the panel).
	panelBottom := float32(800+800-40) / 2
	if rect.Y+rect.H > panelBottom {
		t.Fatalf("bottom action bottom=%v is past the settings panel bottom=%v", rect.Y+rect.H, panelBottom)
	}
}

// TestSettingsCloseButtonAndAdaptiveSize covers the settings dialog close
// control and the window-adaptive surface size on a small window.
func TestSettingsCloseButtonAndAdaptiveSize(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustPage(t, r, contracts.SettingsPageSpec{
		ID: "about", Section: "general", Title: "关于", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "about-body") },
	})

	for _, size := range [][2]int{{1280, 800}, {1024, 720}, {900, 600}} {
		tt := newTestTester(t, r, size[0], size[1])
		tt.Frame()
		r.Navigator().ToggleSettings()
		tt.Frame()
		rect, ok := tt.Find("关闭设置")
		if !ok {
			t.Fatalf("%dx%d: settings close control missing", size[0], size[1])
		}
		if rect.X < 0 || rect.Y < 0 || rect.X+rect.W > float32(size[0]) || rect.Y+rect.H > float32(size[1]) {
			t.Fatalf("%dx%d: settings close control outside the window: %+v", size[0], size[1], rect)
		}
		if err := tt.Click("关闭设置"); err != nil {
			t.Fatalf("%dx%d: click close: %v", size[0], size[1], err)
		}
		tt.Frame()
		if r.Navigator().SettingsOpen() {
			t.Fatalf("%dx%d: settings stayed open after the close control", size[0], size[1])
		}
	}
}

// TestRightPanelDrawerOnNarrowWindow covers the responsive inspector: docked on
// a wide window, a drawer over the content on a narrow one.
func TestRightPanelDrawerOnNarrowWindow(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("session", "会话", "body-session", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotSessionPanelRight, "inspector", "inspector-body", 10))

	wide := newTestTester(t, r, 1280, 800)
	wide.Frame()
	r.SetRightPanelOpen(true)
	wide.Frame()
	requireHasText(t, wide, "inspector-body")
	requireNoText(t, wide, "检查器") // docked: the pane contributes its own header

	narrow := newTestTester(t, r, 1024, 720)
	narrow.Frame()
	requireHasText(t, narrow, "inspector-body")
	requireHasText(t, narrow, "检查器") // the drawer names itself
}

// TestSettingsNavGroups covers the grouped settings navigation: sections are
// rendered as headers in rank order, and the first business page is selected.
func TestSettingsNavGroups(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustPage(t, r, contracts.SettingsPageSpec{ID: "about", Section: "关于", Title: "关于页", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "about-body") }})
	mustPage(t, r, contracts.SettingsPageSpec{ID: "behavior", Section: "general", Title: "行为页", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "behavior-body") }})
	mustPage(t, r, contracts.SettingsPageSpec{ID: "appearance", Section: "外观", Title: "外观页", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "appearance-body") }})
	mustPage(t, r, contracts.SettingsPageSpec{ID: "memory", Section: "高级", Title: "记忆页", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "memory-body") }})

	tt := newTestTester(t, r, 1280, 800)
	tt.Frame()
	r.Navigator().ToggleSettings()
	tt.Frame()
	// general before 外观 before 高级 before 关于; the 高级 group is collapsed
	// by default (settings-complexity simplification), so its pages are hidden
	// behind the group header.
	requireTextBefore(t, tt, "通用", "行为页", "外观与技能", "外观页", "高级", "关于", "关于页")
	requireNoText(t, tt, "记忆页")
	// The first business page is selected, not the About page.
	requireHasText(t, tt, "behavior-body")
	requireNoText(t, tt, "about-body")

	// Clicking the 高级 header unfolds the low-frequency pages.
	if err := tt.Click("高级"); err != nil {
		t.Fatalf("expand 高级: %v", err)
	}
	tt.Frame()
	requireHasText(t, tt, "记忆页")
	requireTextBefore(t, tt, "高级", "记忆页", "关于", "关于页")

	// A deep link to a page at the bottom of the grouped navigation shows that
	// page and scrolls its nav entry into view instead of leaving it off-screen.
	if err := r.OpenSettingsPage("about"); err != nil {
		t.Fatalf("OpenSettingsPage(about): %v", err)
	}
	tt.Frame()
	requireHasText(t, tt, "about-body")
	requireNoText(t, tt, "behavior-body")
	if rect, ok := tt.Find("关于页"); !ok {
		t.Fatalf("about page nav item missing")
	} else if rect.Y < 0 || rect.Y+rect.H > float32(800+800-40)/2 {
		t.Fatalf("about page nav item not scrolled inside the settings panel: %+v", rect)
	}
	// A deep link into the collapsed advanced group opens it, so the target
	// page's navigation entry is visible.
	if err := r.OpenSettingsPage("memory"); err != nil {
		t.Fatalf("OpenSettingsPage(memory): %v", err)
	}
	tt.Frame()
	requireHasText(t, tt, "memory-body")
	if _, ok := tt.Find("记忆页"); !ok {
		t.Fatalf("advanced deep link did not expand the 高级 navigation group")
	}
	// Choosing a daily page folds the 高级 group again: the navigation mirrors
	// the page on screen instead of keeping the previous visit's expanded layout.
	if err := tt.Click("行为页"); err != nil {
		t.Fatalf("click 行为页: %v", err)
	}
	tt.Frame()
	requireHasText(t, tt, "behavior-body")
	requireNoText(t, tt, "记忆页")

	if err := r.OpenSettingsPage("missing-page"); err == nil {
		t.Fatalf("OpenSettingsPage(missing-page) succeeded, want an error")
	}
}
