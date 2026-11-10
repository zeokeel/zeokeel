package uiroot

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// TestValidateLifecycle covers Root.Validate: a UI assembly without a screen is
// invalid, one with a screen is valid, and unregistering the last screen makes
// it invalid again.
func TestValidateLifecycle(t *testing.T) {
	r := newTestRoot(t)
	if err := r.Validate(); err == nil {
		t.Fatal("Validate() succeeded before any screen was registered")
	}
	unregister := mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate() with one screen: %v", err)
	}
	unregister()
	if err := r.Validate(); err == nil {
		t.Fatal("Validate() succeeded after the last screen was unregistered")
	}
}

// TestNavigationPushBackCurrent covers host navigation: unknown screens are an
// error, Push/Back maintain a stack of registered screens, and the frame only
// builds the current screen.
func TestNavigationPushBackCurrent(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("alpha", "阿尔法屏", "body-alpha", 10))
	mustScreen(t, r, textScreen("beta", "贝塔屏", "body-beta", 20))
	nav := r.Navigator()

	if got := nav.Current(); got != "" {
		t.Fatalf("Current() = %q before any frame, want empty", got)
	}
	tt := newTestTester(t, r, 1400, 900)
	tt.Frame()
	// The host selects the first registered screen when navigation is empty.
	if got, want := nav.Current(), "alpha"; got != want {
		t.Fatalf("Current() = %q after the first frame, want %q", got, want)
	}
	requireHasText(t, tt, "body-alpha")
	requireNoText(t, tt, "body-beta")

	// Unknown screen IDs are reported to the caller, not silently ignored.
	err := nav.Push("nope")
	var unknown *UnknownScreenError
	if !errors.As(err, &unknown) {
		t.Fatalf("Push(unknown) error = %T %v, want *UnknownScreenError", err, err)
	}
	if unknown.ID != "nope" {
		t.Fatalf("UnknownScreenError.ID = %q, want %q", unknown.ID, "nope")
	}
	requireContains(t, err, "unknown screen nope")
	if got := nav.Current(); got != "alpha" {
		t.Fatalf("Current() = %q after a failed Push, want %q", got, "alpha")
	}
	if err := nav.Push(""); err == nil {
		t.Fatal("Push(\"\") was accepted")
	}

	// Push switches the rendered screen.
	if err := nav.Push("beta"); err != nil {
		t.Fatalf("Push(beta): %v", err)
	}
	tt.Frame()
	if got, want := nav.Current(), "beta"; got != want {
		t.Fatalf("Current() = %q, want %q", got, want)
	}
	requireHasText(t, tt, "body-beta")
	requireNoText(t, tt, "body-alpha")

	// Back pops to the previous screen; the stack bottom is stable.
	if !nav.Back() {
		t.Fatal("Back() = false with two entries on the stack")
	}
	if got, want := nav.Current(), "alpha"; got != want {
		t.Fatalf("Current() = %q after Back, want %q", got, want)
	}
	tt.Frame()
	requireHasText(t, tt, "body-alpha")
	requireNoText(t, tt, "body-beta")
	if nav.Back() {
		t.Fatal("Back() = true at the bottom of the stack")
	}
	if nav.Back() {
		t.Fatal("Back() = true on a repeated call at the bottom")
	}

	// Pushing the current screen twice does not grow the stack.
	if err := nav.Push("beta"); err != nil {
		t.Fatalf("Push(beta): %v", err)
	}
	if err := nav.Push("beta"); err != nil {
		t.Fatalf("Push(beta) again: %v", err)
	}
	if !nav.Back() {
		t.Fatal("Back() = false after a duplicate Push")
	}
	if got, want := nav.Current(), "alpha"; got != want {
		t.Fatalf("Current() = %q, want %q (duplicate Push must not stack)", got, want)
	}
}

