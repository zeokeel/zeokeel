package settings_test

// Tests for the model page's "运行参数 / Runtime" section: the optional max
// iterations and context budget drafts saved by the page footer. An empty draft
// means "use the module config" (no settings row); a value persists that row.
// Stream completeness is always on, so its switch is deleted from the page.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// runtimeHarness opens the model page with a chat config reporting the given
// module-level runtime limits and an empty settings store.
func runtimeHarness(t *testing.T, cfg contracts.ChatConfig) (*harness, *fakeSettings, *ui.Tester) {
	t.Helper()
	store := newFakeSettings(nil)
	return runtimeHarnessWithStore(t, cfg, store)
}

// runtimeHarnessWithStore is runtimeHarness over a scripted settings store.
func runtimeHarnessWithStore(t *testing.T, cfg contracts.ChatConfig, store *fakeSettings) (*harness, *fakeSettings, *ui.Tester) {
	t.Helper()
	chat := &fakeChat{config: cfg}
	h := newHarness(t, chat, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	return h, store, tester
}

// expandRuntimeAdvanced opens the collapsed 高级选项 disclosure.
func expandRuntimeAdvanced(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	// Runtime limits live behind the collapsed 高级选项 disclosure; the native
	// disclosure animates, so let its layout settle before focusing inputs.
	h.click(tester, "高级选项")
	for range 4 {
		tester.Frame()
	}
}

// clearRuntimeInput replaces the text of one optional numeric draft with an
// empty value (the "use the module config" state).
func clearRuntimeInput(t *testing.T, h *harness, tester *ui.Tester, label string) {
	t.Helper()
	target := label + " 输入"
	h.ensureVisible(tester, target)
	rect, ok := tester.Find(target)
	if !ok {
		t.Fatalf("input labeled %q not found; texts = %v", target, tester.Texts())
	}
	tester.ClickAt(rect.X+16, rect.Y+rect.H/2)
	tester.Frame()
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
}

func runtimeBaseConfig() contracts.ChatConfig {
	return contracts.ChatConfig{
		Model:   "deepseek-chat",
		BaseURL: "https://api.example.com/v1",
	}
}

// TestModelRuntimeOptionalDraftsRender covers the optional drafts: the numeric
// inputs are always visible, an empty draft shows the module default, and the
// fixed-behavior switches (use-default limits, stream completeness) are gone.
func TestModelRuntimeOptionalDraftsRender(t *testing.T) {
	cfg := runtimeBaseConfig()
	cfg.MaxIterations = 7
	cfg.ContextBudget = 9000
	h, store, tester := runtimeHarness(t, cfg)
	// The collapsed summary names the module defaults and no longer mentions
	// stream completeness.
	h.assertTexts(tester, "最大迭代 模块默认 7", "上下文预算 模块默认 9000")
	h.assertNoTexts(tester, "流完整性校验")
	expandRuntimeAdvanced(t, h, tester)
	h.assertTexts(tester, "提示词与运行参数（页脚保存）", "最大迭代次数", "上下文预算",
		"最大迭代次数 输入", "上下文预算 输入",
		"模型/工具轮数上限；留空使用模块默认 7",
		"自动压缩的 prompt token 预算；留空使用模块默认 9000")
	h.assertNoTexts(tester, "使用默认上限（不限制）", "使用默认预算",
		"流完整性校验", "Stream completeness")
	h.click(tester, "保存提示词与运行参数")
	if got := store.get(contracts.SettingsChatMaxIterations); got != "" {
		t.Fatalf("module default max iterations became a settings row: %q", got)
	}
	if got := store.get(contracts.SettingsChatContextBudget); got != "" {
		t.Fatalf("module default context budget became a settings row: %q", got)
	}
}

// TestModelRuntimeDraftsSaveAndClear covers the footer Save: typed values
// persist their rows, and clearing both drafts returns to the module config by
// persisting empty rows.
func TestModelRuntimeDraftsSaveAndClear(t *testing.T) {
	cfg := runtimeBaseConfig()
	cfg.MaxIterations = 20
	cfg.ContextBudget = 4000
	h, store, tester := runtimeHarness(t, cfg)
	expandRuntimeAdvanced(t, h, tester)
	typeNumberInput(t, h, tester, "最大迭代次数", "12")
	typeNumberInput(t, h, tester, "上下文预算", "4096")
	h.assertTexts(tester, "未保存")
	h.click(tester, "保存提示词与运行参数")
	h.assertTexts(tester, "已保存")
	h.assertNoTexts(tester, "未保存")
	if got := store.get(contracts.SettingsChatMaxIterations); got != "12" {
		t.Fatalf("saved max iterations = %q, want 12", got)
	}
	if got := store.get(contracts.SettingsChatContextBudget); got != "4096" {
		t.Fatalf("saved context budget = %q, want 4096", got)
	}

	clearRuntimeInput(t, h, tester, "最大迭代次数")
	clearRuntimeInput(t, h, tester, "上下文预算")
	h.assertTexts(tester, "未保存")
	h.click(tester, "保存提示词与运行参数")
	if got := store.get(contracts.SettingsChatMaxIterations); got != "" {
		t.Fatalf("cleared max iterations = %q, want empty", got)
	}
	if got := store.get(contracts.SettingsChatContextBudget); got != "" {
		t.Fatalf("cleared context budget = %q, want empty", got)
	}
	h.assertNoTexts(tester, "未保存")
}

// TestModelRuntimeInvalidDraftBlocksSave covers validation: an empty draft is
// valid, a malformed/out-of-range one keeps the stored row and shows the inline
// error until it is corrected.
func TestModelRuntimeInvalidDraftBlocksSave(t *testing.T) {
	cfg := runtimeBaseConfig()
	store := newFakeSettings(map[string]string{contracts.SettingsChatMaxIterations: "9"})
	h, store, tester := runtimeHarnessWithStore(t, cfg, store)
	expandRuntimeAdvanced(t, h, tester)
	typeNumberInput(t, h, tester, "最大迭代次数", "0")
	h.click(tester, "保存提示词与运行参数")
	// The invalid draft keeps the inline error (and the host's unsaved badge);
	// nothing is persisted.
	h.assertTexts(tester, "未保存",
		"请输入大于 0 的整数；留空使用模块默认值。")
	h.assertNoTexts(tester, "已保存")
	if got := store.get(contracts.SettingsChatMaxIterations); got != "9" {
		t.Fatalf("invalid draft overwrote the stored row: %q", got)
	}
	typeNumberInput(t, h, tester, "最大迭代次数", "15")
	h.click(tester, "保存提示词与运行参数")
	h.assertTexts(tester, "已保存")
	if got := store.get(contracts.SettingsChatMaxIterations); got != "15" {
		t.Fatalf("corrected max iterations = %q, want 15", got)
	}
}

// TestModelRuntimeZeroBudgetAllowed covers the explicit "0" budget: it is a
// valid draft meaning "no automatic compaction".
func TestModelRuntimeZeroBudgetAllowed(t *testing.T) {
	h, store, tester := runtimeHarness(t, runtimeBaseConfig())
	expandRuntimeAdvanced(t, h, tester)
	typeNumberInput(t, h, tester, "上下文预算", "0")
	h.click(tester, "保存提示词与运行参数")
	h.assertTexts(tester, "已保存")
	if got := store.get(contracts.SettingsChatContextBudget); got != "0" {
		t.Fatalf("saved context budget = %q, want explicit 0", got)
	}
}

// TestModelRuntimeEnglishCopy covers the localized optional drafts and the
// absent fixed-behavior switches.
func TestModelRuntimeEnglishCopy(t *testing.T) {
	cfg := runtimeBaseConfig()
	cfg.MaxIterations = 3
	cfg.ContextBudget = 500
	store := newFakeSettings(nil)
	h := newLocaleHarness(t, &fakeChat{config: cfg}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Model")
	h.click(tester, "Advanced options")
	for range 4 {
		tester.Frame()
	}
	h.assertTexts(tester, "Prompt & runtime (saved by the footer)", "Max iterations",
		"Context budget",
		"Model/tool rounds per run; Leave blank for the module default 3.",
		"Prompt-token budget for automatic compaction; Leave blank for the module default 500.")
	h.assertNoTexts(tester, "Use default limit (unlimited)", "Use default budget",
		"Stream completeness")
}
