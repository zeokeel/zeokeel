package settings_test

// Tests for the appearance page's global-hotkey input: the empty default, the
// saved/trimmed accelerator, current-value display, validation errors, the
// combination with the page's save action and the English copy.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

func TestAppearanceGlobalHotkeyDefaultsEmpty(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "外观")

	h.assertTexts(tester, "全局热键", "组合键 输入", "录入")
	joined := strings.Join(tester.Texts(), " ")
	for _, want := range []string{"留空关闭", "点「应用」并重启后生效"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("hotkey help does not mention %q; texts = %v", want, tester.Texts())
		}
	}
}

func TestAppearanceGlobalHotkeySavesTrimmedValue(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "外观")

	h.focusInput(tester, "组合键 输入")
	tester.Type("  Ctrl+Shift+K  ")
	tester.Frame()
	h.click(tester, "应用")

	if got := store.get(contracts.SettingGlobalHotkey); got != "Ctrl+Shift+K" {
		t.Fatalf("%s = %q, want the trimmed accelerator", contracts.SettingGlobalHotkey, got)
	}
	h.assertTexts(tester, "已保存", "Ctrl + Shift + K")
}

func TestAppearanceGlobalHotkeyRejectsInvalidValue(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "外观")

	h.focusInput(tester, "组合键 输入")
	tester.Type("not-a-hotkey")
	tester.Frame()
	h.click(tester, "应用")

	h.assertTexts(tester, "全局热键格式无效")
	if !strings.Contains(strings.Join(tester.Texts(), " "), "保存失败：") {
		t.Fatalf("invalid apply does not report the failure; texts = %v", tester.Texts())
	}
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

	h.assertTexts(tester, "全局热键", "Ctrl + Alt + K", "当前：Ctrl + Alt + K")
}

func TestAppearanceGlobalHotkeyEnglish(t *testing.T) {
	h := newLocaleHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Model")
	h.click(tester, "Appearance")

	h.assertTexts(tester, "Global hotkey", "Shortcut input", "Record")
	joined := strings.Join(tester.Texts(), " ")
	for _, want := range []string{"Empty disables it", "after Apply and a restart"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("hotkey help does not mention %q; texts = %v", want, tester.Texts())
		}
	}
	h.focusInput(tester, "Shortcut input")
	tester.Type("CmdOrCtrl+Shift+Space")
	tester.Frame()
	h.click(tester, "Apply")
	if got := h.settings.get(contracts.SettingGlobalHotkey); got != "CmdOrCtrl+Shift+Space" {
		t.Fatalf("%s = %q", contracts.SettingGlobalHotkey, got)
	}
	h.assertTexts(tester, "Saved")
}

// TestHotkeyRecordCapture covers H6's key recorder: clicking Record and pressing
// a combination fills the friendly accelerator form, and Esc cancels recording.
func TestHotkeyRecordCapture(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "外观")

	h.click(tester, "录入")
	h.assertTexts(tester, "按下组合键，Esc 取消")
	tester.Key(ui.Cmd|ui.Shift, ui.KeyK)
	tester.Frame()
	h.assertNoTexts(tester, "按下组合键，Esc 取消")
	h.assertTexts(tester, uikit.FormatShortcut("cmdorctrl+shift+k"), "未应用")
	h.click(tester, "应用")
	if got := store.get(contracts.SettingGlobalHotkey); got != "CmdOrCtrl+Shift+K" {
		t.Fatalf("recorded hotkey = %q, want CmdOrCtrl+Shift+K", got)
	}

	// Esc leaves the recorder without changing the draft.
	h.focusInput(tester, "组合键 输入")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
	h.click(tester, "录入")
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	h.assertNoTexts(tester, "按下组合键，Esc 取消")
}

// TestHotkeyPresetChips covers the one-click accelerator presets.
func TestHotkeyPresetChips(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "外观")
	h.click(tester, uikit.FormatShortcut("cmdorctrl+alt+space"))
	h.assertTexts(tester, "未应用")
	h.click(tester, "应用")
	if got := store.get(contracts.SettingGlobalHotkey); got != "CmdOrCtrl+Alt+Space" {
		t.Fatalf("preset hotkey = %q", got)
	}
}

// TestHotkeyInvalidDraftShowsInlineError covers the inline validation message.
func TestHotkeyInvalidDraftShowsInlineError(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "外观")
	h.focusInput(tester, "组合键 输入")
	tester.Type("banana")
	tester.Frame()
	h.assertTexts(tester, "全局热键格式无效")
}