// TestSettingsSurface covers the host-owned settings surface: it opens and
// closes through the navigator, renders contributed pages in the settings
// sidebar and page slot regions, and the whole surface disappears when closed.
func TestSettingsSurface(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	unregAppearance := mustPage(t, r, contracts.SettingsPageSpec{ID: "appearance", Section: "general", Title: "外观设置", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "settings-appearance-body") }})
	unregAccount := mustPage(t, r, contracts.SettingsPageSpec{ID: "account", Section: "account", Title: "账户设置", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "settings-account-body") }})
	mustSlot(t, r, textSlotItem(contracts.SlotSettingsSidebar, "extra", "settings-sidebar-extra", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotSettingsHeader, "header", "settings-header-slot", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotSettingsPageBody, "body", "settings-pagebody-slot", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotSettingsPageFooter, "footer", "settings-footer-slot", 10))

	nav := r.Navigator()
	tt := newTestTester(t, r, 1600, 1000)
	tt.Frame()
	if nav.SettingsOpen() {
		t.Fatal("SettingsOpen() = true before it was opened")
	}
	for _, text := range []string{"账户设置", "外观设置", "settings-sidebar-extra", "settings-header-slot"} {
		requireNoText(t, tt, text)
	}
	requireHasText(t, tt, "body-home") // the screen stays behind the surface

	nav.ToggleSettings()
	tt.Frame()
	if !nav.SettingsOpen() {
		t.Fatal("ToggleSettings did not open the settings surface")
	}
	// The settings sidebar groups pages by section (the general group first),
	// before the settings.sidebar extension slot.
	requireTextBefore(t, tt, "外观设置", "账户设置", "settings-sidebar-extra")
	// The selected page (the first of the sorted pages) is built inside the
	// page regions, between the header and footer slots.
	requireTextBefore(t, tt, "settings-header-slot", "settings-pagebody-slot",
		"settings-appearance-body", "settings-footer-slot")
	// Only the selected page body is built.
	requireNoText(t, tt, "settings-account-body")
	// The page list sits in the settings sidebar, the body in the page column.
	requireRegionLeftOf(t, tt, "外观设置", "settings-appearance-body")
	requireRegionAbove(t, tt, "settings-header-slot", "settings-appearance-body")
	requireRegionAbove(t, tt, "settings-appearance-body", "settings-footer-slot")

	// Selecting another page in the settings sidebar switches the page body.
	if err := tt.Click("账户设置"); err != nil {
		t.Fatalf("Click(账户设置): %v", err)
	}
	tt.Frame()
	requireHasText(t, tt, "settings-account-body")
	requireNoText(t, tt, "settings-appearance-body")

	// Unregistering the other page leaves the selected one in place.
	unregAppearance()
	tt.Frame()
	requireHasText(t, tt, "settings-account-body")
	requireNoText(t, tt, "外观设置")

	// With no pages left the surface falls back to the host copy.
	unregAccount()
	tt.Frame()
	requireHasText(t, tt, "还没有注册任何设置页。")
	requireNoText(t, tt, "账户设置")
	requireHasText(t, tt, "settings-sidebar-extra")
	requireHasText(t, tt, "settings-footer-slot")
	requireNoText(t, tt, "settings-pagebody-slot")

	nav.CloseSettings()
	tt.Frame()
	if nav.SettingsOpen() {
		t.Fatal("CloseSettings did not close the settings surface")
	}
	for _, text := range []string{"账户设置", "外观设置", "settings-account-body", "settings-sidebar-extra", "settings-footer-slot"} {
		requireNoText(t, tt, text)
	}
	nav.CloseSettings() // closing an already closed surface is a no-op
	tt.Frame()
	if nav.SettingsOpen() {
		t.Fatal("CloseSettings reopened the settings surface")
	}
}

