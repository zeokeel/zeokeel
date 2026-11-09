package toasts

// Frame tests for the notification bridge: notifications become mygo's native
// toasts (the framework owns fading, hover pause, Escape, announcements,
// stacking and the timeout), while the product's notification switches still
// filter levels.

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

// countText counts how often text shows in the frame.
func countText(tester *ui.Tester, text string) int {
	count := 0
	for _, shown := range tester.Texts() {
		if shown == text {
			count++
		}
	}
	return count
}

// --- native toasts ----------------------------------------------------------

func TestNotificationBecomesBrandedNativeToast(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyError, Text: "导出失败"})
	tester.Frame()
	// The native ToastBase renders our branded look: the level (icon + label),
	// the copy and a close control, all owned by the framework's toast.
	h.assertTexts(tester, "导出失败", "错误", "关闭通知")
	if got := countText(tester, "导出失败"); got != 1 {
		t.Fatalf("queued toast rendered %d times in one frame; texts = %v", got, tester.Texts())
	}

	// Further frames neither duplicate nor re-add the drained notification.
	tester.Frame()
	if got := countText(tester, "导出失败"); got != 1 {
		t.Fatalf("toast rendered %d times after two frames; texts = %v", got, tester.Texts())
	}
}

func TestToastLevelVisualsAndEnglishCopy(t *testing.T) {
	cases := []struct {
		level string
		label string
	}{
		{contracts.NotifyInfo, "提示"},
		{contracts.NotifySuccess, "成功"},
		{contracts.NotifyWarning, "警告"},
		{contracts.NotifyError, "错误"},
	}
	for _, tc := range cases {
		h := newHarness(t, "zh")
		tester := h.tester()
		h.notifier.trigger(contracts.Notification{ID: "n-" + tc.level, Level: tc.level, Text: "级别"})
		tester.Frame()
		h.assertTexts(tester, tc.label)
	}

	h := newHarness(t, "en")
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n-en", Level: contracts.NotifyError, Text: "export failed"})
	tester.Frame()
	h.assertTexts(tester, "Error", "Close notification", "export failed")
}

func TestToastActionRendersAndRuns(t *testing.T) {
	// contracts.ActionNotifier: the toast shows the action label and running it
	// calls the notification's callback on the UI thread, then closes the toast.
	h := newHarness(t, "zh")
	tester := h.tester()
	calls := 0
	h.notifier.trigger(contracts.Notification{
		ID: "n1", Level: contracts.NotifySuccess, Text: "已删除任务",
		ActionLabel: "撤销", Action: func() { calls++ },
	})
	tester.Frame()
	h.assertTexts(tester, "已删除任务", "撤销")

	if err := tester.Click("撤销"); err != nil {
		t.Fatalf("click toast action: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if calls != 1 {
		t.Fatalf("action calls = %d, want 1", calls)
	}
	h.assertNoTexts(tester, "已删除任务")
}

func TestToastCloseButtonDismisses(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyInfo, Text: "可关闭"})
	tester.Frame()
	h.assertTexts(tester, "可关闭")

	if err := tester.Click("关闭通知"); err != nil {
		t.Fatalf("click toast close: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertNoTexts(tester, "可关闭")
	if ids := h.notifier.dismissedIDs(); len(ids) != 1 || ids[0] != "n1" {
		t.Fatalf("Dismiss calls = %v, want [n1]", ids)
	}
}

func TestToastStackClearsComposerHeight(t *testing.T) {
	// The stack sits in the bottom-right corner above the conversation
	// composer: its copy ends at least the composer inset above the window
	// bottom, so a toast can never cover the send row.
	h := newHarness(t, "zh")
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyInfo, Text: "不遮挡输入区"})
	tester.Frame()
	rect, ok := tester.Find("不遮挡输入区")
	if !ok {
		t.Fatalf("toast text missing; texts = %v", tester.Texts())
	}
	const windowHeight = 800
	if bottom := rect.Y + rect.H; bottom > windowHeight-toastBottomInset+1 {
		t.Fatalf("toast bottom = %.1f, want it above the composer reserve (%.1f)", bottom, windowHeight-toastBottomInset)
	}
}

func TestNotificationLevelMapsToNativeType(t *testing.T) {
	cases := map[string]string{
		contracts.NotifyInfo:    "info",
		contracts.NotifySuccess: "success",
		contracts.NotifyWarning: "warning",
		contracts.NotifyError:   "error",
		"":                      "",
		"custom-level":          "",
		" ERROR ":               "error",
	}
	for level, want := range cases {
		if got := notificationType(level); got != want {
			t.Errorf("notificationType(%q) = %q, want %q", level, got, want)
		}
	}
}

func TestToastExpiresThroughTheFramework(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifySuccess, Text: "自动消失"})
	tester.Frame()
	h.assertTexts(tester, "自动消失")

	// The module keeps no TTL goroutine: advancing the tester's clock past the
	// toast timeout runs the framework's expiry frame.
	tester.Advance(5 * time.Second)
	tester.Frame()
	h.assertNoTexts(tester, "自动消失")
	if ids := h.notifier.dismissedIDs(); len(ids) == 0 || ids[0] != "n1" {
		t.Fatalf("native toast close did not dismiss the notifier entry; dismissed = %v", h.notifier.dismissedIDs())
	}
}

