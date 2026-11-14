package settings_test

// Tests for the Behavior page's workspace-instructions file setting.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

func TestBehaviorPageAgentsFileDefaultsToAGENTSmd(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	expandMoreConversationSettings(t, tester, "更多对话设置")
	h.assertTexts(tester, "工作区指令文件",
		"留空关闭；下次运行生效")
	if raw := store.get(contracts.SettingAgentsFile); raw != "" {
		t.Fatalf("defaults wrote the agents-file row: %q", raw)
	}

	h.focusInput(tester, "工作区指令文件")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Type("CUSTOM.md")
	tester.Frame()
	if raw := store.get(contracts.SettingAgentsFile); raw != "CUSTOM.md" {
		t.Fatalf("saving custom name wrote %q", raw)
	}
}

func TestBehaviorPageAgentsFileEmptyDisables(t *testing.T) {
	store := newFakeSettings(map[string]string{contracts.SettingAgentsFile: "AGENTS.md"})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	expandMoreConversationSettings(t, tester, "更多对话设置")
	h.focusInput(tester, "工作区指令文件")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
	if raw := store.get(contracts.SettingAgentsFile); raw != "" {
		t.Fatalf("clearing the setting wrote %q, want empty", raw)
	}
}

func TestBehaviorPageAgentsFileWriteFailure(t *testing.T) {
	store := newFakeSettings(nil)
	store.setErr = errBehaviorStoreWrite
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	expandMoreConversationSettings(t, tester, "更多对话设置")
	h.focusInput(tester, "工作区指令文件")
	tester.Type("x")
	tester.Frame()
	h.assertTexts(tester, "保存失败：")
}

func TestBehaviorPageAgentsFileEnglish(t *testing.T) {
	store := newFakeSettings(nil)
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Behavior")
	expandMoreConversationSettings(t, tester, "More conversation settings")
	h.assertTexts(tester, "Workspace instructions file",
		"Empty disables it; applies on the next run")
}
