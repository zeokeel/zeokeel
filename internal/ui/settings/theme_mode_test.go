package settings_test

// Tests for the appearance page's three-state theme mode: light/dark/system
// selection, the legacy ui.theme.dark fallback, bad-value normalization,
// persistence and the English copy. The old dark-mode toggle test lives in
// settings_test.go and is updated with this change.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

func TestAppearanceThemeModeDefaultLightAndSwitching(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "外观")

	h.assertTexts(tester, "主题", "浅色", "深色", "跟随系统",
		"修改立即生效", "跟随系统由桌面外观决定")

	h.click(tester, "深色")
	if !h.uiRoot.Theme().Dark() {
		t.Fatal("selecting 深色 did not switch the host theme")
	}
	if got := store.get(contracts.SettingThemeMode); got != "dark" {
		t.Fatalf("%s = %q, want dark", contracts.SettingThemeMode, got)
	}
	if got := store.get(contracts.SettingsUIThemeDark); got != "true" {
		t.Fatalf("%s = %q, want true", contracts.SettingsUIThemeDark, got)
	}

	h.click(tester, "跟随系统")
	if got := store.get(contracts.SettingThemeMode); got != "system" {
		t.Fatalf("%s = %q, want system", contracts.SettingThemeMode, got)
	}
	if got := store.get(contracts.SettingsUIThemeDark); got != "false" {
		t.Fatalf("legacy %s = %q, want the false placeholder", contracts.SettingsUIThemeDark, got)
	}

	h.click(tester, "浅色")
	if h.uiRoot.Theme().Dark() {
		t.Fatal("selecting 浅色 did not switch the host theme back")
	}
	if got := store.get(contracts.SettingThemeMode); got != "light" {
		t.Fatalf("%s = %q, want light", contracts.SettingThemeMode, got)
	}
}

func TestAppearanceThemeModeLegacyFallback(t *testing.T) {
	store := newFakeSettings(map[string]string{contracts.SettingsUIThemeDark: "true"})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "外观")
	h.click(tester, "应用")

	// The legacy dark flag normalizes to the dark mode and is persisted.
	if got := store.get(contracts.SettingThemeMode); got != "dark" {
		t.Fatalf("%s = %q, want dark from the legacy flag", contracts.SettingThemeMode, got)
	}
}

func TestAppearanceThemeModeBadValueNormalizesLight(t *testing.T) {
	store := newFakeSettings(map[string]string{contracts.SettingThemeMode: "banana"})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "外观")
	h.click(tester, "应用")
	if got := store.get(contracts.SettingThemeMode); got != "light" {
		t.Fatalf("%s = %q, want light", contracts.SettingThemeMode, got)
	}
}

func TestAppearanceThemeModeEnglish(t *testing.T) {
	store := newFakeSettings(nil)
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Model")
	h.click(tester, "Appearance")

	h.assertTexts(tester, "Theme", "Light", "Dark", "System",
		"Changes apply immediately", "follows the desktop appearance")
	h.click(tester, "Dark")
	if !h.uiRoot.Theme().Dark() {
		t.Fatal("selecting Dark did not switch the host theme")
	}
	if got := store.get(contracts.SettingThemeMode); got != "dark" {
		t.Fatalf("%s = %q, want dark", contracts.SettingThemeMode, got)
	}
}
