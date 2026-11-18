package regression

// Adversarial audit: the toasts UI module (overlay.toasts slot) now feeds
// mygo's native toast system. Negative cases against the frame: more
// notifications than show (the framework shows the newest three), the
// framework-owned 4s timeout (fast-forwarded with the Tester clock), empty
// payloads, a minimal host without a notifier and disposal cleanup.

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
	sidebarui "github.com/zeokeel/zeokeel/internal/ui/sidebar"
	"github.com/zeokeel/zeokeel/internal/ui/toasts"
)

// toastsHarness is one toast module instance over the headless product view.
type toastsHarness struct {
	h        *harness
	notifier *fakeNotifier
	fiber    *cordis.Fiber
	tester   *ui.Tester
}

// startToastsUI activates the real toasts module with the fake notifier and
// renders the product view.
func startToastsUI(t *testing.T) *toastsHarness {
	t.Helper()
	h := newBareHarness(t)
	notifier := &fakeNotifier{}
	provide(t, h.ctx, contracts.KeyNotifier, contracts.Notifier(notifier))
	fiber := activate(t, h.ctx, toasts.Module(), toasts.Config{})
	h.registerScreen("toast-home", "首页", "toast-home-body")
	harness := &toastsHarness{h: h, notifier: notifier, fiber: fiber, tester: h.tester(1200, 800)}
	harness.tester.Frame()
	return harness
}

// raise delivers one notification to the installed module handler.
func (th *toastsHarness) raise(t *testing.T, id, level, text string) {
	t.Helper()
	if !th.notifier.trigger(contracts.Notification{ID: id, Level: level, Text: text}) {
		t.Fatal("the toasts module did not install a notifier handler")
	}
}

// TestToastsShowAtMostTwoNewest covers H14's stack bound: the product shows at
// most two toasts at once, queues the rest in order (with a visible count) and
// fills freed slots as earlier toasts expire.
func TestToastsShowAtMostTwoNewest(t *testing.T) {
	harness := startToastsUI(t)
	for index := 1; index <= 6; index++ {
		harness.raise(t, "notice-"+itoa(int64(index)), contracts.NotifyInfo, "toast-"+itoa(int64(index)))
	}
	harness.tester.Frame()

	requireHasText(t, harness.tester, "toast-1")
	requireHasText(t, harness.tester, "toast-2")
	requireHasText(t, harness.tester, "+ 4 条通知排队")
	for index := 3; index <= 6; index++ {
		requireNoText(t, harness.tester, "toast-"+itoa(int64(index)))
	}
	requireHasText(t, harness.tester, "toast-home-body") // the screen stays behind the overlay

	// Expiry frees slots; the queue drains in order, two at a time.
	harness.tester.Advance(5 * time.Second)
	harness.tester.Frame()
	harness.tester.Frame()
	requireHasText(t, harness.tester, "toast-3")
	requireHasText(t, harness.tester, "toast-4")
	requireNoText(t, harness.tester, "toast-1")
	requireNoText(t, harness.tester, "toast-2")

	harness.tester.Advance(5 * time.Second)
	harness.tester.Frame()
	harness.tester.Frame()
	requireHasText(t, harness.tester, "toast-5")
	requireHasText(t, harness.tester, "toast-6")
}

// TestToastsExpiryDismissesNotifier covers the framework-owned timeout: the
// module starts no TTL goroutine, and the toast's close calls the host
// notifier's Dismiss so the bridge releases the entry.
func TestToastsExpiryDismissesNotifier(t *testing.T) {
	harness := startToastsUI(t)
	harness.raise(t, "ttl-1", contracts.NotifyInfo, "expiring-toast")
	harness.tester.Frame()
	requireHasText(t, harness.tester, "expiring-toast")

	// The native toast shows for 4s; the Tester's clock advances it instantly.
	harness.tester.Advance(5 * time.Second)
	harness.tester.Frame()
	requireNoText(t, harness.tester, "expiring-toast")
	if dismissed := harness.notifier.dismissedIDs(); len(dismissed) != 1 || dismissed[0] != "ttl-1" {
		t.Fatalf("Dismiss calls = %v, want [ttl-1]", dismissed)
	}
}

// TestToastsEmptyPayloadIgnored covers empty/whitespace toast payloads: they
// must not add a native toast.
func TestToastsEmptyPayloadIgnored(t *testing.T) {
	harness := startToastsUI(t)
	harness.raise(t, "empty-1", contracts.NotifyInfo, "   ")
	harness.raise(t, "empty-2", "", "")
	harness.tester.Frame()
	requireHasText(t, harness.tester, "toast-home-body")
	for _, text := range harness.tester.Texts() {
		if text == "" || text == "   " {
			t.Fatalf("empty notification rendered %q", text)
		}
	}
}

