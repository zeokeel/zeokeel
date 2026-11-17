package settings_test

// Tests for the model page's "运行参数 / Runtime" section: the optional context
// budget draft saved by the page footer. An empty draft means "use the module
// config" (no settings row); a value persists that row. The iteration cap has
// no draft: it is assembly-level product definition, and the chat.maxIterations
// user knob was removed in v0.2.50 (negative pin: loop_config_removed_test.go).
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
	// The runtime limit lives behind the collapsed 高级选项 disclosure; the
	// native disclosure animates, so let its layout settle before focusing the
	// input.
	h.click(tester, "高级选项")
	for range 4 {
		tester.Frame()
	}
}

// clearRuntimeInput replaces the text of the optional numeric draft with an
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

// TestModelRuntimeOptionalDraftsRender covers the optional draft: the numeric
// input is always visible, an empty draft shows the module default, and the
// fixed-behavior switches (use-default limits, stream completeness) are gone.
func TestModelRuntimeOptionalDraftsRender(t *testing.T) {
	cfg := runtimeBaseConfig()
	cfg.ContextBudget = 9000
	h, store, tester := runtimeHarness(t, cfg)
	// The collapsed summary names the module default and no longer mentions
	// stream completeness or iterations (the cap has no user knob).
	h.assertTexts(tester, "上下文预算 模块默认 9000")
	h.assertNoTexts(tester, "流完整性校验", "最大迭代")
	expandRuntimeAdvanced(t, h, tester)
	h.assertTexts(tester, "提示词与运行参数（页脚保存）", "上下文预算",
		"上下文预算 输入",
		"自动压缩的 prompt token 预算；留空使用模块默认 9000")
	h.assertNoTexts(tester, "使用默认上限（不限制）", "使用默认预算",
		"流完整性校验", "Stream completeness", "最大迭代次数")
	// An empty draft means "use the module config": there is nothing to save,
	// so the footer primary is disabled (P2-11) and no settings row appears.
	requireFooterDisabled(t, tester, "保存", true, "model with empty runtime drafts")
	if got := store.get(contracts.SettingsChatContextBudget); got != "" {
		t.Fatalf("module default context budget became a settings row: %q", got)
	}
}

// TestModelRuntimeDraftsSaveAndClear covers the footer Save: a typed value
// persists its row, and clearing the draft returns to the module config by
// persisting an empty row.
func TestModelRuntimeDraftsSaveAndClear(t *testing.T) {
	cfg := runtimeBaseConfig()
	cfg.ContextBudget = 4000
	h, store, tester := runtimeHarness(t, cfg)
	expandRuntimeAdvanced(t, h, tester)
	typeNumberInput(t, h, tester, "上下文预算", "4096")
	h.assertTexts(tester, "未保存")
	h.click(tester, "保存")
	h.assertTexts(tester, "已保存")
	h.assertNoTexts(tester, "未保存")
	if got := store.get(contracts.SettingsChatContextBudget); got != "4096" {
		t.Fatalf("saved context budget = %q, want 4096", got)
	}

	clearRuntimeInput(t, h, tester, "上下文预算")
	h.assertTexts(tester, "未保存")
	h.click(tester, "保存")
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
	store := newFakeSettings(map[string]string{contracts.SettingsChatContextBudget: "9000"})
	h, store, tester := runtimeHarnessWithStore(t, cfg, store)
	expandRuntimeAdvanced(t, h, tester)
	typeNumberInput(t, h, tester, "上下文预算", "-1")
	h.click(tester, "保存")
	// The invalid draft keeps the inline error (and the host's unsaved badge);
	// nothing is persisted.
	h.assertTexts(tester, "未保存",
		"请输入大于或等于 0 的整数；留空使用模块默认值。")
	h.assertNoTexts(tester, "已保存")
	if got := store.get(contracts.SettingsChatContextBudget); got != "9000" {
		t.Fatalf("invalid draft overwrote the stored row: %q", got)
	}
	typeNumberInput(t, h, tester, "上下文预算", "15000")
	h.click(tester, "保存")
	h.assertTexts(tester, "已保存")
	if got := store.get(contracts.SettingsChatContextBudget); got != "15000" {
		t.Fatalf("corrected context budget = %q, want 15000", got)
	}
}

// TestModelRuntimeZeroBudgetAllowed covers the explicit "0" budget: it is a
// valid draft meaning "no automatic compaction".
func TestModelRuntimeZeroBudgetAllowed(t *testing.T) {
	h, store, tester := runtimeHarness(t, runtimeBaseConfig())
	expandRuntimeAdvanced(t, h, tester)
	typeNumberInput(t, h, tester, "上下文预算", "0")
	h.click(tester, "保存")
	h.assertTexts(tester, "已保存")
	if got := store.get(contracts.SettingsChatContextBudget); got != "0" {
		t.Fatalf("saved context budget = %q, want explicit 0", got)
	}
}

// TestModelRuntimeEnglishCopy covers the localized optional draft and the
// absent fixed-behavior switches and iteration knob.
func TestModelRuntimeEnglishCopy(t *testing.T) {
	cfg := runtimeBaseConfig()
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
	h.assertTexts(tester, "Prompt & runtime (saved by the footer)", "Context budget",
		"Prompt-token budget for automatic compaction; Leave blank for the module default 500.")
	h.assertNoTexts(tester, "Use default limit (unlimited)", "Use default budget",
		"Stream completeness", "Max iterations")
}
