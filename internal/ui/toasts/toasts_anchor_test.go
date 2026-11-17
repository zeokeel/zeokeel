package toasts

// Frame tests for the host-anchored toast placement (2026-11-17 UI review,
// P2 toast placement / P1 toast yields to overlays): with the host's
// contracts.ToastAnchorService bound, the stack hangs below the screen header
// band at the main content column's right edge (before a docked inspector),
// and yields to the window's bottom-right corner while a modal or a
// module-reported overlay is open. Without the service the module keeps its
// default top-right placement. The queue count rides the same anchor.

import (
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// newAnchorHarness activates the toasts module against a kernel-bound UI root
// (the host provides the anchor service through SetKernelContext, as the
// desktop host does before the assembly loads) and a session-shaped screen:
// a header band with a tabs row and an inspector pane contribution.
func newAnchorHarness(t *testing.T) (*harness, *uiroot.Root) {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel", Language: "zh"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	backend := &fakeNotifier{}
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide[contracts.Notifier](t, ctx, contracts.KeyNotifier, backend)
	if _, err := uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID: "session", Title: "对话", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "会话正文") },
	}); err != nil {
		t.Fatalf("register session screen: %v", err)
	}
	for _, item := range []contracts.SlotItem{
		{Slot: contracts.SlotScreenHeader, ID: "session.header", Order: 10,
			Build: func(c *ui.Context) { ui.Text(c, "会话标题") }},
		{Slot: contracts.SlotScreenTabs, ID: "session.tabs", Order: 10,
			Build: func(c *ui.Context) { ui.Text(c, "会话页签") }},
		{Slot: contracts.SlotSessionPanelRight, ID: "session.right", Order: 10,
			Build: func(c *ui.Context) { ui.Text(c, "检查器正文") }},
	} {
		if _, err := uiRoot.Runtime().RegisterSlotItem(item); err != nil {
			t.Fatalf("register slot %s: %v", item.ID, err)
		}
	}
	// Bind the kernel before activation: the Root provides the anchor service
	// on it, and the module's optional lookup finds it.
	uiRoot.SetKernelContext(ctx)
	h := &harness{t: t, uiRoot: uiRoot, notifier: backend}
	h.fiber = activate(t, ctx, Module(), Config{})
	return h, uiRoot
}

// toastCard returns the first branded toast card's box (ToastBase's Root is
// the status element assistive technology sees).
func toastCard(t *testing.T, tester *ui.Tester) ui.Rect {
	t.Helper()
	info, ok := tester.AccessByRole(ui.RoleStatus, "")
	if !ok {
		t.Fatalf("no toast card on screen; texts = %v", tester.Texts())
	}
	return info.Bounds
}

// toastStackBottom returns the stack's lowest edge: the bottom toast card, or
// the queue count when one rides below the cards — both share the anchor.
func toastStackBottom(t *testing.T, tester *ui.Tester) float32 {
	t.Helper()
	bottom := float32(-1)
	for _, info := range tester.AccessAll() {
		if info.Role != ui.RoleStatus {
			continue
		}
		if b := info.Bounds.Y + info.Bounds.H; b > bottom {
			bottom = b
		}
	}
	if bottom < 0 {
		t.Fatalf("no toast card on screen; texts = %v", tester.Texts())
	}
	if queue, ok := tester.Find("+ 1 条通知排队"); ok {
		if b := queue.Y + queue.H; b > bottom {
			bottom = b
		}
	}
	return bottom
}

// TestToastAnchorHangsBelowHeaderBand is the resting placement: the stack
// starts below the two-row header band and keeps the stack's gap from the
// window's right edge (no inspector open).
func TestToastAnchorHangsBelowHeaderBand(t *testing.T) {
	h, _ := newAnchorHarness(t)
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyInfo, Text: "锚点测试"})
	tester.Frame()

	card := toastCard(t, tester)
	bandBottom := design.Space8 + design.Space2 + design.Space8 // header row + tabs row
	if card.Y < bandBottom {
		t.Fatalf("toast card top = %.1f, want it below the header band (bottom %.1f)", card.Y, bandBottom)
	}
	if right := card.X + card.W; right > 1200-design.Space4+0.5 {
		t.Fatalf("toast card right = %.1f, want it inside the window's Space4 gap (%.1f)", right, 1200-design.Space4)
	}
	if anchor := h.uiRoot.Anchor(); anchor.FromBottom {
		t.Fatalf("resting anchor yields to the bottom: %+v", anchor)
	}
}

