package settings_test

// Tests for the appearance page's close-to-tray switch: default off, the
// to/from persistence of contracts.SettingCloseToTray, normalization of a bad
// stored value and the English copy.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

func TestAppearanceCloseToTrayDefaultsOffAndPersists(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "外观")

	h.assertTexts(tester, "系统托盘", "关闭时最小化到托盘", "需要系统托盘支持")

	h.click(tester, "关闭时最小化到托盘")
	if got := store.get(contracts.SettingCloseToTray); got != "true" {
		t.Fatalf("%s = %q after enabling, want true", contracts.SettingCloseToTray, got)
	}
	h.click(tester, "关闭时最小化到托盘")
	if got := store.get(contracts.SettingCloseToTray); got != "false" {
		t.Fatalf("%s = %q after disabling, want false", contracts.SettingCloseToTray, got)
	}
}

func TestAppearanceCloseToTrayNormalizesBadValue(t *testing.T) {
	store := newFakeSettings(map[string]string{contracts.SettingCloseToTray: "banana"})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "外观")

	// A bad stored value renders as off, so the first click enables it.
	h.click(tester, "关闭时最小化到托盘")
	if got := store.get(contracts.SettingCloseToTray); got != "true" {
		t.Fatalf("%s = %q, want true (the bad value must normalize to off)", contracts.SettingCloseToTray, got)
	}
}

func TestAppearanceCloseToTrayEnglish(t *testing.T) {
	h := newLocaleHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Model")
	h.click(tester, "Appearance")

	h.assertTexts(tester, "System tray", "Close to system tray", "Requires system tray support")
	h.click(tester, "Close to system tray")
	if got := h.settings.get(contracts.SettingCloseToTray); got != "true" {
		t.Fatalf("%s = %q after enabling, want true", contracts.SettingCloseToTray, got)
	}
}
