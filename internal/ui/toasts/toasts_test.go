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
	return newSettingsHarness(t, language, nil)
}

// newSettingsHarness activates the toasts module with an optional settings
// store (the ui.notifications switch is read from it on every frame).
func newSettingsHarness(t *testing.T, language string, settings contracts.SettingsService) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel", Language: language})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	backend := &fakeNotifier{}
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide[contracts.Notifier](t, ctx, contracts.KeyNotifier, backend)
	if settings != nil {
		provide[contracts.SettingsService](t, ctx, contracts.KeySettings, settings)
	}
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

// toastSettings is a mutex-protected settings store for the notification switch.
type toastSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func newToastSettings(raw string) *toastSettings {
	values := map[string]string{}
	if raw != "" {
		values[contracts.SettingNotifications] = raw
	}
	return &toastSettings{values: values}
}

func (s *toastSettings) Get(key, def string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return def
}

func (s *toastSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *toastSettings) All() map[string]string { return nil }

func (s *toastSettings) set(raw string) { _ = s.Set(contracts.SettingNotifications, raw) }

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
	// Expiry must release the notifier entry too (not just the local stack).
	deadline = time.Now().Add(2 * time.Second)
	for {
		for _, id := range h.notifier.dismissedIDs() {
			if id == "n1" {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("expired toast did not dismiss notifier id; dismissed = %v", h.notifier.dismissedIDs())
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

// --- notifications setting --------------------------------------------------

func TestToastSuppressedWhenNotificationsDisabled(t *testing.T) {
	settings := newToastSettings("false")
	h := newSettingsHarness(t, "zh", settings)
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyInfo, Text: "不该出现"})
	tester.Frame()
	h.assertNoTexts(tester, "不该出现", "提示")
}

func TestToastEnabledForTruthyAndUnknownValues(t *testing.T) {
	for _, raw := range []string{"", "true", "1", "yes", "bad-value", " TRUE "} {
		t.Run(raw, func(t *testing.T) {
			h := newSettingsHarness(t, "zh", newToastSettings(raw))
			tester := h.tester()
			h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyInfo, Text: "显示"})
			tester.Frame()
			h.assertTexts(tester, "显示")
		})
	}
}

func TestToastHiddenForFalsyValues(t *testing.T) {
	for _, raw := range []string{"false", "0", "no", " FALSE "} {
		t.Run(raw, func(t *testing.T) {
			h := newSettingsHarness(t, "zh", newToastSettings(raw))
			tester := h.tester()
			h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyInfo, Text: "隐藏"})
			tester.Frame()
			h.assertNoTexts(tester, "隐藏")
		})
	}
}

func TestToastSettingChangeAppliesNextFrame(t *testing.T) {
	settings := newToastSettings("true")
	h := newSettingsHarness(t, "zh", settings)
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyInfo, Text: "第一条"})
	tester.Frame()
	h.assertTexts(tester, "第一条")

	settings.set("false")
	h.notifier.trigger(contracts.Notification{ID: "n2", Level: contracts.NotifyInfo, Text: "第二条"})
	tester.Frame()
	h.assertNoTexts(tester, "第一条", "第二条")

	settings.set("true")
	h.notifier.trigger(contracts.Notification{ID: "n3", Level: contracts.NotifyInfo, Text: "第三条"})
	tester.Frame()
	h.assertTexts(tester, "第三条")
}

func TestToastWithoutSettingsShows(t *testing.T) {
	h := newSettingsHarness(t, "zh", nil)
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifySuccess, Text: "无设置也显示"})
	tester.Frame()
	h.assertTexts(tester, "无设置也显示")
}

// --- hidden levels ----------------------------------------------------------

// newHiddenSettings builds a settings store with both notification rows.
func newHiddenSettings(total, hidden string) *toastSettings {
	settings := newToastSettings(total)
	if hidden != "" {
		_ = settings.Set(contracts.SettingNotificationHiddenLevels, hidden)
	}
	return settings
}

func TestToastHiddenLevelIgnored(t *testing.T) {
	settings := newHiddenSettings("", `["success"]`)
	h := newSettingsHarness(t, "zh", settings)
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifySuccess, Text: "成功消息"})
	h.notifier.trigger(contracts.Notification{ID: "n2", Level: contracts.NotifyError, Text: "错误消息"})
	tester.Frame()
	h.assertTexts(tester, "错误消息")
	h.assertNoTexts(tester, "成功消息")
}

func TestToastHiddenMultipleLevels(t *testing.T) {
	settings := newHiddenSettings("", `["info","warning"]`)
	h := newSettingsHarness(t, "zh", settings)
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyInfo, Text: "信息消息"})
	h.notifier.trigger(contracts.Notification{ID: "n2", Level: contracts.NotifySuccess, Text: "成功消息"})
	h.notifier.trigger(contracts.Notification{ID: "n3", Level: contracts.NotifyWarning, Text: "警告消息"})
	h.notifier.trigger(contracts.Notification{ID: "n4", Level: contracts.NotifyError, Text: "错误消息"})
	tester.Frame()
	h.assertTexts(tester, "成功消息", "错误消息")
	h.assertNoTexts(tester, "信息消息", "警告消息")
}

func TestToastBadHiddenJSONShowsAll(t *testing.T) {
	settings := newHiddenSettings("", "{bad-json")
	h := newSettingsHarness(t, "zh", settings)
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyWarning, Text: "坏 JSON 也显示"})
	tester.Frame()
	h.assertTexts(tester, "坏 JSON 也显示")
}

func TestToastHiddenLevelsCaseInsensitiveAndUnknown(t *testing.T) {
	settings := newHiddenSettings("", `[" SUCCESS "]`)
	h := newSettingsHarness(t, "zh", settings)
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: "Success", Text: "大小写隐藏"})
	h.notifier.trigger(contracts.Notification{ID: "n2", Level: "custom-level", Text: "未知级别显示"})
	tester.Frame()
	h.assertTexts(tester, "未知级别显示")
	h.assertNoTexts(tester, "大小写隐藏")
}

func TestToastHiddenLevelsLiveSwitch(t *testing.T) {
	settings := newHiddenSettings("", "")
	h := newSettingsHarness(t, "zh", settings)
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifySuccess, Text: "旧成功"})
	tester.Frame()
	h.assertTexts(tester, "旧成功")

	_ = settings.Set(contracts.SettingNotificationHiddenLevels, `["success"]`)
	tester.Frame()
	h.assertNoTexts(tester, "旧成功")

	h.notifier.trigger(contracts.Notification{ID: "n2", Level: contracts.NotifySuccess, Text: "新成功"})
	tester.Frame()
	h.assertNoTexts(tester, "新成功")

	_ = settings.Set(contracts.SettingNotificationHiddenLevels, "[]")
	h.notifier.trigger(contracts.Notification{ID: "n3", Level: contracts.NotifySuccess, Text: "再次成功"})
	tester.Frame()
	h.assertTexts(tester, "再次成功")
}

func TestToastTotalSwitchBeatsHiddenLevels(t *testing.T) {
	settings := newHiddenSettings("false", `["success"]`)
	h := newSettingsHarness(t, "zh", settings)
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyError, Text: "全部关闭"})
	tester.Frame()
	h.assertNoTexts(tester, "全部关闭")
}
