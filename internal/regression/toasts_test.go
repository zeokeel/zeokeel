package regression

// Adversarial audit: the toasts UI module (overlay.toasts slot). Negative cases
// against the frame: more than four notifications (the stack caps and drops the
// oldest), close-button dismissal plus Notifier.Dismiss bookkeeping, TTL expiry
// (the real 4s TTL; the unexported variable is only overridable by the module's
// own package tests), empty payloads, a minimal host without a notifier and
// disposal cleanup.

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

// closeButtons counts the exact "×" labels (one per rendered toast).
func closeButtons(tester *ui.Tester) int {
	count := 0
	for _, text := range tester.Texts() {
		if text == "×" {
			count++
		}
	}
	return count
}

// TestToastsFourToastCap covers the stack bound: six notifications render only
// the newest four, in arrival order, and each has a close button.
func TestToastsFourToastCap(t *testing.T) {
	harness := startToastsUI(t)
	for index := 1; index <= 6; index++ {
		harness.raise(t, "notice-"+itoa(int64(index)), contracts.NotifyInfo, "toast-"+itoa(int64(index)))
	}
	harness.tester.Frame()

	// The oldest two are replaced; the newest four stay.
	requireNoText(t, harness.tester, "toast-1")
	requireNoText(t, harness.tester, "toast-2")
	for index := 3; index <= 6; index++ {
		requireHasText(t, harness.tester, "toast-"+itoa(int64(index)))
	}
	if got := closeButtons(harness.tester); got != 4 {
		t.Fatalf("close buttons = %d, want exactly 4 rendered toasts", got)
	}
	requireHasText(t, harness.tester, "toast-home-body") // the screen stays behind the overlay
}

// TestToastsCloseButtonDismisses covers the close action: the clicked (oldest)
// toast is removed, the rest stay, and the host notifier is told the ID.
func TestToastsCloseButtonDismisses(t *testing.T) {
	harness := startToastsUI(t)
	for index := 1; index <= 3; index++ {
		harness.raise(t, "close-"+itoa(int64(index)), contracts.NotifyInfo, "closable-"+itoa(int64(index)))
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "closable-1")

	if err := harness.tester.Click("×"); err != nil {
		t.Fatalf("click the first toast's close button: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "closable-1")
	requireHasText(t, harness.tester, "closable-2")
	requireHasText(t, harness.tester, "closable-3")
	dismissed := harness.notifier.dismissedIDs()
	if len(dismissed) != 1 || dismissed[0] != "close-1" {
		t.Fatalf("Dismiss calls = %v, want [close-1]", dismissed)
	}
}

// TestToastsTTLExpiry covers automatic expiry with the real production TTL: the
// toast disappears without user action. The unexported toastTTL cannot be
// shortened from this black-box package, so this test waits the real 4s.
func TestToastsTTLExpiry(t *testing.T) {
	harness := startToastsUI(t)
	harness.raise(t, "ttl-1", contracts.NotifyInfo, "expiring-toast")
	harness.tester.Frame()
	requireHasText(t, harness.tester, "expiring-toast")

	started := time.Now()
	deadline := started.Add(8 * time.Second)
	expired := false
	for time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
		harness.tester.Frame()
		if !harness.tester.HasText("expiring-toast") {
			expired = true
			break
		}
	}
	elapsed := time.Since(started)
	if !expired {
		t.Fatalf("toast did not expire within 8s (AUDIT: TTL not honoured); texts = %v", harness.tester.Texts())
	}
	if elapsed < 3*time.Second {
		t.Fatalf("toast expired after %s, want the 4s TTL", elapsed)
	}
	requireNoText(t, harness.tester, "expiring-toast")
	if dismissed := harness.notifier.dismissedIDs(); len(dismissed) == 0 {
		t.Logf("AUDIT-P3-11: auto-expiry does not call Notifier.Dismiss, so the host bridge keeps the entry (toasts.go removeToast vs notifier.go active map)")
	}
}

// TestToastsEmptyPayloadIgnored covers empty/whitespace toast payloads: they
// must not render a card or consume a slot.
func TestToastsEmptyPayloadIgnored(t *testing.T) {
	harness := startToastsUI(t)
	harness.raise(t, "empty-1", contracts.NotifyInfo, "   ")
	harness.raise(t, "empty-2", "", "")
	harness.tester.Frame()
	if got := closeButtons(harness.tester); got != 0 {
		t.Fatalf("empty notifications rendered %d toast cards, want 0", got)
	}
	requireHasText(t, harness.tester, "toast-home-body")
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
	if got := closeButtons(tester); got != 0 {
		t.Fatalf("close buttons = %d without a notifier, want 0", got)
	}
	for _, name := range h.ui.Runtime().Slots() {
		if name == contracts.SlotOverlayToasts {
			t.Fatalf("the toasts module registered %q without a notifier", name)
		}
	}
}

// TestToastsDisposalClearsHandler covers module disposal: the notifier handler is
// cleared and queued toasts are dropped on the next frame.
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
// uiroot.Notifier().Notify reaches the toast overlay; an empty payload does not.
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
	requireHasText(t, tester, "成功") // the success level badge
	if got := closeButtons(tester); got != 1 {
		t.Fatalf("close buttons = %d, want exactly the one real toast", got)
	}
}
