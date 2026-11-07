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
	h.assertTexts(tester, "工作区指令文件",
		"运行时从当前工作区读取并注入系统提示；重启/下次运行生效")
	if raw := store.get(contracts.SettingAgentsFile); raw != "" {
		t.Fatalf("defaults wrote the agents-file row: %q", raw)
	}

	h.focusInput(tester, "工作区指令文件")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Type("CUSTOM.md")
	tester.Frame()
	h.click(tester, "保存")
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
	h.focusInput(tester, "工作区指令文件")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
	h.click(tester, "保存")
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
	h.click(tester, "保存")
	h.assertTexts(tester, "保存失败：")
}

func TestBehaviorPageAgentsFileEnglish(t *testing.T) {
	store := newFakeSettings(nil)
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Behavior")
	h.assertTexts(tester, "Workspace instructions file",
		"Read from the current workspace at run time and appended to the system prompt; takes effect on the next run or restart")
}
