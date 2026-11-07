package settings_test

// Tests for the prompt-presets settings page (CRUD + ordering).

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

func openPresetsPage(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	if err := tester.Click("预设"); err != nil {
		t.Fatalf("click 预设: %v", err)
	}
	tester.Frame()
}

func decodePresets(t *testing.T, raw string) []contracts.PromptPreset {
	t.Helper()
	presets, err := contracts.ParsePromptPresets(raw)
	if err != nil {
		t.Fatalf("parse stored presets %q: %v", raw, err)
	}
	return presets
}

func TestPresetsPageBuiltInDefaults(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openPresetsPage(t, h, tester)

	h.assertTexts(tester, "预设", "提示词预设", "内置默认", "保存后自定义生效",
		"简洁回答", "代码审查", "逐步推理", "恢复内置默认")
	if got := store.get(contracts.SettingPromptPresets); got != "" {
		t.Fatalf("built-in defaults wrote settings: %q", got)
	}
}

func TestPresetsPageCustomAndBadJSON(t *testing.T) {
	custom := `[{"name":"自定义甲","prompt":"自定义甲提示词"},{"name":"自定义乙","prompt":"自定义乙提示词"}]`
	store := newFakeSettings(map[string]string{contracts.SettingPromptPresets: custom})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openPresetsPage(t, h, tester)
	h.assertTexts(tester, "自定义甲", "自定义甲提示词", "自定义乙")
	h.assertNoTexts(tester, "简洁回答")

	bad := newFakeSettings(map[string]string{contracts.SettingPromptPresets: "not-json"})
	h = newHarness(t, &fakeChat{}, bad, settings.Config{})
	tester = h.tester()
	tester.Frame()
	openPresetsPage(t, h, tester)
	h.assertTexts(tester, "配置解析失败", "简洁回答", "内置默认")
}

func TestPresetsPageAddDeleteReorderAndRestore(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openPresetsPage(t, h, tester)

	// Add a preset to the built-in draft.
	clearAndType(t, h, tester, "预设名称 输入", "我的预设")
	clearAndType(t, h, tester, "预设提示词 输入", "请用英文回答")
	h.click(tester, "添加")
	h.assertTexts(tester, "已保存", "我的预设", "请用英文回答")
	stored := decodePresets(t, store.get(contracts.SettingPromptPresets))
	if len(stored) != 4 || stored[len(stored)-1].Name != "我的预设" || stored[len(stored)-1].Prompt != "请用英文回答" {
		t.Fatalf("stored presets after add = %+v", stored)
	}
	if stored[0].Name != "简洁回答" {
		t.Fatalf("built-in prefix not preserved after add: %+v", stored)
	}

	// Reorder the first row down and the second row up.
	h.click(tester, "下移")
	stored = decodePresets(t, store.get(contracts.SettingPromptPresets))
	if len(stored) != 4 || stored[0].Name != "代码审查" || stored[1].Name != "简洁回答" {
		t.Fatalf("stored presets after move down = %+v", stored)
	}
	// Deleting the first row persists the remaining order.
	h.click(tester, "删除")
	stored = decodePresets(t, store.get(contracts.SettingPromptPresets))
	if len(stored) != 3 || stored[0].Name != "简洁回答" {
		t.Fatalf("stored presets after delete = %+v", stored)
	}
	h.assertTexts(tester, "已保存")

	// Restore built-in defaults writes the explicit empty list.
	h.click(tester, "恢复内置默认")
	if got := store.get(contracts.SettingPromptPresets); got != "[]" {
		t.Fatalf("stored presets after restore = %q, want []", got)
	}
	h.assertTexts(tester, "简洁回答", "内置默认")
}

func TestPresetsPageValidationAndSaveFailure(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openPresetsPage(t, h, tester)

	clearAndType(t, h, tester, "预设名称 输入", " ")
	clearAndType(t, h, tester, "预设提示词 输入", "prompt")
	h.click(tester, "添加")
	h.assertTexts(tester, "名称不能为空")
	if got := store.get(contracts.SettingPromptPresets); got != "" {
		t.Fatalf("invalid add wrote %q", got)
	}
	clearAndType(t, h, tester, "预设名称 输入", "简洁回答")
	clearAndType(t, h, tester, "预设提示词 输入", "duplicate prompt")
	h.click(tester, "添加")
	h.assertTexts(tester, "该名称已存在")
	clearAndType(t, h, tester, "预设名称 输入", "合法名称")
	clearAndType(t, h, tester, "预设提示词 输入", " ")
	h.click(tester, "添加")
	h.assertTexts(tester, "提示词不能为空")

	// A failed settings write surfaces DangerText and keeps the old list.
	failing := newFakeSettings(nil)
	failing.setErr = errSettingsWrite
	h = newHarness(t, &fakeChat{}, failing, settings.Config{})
	tester = h.tester()
	tester.Frame()
	openPresetsPage(t, h, tester)
	clearAndType(t, h, tester, "预设名称 输入", "失败预设")
	clearAndType(t, h, tester, "预设提示词 输入", "失败提示词")
	h.click(tester, "添加")
	h.assertTexts(tester, "保存失败")
	if got := failing.get(contracts.SettingPromptPresets); got != "" {
		t.Fatalf("failed write stored %q", got)
	}
}

func TestPresetsPageEnglish(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	h.uiRoot.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	if err := tester.Click("Presets"); err != nil {
		t.Fatalf("click Presets: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "Prompt presets", "Built-in defaults", "Restore built-in defaults", "Add")
}

// decodePresetsJSON is a sanity check that the encoder keeps array order.
func decodePresetsJSON(t *testing.T, raw string) []contracts.PromptPreset {
	t.Helper()
	var presets []contracts.PromptPreset
	if err := json.Unmarshal([]byte(raw), &presets); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return presets
}

var _ = decodePresetsJSON

var errSettingsWrite = errors.New("settings write failed")