// TestSettingsSurfaceEscapeClose covers the settings surface: a module footer
// action (or a command) opens it through the navigator, and Escape dismisses it
// (the Modal reports its open flag back to the host navigator).
func TestSettingsSurfaceEscapeClose(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustPage(t, r, contracts.SettingsPageSpec{ID: "appearance", Section: "general", Title: "外观设置", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "settings-appearance-body") }})

	nav := r.Navigator()
	tt := newTestTester(t, r, 1400, 900)
	tt.Frame()
	requireNoText(t, tt, "外观设置")

	// The settings entry is a module contribution (the sidebar footer); the
	// navigator opens the surface, exactly as that button does.
	nav.ToggleSettings()
	tt.Frame()
	if !nav.SettingsOpen() {
		t.Fatal("the navigator did not open the settings surface")
	}
	requireHasText(t, tt, "外观设置")

	// Escape dismisses the Modal, which tells the host to close the surface.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if nav.SettingsOpen() {
		t.Fatal("Escape did not close the settings surface")
	}
	requireNoText(t, tt, "外观设置")
	requireHasText(t, tt, "body-home")
}

// TestSettingsSurfaceWithoutPages covers the empty settings surface: it shows
// the host copy instead of pages and does not panic.
func TestSettingsSurfaceWithoutPages(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	tt := newTestTester(t, r, 1400, 900)
	tt.Frame()

	r.Navigator().ToggleSettings()
	tt.Frame()
	requireHasText(t, tt, "还没有注册任何设置页。")
}

// TestMultiScreenVisibility covers screen switching and Visible(): only the
// current screen body is built, invisible screens never appear in the sidebar,
// and a current-but-invisible screen falls back to the first visible one.
func TestMultiScreenVisibility(t *testing.T) {
	r := newTestRoot(t)
	var toggleVisible counter // 0 = hidden, 1 = visible

	mustScreen(t, r, contracts.ScreenSpec{ID: "hidden", Title: "隐藏屏", Order: 5,
		Visible: func() bool { return false },
		Build:   func(c *ui.Context) { ui.Text(c, "body-hidden") }})
	mustScreen(t, r, textScreen("alpha", "阿尔法屏", "body-alpha", 10))
	mustScreen(t, r, contracts.ScreenSpec{ID: "toggle", Title: "切换屏", Order: 15,
		Visible: func() bool { return toggleVisible.Load() == 1 },
		Build:   func(c *ui.Context) { ui.Text(c, "body-toggle") }})
	mustScreen(t, r, textScreen("beta", "贝塔屏", "body-beta", 20))

	nav := r.Navigator()
	tt := newTestTester(t, r, 1400, 900)
	tt.Frame()
	// Invisible screens are neither listed nor selected by default.
	requireNoText(t, tt, "隐藏屏")
	requireNoText(t, tt, "切换屏")
	requireHasText(t, tt, "body-alpha")

	// A hidden screen can be pushed but does not render its body: the frame
	// falls back to the first visible screen.
	if err := nav.Push("hidden"); err != nil {
		t.Fatalf("Push(hidden): %v", err)
	}
	tt.Frame()
	requireNoText(t, tt, "body-hidden")
	requireHasText(t, tt, "body-alpha")

	// A Visible() function is evaluated per frame.
	toggleVisible.Set(1)
	if err := nav.Push("toggle"); err != nil {
		t.Fatalf("Push(toggle): %v", err)
	}
	tt.Frame()
	requireHasText(t, tt, "body-toggle")
	requireNoText(t, tt, "body-alpha")

	toggleVisible.Set(0)
	tt.Frame()
	requireNoText(t, tt, "body-toggle")
	requireHasText(t, tt, "body-alpha")

	// Switching screens renders exactly one body.
	if err := nav.Push("beta"); err != nil {
		t.Fatalf("Push(beta): %v", err)
	}
	tt.Frame()
	requireHasText(t, tt, "body-beta")
	requireNoText(t, tt, "body-alpha")
	requireNoText(t, tt, "body-toggle")
	requireNoText(t, tt, "body-hidden")
}

// TestScreenBuildPanicIsContained covers a panicking screen builder: the host
// contains it, keeps the window chrome, and other screens still render.
func TestScreenBuildPanicIsContained(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, contracts.ScreenSpec{ID: "broken", Title: "坏屏", Order: 10,
		Build: func(*ui.Context) { panic("screen build boom") }})
	mustScreen(t, r, textScreen("good", "好屏", "body-good", 20))

	tt := newTestTester(t, r, 1400, 900)
	tt.Frame()
	// The shell still renders; only the broken screen body is missing.
	requireHasText(t, tt, "ZeoKeelTest")
	requireNoText(t, tt, "body-good")

	if err := r.Navigator().Push("good"); err != nil {
		t.Fatalf("Push(good): %v", err)
	}
	tt.Frame()
	requireHasText(t, tt, "body-good")
	requireHasText(t, tt, "ZeoKeelTest")
}

