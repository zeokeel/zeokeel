package settings_test

// Tests for the model page's "运行参数 / Runtime" section: max iterations and
// context budget numeric drafts saved by the page footer, and the immediate-
// write stream-completeness switch. The three settings rows are read by the
// agent backend (chat.maxIterations / chat.contextBudget / chat.requireFinishReason);
// an empty numeric draft clears the row so the module config applies.

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
	chat := &fakeChat{config: cfg}
	h := newHarness(t, chat, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	// Runtime limits live behind the collapsed 高级选项 disclosure; the native
	// disclosure animates, so let its layout settle before focusing inputs.
	h.click(tester, "高级选项")
	for range 4 {
		tester.Frame()
	}
	return h, store, tester
}

func runtimeBaseConfig() contracts.ChatConfig {
	return contracts.ChatConfig{
		Model:   "deepseek-chat",
		BaseURL: "https://api.example.com/v1",
	}
}

// TestModelRuntimeDefaultsFromModuleConfig covers the "use default" semantics
// with a module-configured limit and an empty settings store: the switches are
// on (the module values are displayed as defaults, not as drafts), and a plain
// Save persists no setting rows, so a later module-config change still applies.
func TestModelRuntimeDefaultsFromModuleConfig(t *testing.T) {
	cfg := runtimeBaseConfig()
	cfg.MaxIterations = 7
	cfg.ContextBudget = 9000
	h, store, tester := runtimeHarness(t, cfg)
	h.assertTexts(tester, "运行参数", "最大迭代次数", "上下文预算", "流完整性校验",
		"模块默认：7", "模块默认：9000")
	h.assertNoTexts(tester, "未保存")
	// The numeric inputs are hidden while their defaults are on.
	h.assertNoTexts(tester, "最大迭代次数 输入", "上下文预算 输入")
	h.click(tester, "保存")
	if got := store.get(contracts.SettingsChatMaxIterations); got != "" {
		t.Fatalf("module default max iterations became a settings row: %q", got)
	}
	if got := store.get(contracts.SettingsChatContextBudget); got != "" {
		t.Fatalf("module default context budget became a settings row: %q", got)
	}

	// Turning the default off and saving a concrete value writes the row.
	h.click(tester, "使用默认上限（不限制）")
	typeNumberInput(t, h, tester, "最大迭代次数", "12")
	h.click(tester, "保存")
	if got := store.get(contracts.SettingsChatMaxIterations); got != "12" {
		t.Fatalf("explicit max iterations = %q, want 12", got)
	}
	if got := store.get(contracts.SettingsChatContextBudget); got != "" {
		t.Fatalf("the untouched default budget was written: %q", got)
	}
}

// TestModelRuntimeSaveRoundTrip covers the footer Save persisting both numeric
// drafts and the page showing the saved state.
func TestModelRuntimeSaveRoundTrip(t *testing.T) {
	cfg := runtimeBaseConfig()
	cfg.MaxIterations = 20
	h, store, tester := runtimeHarness(t, cfg)
	// A module max-iterations limit exists: it is the default, so turn the switch
	// off before entering an explicit value.
	h.click(tester, "使用默认上限（不限制）")
	typeNumberInput(t, h, tester, "最大迭代次数", "12")
	h.click(tester, "使用默认预算") // the budget has no explicit row yet: turn the default off
	typeNumberInput(t, h, tester, "上下文预算", "4096")
	h.assertTexts(tester, "未保存")
	h.click(tester, "保存")
	h.assertTexts(tester, "已保存")
	h.assertNoTexts(tester, "未保存")
	if got := store.get(contracts.SettingsChatMaxIterations); got != "12" {
		t.Fatalf("saved max iterations = %q, want 12", got)
	}
	if got := store.get(contracts.SettingsChatContextBudget); got != "4096" {
		t.Fatalf("saved context budget = %q, want 4096", got)
	}
}