// TestToastAnchorStopsBeforeDockedInspector keeps the stack out of the
// inspector pane: its right edge stays at or before the main column's right
// edge (window less the docked pane and the split divider).
func TestToastAnchorStopsBeforeDockedInspector(t *testing.T) {
	h, uiRoot := newAnchorHarness(t)
	tester := h.tester()
	uiRoot.SetRightPanelOpen(true)
	tester.Frame()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyInfo, Text: "锚点测试"})
	tester.Frame()

	card := toastCard(t, tester)
	columnRight := 1200 - design.InspectorWidth - design.Space1
	if right := card.X + card.W; right > columnRight+0.5 {
		t.Fatalf("toast card right = %.1f covers the docked inspector (main column right %.1f)", right, columnRight)
	}
	if anchor := uiRoot.Anchor(); anchor.FromBottom {
		t.Fatalf("a docked inspector must not yield the stack: %+v", anchor)
	}
}

// TestToastAnchorYieldsToModalAndOverlay covers the bottom-right yield: an
// open settings modal anchors the stack's bottom edge at the window's bottom
// gap, the queue count rides the same anchor, and closing the modal (or a
// module-reported overlay) restores the header anchor.
func TestToastAnchorYieldsToModalAndOverlay(t *testing.T) {
	h, uiRoot := newAnchorHarness(t)
	tester := h.tester()
	// Three notifications: two cards show, one waits in the visible queue.
	for _, id := range []string{"n1", "n2", "n3"} {
		h.notifier.trigger(contracts.Notification{ID: id, Level: contracts.NotifyInfo, Text: "消息" + id})
	}
	tester.Frame()

	uiRoot.Navigator().ToggleSettings()
	tester.Frame()
	bottomGap := 800 - design.Space4
	if bottom := toastStackBottom(t, tester); bottom < bottomGap-0.5 || bottom > bottomGap+0.5 {
		t.Fatalf("modal-open toast stack bottom = %.1f, want it anchored at the window's bottom gap %.1f",
			bottom, bottomGap)
	}
	queue, ok := tester.Find("+ 1 条通知排队")
	if !ok {
		t.Fatalf("queue count missing under the modal anchor; texts = %v", tester.Texts())
	}
	if queue.Y+queue.H < bottomGap-0.5 || queue.Y+queue.H > bottomGap+0.5 {
		t.Fatalf("queue count bottom = %.1f, want the stack's bottom anchor %.1f", queue.Y+queue.H, bottomGap)
	}

	// Closing the modal restores the header anchor.
	uiRoot.Navigator().CloseSettings()
	tester.Frame()
	if uiRoot.Anchor().FromBottom {
		t.Fatal("closing the modal did not restore the header anchor")
	}
	card := toastCard(t, tester)
	if card.Y < design.Space8+design.Space2+design.Space8 {
		t.Fatalf("restored toast top = %.1f, want it below the header band", card.Y)
	}

	// A module-reported overlay yields the stack exactly like the modal.
	uiRoot.ReportOverlay(true)
	tester.Frame()
	if bottom := toastStackBottom(t, tester); bottom < bottomGap-0.5 || bottom > bottomGap+0.5 {
		t.Fatalf("overlay-open toast stack bottom = %.1f, want the bottom anchor %.1f", bottom, bottomGap)
	}
	uiRoot.ReportOverlay(false)
	tester.Frame()
	if uiRoot.Anchor().FromBottom {
		t.Fatal("closing the reported overlay did not restore the header anchor")
	}
}

// TestToastDefaultPlacementWithoutAnchorService pins the fallback: a host that
// does not provide the anchor service keeps the module's own top-right place
// below the 72 DIP band plus the Space3 inset.
func TestToastDefaultPlacementWithoutAnchorService(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyInfo, Text: "锚点测试"})
	tester.Frame()

	card := toastCard(t, tester)
	if want := toastTopInset; card.Y < want-0.5 || card.Y > want+0.5 {
		t.Fatalf("default toast top = %.1f, want the module's own inset %.1f", card.Y, want)
	}
	if right := card.X + card.W; right > 1200-design.Space4+0.5 {
		t.Fatalf("default toast right = %.1f, want the window's Space4 gap", right)
	}
}
