package settings_test

// Fix-round regression tests: the settings pages' unsaved-draft guard (ST-02),
// transient badge expiry on page re-entry (ST-06), appearance re-read on
// re-entry (ST-08), the layout fixes (ST-03/ST-04/ST-05) and the de-duplicated
// global-hotkey field label (L-02).

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// inputValue reads one text input's value through the accessibility tree (the
// headless tester does not expose input text as a frame label).
func inputValue(t *testing.T, tester *ui.Tester, label string) string {
	t.Helper()
	info, ok := tester.Access(label)
	if !ok {
		t.Fatalf("input %q not accessible; texts = %v", label, tester.Texts())
	}
	return info.Value
}

// reopenSettings opens the settings surface again after a Discard closed it.
func reopenSettings(h *harness, tester *ui.Tester) {
	h.uiRoot.Navigator().ToggleSettings()
	tester.Frame()
	tester.Frame()
}

// assertCloseGuard presses Esc and asserts the host's unsaved-changes guard
// kept the settings surface open.
func assertCloseGuard(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if !h.uiRoot.Navigator().SettingsOpen() {
		t.Fatalf("Esc closed a dirty page without asking; texts = %v", tester.Texts())
	}
	h.assertTexts(tester, "有未保存的修改", "继续编辑", "放弃修改")
}

// TestLSPDraftGuardOnClose covers ST-02 for the LSP add form.
func TestLSPDraftGuardOnClose(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "LSP")
	h.focusInput(tester, "名称 输入")
	tester.Type("gopls")
	tester.Frame()
	assertCloseGuard(t, h, tester)
	h.click(tester, "放弃修改")
	tester.Frame()
	reopenSettings(h, tester)
	h.click(tester, "LSP")
	if got := inputValue(t, tester, "名称 输入"); got != "" {
		t.Fatalf("the discarded LSP draft survived: %q", got)
	}
}

// TestMemoryDraftGuardOnClose covers ST-02 for the 添加记忆 input.
// TestBehaviorDraftGuardOnlyWithDraft covers ST-02's "only when a real draft
// exists" rule for the workspace instruction file: the field lives behind the
// collapsed 更多对话设置 disclosure, so it only has a draft once that is open; a
// difference between the loaded draft and the stored row is guarded, and Discard
// re-reads the stored value instead of keeping the stale draft.
func TestBehaviorDraftGuardOnlyWithDraft(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()

	// The collapsed page has no instruction-file draft yet: closing it does not
	// ask, even after the stored row changes elsewhere.
	h.click(tester, "模型")
	tester.Frame()
	if err := store.Set(contracts.SettingAgentsFile, "TEAM.md"); err != nil {
		t.Fatalf("external write: %v", err)
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if h.uiRoot.Navigator().SettingsOpen() {
		t.Fatal("a collapsed behavior page kept the guard")
	}

	// Opening the disclosure loads the stored value; a later write from
	// elsewhere makes the loaded draft stale, so closing asks first.
	reopenSettings(h, tester)
	h.click(tester, "行为与限额")
	h.click(tester, "更多对话设置")
	tester.Frame()
	if err := store.Set(contracts.SettingAgentsFile, "OTHER.md"); err != nil {
		t.Fatalf("second external write: %v", err)
	}
	tester.Frame()
	assertCloseGuard(t, h, tester)
	h.click(tester, "放弃修改")
	tester.Frame()
	reopenSettings(h, tester)
	h.click(tester, "行为与限额")
	tester.Frame()
	if got := store.get(contracts.SettingAgentsFile); got != "OTHER.md" {
		t.Fatalf("stored instruction file = %q, want OTHER.md", got)
	}
}

// TestDiagnosticsCopiedBadgeClearsOnReentry covers ST-06: the one-shot "已复制"
// badge of the diagnostics page belongs to the visit it happened in.
func TestDiagnosticsCopiedBadgeClearsOnReentry(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "关于")
	h.click(tester, "复制诊断信息")
	h.assertTexts(tester, "已复制")
	h.click(tester, "模型")
	tester.Frame()
	h.click(tester, "关于")
	tester.Frame()
	h.assertNoTexts(tester, "已复制")
}

