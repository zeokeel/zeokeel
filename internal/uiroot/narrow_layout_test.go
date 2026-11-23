package uiroot

// Frame tests for the responsive inspector layout (P1-6): when the right
// inspector opens on a window that cannot hold the expanded sidebar plus the
// content minimum plus a usable inspector, the shell temporarily renders the
// sidebar as its 64 DIP rail — without touching the user's Cmd/Ctrl+B
// preference — so the inspector docks beside the content instead of becoming a
// blocking modal drawer. Closing the inspector or widening the window restores
// the expanded sidebar.
//
// The tests measure committed geometry (content column width, send control,
// inspector pane) and the frame's own drawer signal, not hard-coded pixels.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
	"github.com/zeokeel/zeokeel/internal/design"
)

// composerScreen is a session-shaped screen for the inspector layout tests: the
// content column fills its width (so the frame can measure the column), the
// bottom row stands in for the composer with a working send control, and the
// column fills the window height.
func composerScreen(id, title string, sends *int) uinative.ScreenSpec {
	return uinative.ScreenSpec{
		ID: id, Title: title, Order: 10,
		Build: func(c *ui.Context) {
			ui.Column(c).FillWidth().FillHeight().Children(func() {
				ui.Text(c, "content-marker").FillWidth()
				ui.Text(c, "screen-top").FillWidth()
				ui.Spacer(c).Grow(1)
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					ui.Text(c, "composer-body").FillWidth().Grow(1)
					send := ui.Button(c, "发送")
					if send.Clicked() {
						*sends++
					}
				})
			})
		},
	}
}

// mustInspector registers the session screen and the right-inspector slot the
// responsive tests open: the pane body fills the pane's width so its committed
// geometry is the docked inspector's real width.
func mustInspector(t *testing.T, r *Root, sends *int) {
	t.Helper()
	mustScreen(t, r, composerScreen(sessionScreenID, "会话", sends))
	mustSlot(t, r, uinative.SlotItem{
		Slot: contracts.SlotSessionPanelRight, ID: "pane", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "inspector-body").FillWidth() },
	})
}

// requireInspectorDocked asserts the inspector renders as the docked pane, not
// the modal drawer: the host's own drawer signal is off, no named dialog is in
// the accessibility tree, and the pane body paints.
func requireInspectorDocked(t *testing.T, tt *ui.Tester, r *Root) {
	t.Helper()
	if r.inspectorDrawerShown {
		t.Fatalf("inspector rendered as the modal drawer; texts: %v", tt.Texts())
	}
	if _, ok := tt.AccessByRole(ui.RoleDialog, "检查器"); ok {
		t.Fatalf("inspector rendered as a modal dialog; texts: %v", tt.Texts())
	}
	requireHasText(t, tt, "inspector-body")
}

// requireSidebarRail asserts the frame renders the 64 DIP icon rail: the rail's
// expand affordance is present and the content column starts left of the
// expanded sidebar's width.
func requireSidebarRail(t *testing.T, tt *ui.Tester) {
	t.Helper()
	if _, ok := tt.AccessByRole(ui.RoleButton, "展开侧边栏"); !ok {
		t.Fatalf("sidebar is not the icon rail; texts: %v", tt.Texts())
	}
	content, ok := tt.Find("content-marker")
	if !ok {
		t.Fatalf("content marker missing; texts: %v", tt.Texts())
	}
	if content.X >= design.SidebarWidth {
		t.Fatalf("content column starts at x=%v, want it left of the expanded sidebar (%v)",
			content.X, design.SidebarWidth)
	}
}

// requireSidebarExpanded asserts the frame renders the expanded sidebar: the
// wordmark row's collapse affordance is present and the content column starts
// at or right of the expanded sidebar's width.
func requireSidebarExpanded(t *testing.T, tt *ui.Tester) {
	t.Helper()
	if _, ok := tt.AccessByRole(ui.RoleButton, "折叠侧边栏"); !ok {
		t.Fatalf("sidebar is not expanded; texts: %v", tt.Texts())
	}
	content, ok := tt.Find("content-marker")
	if !ok {
		t.Fatalf("content marker missing; texts: %v", tt.Texts())
	}
	if content.X < design.SidebarWidth-1 {
		t.Fatalf("expanded sidebar still starts the content at x=%v, want at least %v",
			content.X, design.SidebarWidth)
	}
}

