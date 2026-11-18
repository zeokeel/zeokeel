package uiroot

// Frame tests for the 2026-11-17 UI review's shell fixes: the collapsed rail
// reads the shared uikit single navigation table (P2 icon drift), the rail's
// conversation entry marks the session screen as its current destination (P3),
// the palette keeps one fixed shortcut column in the product's single chord
// notation (P3), the settings navigation keeps the dark surface ramp below the
// modal panel (P2), and the host toast anchor yields to transient surfaces and
// otherwise hangs below the header band at the main column's right edge
// (P2/P1, contracts.ToastAnchorService).

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// TestRailReadsTheSingleScreenNavTable pins the rail's glyph and label mapping
// to the shared uikit table: every registered screen id resolves to the same
// glyph the expanded sidebar rows draw, and "insights" keeps the
// Activity pulse instead of collapsing onto the neutral cube (the drift the
// review found in the rail's private switch).
func TestRailReadsTheSingleScreenNavTable(t *testing.T) {
	r := newTestRoot(t)
	for _, id := range []string{"home", "session", "automation", "insights", "plugins", "settings", "custom-screen"} {
		if got, want := r.screenIcon(id), uikit.ScreenNavIcon(id); got != want {
			t.Errorf("screenIcon(%q) = %p, want the shared table's glyph %p", id, got, want)
		}
	}
	if r.screenIcon("insights") != icons.Activity() {
		t.Error("insights rail glyph is not icons.Activity()")
	}
	if r.screenIcon("insights") == icons.Data() {
		t.Error("insights rail glyph collapsed onto the neutral cube (icons.Data)")
	}
	// Known ids take the table's localized label; an unregistered screen keeps
	// its module-registered title, as before the delegation.
	for _, id := range []string{"home", "session", "automation", "insights", "plugins", "settings"} {
		if got, want := r.screenLabel(contracts.ScreenSpec{ID: id, Title: "module-" + id}), uikit.ScreenNavLabel(r.locale, id); got != want {
			t.Errorf("screenLabel(%q) = %q, want the table label %q", id, got, want)
		}
	}
	if got := r.screenLabel(contracts.ScreenSpec{ID: "custom-screen", Title: "自定义屏幕"}); got != "自定义屏幕" {
		t.Errorf("unknown screen label = %q, want the registered title", got)
	}
}

// TestRailDrawsInsightsDestination renders both chrome forms with an insights
// screen registered: the rail and the expanded navigation both offer the
// destination under the table's localized label (the glyph agreement itself is
// pinned by TestRailReadsTheSingleScreenNavTable).
func TestRailDrawsInsightsDestination(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("session", "对话", "body-session", 10))
	// The expanded navigation rows are the sidebar module's slot contribution;
	// the module builds them from the same exported table (ScreenNavIcon/
	// ScreenNavLabel), so the stub rows read exactly what the module reads.
	screens := []struct{ id, title string }{
		{"automation", "自动化"},
		{"insights", "洞察"},
		{"plugins", "能力"},
	}
	for index, screen := range screens {
		screen := screen
		mustScreen(t, r, textScreen(screen.id, screen.title, "body-"+screen.id, 20+index*10))
		mustSlot(t, r, contracts.SlotItem{
			Slot: contracts.SlotSidebarNav, ID: screen.id + ".nav", Order: 20 + index*10,
			Build: func(c *ui.Context) {
				ui.Icon(c, uikit.ScreenNavIcon(screen.id)).Size(design.IconSizeS, design.IconSizeS)
				ui.Text(c, uikit.ScreenNavLabel(r.locale, screen.id))
			},
		})
	}
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()

	// Every registered module screen appears in both chrome forms under the
	// table's one localized label (the glyph agreement itself is pinned by
	// TestRailReadsTheSingleScreenNavTable).
	for _, screen := range screens {
		label := uikit.ScreenNavLabel(r.locale, screen.id)
		requireHasText(t, tt, label) // expanded navigation row
	}
	r.SetSidebarCollapsed(true)
	tt.Frame()
	for _, screen := range screens {
		label := uikit.ScreenNavLabel(r.locale, screen.id)
		if _, ok := tt.AccessByRole(ui.RoleToggleButton, label); !ok {
			t.Fatalf("collapsed rail has no %q destination; texts = %v", label, tt.Texts())
		}
	}
}

