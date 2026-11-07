package settings_test

// TDD tests (RED first) for the notification-toast settings page.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

func TestNotificationsPageDefaultsOn(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "通知")
	h.assertTexts(tester, "通知", "显示通知气泡", "关闭后导出、更新等提示不再以气泡显示")
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
	h.assertTexts(tester, "Notifications", "Show notification toasts",
		"When off, export and update notices stay in the app instead of showing toasts")
	h.click(tester, "Show notification toasts")
	if raw := store.get(contracts.SettingNotifications); raw != "false" {
		t.Fatalf("English toggle wrote %q, want false", raw)
	}
}