// TestPanicErrorReportsContainedPanic covers the error value the host hands to
// its recovery paths when module code panics.
func TestPanicErrorReportsContainedPanic(t *testing.T) {
	err := runSafelyErr(func() error { panic("module exploded") })
	if err == nil {
		t.Fatal("runSafelyErr returned nil after a panic")
	}
	var pe panicError
	if !errors.As(err, &pe) {
		t.Fatalf("error = %T, want uiroot.panicError", err)
	}
	requireContains(t, err, "panic in module code")
	if err := runSafelyErr(func() error { return nil }); err != nil {
		t.Fatalf("runSafelyErr without a panic = %v, want nil", err)
	}
}

// TestMultiTesterAlternating covers the multi-window semantics the host relies
// on: one Root.View can drive two headless windows serially, each with its own
// frame state, and the root frame counter advances once per built frame.
func TestMultiTesterAlternating(t *testing.T) {
	r := newTestRoot(t)
	var state counter
	mustScreen(t, r, contracts.ScreenSpec{ID: "home", Title: "首页", Order: 10,
		Build: func(c *ui.Context) { ui.Textf(c, "state=%d", state.Load()) }})

	t1 := newTestTester(t, r, 1200, 800)
	t2 := newTestTester(t, r, 900, 600)
	if got := t1.Image().Rect.Dx(); got != 1200 {
		t.Fatalf("first window width = %d, want 1200", got)
	}
	if got := t2.Image().Rect.Dx(); got != 900 {
		t.Fatalf("second window width = %d, want 900", got)
	}

	before := r.Frames()
	t1.Frame()
	if got, want := r.Frames(), before+1; got != want {
		t.Fatalf("Frames() = %d after one tester frame, want %d", got, want)
	}
	t2.Frame()
	requireHasText(t, t1, "state=0")
	requireHasText(t, t2, "state=0")

	// Alternating windows each build from the latest root state; a window that
	// did not build keeps its own last frame.
	for i := int64(1); i <= 3; i++ {
		state.Set(i)
		t1.Frame()
		requireHasText(t, t1, "state="+itoa(i))
		requireHasText(t, t2, "state="+itoa(i-1)) // t2 has not built this state yet
		t2.Frame()
		requireHasText(t, t2, "state="+itoa(i))
	}
}

// TestSidebarCollapseShortcut covers the old shell's Cmd/Ctrl+B chrome
// shortcut and the collapsed 64 DIP rail: the state flips, the screen body
// keeps rendering, and expanding returns the full sidebar.
func TestSidebarCollapseShortcut(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	requireHasText(t, tt, "body-home")

	tt.Key(ui.Cmd, ui.KeyB)
	tt.Frame()
	if !r.SidebarCollapsed() {
		t.Fatal("Cmd+B did not collapse the sidebar")
	}
	requireHasText(t, tt, "body-home") // the screen column survives the rail

	tt.Key(ui.Cmd, ui.KeyB)
	tt.Frame()
	if r.SidebarCollapsed() {
		t.Fatal("Cmd+B did not expand the collapsed sidebar")
	}

	// The host API and the shortcut share one state.
	r.SetSidebarCollapsed(true)
	tt.Frame()
	if !r.SidebarCollapsed() {
		t.Fatal("SetSidebarCollapsed(true) had no effect")
	}
	r.SetSidebarCollapsed(false)
}