func TestToastShowsAtMostTwoAndQueuesRest(t *testing.T) {
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

	// H14: at most two toasts show at once; the rest wait in the module's queue
	// with a visible count so a burst never stacks over the transcript.
	h.assertTexts(tester, "消息1", "消息2", "+ 3 条通知排队")
	h.assertNoTexts(tester, "消息3", "消息4", "消息5")

	// As the front toasts expire, queued notifications take their slots.
	tester.Advance(5 * time.Second)
	tester.Frame()
	h.assertTexts(tester, "消息3", "消息4", "+ 1 条通知排队")
	h.assertNoTexts(tester, "消息1", "消息2", "消息5")

	tester.Advance(5 * time.Second)
	tester.Frame()
	h.assertTexts(tester, "消息5")
}

func TestToastDisposeClearsHandler(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyInfo, Text: "待清理"})
	tester.Frame()
	h.assertTexts(tester, "待清理")

	if err := h.fiber.Dispose(); err != nil {
		t.Fatalf("dispose: %v", err)
	}
	if !h.notifier.isCleared() {
		t.Fatal("dispose did not clear the notifier handler")
	}
	// A disposed module queues nothing new.
	h.notifier.trigger(contracts.Notification{ID: "n2", Level: contracts.NotifyInfo, Text: "不应出现"})
	tester.Advance(5 * time.Second)
	tester.Frame()
	h.assertNoTexts(tester, "待清理", "不应出现")
}

func TestToastIgnoresEmptyPayload(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyInfo, Text: "   "})
	tester.Frame()
	h.assertNoTexts(tester, "   ")
	h.assertTexts(tester, "主屏幕")
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

	// Hiding a level closes the native toast already showing it.
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

// --- dedupe by level+text ---------------------------------------------------

// TestDuplicateNotificationsMergeIntoOneToast covers C-18: the same level+text
// within the toast's life updates the toast in place (time reset, a quiet ×N)
// instead of stacking an identical row.
func TestDuplicateNotificationsMergeIntoOneToast(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifySuccess, Text: "对话已导出"})
	tester.Frame()
	h.notifier.trigger(contracts.Notification{ID: "n2", Level: contracts.NotifySuccess, Text: "对话已导出"})
	tester.Frame()
	if got := countText(tester, "对话已导出"); got != 1 {
		t.Fatalf("duplicate notification rendered %d toasts, want 1; texts = %v", got, tester.Texts())
	}
	h.assertTexts(tester, "×2")
	// The merged toast's time was reset: after 3+3 seconds it is still up.
	tester.Advance(3 * time.Second)
	tester.Frame()
	h.assertTexts(tester, "对话已导出")
	h.notifier.trigger(contracts.Notification{ID: "n3", Level: contracts.NotifySuccess, Text: "对话已导出"})
	tester.Frame()
	h.assertTexts(tester, "×3")
	tester.Advance(5 * time.Second)
	tester.Frame()
	h.assertNoTexts(tester, "对话已导出")
	// Closing the merged toast forgets every notifier entry it stood for.
	if ids := h.notifier.dismissedIDs(); len(ids) != 3 {
		t.Fatalf("Dismiss calls = %v, want the three merged ids", ids)
	}
}

// TestSameTextDifferentLevelsStaySeparate pins the key: a success and an error
// with the same words are different messages.
func TestSameTextDifferentLevelsStaySeparate(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifySuccess, Text: "完成"})
	h.notifier.trigger(contracts.Notification{ID: "n2", Level: contracts.NotifyError, Text: "完成"})
	tester.Frame()
	if got := countText(tester, "完成"); got != 2 {
		t.Fatalf("level-distinct notifications rendered %d rows, want 2; texts = %v", got, tester.Texts())
	}
	h.assertTexts(tester, "成功", "错误")
}

// TestDuplicateAfterExpiryStartsANewToast pins the TTL boundary: once the toast
// is gone, the same message shows as a fresh toast (no stale ×N).
func TestDuplicateAfterExpiryStartsANewToast(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	h.notifier.trigger(contracts.Notification{ID: "n1", Level: contracts.NotifyInfo, Text: "已复制"})
	tester.Frame()
	tester.Advance(5 * time.Second)
	tester.Frame()
	h.assertNoTexts(tester, "已复制")
	h.notifier.trigger(contracts.Notification{ID: "n2", Level: contracts.NotifyInfo, Text: "已复制"})
	tester.Frame()
	h.assertTexts(tester, "已复制")
	h.assertNoTexts(tester, "×2")
}

// TestMergedDuplicateKeepsTheLatestAction pins the action half: merging keeps a
// toast's button working and points it at the most recent duplicate.
func TestMergedDuplicateKeepsTheLatestAction(t *testing.T) {
	h := newHarness(t, "zh")
	tester := h.tester()
	undone := []string{}
	h.notifier.trigger(contracts.Notification{
		ID: "n1", Level: contracts.NotifySuccess, Text: "已删除任务",
		ActionLabel: "撤销", Action: func() { undone = append(undone, "first") },
	})
	tester.Frame()
	h.notifier.trigger(contracts.Notification{
		ID: "n2", Level: contracts.NotifySuccess, Text: "已删除任务",
		ActionLabel: "撤销", Action: func() { undone = append(undone, "second") },
	})
	tester.Frame()
	if got := countText(tester, "已删除任务"); got != 1 {
		t.Fatalf("merged action toast rendered %d times; texts = %v", got, tester.Texts())
	}
	if err := tester.Click("撤销"); err != nil {
		t.Fatalf("click merged action: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if len(undone) != 1 || undone[0] != "second" {
		t.Fatalf("merged toast action = %v, want the latest one", undone)
	}
}
