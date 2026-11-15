package settings_test

// Frame tests for the 能力 / Capabilities settings page: the delegation and shell
// limits (1–8 concurrency, 5–600s timeout, 0/default or KiB output) load from
// the stored rows, the fixed footer's Save is disabled while the drafts are
// clean, saving persists the rows (defaults become clean rows), restore resets
// the drafts to the product defaults, invalid stored rows fall back to the
// defaults, and the page states the rig's hardcoded parallel-tool cap instead of
// pretending it is configurable.

import (
	"strconv"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// openCapabilities opens the settings surface and selects the capabilities page.
func openCapabilities(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	if err := tester.Click("能力"); err != nil {
		t.Fatalf("open the capabilities page: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
}

// saveButton returns the fixed footer's Save control.
func saveButton(t *testing.T, tester *ui.Tester) ui.AccessInfo {
	t.Helper()
	save, ok := tester.AccessByRole(ui.RoleButton, "保存")
	if !ok {
		t.Fatalf("the footer's 保存 button is missing; access = %v", tester.AccessAll())
	}
	return save
}

// requireNumberValue asserts one numeric field's displayed value. The official
// ui.NumberInput exposes the number as the spin button's accessible value (its
// name is the field label), so the helper reads that instead of the inner text
// field.
func requireNumberValue(t *testing.T, tester *ui.Tester, label, want string) {
	t.Helper()
	for _, info := range tester.AccessAll() {
		if info.Name != label {
			continue
		}
		if _, err := strconv.Atoi(info.Value); err != nil {
			continue
		}
		if info.Value != want {
			t.Fatalf("%s = %q, want %q; texts = %v", label, info.Value, want, tester.Texts())
		}
		return
	}
	t.Fatalf("the %s number input is not accessible; access = %v", label, tester.AccessAll())
}

// TestCapabilitiesPageShowsDefaultsAndCleanSave covers the first visit: the
// product defaults fill the fields, the fixed footer Save is disabled while the
// drafts are clean, and the page explains where each limit applies.
func TestCapabilitiesPageShowsDefaultsAndCleanSave(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	openCapabilities(t, h, tester)

	h.assertTexts(tester, "能力",
		"Subagent 并发", "Team member 并发", "Shell 命令超时", "Shell 输出上限")
	// Product defaults: subagent 2, team 4, shell 30s, output 0 (tool default).
	requireNumberValue(t, tester, "Subagent 并发", "2")
	requireNumberValue(t, tester, "Team member 并发", "4")
	requireNumberValue(t, tester, "Shell 命令超时", "30")
	requireNumberValue(t, tester, "Shell 输出上限", "0")
	// Clean drafts: the primary Save in the footer is disabled.
	if save := saveButton(t, tester); !save.Disabled {
		t.Fatalf("the footer's Save is enabled on a clean page: %+v", save)
	}
	h.assertNoTexts(tester, "未保存")
	// The page states the fixed rig parallel cap instead of faking a setting.
	h.assertTexts(tester, "工具并行上限由 rig 固定为 8，当前不可配置。")
}

// TestCapabilitiesPageSavesDraftRows covers the save path: edits enable the
// footer Save, the four keys persist, and values equal to the product defaults
// are cleaned back to an empty row (so the modules keep following their config).
func TestCapabilitiesPageSavesDraftRows(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openCapabilities(t, h, tester)

	typeNumberInput(t, h, tester, "Subagent 并发", "5")
	typeNumberInput(t, h, tester, "Team member 并发", "1")
	typeNumberInput(t, h, tester, "Shell 命令超时", "90")
	typeNumberInput(t, h, tester, "Shell 输出上限", "256")
	h.assertTexts(tester, "未保存")
	if save := saveButton(t, tester); save.Disabled {
		t.Fatalf("the footer's Save stayed disabled after edits: %+v", save)
	}

	h.click(tester, "保存")
	h.assertTexts(tester, "已保存")
	want := map[string]string{
		contracts.SettingAgentSubagentMaxConcurrent: "5",
		contracts.SettingAgentTeamMaxConcurrent:     "1",
		contracts.SettingToolkitShellTimeoutSeconds: "90",
		contracts.SettingToolkitShellMaxOutputKiB:   "256",
	}
	for key, value := range want {
		if got := store.Get(key, ""); got != value {
			t.Fatalf("%s = %q, want %q", key, got, value)
		}
	}
	// A saved page is clean again: no unsaved badge and Save is disabled.
	h.assertNoTexts(tester, "未保存")
	if save := saveButton(t, tester); !save.Disabled {
		t.Fatalf("the footer's Save is enabled after a clean save: %+v", save)
	}
}

// TestCapabilitiesPageCleansDefaultRows covers the "clean" rule: saving the
// product defaults removes the stored rows instead of pinning redundant values.
func TestCapabilitiesPageCleansDefaultRows(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingAgentSubagentMaxConcurrent: "6",
		contracts.SettingAgentTeamMaxConcurrent:     "7",
		contracts.SettingToolkitShellTimeoutSeconds: "120",
		contracts.SettingToolkitShellMaxOutputKiB:   "512",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openCapabilities(t, h, tester)
	requireNumberValue(t, tester, "Subagent 并发", "6")
	requireNumberValue(t, tester, "Shell 命令超时", "120")

	h.click(tester, "恢复默认")
	h.assertTexts(tester, "已恢复默认（未保存）")
	requireNumberValue(t, tester, "Subagent 并发", "2")
	requireNumberValue(t, tester, "Team member 并发", "4")
	requireNumberValue(t, tester, "Shell 命令超时", "30")
	requireNumberValue(t, tester, "Shell 输出上限", "0")

	h.click(tester, "保存")
	h.assertTexts(tester, "已保存")
	for _, key := range []string{
		contracts.SettingAgentSubagentMaxConcurrent,
		contracts.SettingAgentTeamMaxConcurrent,
		contracts.SettingToolkitShellTimeoutSeconds,
		contracts.SettingToolkitShellMaxOutputKiB,
	} {
		if got := store.Get(key, ""); got != "" {
			t.Fatalf("default row %s was persisted as %q, want a cleaned (empty) row", key, got)
		}
	}
}

// TestCapabilitiesPageNormalizesInvalidRows covers the validation fallback: an
// out-of-range or malformed stored row shows the product default (the modules
// clamp the same way), so the page can never display a value it would not save.
func TestCapabilitiesPageNormalizesInvalidRows(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingAgentSubagentMaxConcurrent: "99",
		contracts.SettingAgentTeamMaxConcurrent:     "0",
		contracts.SettingToolkitShellTimeoutSeconds: "1000",
		contracts.SettingToolkitShellMaxOutputKiB:   "12.5",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openCapabilities(t, h, tester)

	requireNumberValue(t, tester, "Subagent 并发", "2")
	requireNumberValue(t, tester, "Team member 并发", "4")
	requireNumberValue(t, tester, "Shell 命令超时", "30")
	requireNumberValue(t, tester, "Shell 输出上限", "0")
	if save := saveButton(t, tester); !save.Disabled {
		t.Fatalf("the invalid rows made the clean page dirty: %+v", save)
	}
}

// TestCapabilitiesPageDiscardKeepsSavedRows covers the host's Discard hook: the
// draft is dropped, the surface closes without a second guard, and reopening
// shows the saved rows again.
func TestCapabilitiesPageDiscardKeepsSavedRows(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingAgentSubagentMaxConcurrent: "5",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openCapabilities(t, h, tester)
	requireNumberValue(t, tester, "Subagent 并发", "5")

	typeNumberInput(t, h, tester, "Subagent 并发", "3")
	h.assertTexts(tester, "未保存")
	closeSettings(h, tester)
	h.assertTexts(tester, "有未保存的修改")
	h.click(tester, "放弃修改")
	if h.uiRoot.Navigator().SettingsOpen() {
		t.Fatal("discard did not close the settings surface")
	}

	reopenSettings(h, tester)
	openCapabilities(t, h, tester)
	requireNumberValue(t, tester, "Subagent 并发", "5")
	if save := saveButton(t, tester); !save.Disabled {
		t.Fatalf("the discarded draft left the page dirty: %+v", save)
	}
}
