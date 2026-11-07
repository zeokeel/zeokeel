package settings_test

// Tests for the appearance page's global-hotkey input: the empty default, the
// saved/trimmed accelerator, current-value display, validation errors, the
// combination with the page's save action and the English copy.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

func TestAppearanceGlobalHotkeyDefaultsEmpty(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "外观")

	h.assertTexts(tester, "全局热键", "全局热键 输入", "CmdOrCtrl+Shift+Space",
		"留空关闭", "需要桌面支持", "修改后重启应用生效")
}

func TestAppearanceGlobalHotkeySavesTrimmedValue(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "外观")

	h.focusInput(tester, "全局热键 输入")
	tester.Type("  Ctrl+Shift+K  ")
	tester.Frame()
	h.click(tester, "应用")

	if got := store.get(contracts.SettingGlobalHotkey); got != "Ctrl+Shift+K" {
		t.Fatalf("%s = %q, want the trimmed accelerator", contracts.SettingGlobalHotkey, got)
	}
	h.assertTexts(tester, "已保存", "Ctrl+Shift+K")
}

func TestAppearanceGlobalHotkeyRejectsInvalidValue(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "外观")

	h.focusInput(tester, "全局热键 输入")
	tester.Type("not-a-hotkey")
	tester.Frame()
	h.click(tester, "应用")

	h.assertTexts(tester, "保存失败：", "全局热键格式无效")
	if got := store.get(contracts.SettingGlobalHotkey); got != "" {
		t.Fatalf("%s = %q after an invalid accelerator, want it unwritten", contracts.SettingGlobalHotkey, got)
	}
}

func TestAppearanceGlobalHotkeyShowsCurrentValue(t *testing.T) {
	store := newFakeSettings(map[string]string{contracts.SettingGlobalHotkey: "Ctrl+Alt+K"})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "外观")

	h.assertTexts(tester, "全局热键", "Ctrl+Alt+K", "当前：Ctrl+Alt+K")
}

func TestAppearanceGlobalHotkeyEnglish(t *testing.T) {
	h := newLocaleHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Model")
	h.click(tester, "Appearance")

	h.assertTexts(tester, "Global hotkey", "Global hotkey input", "CmdOrCtrl+Shift+Space",
		"Leave empty to disable", "requires desktop support", "takes effect after restart")
	h.focusInput(tester, "Global hotkey input")
	tester.Type("CmdOrCtrl+Shift+Space")
	tester.Frame()
	h.click(tester, "Apply")
	if got := h.settings.get(contracts.SettingGlobalHotkey); got != "CmdOrCtrl+Shift+Space" {
		t.Fatalf("%s = %q", contracts.SettingGlobalHotkey, got)
	}
	h.assertTexts(tester, "Saved")
}