// TestRailSessionsEntryMarksCurrentScreen covers the rail's missing location
// state: on the session screen the conversation entry reads as selected even
// with its popover closed (the session screen has no rail row of its own), and
// the click stays a popover toggle.
func TestRailSessionsEntryMarksCurrentScreen(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("session", "对话", "body-session", 10))
	mustScreen(t, r, textScreen("automation", "自动化", "body-automation", 20))
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	r.SetSidebarCollapsed(true)
	tt.Frame()

	sessionsSelected := func() bool {
		info, ok := tt.AccessByRole(ui.RoleToggleButton, "会话")
		if !ok {
			t.Fatalf("rail conversation entry is missing; texts = %v", tt.Texts())
		}
		return info.Selected || info.Checked
	}

	if err := r.Navigator().Push("session"); err != nil {
		t.Fatalf("push session: %v", err)
	}
	tt.Frame()
	if !sessionsSelected() {
		t.Fatal("rail conversation entry is not selected while the session screen is current")
	}

	// Another screen drops the destination mark again (popover still closed).
	if err := r.Navigator().Push("automation"); err != nil {
		t.Fatalf("push automation: %v", err)
	}
	tt.Frame()
	if sessionsSelected() {
		t.Fatal("rail conversation entry stays selected on a non-session screen")
	}

	// The click toggles the popover, and the open popover reads as selected on
	// any screen; closing it on the session screen keeps the destination mark.
	if err := tt.Click("会话"); err != nil {
		t.Fatalf("open rail sessions popover: %v", err)
	}
	tt.Frame()
	if !sessionsSelected() {
		t.Fatal("open rail sessions popover does not read as selected")
	}
	if err := tt.Click("会话"); err != nil {
		t.Fatalf("close rail sessions popover: %v", err)
	}
	tt.Frame()
	if sessionsSelected() {
		t.Fatal("closed popover on a non-session screen still reads as selected")
	}
	if err := r.Navigator().Push("session"); err != nil {
		t.Fatalf("push session: %v", err)
	}
	tt.Frame()
	if !sessionsSelected() {
		t.Fatal("closed popover on the session screen lost the destination mark")
	}
}

// TestPaletteShortcutColumnIsFixed covers the palette's drifting shortcut
// start: the kbd column begins at one x for every chord length, and a row
// without a chord reserves the same column (its hint ends where the chord
// row's hint ends). The chord itself is the product formatter's output.
func TestPaletteShortcutColumnIsFixed(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustCommand(t, r, contracts.CommandSpec{ID: "demo.short", Title: "有键命令", Hint: "短提示",
		Order: 10, Shortcut: "Cmd/Ctrl+N", Run: func() error { return nil }})
	mustCommand(t, r, contracts.CommandSpec{ID: "demo.none", Title: "无键命令", Hint: "这是一个比较长的提示文本",
		Order: 20, Run: func() error { return nil }})
	mustCommand(t, r, contracts.CommandSpec{ID: "demo.long", Title: "长键命令",
		Order: 30, Shortcut: "Cmd/Ctrl + Alt + Shift + P", Run: func() error { return nil }})
	tt := newTestTester(t, r, 1280, 800)
	tt.Frame()
	r.togglePalette()
	tt.Frame()

	shortChord := uikit.FormatShortcut("Cmd/Ctrl+N")
	longChord := uikit.FormatShortcut("Cmd/Ctrl + Alt + Shift + P")
	requireHasText(t, tt, shortChord)
	requireHasText(t, tt, longChord)
	short, ok := tt.Find(shortChord)
	if !ok {
		t.Fatalf("short chord %q missing; texts = %v", shortChord, tt.Texts())
	}
	long, ok := tt.Find(longChord)
	if !ok {
		t.Fatalf("long chord %q missing; texts = %v", longChord, tt.Texts())
	}
	// The chords are key caps in one reserved column, right-aligned inside it (the
	// desktop key-reference convention: the caps' trailing edges line up).
	if right := short.X + short.W; right != long.X+long.W {
		t.Fatalf("shortcut column edge drifts with the chord length: %q right=%v, %q right=%v",
			shortChord, right, longChord, long.X+long.W)
	}

	// The hint of the chord-less row ends at the same edge as the chord row's
	// hint: both sit against the one reserved column.
	withChord, ok := tt.Find("短提示")
	if !ok {
		t.Fatalf("chord row hint missing; texts = %v", tt.Texts())
	}
	without, ok := tt.Find("这是一个比较长的提示文本")
	if !ok {
		t.Fatalf("chord-less row hint missing; texts = %v", tt.Texts())
	}
	if withChord.X+withChord.W != without.X+without.W {
		t.Fatalf("chord-less row does not reserve the shortcut column: hint right edges %v vs %v",
			withChord.X+withChord.W, without.X+without.W)
	}
	if short.X <= withChord.X+withChord.W {
		t.Fatalf("chord %q (x=%v) overlaps the hint column (right=%v)", shortChord, short.X, withChord.X+withChord.W)
	}
}