// TestCommandPaletteShortcut covers Cmd/Ctrl+K: the host palette lists the
// registered commands, filters them and runs the chosen one.
func TestCommandPaletteShortcut(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	var runs counter
	mustCommand(t, r, contracts.CommandSpec{ID: "open.settings", Title: "打开设置",
		Hint: "设置面板", Order: 10, Run: func() error { runs.Inc(); return nil }})
	mustCommand(t, r, contracts.CommandSpec{ID: "other", Title: "其他命令",
		Order: 20, Run: func() error { return nil }})

	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	for _, want := range []string{"命令面板", "打开设置", "其他命令", "Esc 关闭"} {
		requireHasText(t, tt, want)
	}

	// The filter narrows the list.
	if rect, ok := tt.Find("搜索命令"); ok {
		tt.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
		tt.Frame()
		tt.Type("其他")
		tt.Frame()
		requireHasText(t, tt, "其他命令")
		requireNoText(t, tt, "打开设置")
	}
	// Choosing a command closes the palette and runs it. The row is a native
	// button whose accessible name is the command title; v0.8.3's strict
	// Tester.Click cannot pick it by text (the title is rendered more than once
	// inside the row), so query the access tree and click the button's bounds.
	info, ok := tt.AccessByRole(ui.RoleButton, "其他命令")
	if !ok {
		t.Fatalf("palette row is not an accessible button; texts = %v", tt.Texts())
	}
	tt.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
	tt.Frame()
	requireNoText(t, tt, "命令面板")
	requireHasText(t, tt, "body-home")

	// Cmd+K again, then Escape dismisses the palette without running.
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	requireHasText(t, tt, "命令面板")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	requireNoText(t, tt, "命令面板")
	if got := runs.Load(); got != 0 {
		t.Fatalf("commands ran %d times, want 0", got)
	}
}

// TestSettingsPinnedFooter covers the usability fix H1: a page's primary
// action contributes a Footer that the host renders in a fixed footer outside
// the scrolling body, so a long page still shows its action without scrolling.
func TestSettingsPinnedFooter(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustPage(t, r, contracts.SettingsPageSpec{
		ID: "long", Section: "general", Title: "长页设置", Order: 10,
		Build: func(c *ui.Context) {
			// Far taller than the 800 DIP window: an inline footer would be laid
			// out below the visible area.
			for i := 0; i < 40; i++ {
				ui.Text(c, "row-line").Height(32)
			}
		},
		Footer: func(c *ui.Context) { ui.Text(c, "pinned-primary-action") },
	})
	tt := newTestTester(t, r, 1280, 800)
	r.Navigator().ToggleSettings()
	tt.Frame()
	requireHasText(t, tt, "pinned-primary-action")
	box, ok := tt.Find("pinned-primary-action")
	if !ok {
		t.Fatalf("pinned action not found; texts: %v", tt.Texts())
	}
	// The fixed footer must sit inside the window, near the bottom: an inline
	// action after 40 tall rows would be far past this Y.
	if box.Y < 500 || box.Y+box.H > 800 {
		t.Fatalf("pinned action box = y %v h %v, want inside the lower window area", box.Y, box.H)
	}
}

// TestSettingsSearchFiltersNavigation covers H5's host half: the grouped
// settings navigation gets a search field that filters pages by title.
func TestSettingsSearchFiltersNavigation(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustPage(t, r, contracts.SettingsPageSpec{ID: "appearance", Section: "general", Title: "外观设置", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "settings-appearance-body") }})
	mustPage(t, r, contracts.SettingsPageSpec{ID: "account", Section: "general", Title: "账户设置", Order: 20,
		Build: func(c *ui.Context) { ui.Text(c, "settings-account-body") }})
	tt := newTestTester(t, r, 1400, 900)
	r.Navigator().ToggleSettings()
	tt.Frame()
	requireHasText(t, tt, "外观设置")
	requireHasText(t, tt, "账户设置")

	if err := tt.Click("搜索设置"); err != nil {
		t.Fatalf("Click(搜索设置): %v", err)
	}
	tt.Type("账户")
	tt.Frame()
	requireNoText(t, tt, "外观设置")
	requireHasText(t, tt, "账户设置")
	requireHasText(t, tt, "settings-account-body")
}

