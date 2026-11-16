package settings_test

// Frame tests for UX-11 (2026-11 wave D): saving a global hotkey that conflicts
// with a chrome shortcut (the command palette's Ctrl/Cmd+K, the sidebar toggle,
// or a fixed window chord) warns visibly and requires a second confirmation
// before it overwrites — the personal-scale ruling (warning +二次确认, not a
// hard block). The direct-input path is the gap the audit proved (the record
// path is intercepted shortcut-first by the palette).

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// TestAppearanceHotkeyConflictWarns covers the visible warning on the
// direct-input path: typing the palette's own chord names the conflict.
func TestAppearanceHotkeyConflictWarns(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "外观")

	h.focusInput(tester, "组合键 输入")
	tester.Type("Cmd+K")
	tester.Frame()

	joined := strings.Join(tester.Texts(), " ")
	if !strings.Contains(joined, "冲突") || !strings.Contains(joined, "命令面板") {
		t.Fatalf("UX-11: typing the palette chord shows no conflict warning; texts = %v", tester.Texts())
	}
}

// TestAppearanceHotkeyConflictRequiresConfirm covers the two-step save: the
// first Save arms the confirmation and writes nothing; the second Save writes.
func TestAppearanceHotkeyConflictRequiresConfirm(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "外观")

	h.focusInput(tester, "组合键 输入")
	tester.Type("Cmd+K")
	tester.Frame()

	// First Save: conflict armed, nothing persisted, the status asks to confirm.
	h.click(tester, "保存")
	if got := store.get(contracts.SettingGlobalHotkey); got != "" {
		t.Fatalf("UX-11: the first Save persisted a conflicting hotkey %q without confirmation", got)
	}
	if !strings.Contains(strings.Join(tester.Texts(), " "), "再次") {
		t.Fatalf("UX-11: the first Save does not ask for a second confirmation; texts = %v", tester.Texts())
	}

	// Second Save: confirmed, the hotkey is persisted.
	h.click(tester, "保存")
	if got := store.get(contracts.SettingGlobalHotkey); got != "Cmd+K" {
		t.Fatalf("UX-11: the confirmed Save did not persist the hotkey: %q", got)
	}
}

// TestAppearanceHotkeyNoConflictSavesOnce is the contrast: a non-conflicting
// chord saves on the first Save (no spurious confirmation gate).
func TestAppearanceHotkeyNoConflictSavesOnce(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "外观")

	h.focusInput(tester, "组合键 输入")
	tester.Type("Ctrl+Shift+Space")
	tester.Frame()
	h.click(tester, "保存")
	if got := store.get(contracts.SettingGlobalHotkey); got != "Ctrl+Shift+Space" {
		t.Fatalf("a non-conflicting hotkey did not save on the first Save: %q", got)
	}
}
