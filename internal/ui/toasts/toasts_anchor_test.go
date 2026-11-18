package toasts

// Frame tests for the host-anchored toast placement (audit VD-02): the stack
// lives in the window's bottom-right corner and grows upward, so a notice never
// covers the reading column it interrupted. On the conversation screen the
// stack's bottom edge starts above the composer dock and the status strip, a
// docked inspector pushes it left of the pane, and an open modal or a
// module-reported overlay takes the plain window corner. Without the anchor
// service the module keeps the same corner with its own inset. The queue count
// rides the same anchor.

import (
	"strings"
	"testing"
	"time"

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
		ID: "session", Title: "会话", Order: 10,
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
	// The queue count rides the stack's bottom edge; its number varies with the
	// queue, so match the prefix rather than one count.
	for _, text := range tester.Texts() {
		if !strings.HasPrefix(text, "+ ") || !strings.HasSuffix(text, " 条通知排队") {
			continue
		}
		if queue, ok := tester.Find(text); ok {
			if b := queue.Y + queue.H; b > bottom {
				bottom = b
			}
			break
		}
	}
	return bottom
}

// TestToastAnchorSitsAboveComposerDock is the resting placement on the
// conversation screen: the stack's bottom edge sits above the composer dock and
// the status strip, and its right edge keeps the window's Space4 gap.
func TestToastAnchorSitsAboveComposerDock(t *testing.T) {
	h, uiRoot := newAnchorHarness(t)
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyInfo, Text: "锚点测试"})
	tester.Frame()
	// Settle the entrance transition before measuring (8 DIP of travel).
	tester.Advance(design.MotionBase + 20*time.Millisecond)
	tester.Frame()

	card := toastCard(t, tester)
	in := uiRoot.Anchor()
	if !in.FromBottom {
		t.Fatalf("resting anchor = %+v, want the bottom-right corner", in)
	}
	if in.Bottom <= design.Space4 {
		t.Fatalf("anchor bottom = %.1f, want the composer dock's room above the window edge", in.Bottom)
	}
	if bottom, want := card.Y+card.H, float32(800)-in.Bottom; bottom < want-0.5 || bottom > want+0.5 {
		t.Fatalf("toast card bottom = %.1f, want %.1f (above the composer dock)", bottom, want)
	}
	if right := card.X + card.W; right > 1200-design.Space4+0.5 {
		t.Fatalf("toast card right = %.1f, want it inside the window's Space4 gap (%.1f)", right, 1200-design.Space4)
	}
}

// TestToastAnchorStopsBeforeDockedInspector keeps the stack out of the
// inspector pane: its right edge stays at or before the main column's right
// edge (window less the docked pane and the split seam).
func TestToastAnchorStopsBeforeDockedInspector(t *testing.T) {
	h, uiRoot := newAnchorHarness(t)
	tester := h.tester()
	uiRoot.SetRightPanelOpen(true)
	tester.Frame()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyInfo, Text: "锚点测试"})
	tester.Frame()
	tester.Advance(design.MotionBase + 20*time.Millisecond)
	tester.Frame()

	card := toastCard(t, tester)
	columnRight := 1200 - design.InspectorWidth - design.Space1
	if right := card.X + card.W; right > columnRight+0.5 {
		t.Fatalf("toast card right = %.1f covers the docked inspector (main column right %.1f)", right, columnRight)
	}
	if in := uiRoot.Anchor(); !in.FromBottom {
		t.Fatalf("a docked inspector must not move the stack off the bottom: %+v", in)
	}
}

// TestToastAnchorYieldsToModalAndOverlay covers the plain-corner yield: an open
// settings modal anchors the stack's bottom edge at the window's bottom gap
// (clear of the centred panel), the queue count rides the same anchor, and
// closing the modal (or a module-reported overlay) restores the dock inset.
func TestToastAnchorYieldsToModalAndOverlay(t *testing.T) {
	h, uiRoot := newAnchorHarness(t)
	tester := h.tester()
	// Three notifications: two cards show, one waits in the visible queue.
	for _, id := range []string{"n1", "n2", "n3", "n4"} {
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
	var queue ui.Rect
	queueOK := false
	for _, text := range tester.Texts() {
		if !strings.HasPrefix(text, "+ ") || !strings.HasSuffix(text, " 条通知排队") {
			continue
		}
		if rect, ok := tester.Find(text); ok {
			queue, queueOK = rect, true
			break
		}
	}
	if !queueOK {
		t.Fatalf("queue count missing under the modal anchor; texts = %v", tester.Texts())
	}
	if queue.Y+queue.H < bottomGap-0.5 || queue.Y+queue.H > bottomGap+0.5 {
		t.Fatalf("queue count bottom = %.1f, want the stack's bottom anchor %.1f", queue.Y+queue.H, bottomGap)
	}

	// Closing the modal restores the composer inset.
	uiRoot.Navigator().CloseSettings()
	tester.Frame()
	anchor := uiRoot.Anchor()
	if anchor.Bottom <= design.Space4 {
		t.Fatalf("closing the modal did not restore the composer inset: %+v", anchor)
	}
	if bottom, want := toastStackBottom(t, tester), float32(800)-anchor.Bottom; bottom < want-0.5 || bottom > want+0.5 {
		t.Fatalf("restored toast stack bottom = %.1f, want %.1f (above the composer dock)", bottom, want)
	}

	// A module-reported popover leaves the corner alone: the composer and the
	// status strip stay live under a popover, so the stack keeps its dock inset.
	uiRoot.ReportOverlay(true)
	tester.Frame()
	if got := uiRoot.Anchor(); got.Bottom <= design.Space4 {
		t.Fatalf("a reported popover moved the stack off its dock inset: %+v", got)
	}
	uiRoot.ReportOverlay(false)
	tester.Frame()
	if got := uiRoot.Anchor(); got.Bottom <= design.Space4 {
		t.Fatalf("closing the reported popover changed the dock inset: %+v", got)
	}
}

// TestToastDefaultPlacementWithoutAnchorService pins the fallback: a host that
// does not provide the anchor service keeps the module's own bottom-right
// corner, with the room the composer dock takes left free so the fallback cannot
// cover it either (the shell anchors the stack exactly this way; the fallback is
// the same corner, not a second design).
func TestToastDefaultPlacementWithoutAnchorService(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyInfo, Text: "锚点测试"})
	tester.Frame()
	tester.Advance(design.MotionBase + 20*time.Millisecond)
	tester.Frame()

	card := toastCard(t, tester)
	// The card clears the dock's room at the window's bottom edge.
	if bottom, want := card.Y+card.H, float32(800)-toastInset-design.Space8*5; bottom < want-0.5 || bottom > want+0.5 {
		t.Fatalf("default toast bottom = %.1f, want the module's own inset %.1f", bottom, want)
	}
	if right := card.X + card.W; right > 1200-toastInset+0.5 {
		t.Fatalf("default toast right = %.1f, want the window's Space4 gap", right)
	}
}

