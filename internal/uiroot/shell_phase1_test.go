package uiroot

// Frame tests for the shell Phase 1 audit fixes: the inspector's screen scope,
// the drawer's single-Escape close and focus return, the live sidebar width
// clamp, the persisted chrome open states, and the removed back chord.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestInspectorOnlyOnSessionScreen covers D-03: the window-level inspector
// reads the session read model, so the host only offers it on the session
// screen; Home keeps no inspector toggle and the shortcut does nothing there.
func TestInspectorOnlyOnSessionScreen(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustScreen(t, r, textScreen(sessionScreenID, "会话", "body-session", 20))
	mustSlot(t, r, textSlotItem(contracts.SlotSessionPanelRight, "pane", "inspector-pane", 10))
	// The B3 top band (and its inspector toggle) is host chrome for screens that
	// contribute a header; the session screen does in the product.
	mustSlot(t, r, textSlotItem(contracts.SlotScreenHeader, "hdr", "screen-title", 10))
	tt := newTestTester(t, r, 1280, 800)
	tt.Frame()

	// Home: the shortcut and the toolbar entry do nothing.
	if got := r.Navigator().Current(); got != "home" {
		t.Fatalf("current screen = %q, want home", got)
	}
	tt.Key(ui.Cmd|ui.Alt, ui.KeyI)
	tt.Frame()
	if r.RightPanelOpen() {
		t.Fatal("Cmd+Alt+I opened the inspector on Home")
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "打开右侧栏"); ok {
		t.Fatal("Home shows the inspector toggle")
	}

	// Session: the inspector opens and renders its pane.
	if err := r.Navigator().Push(sessionScreenID); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	tt.Frame()
	tt.Key(ui.Cmd|ui.Alt, ui.KeyI)
	tt.Frame()
	if !r.RightPanelOpen() {
		t.Fatal("Cmd+Alt+I did not open the inspector on the session screen")
	}
	requireHasText(t, tt, "inspector-pane")

	// Leaving the session screen hides it again: the open state is kept for
	// when the user returns, but no other screen shows another screen's pane.
	if err := r.Navigator().Push("home"); err != nil {
		t.Fatalf("Push(home): %v", err)
	}
	tt.Frame()
	requireNoText(t, tt, "inspector-pane")
	if _, ok := tt.AccessByRole(ui.RoleButton, "收起右侧栏"); ok {
		t.Fatal("Home shows the inspector toggle after leaving the session screen")
	}
	if !r.RightPanelOpen() {
		t.Fatal("leaving the session screen forgot the inspector open state")
	}
	if err := r.Navigator().Push(sessionScreenID); err != nil {
		t.Fatalf("Push(session) again: %v", err)
	}
	tt.Frame()
	requireHasText(t, tt, "inspector-pane")
}