// TestSettingsNavKeepsDarkRampBelowPanel samples the settings surface's pixels:
// the navigation column is a sunken surface in both themes, darker than the
// modal panel behind the page body (the dark theme used to invert the ramp
// because ui.Sidebar defaults to theme.Surface).
func TestSettingsNavKeepsDarkRampBelowPanel(t *testing.T) {
	for _, dark := range []bool{false, true} {
		r := newTestRoot(t)
		mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
		mustPage(t, r, contracts.SettingsPageSpec{
			ID: "demo", Section: "常用", Title: "演示页", Order: 10,
			Build: func(c *ui.Context) { ui.Text(c, "page-body") },
		})
		tt := newTestTester(t, r, 1280, 800)
		tt.Frame()
		r.Theme().SetDark(dark)
		if err := r.OpenSettingsPage("demo"); err != nil {
			t.Fatalf("open settings: %v", err)
		}
		tt.Frame()

		nav, ok := tt.AccessByRole(ui.RoleTree, "设置导航")
		if !ok {
			t.Fatalf("settings navigation tree missing (dark=%v); texts = %v", dark, tt.Texts())
		}
		img := tt.Image()
		// The scroll's left padding strip is the navigation surface itself,
		// clear of any item row; the page body's left padding strip at mid
		// height is the modal panel surface.
		navPixel := img.At(int(nav.Bounds.X)+2, int(nav.Bounds.Y+nav.Bounds.H/2))
		panelPixel := img.At(int(nav.Bounds.X+nav.Bounds.W)+int(design.Space4)+2, int(nav.Bounds.Y+nav.Bounds.H/2))
		luminance := func(c interface{ RGBA() (r, g, b, a uint32) }) uint32 {
			r, g, b, _ := c.RGBA()
			return r>>8 + g>>8 + b>>8
		}
		if luminance(navPixel) >= luminance(panelPixel) {
			t.Fatalf("dark=%v: settings navigation surface (%v) is not darker than the modal panel (%v)",
				dark, navPixel, panelPixel)
		}
	}
}

// TestToastAnchorFollowsChromeState covers the host half of the toast placement
// contract (audit VD-02): the window's bottom-right corner, growing upward,
// clear of what the conversation screen docks at the bottom (the composer and
// the status strip), and clear of a docked inspector.
func TestToastAnchorFollowsChromeState(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, contracts.ScreenSpec{
		ID: "session", Title: "会话", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "body-session") },
	})
	mustSlot(t, r, textSlotItem(contracts.SlotScreenHeader, "session.header", "screen-title", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenTabs, "session.tabs", "screen-tab", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotSessionPanelRight, "session.right", "inspector-pane", 10))
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()

	// Default on the conversation screen: the bottom-right corner, above the
	// composer dock, the status strip and the transcript's trailing meta line (a
	// stack that stopped at the dock's edge covered that row's tail and it read as
	// the app truncating its own status).
	anchor := r.Anchor()
	if !anchor.FromBottom {
		t.Fatalf("default anchor = %+v, want the bottom-right corner", anchor)
	}
	if want := design.Space4 + toastComposerDockHeight + transcriptMetaRowHeight; anchor.Bottom != want {
		t.Fatalf("default anchor bottom = %v, want %v (above the composer dock and the meta line)", anchor.Bottom, want)
	}
	if anchor.Right != design.Space4 {
		t.Fatalf("default anchor right = %v, want %v", anchor.Right, design.Space4)
	}

	// Docked inspector: the stack stops before the pane (and the split seam).
	r.SetRightPanelOpen(true)
	tt.Frame()
	anchor = r.Anchor()
	if want := design.Space4 + design.InspectorWidth + design.Space1; anchor.Right != want {
		t.Fatalf("docked anchor right = %v, want %v (main column right edge)", anchor.Right, want)
	}
	if !anchor.FromBottom {
		t.Fatalf("the docked inspector must not move the stack off the bottom: %+v", anchor)
	}
	r.SetRightPanelOpen(false)
	tt.Frame()

	// Settings modal: the plain corner, clear of the centred panel.
	r.Navigator().ToggleSettings()
	tt.Frame()
	anchor = r.Anchor()
	if !anchor.FromBottom || anchor.Right != design.Space4 || anchor.Bottom != design.Space4 {
		t.Fatalf("settings modal anchor = %+v, want the window corner", anchor)
	}
	r.Navigator().CloseSettings()
	tt.Frame()
	if got := r.Anchor(); got.Bottom != design.Space4+toastComposerDockHeight+transcriptMetaRowHeight {
		t.Fatalf("closing the settings modal did not restore the composer inset: %+v", got)
	}

	// A module-reported popover does NOT move the stack: a popover opens beside
	// its trigger and leaves the composer and the status strip live, so the
	// stack keeps its dock inset and covers neither.
	r.ReportOverlay(true)
	tt.Frame()
	if got := r.Anchor(); got.Bottom != design.Space4+toastComposerDockHeight+transcriptMetaRowHeight {
		t.Fatalf("a reported popover moved the stack off its dock inset: %+v", got)
	}
	// The count still floors at zero: an unpaired close leaves it armed.
	r.ReportOverlay(false)
	r.ReportOverlay(false)
	tt.Frame()
	if got := r.Anchor(); got.Bottom != design.Space4+toastComposerDockHeight+transcriptMetaRowHeight {
		t.Fatalf("an unpaired close changed the stack's place: %+v", got)
	}
}

// TestToastAnchorWithoutComposer keeps the plain corner inset on a screen that
// docks no composer. The landing screen is NOT one of them: it holds a composer
// and its action row sits where the stack would land, so it keeps the dock
// inset like a conversation.
func TestToastAnchorWithoutBand(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("insights", "洞察", "body-insights", 10))
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	if anchor := r.Anchor(); !anchor.FromBottom || anchor.Bottom != design.Space4 {
		t.Fatalf("band-less anchor = %+v, want the plain Space4 corner inset", anchor)
	}
}