// TestToastsWithoutNotifierIsNoop covers a minimal host with no notifier bridge:
// activation does not fail, no overlay slot is registered and frames stay clean.
func TestToastsWithoutNotifierIsNoop(t *testing.T) {
	h := newBareHarness(t)
	activate(t, h.ctx, toasts.Module(), toasts.Config{})
	h.registerScreen("toast-home", "首页", "toast-home-body")
	tester := h.tester(1200, 800)
	tester.Frame()
	requireHasText(t, tester, "toast-home-body")
	for _, name := range h.ui.Runtime().Slots() {
		if name == contracts.SlotOverlayToasts {
			t.Fatalf("the toasts module registered %q without a notifier", name)
		}
	}
}

// TestToastsDisposalClearsHandler covers module disposal: the notifier handler is
// cleared; toasts already handed to the framework expire on their own and a
// late notification cannot reach the disposed module.
func TestToastsDisposalClearsHandler(t *testing.T) {
	harness := startToastsUI(t)
	harness.raise(t, "dispose-1", contracts.NotifyInfo, "dispose-me")
	harness.tester.Frame()
	requireHasText(t, harness.tester, "dispose-me")

	if err := harness.fiber.Dispose(); err != nil {
		t.Fatalf("dispose toasts module: %v", err)
	}
	if !harness.notifier.handlerCleared() {
		t.Fatal("the toasts module did not clear the notifier handler on disposal")
	}
	// The native toast lives in the window, so it shows out its own timeout
	// rather than being removed by the disposed module.
	harness.tester.Advance(5 * time.Second)
	harness.tester.Frame()
	requireNoText(t, harness.tester, "dispose-me")
	// A late notification cannot reach the disposed module.
	harness.notifier.SetHandler(nil)
	if harness.notifier.trigger(contracts.Notification{ID: "late", Text: "late-toast"}) {
		t.Fatal("the fake notifier still has a handler after disposal")
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "late-toast")
}

// TestToastsHostBridgeEndToEnd covers the real host bridge feeding the module:
// uiroot.Notifier().Notify reaches the native toast overlay; an empty payload
// does not.
func TestToastsHostBridgeEndToEnd(t *testing.T) {
	h := newBareHarness(t)
	provide(t, h.ctx, contracts.KeyNotifier, contracts.Notifier(h.ui.Notifier()))
	activate(t, h.ctx, toasts.Module(), toasts.Config{})
	h.registerScreen("toast-home", "首页", "toast-home-body")
	tester := h.tester(1200, 800)
	tester.Frame()

	h.ui.Notifier().Notify(contracts.NotifySuccess, "from the host bridge")
	h.ui.Notifier().Notify(contracts.NotifyError, "   ")
	tester.Frame()
	requireHasText(t, tester, "from the host bridge")
	for _, text := range tester.Texts() {
		if text == "   " {
			t.Fatalf("the empty notification rendered its payload")
		}
	}
}

// flushToasts drains the product's toast stack (two visible at a time) and its
// queue by fast-forwarding the framework timeout until no toast remains.
// flushToasts closes every toast on screen.
//
// It closes them through the close control rather than by advancing the clock:
// an ERROR toast is sticky by contract (B-spec §3.1.5, audit VD-02 — a failure
// the user missed leaves no trace), so the framework's timeout never clears one
// and a clock advance only empties the success/info/warning cards.
func flushToasts(harness *toastsHarness) {
	for attempt := 0; attempt < 8; attempt++ {
		closeInfo, ok := harness.tester.AccessByRole(ui.RoleButton, "关闭通知")
		if !ok {
			return
		}
		harness.tester.ClickAt(closeInfo.Bounds.X+closeInfo.Bounds.W/2,
			closeInfo.Bounds.Y+closeInfo.Bounds.H/2)
		harness.tester.Frame()
		harness.tester.Frame()
		// Park the pointer away from the stack: a toast under the pointer pauses
		// its own clock, so a click left resting there would keep the next card
		// alive past its timeout and the caller's clock advances would not expire
		// it.
		harness.tester.Move(4, 4)
		harness.tester.Frame()
	}
}

