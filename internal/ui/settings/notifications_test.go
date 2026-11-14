package settings_test

// TDD tests (RED first) for the notification-toast settings page.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

func TestNotificationsPageDefaultsOn(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "通知")
	h.assertTexts(tester, "通知", "显示通知气泡")
	if raw := store.get(contracts.SettingNotifications); raw != "" {
		t.Fatalf("defaults wrote the notification row: %q", raw)
	}

	h.click(tester, "显示通知气泡")
	if raw := store.get(contracts.SettingNotifications); raw != "false" {
		t.Fatalf("disabling wrote %q, want false", raw)
	}
	h.click(tester, "显示通知气泡")
	if raw := store.get(contracts.SettingNotifications); raw != "true" {
		t.Fatalf("enabling wrote %q, want true", raw)
	}
}

func TestNotificationsPageBadValueNormalizedOn(t *testing.T) {
	store := newFakeSettings(map[string]string{contracts.SettingNotifications: "garbage"})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "通知")
	// The bad stored value displays as on, so one click disables it.
	h.click(tester, "显示通知气泡")
	if raw := store.get(contracts.SettingNotifications); raw != "false" {
		t.Fatalf("toggle from a malformed value wrote %q, want false", raw)
	}
}

func TestNotificationsPageEnglishCopy(t *testing.T) {
	store := newFakeSettings(nil)
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Notifications")
	h.assertTexts(tester, "Notifications", "Show notification toasts")
	h.click(tester, "Show notification toasts")
	if raw := store.get(contracts.SettingNotifications); raw != "false" {
		t.Fatalf("English toggle wrote %q, want false", raw)
	}
}

// fakeSystemNotifier is a contracts.Notifier that also implements the optional
// contracts.SystemNotifier half, so the page renders the system switch enabled.
type fakeSystemNotifier struct{ supported bool }

func (f fakeSystemNotifier) SetHandler(func(contracts.Notification)) {}
func (f fakeSystemNotifier) Notify(level, text string) contracts.Notification {
	return contracts.Notification{}
}
func (f fakeSystemNotifier) Dismiss(string) {}
func (f fakeSystemNotifier) NotifySystem(level, text string) contracts.Notification {
	return contracts.Notification{}
}
func (f fakeSystemNotifier) SystemNotificationsSupported() bool { return f.supported }

// TestNotificationsPageSystemSwitchDefaultsOn covers the new system switch: a
// host with OS notification support shows it on by default and persists each
// toggle immediately, separately from the toast switch.
func TestNotificationsPageSystemSwitchDefaultsOn(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	provide(t, h.ctx, contracts.KeyNotifier, contracts.Notifier(fakeSystemNotifier{supported: true}))
	tester := h.tester()
	tester.Frame()
	h.click(tester, "通知")
	h.assertTexts(tester, "系统通知", "显示系统通知",
		"任务在后台完成或失败时显示系统通知",
		"点击通知会打开对应会话")
	if raw := store.get(contracts.SettingSystemNotifications); raw != "" {
		t.Fatalf("defaults wrote the system notification row: %q", raw)
	}

	h.click(tester, "显示系统通知")
	if raw := store.get(contracts.SettingSystemNotifications); raw != "false" {
		t.Fatalf("disabling system notifications wrote %q, want false", raw)
	}
	h.click(tester, "显示系统通知")
	if raw := store.get(contracts.SettingSystemNotifications); raw != "true" {
		t.Fatalf("enabling system notifications wrote %q, want true", raw)
	}
	// The toast switch stays independent.
	if raw := store.get(contracts.SettingNotifications); raw != "" {
		t.Fatalf("the system switch also wrote the toast row: %q", raw)
	}
}

// TestNotificationsPageSystemSwitchDisabledWhenUnsupported covers the platform
// fallback: without a host system-notification sink the switch is disabled with
// an explanation, and clicking it writes nothing.
func TestNotificationsPageSystemSwitchDisabledWhenUnsupported(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "通知")
	h.assertTexts(tester, "系统通知", "显示系统通知",
		"当前平台不支持系统通知；应用内通知气泡仍然可用。")
	access, ok := tester.AccessByRole(ui.RoleSwitch, "显示系统通知")
	if !ok {
		t.Fatal("the system-notification switch is not in the accessibility tree")
	}
	if !access.Disabled {
		t.Fatal("the system-notification switch is enabled on an unsupported platform")
	}
	h.click(tester, "显示系统通知")
	if raw := store.get(contracts.SettingSystemNotifications); raw != "" {
		t.Fatalf("clicking the disabled switch wrote %q", raw)
	}
}

// TestNotificationsPageSystemSwitchBadValueNormalizedOn covers a malformed
// stored value: it displays as on, so one click disables it.
func TestNotificationsPageSystemSwitchBadValueNormalizedOn(t *testing.T) {
	store := newFakeSettings(map[string]string{contracts.SettingSystemNotifications: "garbage"})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	provide(t, h.ctx, contracts.KeyNotifier, contracts.Notifier(fakeSystemNotifier{supported: true}))
	tester := h.tester()
	tester.Frame()
	h.click(tester, "通知")
	h.click(tester, "显示系统通知")
	if raw := store.get(contracts.SettingSystemNotifications); raw != "false" {
		t.Fatalf("toggle from a malformed system value wrote %q, want false", raw)
	}
}

// TestNotificationsPageSystemSwitchEnglishCopy covers the English labels and
// the unsupported-platform explanation.
func TestNotificationsPageSystemSwitchEnglishCopy(t *testing.T) {
	store := newFakeSettings(nil)
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	provide(t, h.ctx, contracts.KeyNotifier, contracts.Notifier(fakeSystemNotifier{supported: true}))
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Notifications")
	h.assertTexts(tester, "System notifications", "Show system notifications",
		"Clicking a notification opens its conversation")
	h.click(tester, "Show system notifications")
	if raw := store.get(contracts.SettingSystemNotifications); raw != "false" {
		t.Fatalf("English system toggle wrote %q, want false", raw)
	}

	unsupportedStore := newFakeSettings(nil)
	unsupported := newLocaleHarness(t, &fakeChat{}, unsupportedStore, settings.Config{}, "en")
	unsupportedTester := unsupported.tester()
	unsupportedTester.Frame()
	unsupported.click(unsupportedTester, "Notifications")
	unsupported.assertTexts(unsupportedTester,
		"System notifications are not available on this platform; in-app toasts still work.")
}
