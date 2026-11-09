package uiroot

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestReservedSlotsAppearInHostRegions is the end-to-end slot test: every
// reserved slot the host renders is registered with two or more contributors
// and asserted to appear inside the region the host owns (sidebar, top bar,
// screen surface, overlay), in Order/ID order.
func TestReservedSlotsAppearInHostRegions(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, contracts.ScreenSpec{
		ID: "home", Title: "首页", Order: 10,
		Build: func(c *ui.Context) {
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				ui.Text(c, "screen-body")
				// The session module will render its own reserved slot; the
				// host itself does not know session.trajectory.bar.
				r.Runtime().RenderSlot(c, contracts.SlotSessionTrajectoryBar)
			})
		},
	})

	mustSlot(t, r, textSlotItem(contracts.SlotSidebarPrimary, "primary", "sidebar-primary", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotSidebarNav, "nav-b", "sidebar-nav-b", 20))
	mustSlot(t, r, textSlotItem(contracts.SlotSidebarNav, "nav-a", "sidebar-nav-a", 10)) // registered after, sorted first
	mustSlot(t, r, textSlotItem(contracts.SlotSidebarTree, "tree", "sidebar-tree", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotSidebarFooter, "footer", "sidebar-footer", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenHeader, "header", "screen-header", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenActions, "actions", "screen-actions", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenFooter, "footer", "screen-footer", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotSessionTrajectoryBar, "bar", "traj-bar", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotOverlayToasts, "toast", "toast-text", 10))
	// A module slot the host does not render: registered, diagnosable, absent.
	mustSlot(t, r, textSlotItem("test.custom", "orphan", "custom-orphan-text", 10))

	tt := newTestTester(t, r, 1600, 1000)
	tt.Frame()

	// Sidebar region: brand row, primary actions, nav slot, tree, footer. The
	// destination rows are module contributions now, so the host paints no
	// screen list ("界面"/"首页") of its own.
	requireTextBefore(t, tt, "ZeoKeelTest", "sidebar-primary", "sidebar-nav-a", "sidebar-nav-b", "sidebar-tree", "sidebar-footer")
	// Screen region: header, actions, body, the module's trajectory bar, footer.
	requireTextBefore(t, tt, "screen-header", "screen-actions", "screen-body", "traj-bar", "screen-footer")
	// Overlay region is painted after the window content.
	requireTextBefore(t, tt, "screen-footer", "toast-text")
	requireNoText(t, tt, "custom-orphan-text")
	if got := r.Runtime().Slots(); indexOfText(got, "test.custom") < 0 {
		t.Fatalf("Slots() = %v does not diagnose the registered custom slot", got)
	}

	// Region geometry: the sidebar is a left column, the top bar sits above the
	// screen, and the screen header/body/footer stack in that order.
	requireRegionLeftOf(t, tt, "sidebar-nav-a", "screen-header")
	requireRegionAbove(t, tt, "screen-header", "screen-body")
	requireRegionAbove(t, tt, "screen-body", "traj-bar")
	requireRegionAbove(t, tt, "traj-bar", "screen-footer")

	for _, text := range []string{"sidebar-primary", "sidebar-tree", "sidebar-footer"} {
		rect, ok := tt.Find(text)
		if !ok {
			t.Fatalf("frame does not show sidebar text %q", text)
		}
		if rect.X+rect.W > 280 {
			t.Fatalf("%q is not inside the 280 DIP sidebar: x=%v w=%v", text, rect.X, rect.W)
		}
	}
	for _, text := range []string{"screen-header", "screen-body"} {
		rect, ok := tt.Find(text)
		if !ok {
			t.Fatalf("frame does not show screen-region text %q", text)
		}
		if rect.X < 280 {
			t.Fatalf("%q is not in the content column right of the sidebar: x=%v", text, rect.X)
		}
	}

	// The complete slot inventory is reported sorted for diagnostics.
	want := []string{
		"overlay.toasts", "screen.actions", "screen.footer", "screen.header",
		"session.trajectory.bar", "sidebar.footer", "sidebar.nav",
		"sidebar.primary", "sidebar.tree", "test.custom",
	}
	if got := r.Runtime().Slots(); !equalStrings(got, want) {
		t.Fatalf("Slots() = %v, want %v", got, want)
	}
}

// TestCustomSlotRenderedByOwnerModule covers the child-slot mechanism: one
// module registers items into a slot it does not own, the owner module renders
// that slot with UIRuntime.RenderSlot from its own Build, and the host never
// renders it on its own.
func TestCustomSlotRenderedByOwnerModule(t *testing.T) {
	r := newTestRoot(t)
	const customSlot = "session.transcript.turn"

	// The host renders this item only if a module renders the slot itself.
	mustSlot(t, r, textSlotItem(customSlot, "turn-b", "turn-b-text", 20))
	unregA := mustSlot(t, r, textSlotItem(customSlot, "turn-a", "turn-a-text", 10))
	mustSlot(t, r, textSlotItem(customSlot, "turn-a2", "turn-a2-text", 10)) // Order tie, ID breaks it

	unregisterOwner := mustScreen(t, r, contracts.ScreenSpec{
		ID: "session", Title: "会话", Order: 10,
		Build: func(c *ui.Context) {
			ui.Text(c, "session-body")
			r.Runtime().RenderSlot(c, customSlot)
		},
	})
	if got := r.Runtime().Slots(); indexOfText(got, customSlot) < 0 {
		t.Fatalf("Slots() = %v does not contain the custom slot", got)
	}

	tt := newTestTester(t, r, 1400, 900)
	tt.Frame()
	requireTextBefore(t, tt, "session-body", "turn-a-text", "turn-a2-text", "turn-b-text")

	// Unregistering one contributor removes only its entry; order is preserved.
	requireHasText(t, tt, "turn-a-text")
	unregA()
	tt.Frame()
	requireNoText(t, tt, "turn-a-text")
	requireTextBefore(t, tt, "turn-a2-text", "turn-b-text")

	// Unregistering the owner removes the whole slot's contribution from the
	// frame (no host fallback for custom slots) and leaves the no-screen state.
	unregisterOwner()
	tt.Frame()
	requireNoText(t, tt, "session-body")
	requireNoText(t, tt, "turn-b-text")
	requireHasText(t, tt, "未注册界面") // locale zh: status.no-screen
}

// TestSlotItemPanicIsContained covers a panicking slot contributor: the host
// contains it and still renders the remaining contributors of the slot.
func TestSlotItemPanicIsContained(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustSlot(t, r, contracts.SlotItem{Slot: contracts.SlotSidebarNav, ID: "bad", Order: 10,
		Build: func(*ui.Context) { panic("slot item boom") }})
	mustSlot(t, r, textSlotItem(contracts.SlotSidebarNav, "good", "sidebar-good", 20))

	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()

	requireHasText(t, tt, "body-home")
	requireHasText(t, tt, "sidebar-good")
	requireTextBefore(t, tt, "sidebar-good", "body-home")
}