// requireContentAndInspectorUsable asserts the content column keeps its usable
// minimum, the inspector stays at least its minimum width, the send control
// sits left of the inspector, and clicking it reaches the screen. The column
// widths are the landmarks' committed geometry: the screen body insets its
// content, so the content marker is narrower than the column by that padding.
func requireContentAndInspectorUsable(t *testing.T, tt *ui.Tester, sends *int) {
	t.Helper()
	main, ok := tt.AccessByRole(ui.RoleMain, "主内容")
	if !ok {
		t.Fatalf("main landmark missing; texts: %v", tt.Texts())
	}
	if main.Bounds.W < design.ContentMinWidth-0.5 {
		t.Fatalf("content column width = %v, want at least ContentMinWidth (%v)",
			main.Bounds.W, design.ContentMinWidth)
	}
	inspector, ok := tt.AccessByRole(ui.RoleComplementary, "右侧栏")
	if !ok {
		t.Fatalf("docked inspector landmark missing; texts: %v", tt.Texts())
	}
	if inspector.Bounds.W < design.InspectorMinWidth-0.5 {
		t.Fatalf("inspector width = %v, want at least InspectorMinWidth (%v)",
			inspector.Bounds.W, design.InspectorMinWidth)
	}
	send, ok := tt.AccessByRole(ui.RoleButton, "发送")
	if !ok {
		t.Fatalf("send control missing; texts: %v", tt.Texts())
	}
	if send.Bounds.X+send.Bounds.W > inspector.Bounds.X {
		t.Fatalf("send control ends at x=%v, under the inspector pane at x=%v",
			send.Bounds.X+send.Bounds.W, inspector.Bounds.X)
	}
	before := *sends
	tt.ClickAt(send.Bounds.X+send.Bounds.W/2, send.Bounds.Y+send.Bounds.H/2)
	tt.Frame()
	if *sends != before+1 {
		t.Fatalf("send clicks = %d, want %d: the composer is not usable with the inspector docked",
			*sends, before+1)
	}
}

// TestNarrowInspectorDocksWithAutoRail covers 1024x720: opening the inspector
// would leave it under InspectorMinWidth with the expanded sidebar, so the
// shell temporarily shows the rail and the inspector docks beside the content
// instead of covering it with the modal drawer.
func TestNarrowInspectorDocksWithAutoRail(t *testing.T) {
	sends := 0
	r := newTestRoot(t)
	mustInspector(t, r, &sends)
	tt := newTestTester(t, r, 1024, 720)
	tt.Frame()

	r.SetRightPanelOpen(true)
	tt.Frame()
	tt.Frame() // the responsive state settles on the next frame

	requireInspectorDocked(t, tt, r)
	requireSidebarRail(t, tt)
	// The automatic rail is a layout state: the user's own collapse preference
	// is untouched.
	if r.SidebarCollapsed() {
		t.Fatal("the automatic rail changed the user's collapsed preference")
	}
	requireContentAndInspectorUsable(t, tt, &sends)

	// The docked pane keeps a real close affordance at these widths.
	if err := tt.Click("关闭检查器"); err != nil {
		t.Fatalf("click the docked inspector close control: %v; texts: %v", err, tt.Texts())
	}
	tt.Frame()
	if r.RightPanelOpen() {
		t.Fatal("the docked inspector close control did not close the inspector")
	}
}

// TestMinimumWindowInspectorDocksWithAutoRail covers 900x600: the auto rail
// lets the inspector dock (no blocking scrim), the content column keeps
// ContentMinWidth and the composer's send control is usable.
func TestMinimumWindowInspectorDocksWithAutoRail(t *testing.T) {
	sends := 0
	r := newTestRoot(t)
	mustInspector(t, r, &sends)
	tt := newTestTester(t, r, 900, 600)
	tt.Frame()

	tt.Key(ui.Cmd|ui.Alt, ui.KeyI)
	tt.Frame()
	tt.Frame()
	if !r.RightPanelOpen() {
		t.Fatal("Cmd+Alt+I did not open the inspector at 900x600")
	}

	requireInspectorDocked(t, tt, r)
	requireSidebarRail(t, tt)
	if r.SidebarCollapsed() {
		t.Fatal("the automatic rail changed the user's collapsed preference")
	}
	requireContentAndInspectorUsable(t, tt, &sends)
}

// TestWideWindowInspectorKeepsExpandedSidebar pins the unchanged wide layout:
// the expanded sidebar stays, the inspector docks at its preferred width, and
// no automatic rail appears.
func TestWideWindowInspectorKeepsExpandedSidebar(t *testing.T) {
	sends := 0
	r := newTestRoot(t)
	mustInspector(t, r, &sends)
	tt := newTestTester(t, r, 1600, 900)
	tt.Frame()

	r.SetRightPanelOpen(true)
	tt.Frame()
	tt.Frame()

	requireInspectorDocked(t, tt, r)
	requireSidebarExpanded(t, tt)
	if r.SidebarCollapsed() {
		t.Fatal("wide window collapsed the sidebar")
	}
	if got := r.inspectorWidth; got != design.InspectorWidth {
		t.Fatalf("inspector width = %v, want the preferred %v", got, design.InspectorWidth)
	}
	requireContentAndInspectorUsable(t, tt, &sends)
	// The preferred inspector width survives the layout: the auto-shrink only
	// applies while the window cannot afford it.
	inspector, ok := tt.Find("inspector-body")
	if !ok {
		t.Fatalf("inspector body missing; texts: %v", tt.Texts())
	}
	if inspector.W < design.InspectorWidth-2 {
		t.Fatalf("wide inspector width = %v, want the preferred %v", inspector.W, design.InspectorWidth)
	}
}