// TestSettingsUnsavedCloseGuard covers H1's close guard: closing the settings
// surface with a dirty page keeps it open and offers keep-editing/discard.
func TestSettingsUnsavedCloseGuard(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	dirty := false
	discarded := false
	mustPage(t, r, contracts.SettingsPageSpec{
		ID: "draft", Section: "general", Title: "草稿设置", Order: 10,
		Build:  func(c *ui.Context) { ui.Text(c, "draft-body") },
		Footer: func(c *ui.Context) { ui.Text(c, "draft-save") },
		Dirty:  func() bool { return dirty },
		Discard: func() {
			discarded = true
			dirty = false
		},
	})
	tt := newTestTester(t, r, 1280, 800)
	nav := r.Navigator()
	nav.ToggleSettings()
	tt.Frame()
	dirty = true
	tt.Frame()
	requireHasText(t, tt, "未保存")

	// Escape closes the native modal; the host must keep the surface open and
	// ask instead of discarding the draft.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if !nav.SettingsOpen() {
		t.Fatal("Escape closed the settings surface with unsaved edits")
	}
	requireHasText(t, tt, "有未保存的修改")

	if err := tt.Click("继续编辑"); err != nil {
		t.Fatalf("Click(继续编辑): %v", err)
	}
	tt.Frame()
	requireNoText(t, tt, "有未保存的修改")

	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	requireHasText(t, tt, "有未保存的修改")
	if err := tt.Click("放弃修改"); err != nil {
		t.Fatalf("Click(放弃修改): %v", err)
	}
	tt.Frame()
	if nav.SettingsOpen() {
		t.Fatal("Discard did not close the settings surface")
	}
	if !discarded {
		t.Fatal("Discard did not call the page's Discard hook")
	}
}

// TestInspectorDrawerIsModal covers the drawer as mygo's overlay recipe
// (docs/ui/overlays.md "A drawer on the right"): over a narrow window the
// inspector floats above the content as a RoleDialog, a press outside or Escape
// closes it, the focus is trapped while it shows, and the content keeps its own
// layout (the drawer covers it rather than squeezing it).
func TestInspectorDrawerIsModal(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("session", "会话", "body-session", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotSessionPanelRight, "pane", "inspector-pane", 10))
	tt := newTestTester(t, r, 1000, 720)
	tt.Frame()
	if _, ok := tt.Find("body-session"); !ok {
		t.Fatalf("body-session not found; texts: %v", tt.Texts())
	}

	tt.Key(ui.Cmd|ui.Alt, ui.KeyI)
	tt.Frame()
	requireHasText(t, tt, "inspector-pane")
	if _, ok := tt.AccessByRole(ui.RoleDialog, "检查器"); !ok {
		t.Fatalf("drawer is not an accessible dialog; texts: %v", tt.Texts())
	}

	// Escape closes the modal drawer and the content is usable again.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if r.RightPanelOpen() {
		t.Fatal("Escape did not close the inspector drawer")
	}
	requireNoText(t, tt, "inspector-pane")
	requireHasText(t, tt, "body-session")
}

// TestPaletteShowsCommandShortcut covers H26's palette half: a command that
// declares a shortcut shows it in the palette row.
func TestPaletteShowsCommandShortcut(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustCommand(t, r, contracts.CommandSpec{ID: "demo.action", Title: "演示命令", Order: 10,
		Shortcut: "Cmd/Ctrl + Alt + D", Run: func() error { return nil }})
	tt := newTestTester(t, r, 1280, 800)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	requireHasText(t, tt, "演示命令")
	requireHasText(t, tt, "Cmd/Ctrl + Alt + D")
}

// heightScreen returns a screen shaped like a session: a FillHeight column with
// a top marker, a transcript taking the free space and a composer pinned to the
// bottom. A screen built this way only reaches the window's bottom band when
// the host content column resolves to the window height (the right inspector
// split used to leave it auto-sized, so the FillHeight column collapsed to its
// intrinsic height and the composer floated near the top).
func heightScreen(id, title, top, bottom string) contracts.ScreenSpec {
	return contracts.ScreenSpec{
		ID: id, Title: title, Order: 10,
		Build: func(c *ui.Context) {
			ui.Column(c).FillWidth().FillHeight().Children(func() {
				ui.Text(c, top)
				ui.Spacer(c).Grow(1)
				ui.Text(c, bottom).FillWidth()
			})
		},
	}
}

