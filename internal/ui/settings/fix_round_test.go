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

// TestMCPDraftGuardOnClose covers ST-02 for the MCP add form: a half-filled
// server form is an unsaved draft, so closing the settings surface asks first.
func TestMCPDraftGuardOnClose(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)
	h.focusInput(tester, "名称 输入")
	tester.Type("draft-server")
	tester.Frame()
	if got := inputValue(t, tester, "名称 输入"); got != "draft-server" {
		t.Fatalf("name input value = %q, want draft-server", got)
	}

	assertCloseGuard(t, h, tester)
	h.click(tester, "继续编辑")
	if got := inputValue(t, tester, "名称 输入"); got != "draft-server" {
		t.Fatalf("the draft was lost by 继续编辑: %q", got)
	}
	assertCloseGuard(t, h, tester)
	h.click(tester, "放弃修改")
	tester.Frame()
	if h.uiRoot.Navigator().SettingsOpen() {
		t.Fatal("the settings surface stayed open after Discard")
	}
	reopenSettings(h, tester)
	openMCPPage(t, h, tester)
	if got := inputValue(t, tester, "名称 输入"); got != "" {
		t.Fatalf("the discarded draft survived: %q", got)
	}
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

// TestPresetsDraftGuardOnClose covers ST-02 for the 新增预设 form.
func TestPresetsDraftGuardOnClose(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "预设")
	h.focusInput(tester, "预设名称 输入")
	tester.Type("我的预设")
	tester.Frame()
	assertCloseGuard(t, h, tester)
	h.click(tester, "放弃修改")
	tester.Frame()
	reopenSettings(h, tester)
	h.click(tester, "预设")
	if got := inputValue(t, tester, "预设名称 输入"); got != "" {
		t.Fatalf("the discarded preset draft survived: %q", got)
	}
}

// TestMemoryDraftGuardOnClose covers ST-02 for the 添加记忆 input.
func TestMemoryDraftGuardOnClose(t *testing.T) {
	h := memoryPageHarness(t, newFakeMemoryStore())
	tester := h.tester()
	tester.Frame()
	h.focusInput(tester, "记忆内容 输入")
	tester.Type("要添加的记忆")
	tester.Frame()
	assertCloseGuard(t, h, tester)
	h.click(tester, "放弃修改")
	tester.Frame()
	reopenSettings(h, tester)
	h.click(tester, "记忆")
	if got := inputValue(t, tester, "记忆内容 输入"); got != "" {
		t.Fatalf("the discarded memory draft survived: %q", got)
	}
}

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
	h.click(tester, "行为")
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
	h.click(tester, "行为")
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
	h.click(tester, "诊断")
	h.click(tester, "复制诊断信息")
	h.assertTexts(tester, "已复制")
	h.click(tester, "模型")
	tester.Frame()
	h.click(tester, "诊断")
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

// TestBehaviorPermissionChipIsCompact covers ST-04: the default-permission
// popover menu is a chip at its natural width, not a full-row field.
func TestBehaviorPermissionChipIsCompact(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	tester.Frame()
	info, ok := tester.Access("工作区可写")
	if !ok {
		t.Fatalf("permission menu button not found; texts = %v", tester.Texts())
	}
	if info.Bounds.W >= 200 {
		t.Fatalf("permission chip is %v DIP wide, want a natural chip width < 200", info.Bounds.W)
	}
}

// TestDataPageActionsAlignedRight covers ST-03: opening the data directory is a
// row action beside the path, and the export/clear/report actions sit at the
// right end of their sections.
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
	openRect, ok := tester.Find("打开数据目录")
	if !ok {
		t.Fatalf("open-directory action missing; texts = %v", tester.Texts())
	}
	if diff := openRect.Y - copyRect.Y; diff > 24 || diff < -24 {
		t.Fatalf("open-directory action is not inline with the path row: copy.Y=%v open.Y=%v", copyRect.Y, openRect.Y)
	}
	if openRect.X < copyRect.X {
		t.Fatalf("open-directory action is not on the row's right: copy.X=%v open.X=%v", copyRect.X, openRect.X)
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

// TestMCPFormOrder covers ST-05: the add-server form puts its fields before the
// action row (Test connection / Add) instead of the actions above the inputs.
func TestMCPFormOrder(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)
	nameRect, ok := tester.Find("名称 输入")
	if !ok {
		t.Fatalf("name input missing; texts = %v", tester.Texts())
	}
	testRect, ok := tester.Find("测试连接")
	if !ok {
		t.Fatalf("test-connection action missing; texts = %v", tester.Texts())
	}
	addRect, ok := tester.Find("添加")
	if !ok {
		t.Fatalf("add action missing; texts = %v", tester.Texts())
	}
	if testRect.Y < nameRect.Y {
		t.Fatalf("test-connection action sits above the form fields: name.Y=%v test.Y=%v", nameRect.Y, testRect.Y)
	}
	if diff := addRect.Y - testRect.Y; diff > 24 || diff < -24 {
		t.Fatalf("add action is not in the same row as test connection: test.Y=%v add.Y=%v", testRect.Y, addRect.Y)
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