// TestToastActionEndToEnd covers the ActionNotifier transport end to end: the
// real host notifier's NotifyAction reaches the toasts module, the action label
// renders as the toast's button, and pressing it runs the undo callback on the
// UI thread and closes the toast.
func TestToastActionEndToEnd(t *testing.T) {
	h := newBareHarness(t)
	provide(t, h.ctx, contracts.KeyNotifier, contracts.Notifier(h.ui.Notifier()))
	fiber := activate(t, h.ctx, toasts.Module(), toasts.Config{})
	t.Cleanup(func() { _ = fiber.Dispose() })
	h.registerScreen("toast-action-home", "首页", "toast-action-home-body")
	tester := h.tester(1280, 800)
	tester.Frame()

	calls := 0
	h.ui.Notifier().NotifyAction(contracts.NotifySuccess, "已删除任务", "撤销", func() { calls++ })
	tester.Frame()
	tester.Frame()
	requireHasText(t, tester, "已删除任务")
	requireHasText(t, tester, "撤销")

	if err := tester.Click("撤销"); err != nil {
		t.Fatalf("click toast action: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if calls != 1 {
		t.Fatalf("undo calls = %d, want 1", calls)
	}
	requireNoText(t, tester, "已删除任务")
}

// TestToastsStayClearOfTheInspectorAtNarrowWidth is the audit's narrow frame
// (VD-02) through the real product wiring: raise notices on a wide window,
// narrow it, open the inspector, and the stack must stop at the content column's
// edge instead of landing on the pane.
func TestToastsStayClearOfTheInspectorAtNarrowWidth(t *testing.T) {
	h := newBareHarness(t)
	h.ui.SetKernelContext(h.ctx)
	notifier := &fakeNotifier{}
	provide(t, h.ctx, contracts.KeyNotifier, contracts.Notifier(notifier))
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(newFakeChat()))
	activate(t, h.ctx, toasts.Module(), toasts.Config{})
	activate(t, h.ctx, sidebarui.Module(), sidebarui.Config{})
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	harness := &toastsHarness{h: h, notifier: notifier, tester: h.tester(1512, 944)}
	harness.tester.Frame()
	if err := h.ui.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	// The inspector counts only while the screen contributes its pane; the real
	// product's right panel does, so the probe registers one contribution.
	if _, err := h.ui.Runtime().RegisterSlotItem(contracts.SlotItem{
		Slot:  contracts.SlotSessionPanelRight,
		ID:    "probe.right",
		Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "pane") },
	}); err != nil {
		t.Fatalf("register inspector pane: %v", err)
	}
	harness.tester.Frame()

	harness.raise(t, "n1", contracts.NotifySuccess, "会话「后台完成的发布检查」已完成")
	harness.tester.Frame()
	harness.tester.Advance(200 * time.Millisecond)
	harness.tester.Frame()

	// Narrow the window and dock the inspector, as narrow-session-inspector does.
	harness.tester.SetSize(1024, 720)
	h.ui.SetRightPanelOpen(true)
	harness.tester.Frame()
	harness.tester.Frame()

	anchor := h.ui.Anchor()
	if anchor.Right <= design.Space4 {
		t.Fatalf("the stack's anchor does not make room for the inspector: %+v", anchor)
	}
	for _, info := range harness.tester.AccessAll() {
		if info.Role != ui.RoleStatus {
			continue
		}
		if limit := float32(1024) - anchor.Right; info.Bounds.X+info.Bounds.W > limit+0.5 {
			t.Fatalf("toast card right = %.1f reaches over the inspector (content column ends at %.1f)",
				info.Bounds.X+info.Bounds.W, limit)
		}
	}
}

// TestToastsHangFromTheTopOnTheEmptyConversation covers the hero placement: with
// an empty transcript the bottom edge belongs to the setup card and the composer,
// so the host anchors the stack under the top band and no notice lands on a
// button (the landing frame had one on the send control).
func TestToastsHangFromTheTopOnTheEmptyConversation(t *testing.T) {
	h := newBareHarness(t)
	h.ui.SetKernelContext(h.ctx)
	notifier := &fakeNotifier{}
	provide(t, h.ctx, contracts.KeyNotifier, contracts.Notifier(notifier))
	chat := newFakeChat()
	// One empty conversation: the hero state the landing and a fresh session
	// share (no messages, so the composer carries the setup card).
	if id, err := chat.NewSession(""); err != nil || id == "" {
		t.Fatalf("seed an empty session: %v", err)
	}
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	activate(t, h.ctx, toasts.Module(), toasts.Config{})
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	harness := &toastsHarness{h: h, notifier: notifier, tester: h.tester(1512, 944)}
	harness.tester.Frame()
	if err := h.ui.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	harness.tester.Frame()
	anchor := h.ui.Anchor()
	if anchor.FromBottom {
		t.Fatalf("empty conversation anchor = %+v, want the top edge", anchor)
	}
	harness.raise(t, "n1", contracts.NotifyInfo, "首页通知")
	harness.tester.Frame()
	harness.tester.Advance(200 * time.Millisecond)
	harness.tester.Frame()
	for _, info := range harness.tester.AccessAll() {
		if info.Role != ui.RoleStatus {
			continue
		}
		if info.Bounds.Y < anchor.Top-1 || info.Bounds.Y > anchor.Top+1 {
			t.Fatalf("toast top = %.1f, want it under the band at %.1f", info.Bounds.Y, anchor.Top)
		}
	}
}
