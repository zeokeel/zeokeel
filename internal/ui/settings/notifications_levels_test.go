package settings_test

// A-series cleanup: the per-level notification filter section is deleted
// (toasts always show every level), so the master toast switch stays the
// page's only control.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// TestNotificationsPageHasNoLevelFilter covers the removal: the filter section,
// its explanation and the four level check boxes are gone, while the master
// toast switch still renders and persists.
func TestNotificationsPageHasNoLevelFilter(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "通知")
	h.assertTexts(tester, "通知气泡", "显示通知气泡")
	h.assertNoTexts(tester, "按级别过滤", "按级别过滤通知气泡",
		"信息", "成功", "警告", "错误")
	h.click(tester, "显示通知气泡")
	if raw := store.get(contracts.SettingNotifications); raw != "false" {
		t.Fatalf("master toast switch wrote %q, want false", raw)
	}
}

// TestNotificationsPageHasNoLevelFilterEnglish is the English half of the
// removal assertions.
func TestNotificationsPageHasNoLevelFilterEnglish(t *testing.T) {
	store := newFakeSettings(nil)
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Notifications")
	h.assertTexts(tester, "Notification toasts", "Show notification toasts")
	h.assertNoTexts(tester, "Filter by level", "Filter toast bubbles by level",
		"Info", "Success", "Warning", "Error")
}