// requireInBottomBand asserts a marker laid out like a composer sits inside the
// window's bottom band, so the transcript above it can flex instead of pushing
// the marker offscreen.
func requireInBottomBand(t *testing.T, tt *ui.Tester, width, height int, text string) ui.Rect {
	t.Helper()
	rect, ok := tt.Find(text)
	if !ok {
		t.Fatalf("frame does not show %q; texts: %v", text, tt.Texts())
	}
	if rect.W <= 0 || rect.H <= 0 {
		t.Fatalf("%q has no visible area: %+v (window %dx%d)", text, rect, width, height)
	}
	if rect.X < 0 || rect.Y < 0 || rect.X+rect.W > float32(width) || rect.Y+rect.H > float32(height) {
		t.Fatalf("%q is outside the %dx%d window: %+v", text, width, height, rect)
	}
	if rect.Y <= float32(height)*0.6 {
		t.Fatalf("%q is not in the bottom band of the %dx%d window: y=%.1f, want y > %.1f",
			text, width, height, rect.Y, float32(height)*0.6)
	}
	return rect
}

// TestContentAreaFillsHeightWithDockedInspector covers the reported bug where
// opening the docked right inspector left the content column auto-sized: the
// session screen (a FillHeight column with the composer pinned to its bottom)
// rendered at its intrinsic height, so the composer floated near the top with
// an empty conversation and was pushed offscreen by a long one. The inner
// content split must fill the content column's height, as the outer sidebar
// split does (docs/ui/split.md, docs/ui/layout.md: Fill/FillHeight take the
// parent's whole content box).
func TestContentAreaFillsHeightWithDockedInspector(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, heightScreen("session", "会话", "screen-top", "screen-bottom"))
	mustSlot(t, r, textSlotItem(contracts.SlotSessionPanelRight, "pane", "inspector-pane", 10))

	for _, size := range [][2]int{{1534, 1140}, {1280, 800}} {
		width, height := size[0], size[1]
		for _, collapsed := range []bool{false, true} {
			tt := newTestTester(t, r, width, height)
			tt.Frame()
			r.SetSidebarCollapsed(collapsed)
			tt.Frame()
			r.SetRightPanelOpen(true)
			tt.Frame()

			// Docked: the module's pane renders in the split, not in the drawer.
			requireHasText(t, tt, "inspector-pane")
			requireNoText(t, tt, "检查器")
			top, ok := tt.Find("screen-top")
			if !ok {
				t.Fatalf("%dx%d collapsed=%v: screen not built; texts: %v", width, height, collapsed, tt.Texts())
			}
			bottom := requireInBottomBand(t, tt, width, height, "screen-bottom")
			if top.Y >= bottom.Y {
				t.Fatalf("%dx%d collapsed=%v: screen-top y=%v is not above the bottom marker y=%v",
					width, height, collapsed, top.Y, bottom.Y)
			}
			// The docked inspector takes the right slice of the content column:
			// the main column and its composer end left of the inspector pane.
			inspector, ok := tt.Find("inspector-pane")
			if !ok {
				t.Fatalf("%dx%d collapsed=%v: inspector pane missing", width, height, collapsed)
			}
			if bottom.X+bottom.W > inspector.X {
				t.Fatalf("%dx%d collapsed=%v: content ends at x=%v, past the inspector pane x=%v",
					width, height, collapsed, bottom.X+bottom.W, inspector.X)
			}
			// Closing and reopening the inspector must not leave the content
			// column auto-sized for a frame.
			r.SetRightPanelOpen(false)
			tt.Frame()
			requireInBottomBand(t, tt, width, height, "screen-bottom")
			r.SetRightPanelOpen(true)
			tt.Frame()
			requireInBottomBand(t, tt, width, height, "screen-bottom")
		}
	}

	// The narrow window keeps the drawer path: the content column never lost
	// its height there, and the drawer overlays it as a modal dialog.
	r.SetSidebarCollapsed(false)
	narrow := newTestTester(t, r, 1024, 720)
	narrow.Frame()
	requireInBottomBand(t, narrow, 1024, 720, "screen-bottom")
	requireHasText(t, narrow, "inspector-pane")
	requireHasText(t, narrow, "检查器")
	if _, ok := narrow.AccessByRole(ui.RoleDialog, "检查器"); !ok {
		t.Fatalf("narrow inspector is not an accessible dialog; texts: %v", narrow.Texts())
	}
}

