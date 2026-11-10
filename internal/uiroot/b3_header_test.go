package uiroot

// Frame tests for the B3 top band: a screen's header/action/tab contributions
// merge into one quiet band with the host's palette and inspector controls, a
// screen with no such contribution gets no top band at all (and no back
// control), and namespaced contributions stay on their own screen.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestScreenBandOnlyWhenSlotsContribute covers the empty state: without any
// screen contribution there is no top band, so no palette/inspector row and no
// back control sit above the screen body; registering a header contribution
// brings the band (with the palette entry) back.
func TestScreenBandOnlyWhenSlotsContribute(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, contracts.ScreenSpec{
		ID: "home", Title: "首页", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "body-home") },
	})
	tt := newTestTester(t, r, 1280, 800)
	tt.Frame()

	body, ok := tt.Find("body-home")
	if !ok {
		t.Fatalf("screen body missing; texts = %v", tt.Texts())
	}
	if body.Y > 100 {
		t.Fatalf("screen body at Y=%.1f: an empty top band still takes room", body.Y)
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "快捷操作"); ok {
		t.Fatalf("palette entry rendered without a screen band; texts = %v", tt.Texts())
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "返回上一个页面"); ok {
		t.Fatal("the window still exposes a back control")
	}

	// A header contribution owns the band: the header text and the host palette
	// entry render on one row above the body.
	unregister := mustSlot(t, r, textSlotItem(contracts.SlotScreenHeader, "hdr", "screen-title", 10))
	tt.Frame()
	header, ok := tt.Find("screen-title")
	if !ok {
		t.Fatalf("screen header contribution missing; texts = %v", tt.Texts())
	}
	if body, _ := tt.Find("body-home"); header.Y >= body.Y {
		t.Fatalf("band header at Y=%.1f is not above the body at Y=%.1f", header.Y, body.Y)
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "快捷操作"); !ok {
		t.Fatalf("palette entry missing from the band; texts = %v", tt.Texts())
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "返回上一个页面"); ok {
		t.Fatal("the band still exposes a back control")
	}

	// Unregistering the contribution empties the band again.
	unregister()
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleButton, "快捷操作"); ok {
		t.Fatalf("palette entry lingered after the band emptied; texts = %v", tt.Texts())
	}
}

// TestScreenBandScopesNamespacedItems covers the screen scoping of band slots: a
// namespaced item ID belongs to that screen only, while an unnamespaced ID is a
// host/global contribution.
func TestScreenBandScopesNamespacedItems(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, contracts.ScreenSpec{
		ID: "home", Title: "首页", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "body-home") },
	})
	mustScreen(t, r, contracts.ScreenSpec{
		ID: sessionScreenID, Title: "会话", Order: 20,
		Build: func(c *ui.Context) { ui.Text(c, "body-session") },
	})
	mustSlot(t, r, textSlotItem(contracts.SlotScreenHeader, "global-header", "global-header-text", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenHeader, "session.title", "session-title-text", 20))
	if err := r.Navigator().Push("home"); err != nil {
		t.Fatalf("Push(home): %v", err)
	}
	tt := newTestTester(t, r, 1280, 800)
	tt.Frame()

	// Home: the global header renders; the session's own title stays away.
	requireHasText(t, tt, "global-header-text")
	requireNoText(t, tt, "session-title-text")
	// The session screen: both contributions show.
	if err := r.Navigator().Push(sessionScreenID); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	tt.Frame()
	requireHasText(t, tt, "global-header-text")
	requireHasText(t, tt, "session-title-text")
	requireHasText(t, tt, "body-session")

	// With only the namespaced contribution registered, Home has no band at all
	// (the session's chrome must never appear on another screen).
	if err := r.Navigator().Push("home"); err != nil {
		t.Fatalf("Push(home): %v", err)
	}
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleButton, "快捷操作"); !ok {
		t.Fatalf("band with the global header missing; texts = %v", tt.Texts())
	}
	// Unregister the global one through a fresh root registration cleanup: the
	// mustSlot cleanup only runs at test end, so emulate by checking the scoped
	// item directly on a root with just that item.
	scoped := newTestRoot(t)
	mustScreen(t, scoped, contracts.ScreenSpec{
		ID: "home", Title: "首页", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "body-home") },
	})
	mustScreen(t, scoped, contracts.ScreenSpec{
		ID: sessionScreenID, Title: "会话", Order: 20,
		Build: func(c *ui.Context) { ui.Text(c, "body-session") },
	})
	mustSlot(t, scoped, textSlotItem(contracts.SlotScreenHeader, "session.title", "session-title-text", 20))
	if err := scoped.Navigator().Push("home"); err != nil {
		t.Fatalf("Push(home): %v", err)
	}
	scopedTT := newTestTester(t, scoped, 1280, 800)
	scopedTT.Frame()
	if _, ok := scopedTT.AccessByRole(ui.RoleButton, "快捷操作"); ok {
		t.Fatalf("Home got a band from another screen's namespaced contribution; texts = %v", scopedTT.Texts())
	}
	requireNoText(t, scopedTT, "session-title-text")
	if err := scoped.Navigator().Push(sessionScreenID); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	scopedTT.Frame()
	requireHasText(t, scopedTT, "session-title-text")
}

// TestScreenBandTabsSecondRow covers the two-row band: the header row carries
// the screen header, actions, palette and inspector entries; a tab contribution
// adds a second row directly under it and above the screen body.
func TestScreenBandTabsSecondRow(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, contracts.ScreenSpec{
		ID: sessionScreenID, Title: "会话", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "body-session") },
	})
	mustSlot(t, r, textSlotItem(contracts.SlotSessionPanelRight, "pane", "inspector-pane", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenHeader, "hdr", "screen-title", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenActions, "acts", "screen-action", 20))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenTabs, "tabs", "screen-tab", 10))
	tt := newTestTester(t, r, 1280, 800)
	tt.Frame()

	header, ok1 := tt.Find("screen-title")
	action, ok2 := tt.Find("screen-action")
	tab, ok3 := tt.Find("screen-tab")
	body, ok4 := tt.Find("body-session")
	if !ok1 || !ok2 || !ok3 || !ok4 {
		t.Fatalf("band nodes missing; texts = %v", tt.Texts())
	}
	// Header and actions share the 44 DIP first row.
	if header.Y+header.H <= action.Y || action.Y+action.H <= header.Y {
		t.Fatalf("header at %+v and action at %+v are not on one row", header, action)
	}
	// The tabs row sits under the header row and above the body.
	if tab.Y < action.Y {
		t.Fatalf("tabs at Y=%.1f are not below the header row at Y=%.1f", tab.Y, action.Y)
	}
	if tab.Y >= body.Y {
		t.Fatalf("tabs at Y=%.1f are not above the body at Y=%.1f", tab.Y, body.Y)
	}
	// The host controls render in the first row: palette always, inspector on
	// the screen that contributes its pane.
	palette, ok := tt.AccessByRole(ui.RoleButton, "快捷操作")
	if !ok {
		t.Fatalf("palette entry missing; texts = %v", tt.Texts())
	}
	if palette.Bounds.Y > action.Y+action.H {
		t.Fatalf("palette at %+v is not on the header row %+v", palette.Bounds, action)
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "打开右侧栏"); !ok {
		t.Fatalf("inspector toggle missing from the band; texts = %v", tt.Texts())
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "返回上一个页面"); ok {
		t.Fatal("the band still exposes a back control")
	}
}