// TestAutoRailRevertsWhenInspectorCloses covers the temporary nature of the
// rail: closing the inspector brings the user's expanded sidebar back, and a
// window widened while the inspector is open expands it again.
func TestAutoRailRevertsWhenInspectorCloses(t *testing.T) {
	sends := 0
	r := newTestRoot(t)
	mustInspector(t, r, &sends)
	tt := newTestTester(t, r, 900, 600)
	tt.Frame()

	r.SetRightPanelOpen(true)
	tt.Frame()
	tt.Frame()
	requireSidebarRail(t, tt)

	// Closing the inspector restores the expanded sidebar (the user never
	// collapsed it).
	r.SetRightPanelOpen(false)
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleButton, "展开侧边栏"); ok {
		t.Fatalf("the rail lingered after the inspector closed; texts: %v", tt.Texts())
	}
	requireSidebarExpanded(t, tt)

	// Widening the window with the inspector open also restores the expanded
	// sidebar: the rail was only needed while the window was narrow.
	r.SetRightPanelOpen(true)
	tt.Frame()
	tt.Frame()
	requireSidebarRail(t, tt)
	tt.SetSize(1400, 840)
	tt.Frame()
	tt.Frame()
	requireSidebarExpanded(t, tt)
	requireInspectorDocked(t, tt, r)
	requireContentAndInspectorUsable(t, tt, &sends)

	// Narrowing again while the inspector is open takes the rail back.
	tt.SetSize(1024, 720)
	tt.Frame()
	tt.Frame()
	requireSidebarRail(t, tt)
}

// TestAutoRailLeavesManualCollapsePreference covers the interaction with the
// user's own chrome state: Cmd/Ctrl+B still collapses and expands the sidebar,
// the inspector opening or closing never rewrites that preference (the rail
// stays collapsed after the inspector closes), and the persisted preference is
// never overwritten by the temporary layout rail.
func TestAutoRailLeavesManualCollapsePreference(t *testing.T) {
	sends := 0
	settings := &fakeNotifierSettings{}
	r := New(Config{AppName: "ZeoKeelTest", Settings: settings})
	mustInspector(t, r, &sends)
	tt := newTestTester(t, r, 1024, 720)
	tt.Frame()
	requireSidebarExpanded(t, tt)

	// Manual collapse, then open the inspector: the manual rail is kept.
	tt.Key(ui.Cmd, ui.KeyB)
	tt.Frame()
	if !r.SidebarCollapsed() {
		t.Fatal("Cmd+B did not collapse the sidebar")
	}
	requireSidebarRail(t, tt)
	if got := settings.Get(settingsKeySidebarCollapsed, ""); got != "true" {
		t.Fatalf("persisted collapsed = %q after Cmd+B, want true", got)
	}
	r.SetRightPanelOpen(true)
	tt.Frame()
	tt.Frame()
	requireSidebarRail(t, tt)
	if !r.SidebarCollapsed() {
		t.Fatal("opening the inspector forgot the manual collapse")
	}

	// Closing the inspector must not expand a sidebar the user collapsed.
	r.SetRightPanelOpen(false)
	tt.Frame()
	requireSidebarRail(t, tt)
	if !r.SidebarCollapsed() {
		t.Fatal("closing the inspector expanded a manually collapsed sidebar")
	}

	// Cmd+B expands again at the narrow width; the inspector must not fight it.
	tt.Key(ui.Cmd, ui.KeyB)
	tt.Frame()
	if r.SidebarCollapsed() {
		t.Fatal("Cmd+B did not expand the collapsed sidebar")
	}
	requireSidebarExpanded(t, tt)
	if got := settings.Get(settingsKeySidebarCollapsed, ""); got != "false" {
		t.Fatalf("persisted collapsed = %q after expanding, want false", got)
	}

	// The automatic rail is layout-only: opening the inspector takes the rail,
	// but the persisted preference stays the user's expanded choice.
	r.SetRightPanelOpen(true)
	tt.Frame()
	tt.Frame()
	requireSidebarRail(t, tt)
	if r.SidebarCollapsed() {
		t.Fatal("the automatic rail wrote the collapsed preference")
	}
	if got := settings.Get(settingsKeySidebarCollapsed, ""); got != "false" {
		t.Fatalf("persisted collapsed = %q after the automatic rail, want the user's false", got)
	}
}