// TestAppearanceStatusClearsOnReentry covers ST-06 for the appearance page: the
// "语言已切换" status belongs to the visit that changed the language.
func TestAppearanceStatusClearsOnReentry(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "外观")
	h.click(tester, "开机自启动")
	tester.Frame()
	h.assertTexts(tester, "桌面集成设置已更新")
	h.click(tester, "模型")
	tester.Frame()
	h.click(tester, "外观")
	tester.Frame()
	h.assertNoTexts(tester, "桌面集成设置已更新")
}

// TestAppearanceReReadsThemeOnReentry covers ST-08: a value written from
// elsewhere (another window, a preset) shows on the next visit of the page.
func TestAppearanceReReadsThemeOnReentry(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "外观")
	h.click(tester, "模型")
	tester.Frame()
	if err := store.Set(contracts.SettingThemeMode, "dark"); err != nil {
		t.Fatalf("external write: %v", err)
	}
	h.click(tester, "外观")
	tester.Frame()
	info, ok := tester.Access("深色")
	if !ok {
		t.Fatalf("theme option not accessible; texts = %v", tester.Texts())
	}
	if !info.Checked {
		t.Fatalf("the appearance page did not re-read the theme mode on re-entry: checked=%v", info.Checked)
	}
}

// TestDataPageActionsAlignedRight covers ST-03 as refined by the 2026-11-17
// review P3: the section-level actions (open/export/clear/report) sit at the
// right end of their section title rows (SectionAction); the inline copy stays
// beside the path value on the row below the title.
func TestDataPageActionsAlignedRight(t *testing.T) {
	store := newFakeSettings(nil)
	chat := &fakeChat{}
	chat.addSession(contracts.ChatSession{ID: "s1", Title: "会话"})
	h := newHarness(t, chat, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "数据与隐私")
	tester.Frame()

	copyRect, ok := tester.Find("复制")
	if !ok {
		t.Fatalf("copy action missing; texts = %v", tester.Texts())
	}
	titleRect, ok := tester.Find("数据目录")
	if !ok {
		t.Fatalf("data directory title missing; texts = %v", tester.Texts())
	}
	openRect, ok := tester.Find("打开数据目录")
	if !ok {
		t.Fatalf("open-directory action missing; texts = %v", tester.Texts())
	}
	if diff := openRect.Y - titleRect.Y; diff > 24 || diff < -24 {
		t.Fatalf("open-directory action is not on the section title row: title.Y=%v open.Y=%v",
			titleRect.Y, openRect.Y)
	}
	if openRect.X < 600 {
		t.Fatalf("open-directory action is not at the title row's right end: %v", openRect)
	}
	if copyRect.Y <= openRect.Y {
		t.Fatalf("inline copy must stay on the path row below the title: copy.Y=%v open.Y=%v",
			copyRect.Y, openRect.Y)
	}
	exportRect, ok := tester.Find("导出全部会话")
	if !ok {
		t.Fatalf("export action missing; texts = %v", tester.Texts())
	}
	if exportRect.X < 600 {
		t.Fatalf("export action is not right-aligned: %v", exportRect)
	}
	clearRect, ok := tester.Find("清除全部会话")
	if !ok {
		t.Fatalf("clear action missing; texts = %v", tester.Texts())
	}
	if clearRect.X < 600 {
		t.Fatalf("clear action is not right-aligned: %v", clearRect)
	}
	reportRect, ok := tester.Find("导出诊断报告")
	if !ok {
		t.Fatalf("diagnostic report action missing; texts = %v", tester.Texts())
	}
	if reportRect.X < 600 {
		t.Fatalf("diagnostic report action is not right-aligned: %v", reportRect)
	}
}

// TestHotkeyFieldLabelNotDuplicated covers L-02: the 全局热键 section no longer
// holds a field labeled exactly like the section.
func TestHotkeyFieldLabelNotDuplicated(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "外观")
	tester.Frame()
	h.assertTexts(tester, "全局热键", "组合键 输入")
	h.assertNoTexts(tester, "全局热键 输入")
}
