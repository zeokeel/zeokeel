package settings_test

// Tests for the Agent presets section of the Presets page.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

func openAgentPresetsPage(t *testing.T, h *harness, tester interface {
	Click(string) error
	Frame()
}) {
	t.Helper()
	if err := tester.Click("预设"); err != nil {
		t.Fatalf("click 预设: %v", err)
	}
	tester.Frame()
}

func TestAgentPresetsSectionDefaults(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	if err := tester.Click("预设"); err != nil {
		t.Fatalf("click 预设: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "代理预设", "添加代理预设", "还没有代理预设")
	if raw := store.get(contracts.SettingAgentPresets); raw != "" {
		t.Fatalf("defaults wrote agent presets: %q", raw)
	}
}

func TestAgentPresetsAddAndPersist(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	if err := tester.Click("预设"); err != nil {
		t.Fatalf("click 预设: %v", err)
	}
	tester.Frame()

	h.click(tester, "添加代理预设")
	clearAndType(t, h, tester, "人设名称 输入", "Reviewer")
	clearAndType(t, h, tester, "人设 输入", "review carefully")
	clearAndType(t, h, tester, "人设提示词 输入", "check concurrency")
	clearAndType(t, h, tester, "允许工具（逗号分隔） 输入", " read_file, bash ,, ")
	clearAndType(t, h, tester, "默认模型 输入", "review-model")
	h.click(tester, "保存人设")

	stored, err := contracts.ParseAgentPresets(store.get(contracts.SettingAgentPresets))
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored agent presets = %+v, %v", stored, err)
	}
	preset := stored[0]
	if preset.Name != "Reviewer" || preset.Persona != "review carefully" ||
		preset.Prompt != "check concurrency" || preset.Model != "review-model" ||
		len(preset.Tools) != 2 || preset.Tools[0] != "read_file" || preset.Tools[1] != "bash" {
		t.Fatalf("stored preset = %+v", preset)
	}
	if preset.ID == "" || preset.Order != 0 {
		t.Fatalf("id/order = %q/%d", preset.ID, preset.Order)
	}
	h.assertTexts(tester, "已保存", "Reviewer", "工具：read_file, bash", "模型：review-model")
}

func TestAgentPresetsValidation(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	if err := tester.Click("预设"); err != nil {
		t.Fatalf("click 预设: %v", err)
	}
	tester.Frame()
	h.click(tester, "添加代理预设")
	h.click(tester, "保存人设")
	h.assertTexts(tester, "名称不能为空")

	clearAndType(t, h, tester, "人设名称 输入", "Dup")
	h.click(tester, "保存人设")
	h.assertTexts(tester, "已保存")

	h.click(tester, "添加代理预设")
	clearAndType(t, h, tester, "人设名称 输入", "Dup")
	h.click(tester, "保存人设")
	h.assertTexts(tester, "该名称已存在")
}

func TestAgentPresetsEnglish(t *testing.T) {
	store := newFakeSettings(nil)
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	if err := tester.Click("Presets"); err != nil {
		t.Fatalf("click Presets: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "Agent presets", "Add agent preset", "No agent presets yet")
}
