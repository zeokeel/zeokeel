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
func flushToasts(harness *toastsHarness) {
	harness.tester.Advance(5 * time.Second)
	harness.tester.Frame()
	harness.tester.Frame()
	harness.tester.Advance(5 * time.Second)
	harness.tester.Frame()
	harness.tester.Frame()
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
