package toasts

// Frame tests (written before the implementation, TDD) for the toast overlay
// module: rendering, dismissal, the four-toast cap, expiry and disposal.

import (
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakeNotifier captures the toast handler and records dismissed ids.
type fakeNotifier struct {
	mu        sync.Mutex
	handler   func(contracts.Notification)
	dismissed []string
}

func (f *fakeNotifier) SetHandler(fn func(contracts.Notification)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handler = fn
}

func (f *fakeNotifier) Notify(level, text string) contracts.Notification {
	return contracts.Notification{ID: "fake", Level: level, Text: text}
}

func (f *fakeNotifier) Dismiss(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dismissed = append(f.dismissed, id)
}

func (f *fakeNotifier) trigger(notification contracts.Notification) {
	f.mu.Lock()
	handler := f.handler
	f.mu.Unlock()
	if handler != nil {
		handler(notification)
	}
}

func (f *fakeNotifier) isCleared() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.handler == nil
}

func (f *fakeNotifier) dismissedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.dismissed...)
}

type harness struct {
	t        *testing.T
	uiRoot   *uiroot.Root
	notifier *fakeNotifier
	fiber    *cordis.Fiber
}

func newHarness(t *testing.T, language string) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel", Language: language})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	backend := &fakeNotifier{}
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide[contracts.Notifier](t, ctx, contracts.KeyNotifier, backend)
	if _, err := uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID:    "home",
		Title: "首页",
		Build: func(c *ui.Context) { ui.Text(c, "主屏幕") },
	}); err != nil {
		t.Fatalf("register home screen: %v", err)
	}
	h := &harness{t: t, uiRoot: uiRoot, notifier: backend}
	h.fiber = activate(t, ctx, Module(), Config{})
	return h
}

func (h *harness) tester() *ui.Tester {
	h.t.Helper()
	return ui.NewTester(h.uiRoot.View, 1200, 800)
}

func (h *harness) assertTexts(tester *ui.Tester, wanted ...string) {
	h.t.Helper()
	for _, want := range wanted {
		if !tester.HasText(want) {
			h.t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
}

func (h *harness) assertNoTexts(tester *ui.Tester, unwanted ...string) {
	h.t.Helper()
	for _, text := range unwanted {
		if tester.HasText(text) {
			h.t.Fatalf("frame unexpectedly shows %q; texts = %v", text, tester.Texts())
		}
	}
}

func (h *harness) click(tester *ui.Tester, label string) {
	h.t.Helper()
	if err := tester.Click(label); err != nil {
		h.t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

// withToastTTL overrides the auto-dismiss duration for one test.
func withToastTTL(t *testing.T, ttl time.Duration) {
	t.Helper()
	previous := toastTTL
	toastTTL = ttl
	t.Cleanup(func() { toastTTL = previous })
}

// --- tests ------------------------------------------------------------------

func TestToastRendersTextAndLevel(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyError, Text: "导出失败"})
	tester.Frame()
	h.assertTexts(tester, "导出失败", "错误", "×")
}

func TestToastCloseDismisses(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyInfo, Text: "提示消息"})
	tester.Frame()
	h.assertTexts(tester, "提示消息")

	h.click(tester, "×")
	h.assertNoTexts(tester, "提示消息")
	if ids := h.notifier.dismissedIDs(); len(ids) != 1 || ids[0] != "n1" {
		t.Fatalf("Dismiss calls = %v, want [n1]", ids)
	}
}

func TestToastMaxFourNewestWin(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	for index := 1; index <= 5; index++ {
		h.notifier.trigger(contracts.Notification{
			ID:    "n" + string(rune('0'+index)),
			Level: contracts.NotifyInfo,
			Text:  "消息" + string(rune('0'+index)),
		})
	}
	tester.Frame()

	h.assertTexts(tester, "消息2", "消息3", "消息4", "消息5")
	h.assertNoTexts(tester, "消息1")
}

func TestToastExpires(t *testing.T) {
	withToastTTL(t, 25*time.Millisecond)
	h := newHarness(t, "zh")
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifySuccess, Text: "自动消失"})
	tester.Frame()
	h.assertTexts(tester, "自动消失")

	deadline := time.Now().Add(2 * time.Second)
	for {
		tester.Frame()
		if !tester.HasText("自动消失") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("toast did not expire; texts = %v", tester.Texts())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestToastDisposeClearsHandler(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyInfo, Text: "待清理"})
	tester.Frame()

	if err := h.fiber.Dispose(); err != nil {
		t.Fatalf("dispose: %v", err)
	}
	if !h.notifier.isCleared() {
		t.Fatal("dispose did not clear the notifier handler")
	}
	tester.Frame()
	h.assertNoTexts(tester, "待清理")
}

func TestToastEnglishCopy(t *testing.T) {
	h := newHarness(t, "en")
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyWarning, Text: "careful"})
	tester.Frame()
	h.assertTexts(tester, "careful", "Warning")
}

// --- helpers ----------------------------------------------------------------

func provide[T any](t *testing.T, ctx cordis.Context, key cordis.ServiceKey[T], value T) {
	t.Helper()
	if _, err := ctx.Provide(key, value); err != nil {
		t.Fatalf("provide %s: %v", key.Name(), err)
	}
}

type descriptorPlugin struct {
	descriptor *module.Descriptor[Config]
	config     Config
	inject     []string
}

func (p *descriptorPlugin) Name() string { return p.descriptor.ID }

func (p *descriptorPlugin) Inject() []string { return p.inject }

func (p *descriptorPlugin) Activate(ctx cordis.Context, _ cordis.Value) error {
	return p.descriptor.Factory(ctx, p.config)
}

func activate(t *testing.T, ctx cordis.Context, descriptor *module.Descriptor[Config], config Config) *cordis.Fiber {
	t.Helper()
	plugin := &descriptorPlugin{descriptor: descriptor, config: config}
	fiber, err := ctx.Plugin(plugin, nil)
	if err != nil {
		t.Fatalf("activate %s: %v", descriptor.ID, err)
	}
	if state := fiber.State(); state != cordis.Active {
		t.Fatalf("module %s state = %s (%v), want Active", descriptor.ID, state, fiber.ErrorDetail())
	}
	return fiber
}