// TestModelRuntimeEmptyClearsRows covers the documented fallback: clearing a
// numeric draft persists an empty row.
func TestModelRuntimeEmptyClearsRows(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingsChatMaxIterations: "9",
		contracts.SettingsChatContextBudget: "5000",
	})
	h := newHarness(t, &fakeChat{config: runtimeBaseConfig()}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	h.click(tester, "高级选项")
	h.click(tester, "使用默认上限（不限制）")
	h.click(tester, "使用默认预算")
	h.click(tester, "保存")
	if got := store.get(contracts.SettingsChatMaxIterations); got != "" {
		t.Fatalf("cleared max iterations = %q, want empty", got)
	}
	if got := store.get(contracts.SettingsChatContextBudget); got != "" {
		t.Fatalf("cleared context budget = %q, want empty", got)
	}
}

// TestModelRuntimeNumberInputsIgnoreOutOfRange covers the official
// ui.NumberInput rule: a typed number outside the bounds is ignored, so the page
// never holds an invalid draft and a save persists the last valid value (the
// old free-text inline-error path is gone).
func TestModelRuntimeNumberInputsIgnoreOutOfRange(t *testing.T) {
	cfg := runtimeBaseConfig()
	cfg.MaxIterations = 20
	h, store, tester := runtimeHarness(t, cfg)
	h.click(tester, "使用默认上限（不限制）")               // the module limit is the default: turn it off
	typeNumberInput(t, h, tester, "最大迭代次数", "0") // below the bound 1: ignored
	h.click(tester, "使用默认预算")
	typeNumberInput(t, h, tester, "上下文预算", "-1") // below 0: ignored
	h.click(tester, "保存")
	if got := store.get(contracts.SettingsChatMaxIterations); got != "20" {
		t.Fatalf("max iterations saved = %q, want last valid value 20", got)
	}
	if got := store.get(contracts.SettingsChatContextBudget); got != "0" {
		t.Fatalf("context budget saved = %q, want 0", got)
	}
	h.assertNoTexts(tester, "请输入大于 0 的整数", "请输入大于或等于 0 的整数", "运行参数有误")
}

// TestModelRuntimeZeroBudgetAllowed covers the explicit "0" budget: it is a
// valid draft meaning "no automatic compaction".
func TestModelRuntimeZeroBudgetAllowed(t *testing.T) {
	cfg := runtimeBaseConfig()
	cfg.MaxIterations = 20
	h, store, tester := runtimeHarness(t, cfg)
	h.click(tester, "使用默认预算")
	typeNumberInput(t, h, tester, "上下文预算", "0")
	h.click(tester, "保存")
	h.assertTexts(tester, "已保存")
	if got := store.get(contracts.SettingsChatContextBudget); got != "0" {
		t.Fatalf("saved context budget = %q, want explicit 0", got)
	}
}

// TestModelStreamCompletenessToggleImmediateWrite covers the switch: clicking
// it writes "false"/"true" at once (no footer save) and shows the status.
func TestModelStreamCompletenessToggleImmediateWrite(t *testing.T) {
	h, store, tester := runtimeHarness(t, runtimeBaseConfig())
	if got := store.get(contracts.SettingsChatRequireFinishReason); got != "" {
		t.Fatalf("stream setting touched before the click: %q", got)
	}
	h.click(tester, "流完整性校验")
	h.assertTexts(tester, "已保存")
	if got := store.get(contracts.SettingsChatRequireFinishReason); got != "false" {
		t.Fatalf("stream setting after first click = %q, want false", got)
	}
	h.click(tester, "流完整性校验")
	if got := store.get(contracts.SettingsChatRequireFinishReason); got != "true" {
		t.Fatalf("stream setting after second click = %q, want true", got)
	}
}

// TestModelRuntimeEnglishCopy covers the localized section and descriptions.
func TestModelRuntimeEnglishCopy(t *testing.T) {
	cfg := runtimeBaseConfig()
	cfg.MaxIterations = 3
	cfg.ContextBudget = 500
	h, _, tester := runtimeHarness(t, cfg)
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Runtime", "Max iterations", "Context budget",
		"Stream completeness", "Model/tool rounds per run")
}