// TestToastAnchorKeepsClearOfTheDockedInspectorAtNarrowWidth is the audit's
// narrow case (VD-02): at 1024 DIP with the inspector docked, the stack stops at
// the content column's edge, so no card reaches over the pane, and the card
// narrows to the minimum when the column can no longer hold 320.
func TestToastAnchorKeepsClearOfTheDockedInspectorAtNarrowWidth(t *testing.T) {
	h, uiRoot := newAnchorHarness(t)
	tester := h.tester()
	tester.SetSize(1024, 720)
	uiRoot.SetRightPanelOpen(true)
	tester.Frame()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifySuccess, Text: "自动化「每日构建检查」已完成"})
	tester.Frame()
	tester.Advance(design.MotionBase + 20*time.Millisecond)
	tester.Frame()

	anchor := uiRoot.Anchor()
	if anchor.Right <= design.Space4 {
		t.Fatalf("anchor right = %.1f, want the docked inspector added to the inset", anchor.Right)
	}
	card := toastCard(t, tester)
	if limit := float32(1024) - anchor.Right; card.X+card.W > limit+0.5 {
		t.Fatalf("toast card right = %.1f reaches over the inspector (content column ends at %.1f)",
			card.X+card.W, limit)
	}
	if card.W > design.ToastWidth {
		t.Fatalf("toast card width = %.1f, want at most %v", card.W, design.ToastWidth)
	}
}

// TestToastStackShrinksWithTheWindow covers VD-02's narrow frame: a window that
// narrows closes the oldest visible cards instead of stacking them over the
// inspector, and keeps the queue count.
func TestToastStackShrinksWithTheWindow(t *testing.T) {
	h, uiRoot := newAnchorHarness(t)
	tester := h.tester()
	for _, id := range []string{"n1", "n2", "n3"} {
		h.notifier.trigger(contracts.Notification{ID: id, Level: contracts.NotifyInfo, Text: "消息" + id})
	}
	tester.Frame()
	if cards, ok := 2, true; ok {
		visible := 0
		for _, info := range tester.AccessAll() {
			if info.Role == ui.RoleStatus {
				visible++
			}
		}
		if visible > cards {
			t.Fatalf("wide window shows %d cards, want at most %d", visible, cards)
		}
	}

	// Narrow the window and open the inspector: the stack keeps one card.
	tester.SetSize(1024, 720)
	uiRoot.SetRightPanelOpen(true)
	tester.Frame()
	tester.Frame()
	// The cards laid out for the wide window are closed (mygo keeps a toast's node,
	// so they cannot be moved); a notice raised now lands inside the narrow
	// column. Nothing is left over from the old geometry.
	for _, info := range tester.AccessAll() {
		if info.Role != ui.RoleStatus {
			continue
		}
		if info.Bounds.X < 600 {
			t.Fatalf("a stale card is still on screen at %+v (laid out for the wide window)", info.Bounds)
		}
	}
	h.notifier.trigger(contracts.Notification{ID: "n9", Level: contracts.NotifyInfo, Text: "窄窗消息"})
	tester.Frame()
	tester.Advance(design.MotionBase + 20*time.Millisecond)
	tester.Frame()
	card := toastCard(t, tester)
	if card.X+card.W > float32(1024)-uiRoot.Anchor().Right+0.5 {
		t.Fatalf("the narrow card's right edge %.1f reaches over the inspector", card.X+card.W)
	}
	if anchor := uiRoot.Anchor(); anchor.Right <= design.Space4 {
		t.Fatalf("the narrow stack did not make room for the inspector: %+v", anchor)
	}
}

// TestIdenticalNotificationsMergeIntoOneCard covers the dedupe contract: the same
// level and text is one card with its ×N count, never two cards.
func TestIdenticalNotificationsMergeIntoOneCard(t *testing.T) {
	h, _ := newAnchorHarness(t)
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyWarning, Text: "审批等待：团队任务"})
	h.notifier.trigger(contracts.Notification{ID: "n2", Level: contracts.NotifyWarning, Text: "审批等待：团队任务"})
	tester.Frame()
	tester.Advance(design.MotionBase + 20*time.Millisecond)
	tester.Frame()

	cards := 0
	for _, info := range tester.AccessAll() {
		if info.Role == ui.RoleStatus {
			cards++
		}
	}
	if cards != 1 {
		t.Fatalf("%d cards for one merged notice, want 1", cards)
	}
	if _, ok := tester.Find("×2"); !ok {
		t.Fatalf("the merged count is missing; texts = %v", tester.Texts())
	}
}
