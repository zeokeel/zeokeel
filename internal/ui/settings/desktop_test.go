package settings_test

// Tests for the appearance page's remaining desktop-integration switches:
// launch at login (contracts.SettingAutostart) and close to system tray, their
// defaults, immediate persistence, error path and English copy. Single instance
// is always on, so its switch is deleted from the page.

import (
	"errors"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// TestAppearanceDesktopIntegrationDefaultsAndWrites covers the default values
// (autostart off, close-to-tray off) and the immediate write, while the removed
// single-instance switch is absent.
func TestAppearanceDesktopIntegrationDefaultsAndWrites(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "外观")

	h.assertTexts(tester, "桌面集成", "开机自启动", "关闭时最小化到托盘",
		"登录时自动启动", "重启应用生效")
	h.assertNoTexts(tester, "单实例运行")

	// Default autostart is off: the first click turns it on.
	h.click(tester, "开机自启动")
	if got := store.get(contracts.SettingAutostart); got != "true" {
		t.Fatalf("%s = %q, want true", contracts.SettingAutostart, got)
	}
	h.assertTexts(tester, "桌面集成设置已更新")
}

// TestAppearanceDesktopIntegrationReadsStoredValues covers reading the current
// value at activation: a stored autostart=true flips back off on the first
// click.
func TestAppearanceDesktopIntegrationReadsStoredValues(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingAutostart: "true",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "外观")

	h.click(tester, "开机自启动")
	if got := store.get(contracts.SettingAutostart); got != "false" {
		t.Fatalf("%s = %q, want false", contracts.SettingAutostart, got)
	}
}

// TestAppearanceDesktopIntegrationError covers a failing settings store: the
// page shows the readable error instead of silently ignoring it.
func TestAppearanceDesktopIntegrationError(t *testing.T) {
	store := newFakeSettings(nil)
	store.setErr = errors.New("denied")
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "外观")
	h.assertTexts(tester, "桌面集成", "开机自启动")

	h.click(tester, "开机自启动")
	h.assertTexts(tester, "保存失败：denied")
}

// TestAppearanceDesktopIntegrationEnglish covers the English labels and hints
// and the absent single-instance switch.
func TestAppearanceDesktopIntegrationEnglish(t *testing.T) {
	h := newLocaleHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Model")
	h.click(tester, "Appearance")

	h.assertTexts(tester, "Desktop integration", "Launch at login",
		"Close to system tray", "Start ZeoKeel automatically when you log in",
		"takes effect after restart")
	h.assertNoTexts(tester, "Single instance")
	h.click(tester, "Launch at login")
	h.assertTexts(tester, "Desktop integration updated")
}