// TestInspectorDrawerEscapeOnceAndRestoresFocus covers D-02: one Escape closes
// the modal drawer even when the focus sits on the close control, and the
// focus goes back to the content header's inspector toggle. The close control
// carries no Tooltip: a keyboard-raised tooltip is its own overlay that would
// take the first Escape.
func TestInspectorDrawerEscapeOnceAndRestoresFocus(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen(sessionScreenID, "会话", "body-session", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotSessionPanelRight, "pane", "inspector-pane", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenHeader, "hdr", "screen-title", 10))
	tt := newTestTester(t, r, 1000, 720)
	tt.Frame()

	tt.Key(ui.Cmd|ui.Alt, ui.KeyI)
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleDialog, "检查器"); !ok {
		t.Fatalf("drawer is not an accessible dialog; texts: %v", tt.Texts())
	}
	// The drawer's close control is a plain labeled button, not a keyboard
	// tooltip: no Description, so no tooltip overlay can eat Escape.
	closeInfo, ok := tt.AccessByRole(ui.RoleButton, "关闭右侧栏")
	if !ok {
		t.Fatalf("drawer close control missing; texts: %v", tt.Texts())
	}
	if closeInfo.Description != "" {
		t.Fatalf("drawer close control carries a tooltip description %q", closeInfo.Description)
	}

	// Tab reaches the close control; one Escape closes the drawer.
	focusedClose := false
	for i := 0; i < 4; i++ {
		tt.Key(0, ui.KeyTab)
		tt.Frame()
		if info, ok := tt.AccessByRole(ui.RoleButton, "关闭右侧栏"); ok && info.Focused {
			focusedClose = true
			break
		}
	}
	if !focusedClose {
		t.Fatalf("Tab did not reach the drawer close control; texts: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Frame()
	if r.RightPanelOpen() {
		t.Fatal("one Escape did not close the drawer while the focus was on its close control")
	}
	if _, ok := tt.AccessByRole(ui.RoleDialog, "检查器"); ok {
		t.Fatal("drawer still renders after Escape")
	}
	// The focus is back on the inspector toggle, whose label is now the
	// "open" wording.
	if info, ok := tt.AccessByRole(ui.RoleButton, "打开右侧栏"); !ok || !info.Focused {
		t.Fatalf("focus after close = %+v (present=%v), want the inspector toggle focused", info, ok)
	}
}

// TestSidebarDividerClampsDuringDrag covers D-04: the sidebar divider clamps to
// its usable 200-420 DIP range while dragging, so it cannot leave the chrome in
// a 40-199 DIP state that a restart then snaps back from.
func TestSidebarDividerClampsDuringDrag(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	tt := newTestTester(t, r, 1280, 800)
	tt.Frame()

	splitter, ok := tt.AccessByRole(ui.RoleSplitter, "")
	if !ok {
		t.Fatalf("sidebar divider missing; texts: %v", tt.Texts())
	}
	y := splitter.Bounds.Y + splitter.Bounds.H/2
	// Drag far past the minimum: the divider stops at minSidebarWidth.
	tt.Press(splitter.Bounds.X+splitter.Bounds.W/2, y)
	tt.Move(60, y)
	tt.Release(60, y)
	tt.Frame()
	tt.Frame()
	if got := r.sidebarWidth; got != minSidebarWidth {
		t.Fatalf("sidebar width after dragging left = %v, want %v", got, minSidebarWidth)
	}
	if info, ok := tt.AccessByRole(ui.RoleSplitter, ""); !ok || info.Bounds.X != minSidebarWidth {
		t.Fatalf("divider after drag = %+v (present=%v), want x=%v", info.Bounds, ok, minSidebarWidth)
	}

	// Drag far past the maximum: it stops at maxSidebarWidth.
	tt.Press(minSidebarWidth, y)
	tt.Move(760, y)
	tt.Release(760, y)
	tt.Frame()
	tt.Frame()
	if got := r.sidebarWidth; got != maxSidebarWidth {
		t.Fatalf("sidebar width after dragging right = %v, want %v", got, maxSidebarWidth)
	}

	// The split keeps an accessible name for assistive technology.
	if _, ok := tt.AccessByRole(ui.RoleGroup, "侧栏宽度可调"); !ok {
		t.Fatalf("sidebar split has no accessible group label; texts: %v", tt.Texts())
	}
}

// TestChromeOpenStatesPersist covers D-07: the sidebar's rail/expand state and
// the inspector's open state are host layout state and survive a restart, like
// the dragged widths do.
func TestChromeOpenStatesPersist(t *testing.T) {
	settings := &fakeSettings{values: map[string]string{}}
	r := New(Config{AppName: "Test", Language: "zh", Settings: settings})
	r.loadChromeWidths()
	if r.SidebarCollapsed() || r.RightPanelOpen() {
		t.Fatalf("defaults = collapsed=%v open=%v, want false/false", r.SidebarCollapsed(), r.RightPanelOpen())
	}
	r.SetSidebarCollapsed(true)
	r.SetRightPanelOpen(true)
	if got := settings.values[settingsKeySidebarCollapsed]; got != "true" {
		t.Fatalf("%s = %q, want true", settingsKeySidebarCollapsed, got)
	}
	if got := settings.values[settingsKeyInspectorOpen]; got != "true" {
		t.Fatalf("%s = %q, want true", settingsKeyInspectorOpen, got)
	}

	reopened := New(Config{AppName: "Test", Language: "zh", Settings: settings})
	reopened.loadChromeWidths()
	if !reopened.SidebarCollapsed() || !reopened.RightPanelOpen() {
		t.Fatalf("restored = collapsed=%v open=%v, want true/true",
			reopened.SidebarCollapsed(), reopened.RightPanelOpen())
	}

	// Closing writes the closed state too.
	reopened.SetSidebarCollapsed(false)
	reopened.SetRightPanelOpen(false)
	if got := settings.values[settingsKeyInspectorOpen]; got != "false" {
		t.Fatalf("%s after close = %q, want false", settingsKeyInspectorOpen, got)
	}
}

// TestAltLeftNeverNavigates covers the navigation cleanup: the window has no
// back control and no Alt+Left history chord (the sidebar destinations and the
// command palette are the navigation), with or without a focused text field.
func TestAltLeftNeverNavigates(t *testing.T) {
	draft := ""
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustScreen(t, r, contracts.ScreenSpec{
		ID: sessionScreenID, Title: "会话", Order: 20,
		Build: func(c *ui.Context) {
			ui.Text(c, "body-session")
			ui.TextArea(c, &draft).Label("草稿").Placeholder("草稿")
		},
	})
	tt := newTestTester(t, r, 1280, 800)
	tt.Frame()
	if err := r.Navigator().Push(sessionScreenID); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	tt.Frame()
	if err := tt.Click("草稿"); err != nil {
		t.Fatalf("click the composer field: %v", err)
	}
	tt.Frame()
	tt.Type("hello world")
	tt.Frame()
	tt.Key(ui.Alt, ui.KeyLeft)
	tt.Frame()
	if got := r.Navigator().Current(); got != sessionScreenID {
		t.Fatalf("Alt+Left in a text input navigated to %q, want to stay on the session screen", got)
	}
	// Outside the text field the same chord still does not navigate: there is no
	// back chord in the product any more.
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	tt.Key(ui.Alt, ui.KeyLeft)
	tt.Frame()
	if got := r.Navigator().Current(); got != sessionScreenID {
		t.Fatalf("Alt+Left navigated to %q, want no history shortcut", got)
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "返回上一个页面"); ok {
		t.Fatal("the window still exposes a visible back control")
	}
}

// TestDockedInspectorCloseAtMinWindowRail covers D-05: at the 900x600 host
// minimum with the sidebar collapsed to the rail the inspector docks, and the
// docked pane still has a close control. The host draws the docked inspector's
// header while the module hides its own pane-header close at these widths, so
// the affordance is present exactly while it is docked.
func TestDockedInspectorCloseAtMinWindowRail(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, heightScreen(sessionScreenID, "会话", "screen-top", "screen-bottom"))
	mustSlot(t, r, textSlotItem(contracts.SlotSessionPanelRight, "pane", "inspector-pane", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenHeader, "hdr", "screen-title", 10))
	tt := newTestTester(t, r, 900, 600)
	tt.Frame()
	r.SetSidebarCollapsed(true)
	tt.Frame()
	r.SetRightPanelOpen(true)
	tt.Frame()

	// Docked, not the modal drawer.
	if _, ok := tt.AccessByRole(ui.RoleDialog, "检查器"); ok {
		t.Fatalf("figure at 900x600 + rail rendered the drawer; texts: %v", tt.Texts())
	}
	requireHasText(t, tt, "inspector-pane")
	closeInfo, ok := tt.AccessByRole(ui.RoleButton, "关闭检查器")
	if !ok {
		t.Fatalf("docked inspector at 900x600 + rail has no close control; texts: %v", tt.Texts())
	}
	if !closeInfo.Focusable {
		t.Fatalf("dock close control is not focusable: %+v", closeInfo)
	}
	if err := tt.Click("关闭检查器"); err != nil {
		t.Fatalf("click the docked close control: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if r.RightPanelOpen() {
		t.Fatal("the docked close control did not close the inspector")
	}
	if info, ok := tt.AccessByRole(ui.RoleButton, "打开右侧栏"); !ok || !info.Focused {
		t.Fatalf("focus after the docked close = %+v (present=%v), want the inspector toggle", info, ok)
	}

	// On a wide expanded window the module keeps its own close control, so the
	// host must not draw a second docked header/close button.
	wide := newTestTester(t, r, 1280, 800)
	wide.Frame()
	r.SetSidebarCollapsed(false)
	wide.Frame()
	r.SetRightPanelOpen(true)
	wide.Frame()
	if _, ok := wide.AccessByRole(ui.RoleButton, "关闭检查器"); ok {
		t.Fatalf("wide docked inspector shows the host close header (duplicate close); texts: %v", wide.Texts())
	}
}

// TestRailAnnouncesCurrentScreen covers D-22: the collapsed rail's destination
// buttons carry the current-screen state to assistive technology (a plain
// button role dropped it, so the rail looked unselected to a screen reader).
// Fix round SH-05 also pinned the rail's destination set: it mirrors the
// expanded sidebar's screens (Home, Automation, Plugins, ...) and keeps the
// new-chat action, while conversations stay the workspace tree's destination.
func TestRailAnnouncesCurrentScreen(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustScreen(t, r, textScreen("session", "对话", "body-session", 20))
	mustScreen(t, r, textScreen("plugins", "插件", "body-plugins", 30))
	tt := newTestTester(t, r, 1280, 800)
	tt.Frame()
	r.SetSidebarCollapsed(true)
	tt.Frame()
	tt.Frame()

	home, ok := tt.AccessByRole(ui.RoleToggleButton, "首页")
	if !ok {
		t.Fatalf("rail Home destination is not a toggle-like button; texts: %v", tt.Texts())
	}
	if !home.Checked {
		t.Fatalf("rail Home does not report the current screen: %+v", home)
	}
	// The conversation screen is not a rail destination: only the workspace
	// tree lists conversations.
	if session, ok := tt.AccessByRole(ui.RoleToggleButton, "对话"); ok {
		t.Fatalf("rail lists the conversation screen as a destination: %+v", session)
	}
	// The primary action survives the collapse as an icon action.
	if _, ok := tt.AccessByRole(ui.RoleButton, "新对话"); !ok {
		t.Fatalf("rail has no new-chat action; texts: %v", tt.Texts())
	}

	// Navigating from the rail moves the state with the screen.
	if err := tt.Click("插件"); err != nil {
		t.Fatalf("click rail Plugins: %v; texts: %v", err, tt.Texts())
	}
	tt.Frame()
	tt.Frame()
	if got := r.Navigator().Current(); got != "plugins" {
		t.Fatalf("screen after the rail click = %q, want plugins", got)
	}
	plugins, ok := tt.AccessByRole(ui.RoleToggleButton, "插件")
	if !ok || !plugins.Checked {
		t.Fatalf("rail Plugins after navigation = %+v (present=%v), want checked", plugins, ok)
	}
	if home, ok := tt.AccessByRole(ui.RoleToggleButton, "首页"); !ok || home.Checked {
		t.Fatalf("rail Home after navigation = %+v (present=%v), want unchecked", home, ok)
	}
}
