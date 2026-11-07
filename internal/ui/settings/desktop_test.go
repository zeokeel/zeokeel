package settings_test

// Tests for the appearance page's desktop-integration switches: launch at login
// (contracts.SettingAutostart) and single-instance (contracts.SettingSingleInstance),
// their defaults, immediate persistence, error path and English copy.

import (
	"errors"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// TestAppearanceDesktopIntegrationDefaultsAndWrites covers the default values
// (autostart off, single instance on) and the immediate writes of both switches.
func TestAppearanceDesktopIntegrationDefaultsAndWrites(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "外观")

	h.assertTexts(tester, "桌面集成", "开机自启动", "单实例运行",
		"登录时自动启动", "重启应用生效")

	// Default autostart is off: the first click turns it on.
	h.click(tester, "开机自启动")
	if got := store.get(contracts.SettingAutostart); got != "true" {
		t.Fatalf("%s = %q, want true", contracts.SettingAutostart, got)
	}
	// Default single instance is on: the first click turns it off.
	h.click(tester, "单实例运行")
	if got := store.get(contracts.SettingSingleInstance); got != "false" {
		t.Fatalf("%s = %q, want false", contracts.SettingSingleInstance, got)
	}
	h.assertTexts(tester, "桌面集成设置已更新")
}

// TestAppearanceDesktopIntegrationReadsStoredValues covers reading the current
// values at activation: a stored autostart=true and singleInstance=false flip
// back on the first click.
func TestAppearanceDesktopIntegrationReadsStoredValues(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingAutostart:      "true",
		contracts.SettingSingleInstance: "false",
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
	h.click(tester, "单实例运行")
	if got := store.get(contracts.SettingSingleInstance); got != "true" {
		t.Fatalf("%s = %q, want true", contracts.SettingSingleInstance, got)
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

// TestAppearanceDesktopIntegrationEnglish covers the English labels and hints.
func TestAppearanceDesktopIntegrationEnglish(t *testing.T) {
	h := newLocaleHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Model")
	h.click(tester, "Appearance")

	h.assertTexts(tester, "Desktop integration", "Launch at login", "Single instance",
		"Start ZeoKeel automatically when you log in", "takes effect after restart")
	h.click(tester, "Launch at login")
	h.assertTexts(tester, "Desktop integration updated")
}