// TestDockedInspectorSplitKeepsWidthBehavior covers the width half of the
// docked-inspector content split: filling the content column's height must not
// change how the divider resizes the inspector or how SplitReset restores its
// default width (docs/ui/split.md). The test drives the divider itself, so a
// future layout change that breaks dragging or the double-click reset shows up
// here, and the composer-like bottom marker stays at the window bottom through
// both interactions.
func TestDockedInspectorSplitKeepsWidthBehavior(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, heightScreen("session", "会话", "screen-top", "screen-bottom"))
	mustSlot(t, r, textSlotItem(contracts.SlotSessionPanelRight, "pane", "inspector-pane", 10))
	const width, height = 1534, 1140
	tt := newTestTester(t, r, width, height)
	tt.Frame()
	r.SetRightPanelOpen(true)
	tt.Frame()
	if got := r.inspectorWidth; got != design.InspectorWidth {
		t.Fatalf("default inspector width = %v, want %v", got, design.InspectorWidth)
	}

	// The divider is one line plus the pane's 1 DIP start border; the sidebar
	// module owns its page padding, so the docked pane itself has none.
	dividerX := func() float32 {
		inspector, ok := tt.Find("inspector-pane")
		if !ok {
			t.Fatalf("inspector pane missing; texts: %v", tt.Texts())
		}
		return inspector.X - 2
	}
	mid := float32(height) / 2

	// Drag the divider 40 DIPs right: the content pane grows, the inspector
	// shrinks to 280 DIP, and the content column keeps the window height.
	start := dividerX()
	tt.Press(start, mid)
	tt.Move(start+40, mid)
	tt.Release(start+40, mid)
	tt.Frame()
	if got := r.inspectorWidth; got < 279 || got > 281 {
		t.Fatalf("after dragging the divider 40 DIPs, inspector width = %v, want 280", got)
	}
	requireInBottomBand(t, tt, width, height, "screen-bottom")

	// A double click on the moved divider restores SplitReset's default
	// inspector width.
	moved := dividerX()
	tt.ClickAt(moved, mid)
	tt.ClickAt(moved, mid)
	tt.Frame()
	if got := r.inspectorWidth; got != design.InspectorWidth {
		t.Fatalf("after a double click on the divider, inspector width = %v, want %v",
			got, design.InspectorWidth)
	}
	requireInBottomBand(t, tt, width, height, "screen-bottom")
}

// TestCollapsedSidebarStartupResolvesDefaultScreen covers the audit P0: a
// persisted collapsed sidebar never drew the screen list, so nav.Current()
// stayed empty and every screen-scoped slot (title/actions/tabs) plus the
// inspector toggle was filtered out until the user expanded the sidebar.
func TestCollapsedSidebarStartupResolvesDefaultScreen(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustScreen(t, r, textScreen("session", "对话", "body-session", 20))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenHeader, "session.title", "session-title-text", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotSessionPanelRight, "pane", "inspector-pane", 10))
	r.SetSidebarCollapsed(true)

	tt := newTestTester(t, r, 1280, 800)
	tt.Frame()
	if got := r.Navigator().Current(); got != "session" {
		t.Fatalf("collapsed startup current screen = %q, want session", got)
	}
	requireHasText(t, tt, "session-title-text")
	requireHasText(t, tt, "body-session")
	r.SetRightPanelOpen(true)
	tt.Frame()
	requireHasText(t, tt, "inspector-pane")
}
