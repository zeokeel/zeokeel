package settings_test

// Tests for the editable model catalog settings page (CRUD over
// modelcatalog.models). The page writes the same row the modelcatalog module
// reads dynamically, so the composer model menu follows immediately.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

func TestModelCatalogPageDefaults(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	if err := tester.Click("模型"); err != nil {
		t.Fatalf("click 模型: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "模型", "模型列表", "DeepSeek Chat", "deepseek-chat",
		"DeepSeek Reasoner", "deepseek-reasoner", "恢复默认", "添加模型")
	// The add form is not permanently open: the list owns the page.
	h.assertNoTexts(tester, "名称 输入", "模型 ID 输入")
	if got := store.get(contracts.SettingModelCatalogModels); got != "" {
		t.Fatalf("defaults wrote settings: %q", got)
	}
}

func TestModelCatalogPageAddAndDelete(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelCatalog(t, h)

	openCatalogAddModal(t, h, tester)
	clearAndType(t, h, tester, "名称 输入", "My Model")
	clearAndType(t, h, tester, "模型 ID 输入", "my-model")
	clearAndType(t, h, tester, "备注 输入", "test note")
	catalogClick(t, h, tester, "添加")
	h.assertNoTexts(tester, "新增模型")
	models, err := contracts.ParseModelInfoList(store.get(contracts.SettingModelCatalogModels))
	if err != nil {
		t.Fatalf("parse stored catalog: %v", err)
	}
	if len(models) != 3 || models[2].ID != "my-model" || models[2].Name != "My Model" {
		t.Fatalf("stored catalog = %+v", models)
	}

	// Duplicate IDs are rejected (case-insensitive) inline in the modal and do
	// not write a row.
	openCatalogAddModal(t, h, tester)
	clearAndType(t, h, tester, "名称 输入", "Dup")
	clearAndType(t, h, tester, "模型 ID 输入", "MY-MODEL")
	catalogClick(t, h, tester, "添加")
	h.assertTexts(tester, "模型 ID 已存在", "新增模型")
	models, _ = contracts.ParseModelInfoList(store.get(contracts.SettingModelCatalogModels))
	if len(models) != 3 {
		t.Fatalf("duplicate add changed catalog: %+v", models)
	}
	h.click(tester, "取消")
	h.assertNoTexts(tester, "新增模型")

	// Removing the first row asks for confirmation, then rewrites the list.
	if err := tester.Click("删除"); err != nil {
		t.Fatalf("remove model: %v", err)
	}
	tester.Frame()
	if err := tester.Click("确认删除"); err != nil {
		t.Fatalf("confirm remove model: %v", err)
	}
	tester.Frame()
	models, _ = contracts.ParseModelInfoList(store.get(contracts.SettingModelCatalogModels))
	if len(models) != 2 {
		t.Fatalf("after remove catalog = %+v", models)
	}
}

func TestModelCatalogPageRestoreDefaults(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"x","name":"X"}]`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	if err := tester.Click("模型"); err != nil {
		t.Fatalf("click 模型: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "X")
	h.click(tester, "恢复默认")
	if err := tester.Click("恢复默认值"); err != nil {
		t.Fatalf("confirm restore defaults: %v", err)
	}
	tester.Frame()
	if got := store.get(contracts.SettingModelCatalogModels); got != "[]" {
		t.Fatalf("restore wrote %q, want []", got)
	}
}

func TestModelCatalogPageContextWindow(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelCatalog(t, h)
	h.assertTexts(tester, "上下文 128k")

	// A non-numeric context window is stored as 0 (not shown in the list).
	openCatalogAddModal(t, h, tester)
	clearAndType(t, h, tester, "名称 输入", "Bad Window")
	clearAndType(t, h, tester, "模型 ID 输入", "bad-window")
	typeNumberInput(t, h, tester, "上下文窗口", "not-a-number")
	catalogClick(t, h, tester, "添加")
	models, err := contracts.ParseModelInfoList(store.get(contracts.SettingModelCatalogModels))
	if err != nil {
		t.Fatalf("parse stored catalog: %v", err)
	}
	if len(models) != 3 || models[2].ContextWindow != 0 {
		t.Fatalf("bad window stored = %+v", models)
	}

	openCatalogAddModal(t, h, tester)
	clearAndType(t, h, tester, "名称 输入", "Windowed")
	clearAndType(t, h, tester, "模型 ID 输入", "windowed")
	typeNumberInput(t, h, tester, "上下文窗口", "128000")
	catalogClick(t, h, tester, "添加")
	models, _ = contracts.ParseModelInfoList(store.get(contracts.SettingModelCatalogModels))
	if len(models) != 4 || models[3].ContextWindow != 128000 {
		t.Fatalf("context window stored = %+v", models)
	}
	h.assertTexts(tester, "上下文 128k")
}

func TestModelCatalogPageContextWindowEnglish(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"x","name":"X","contextWindow":128000}]`,
	})
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	if err := tester.Click("Model"); err != nil {
		t.Fatalf("click Model: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "128k context")
}

// --- thinking configuration ------------------------------------------------

// openModelCatalog opens the merged 模型 page (the catalog section) and
// returns its tester.
func openModelCatalog(t *testing.T, h *harness) *ui.Tester {
	t.Helper()
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	return tester
}

// catalogClick clicks one label (scroll-aware) and settles one more frame, so a
// select's Changed notice lands in the draft before the next assertion.
func catalogClick(t *testing.T, h *harness, tester *ui.Tester, label string) {
	t.Helper()
	h.click(tester, label)
	tester.Frame()
}

// openCatalogAddModal opens the add-model modal from the page's action row.
func openCatalogAddModal(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	catalogClick(t, h, tester, "添加模型")
	h.assertTexts(tester, "新增模型")
}

// expandCatalogAdvanced opens the editor's generation/custom-mode disclosure so the
// generation and custom-thinking fields become visible.
func expandCatalogAdvanced(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	// "温度 0–2" only renders inside the expanded group; "生成参数" alone is
	// also a substring of the collapsed summary.
	if catalogExactText(tester, "温度 0–2") {
		return
	}
	catalogClick(t, h, tester, "生成与自定义模式")
	h.assertTexts(tester, "生成参数", "温度 0–2", "自定义思考模式")
}

// catalogStoredModels parses the persisted catalog row.
func catalogStoredModels(t *testing.T, store *fakeSettings) []contracts.ModelInfo {
	t.Helper()
	models, err := contracts.ParseModelInfoList(store.get(contracts.SettingModelCatalogModels))
	if err != nil {
		t.Fatalf("parse stored catalog: %v", err)
	}
	return models
}

// catalogExactText reports whether the frame shows a label exactly want.
func catalogExactText(tester *ui.Tester, want string) bool {
	for _, text := range tester.Texts() {
		if text == want {
			return true
		}
	}
	return false
}

// catalogAssertNoExactTexts asserts none of the labels is shown as a whole
// label (the list rows show e.g. "思考：高强度" as one longer line, which a
// substring check would confuse with a select option).
func catalogAssertNoExactTexts(t *testing.T, tester *ui.Tester, unwanted ...string) {
	t.Helper()
	for _, text := range unwanted {
		if catalogExactText(tester, text) {
			t.Fatalf("frame unexpectedly shows the label %q; texts = %v", text, tester.Texts())
		}
	}
}

// TestModelCatalogPageThinkingSummary covers the row metadata: the effective
// family, the default thinking mode and the custom-mode count.
func TestModelCatalogPageThinkingSummary(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[
			{"id":"qwen-max","name":"Qwen Max","family":"qwen","defaultThinking":"qwen-deep","thinkingModes":[{"id":"fast","nameZh":"极速","params":{"enable_thinking":false}}]},
			{"id":"deepseek-reasoner","name":"DeepSeek Reasoner","defaultThinking":"reasoning-high"}
		]`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelCatalog(t, h)
	h.assertTexts(tester,
		"系列：千问 · 思考：深度思考 · 自定义模式 1",
		"系列：DeepSeek · 思考：高强度")
}

// TestModelCatalogPageEditThinkingConfig covers the editor: family, default
// mode and a custom mode are saved together, apply immediately to the row, and
// are visible again when the editor is reopened.
func TestModelCatalogPageEditThinkingConfig(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"m1","name":"M1","family":"other"}]`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelCatalog(t, h)

	catalogClick(t, h, tester, "编辑模型 m1")
	h.assertTexts(tester, "编辑模型", "模型名称", "思考系列", "默认思考", "取消", "保存模型")
	// Advanced settings start collapsed behind a one-line summary.
	h.assertTexts(tester, "生成与自定义模式", "未配置生成参数或自定义模式")
	catalogAssertNoExactTexts(t, tester, "生成参数", "自定义思考模式", "添加思考模式")

	// The family picker offers the four families; Qwen switches the effective
	// built-in mode list.
	catalogClick(t, h, tester, "思考系列菜单")
	h.assertTexts(tester, "千问", "DeepSeek", "其他", "自定义")
	catalogClick(t, h, tester, "千问")
	h.assertTexts(tester, "千问")

	// The default picker lists the Qwen built-ins and saves the chosen one.
	catalogClick(t, h, tester, "默认思考菜单")
	h.assertTexts(tester, "关闭思考", "思考开启", "深度思考")
	catalogClick(t, h, tester, "思考开启")

	// Custom modes live behind the 高级选项 disclosure.
	expandCatalogAdvanced(t, h, tester)
	// A custom mode carries an ID, a display name and request parameters.
	catalogClick(t, h, tester, "添加思考模式")
	h.assertTexts(tester, "模式 1 ID", "模式 1 名称", "模式 1 参数", "删除思考模式 1")
	clearAndType(t, h, tester, "模式 1 ID", "speed")
	clearAndType(t, h, tester, "模式 1 名称", "极速")
	clearAndType(t, h, tester, "模式 1 参数",
		`{"enable_thinking": false, "thinking_budget": 1024}`)
	catalogClick(t, h, tester, "保存模型")

	h.assertNoTexts(tester, "自定义思考模式", "添加思考模式")
	// The row reflects the saved configuration immediately.
	h.assertTexts(tester, "系列：千问 · 思考：思考开启 · 自定义模式 1")

	models := catalogStoredModels(t, store)
	if len(models) != 1 {
		t.Fatalf("stored catalog = %+v", models)
	}
	model := models[0]
	if model.Family != contracts.ModelFamilyQwen {
		t.Fatalf("family = %q, want qwen", model.Family)
	}
	if model.DefaultThinking != contracts.ThinkingModeQwenOn {
		t.Fatalf("default thinking = %q, want qwen-on", model.DefaultThinking)
	}
	if len(model.ThinkingModes) != 1 || model.ThinkingModes[0].ID != "speed" {
		t.Fatalf("thinking modes = %+v", model.ThinkingModes)
	}
	if name := model.ThinkingModes[0].NameZH; name != "极速" {
		t.Fatalf("custom mode name = %q, want 极速", name)
	}
	if got := model.ThinkingModes[0].Params["enable_thinking"]; got != false {
		t.Fatalf("enable_thinking = %v, want false", got)
	}
	if got := model.ThinkingModes[0].Params["thinking_budget"]; got != float64(1024) {
		t.Fatalf("thinking_budget = %v, want 1024", got)
	}

	// Reopening the editor shows the persisted configuration; editing the
	// custom mode's name writes it back.
	catalogClick(t, h, tester, "编辑模型 m1")
	h.assertTexts(tester, "千问", "思考开启")
	expandCatalogAdvanced(t, h, tester)
	h.assertTexts(tester, "模式 1 ID", "删除思考模式 1")
	clearAndType(t, h, tester, "模式 1 名称", "极速+")
	catalogClick(t, h, tester, "保存模型")
	models = catalogStoredModels(t, store)
	if len(models) != 1 || len(models[0].ThinkingModes) != 1 {
		t.Fatalf("stored catalog after edit = %+v", models)
	}
	if name := models[0].ThinkingModes[0].NameZH; name != "极速+" {
		t.Fatalf("edited custom mode name = %q, want 极速+", name)
	}
}

// TestModelCatalogPageFamilyChangeClearsDefault covers dependency handling: a
// default mode the new family no longer offers falls back to "follow the family
// default", and the select lists only the modes that remain.
func TestModelCatalogPageFamilyChangeClearsDefault(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"m1","name":"M1","family":"other","defaultThinking":"reasoning-high"}]`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelCatalog(t, h)

	catalogClick(t, h, tester, "编辑模型 m1")
	h.assertTexts(tester, "高强度")

	// Custom family has no built-ins, so the reasoning modes disappear from the
	// default picker and the stale default is cleared.
	catalogClick(t, h, tester, "思考系列菜单")
	catalogClick(t, h, tester, "自定义")
	h.assertTexts(tester, "跟随系列默认")
	catalogClick(t, h, tester, "默认思考菜单")
	catalogAssertNoExactTexts(t, tester, "低强度", "中强度", "高强度")
	tester.Key(0, ui.KeyEscape)
	tester.Frame()

	catalogClick(t, h, tester, "保存模型")
	models := catalogStoredModels(t, store)
	if len(models) != 1 || models[0].Family != contracts.ModelFamilyCustom {
		t.Fatalf("stored catalog = %+v", models)
	}
	if got := models[0].DefaultThinking; got != "" {
		t.Fatalf("default thinking = %q, want empty (follow family default)", got)
	}
	h.assertTexts(tester, "系列：自定义 · 思考：跟随系列默认")
}

// TestModelCatalogPageFamilyOffersBuiltins covers the effective-mode select for
// a Qwen family row: the Qwen built-in modes replace the reasoning-effort ones.
func TestModelCatalogPageFamilyOffersBuiltins(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"m1","name":"M1","family":"other","defaultThinking":"reasoning-high"}]`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelCatalog(t, h)

	catalogClick(t, h, tester, "编辑模型 m1")
	catalogClick(t, h, tester, "思考系列菜单")
	catalogClick(t, h, tester, "千问")
	catalogClick(t, h, tester, "默认思考菜单")
	h.assertTexts(tester, "关闭思考", "思考开启", "深度思考")
	catalogAssertNoExactTexts(t, tester, "低强度", "中强度", "高强度")
	catalogClick(t, h, tester, "深度思考")
	h.assertTexts(tester, "深度思考")
	catalogClick(t, h, tester, "保存模型")

	models := catalogStoredModels(t, store)
	if len(models) != 1 || models[0].Family != contracts.ModelFamilyQwen {
		t.Fatalf("stored catalog = %+v", models)
	}
	if got := models[0].DefaultThinking; got != contracts.ThinkingModeQwenDeep {
		t.Fatalf("default thinking = %q, want qwen-deep", got)
	}
}

// TestModelCatalogPageAddInfersFamily covers the add form: the family follows
// the typed model ID until the user picks one, and is persisted on add.
func TestModelCatalogPageAddInfersFamily(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelCatalog(t, h)

	openCatalogAddModal(t, h, tester)
	clearAndType(t, h, tester, "模型 ID 输入", "qwen2.5-coder")
	h.assertTexts(tester, "千问")
	clearAndType(t, h, tester, "名称 输入", "Qwen Coder")
	catalogClick(t, h, tester, "添加")
	models := catalogStoredModels(t, store)
	if len(models) != 3 || models[2].ID != "qwen2.5-coder" ||
		models[2].Family != contracts.ModelFamilyQwen {
		t.Fatalf("qwen add stored = %+v", models)
	}

	// An explicit pick overrides the inferred family.
	openCatalogAddModal(t, h, tester)
	clearAndType(t, h, tester, "模型 ID 输入", "my-custom-model")
	h.assertTexts(tester, "其他")
	catalogClick(t, h, tester, "模型系列菜单")
	catalogClick(t, h, tester, "自定义")
	catalogClick(t, h, tester, "添加")
	models = catalogStoredModels(t, store)
	if len(models) != 4 || models[3].ID != "my-custom-model" ||
		models[3].Family != contracts.ModelFamilyCustom {
		t.Fatalf("custom add stored = %+v", models)
	}

	// The add form resets: a new ID is inferred again.
	openCatalogAddModal(t, h, tester)
	clearAndType(t, h, tester, "模型 ID 输入", "deepseek-v9")
	h.assertTexts(tester, "DeepSeek")
	h.click(tester, "取消")
}

// TestModelCatalogPageEditorValidation covers the editor's inline validation:
// invalid JSON, a non-object JSON value, duplicate IDs and a mode name without
// an ID all keep the modal open and the catalog untouched.
func TestModelCatalogPageEditorValidation(t *testing.T) {
	const original = `[{"id":"m1","name":"M1","family":"qwen"}]`
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: original,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelCatalog(t, h)
	catalogClick(t, h, tester, "编辑模型 m1")
	expandCatalogAdvanced(t, h, tester)
	catalogClick(t, h, tester, "添加思考模式")
	clearAndType(t, h, tester, "模式 1 ID", "bad")

	// Not JSON at all.
	clearAndType(t, h, tester, "模式 1 参数", "not json")
	catalogClick(t, h, tester, "保存模型")
	h.assertTexts(tester, "不是有效 JSON", "自定义思考模式")

	// Valid JSON but not an object.
	clearAndType(t, h, tester, "模式 1 参数", "[1,2]")
	catalogClick(t, h, tester, "保存模型")
	h.assertTexts(tester, "必须是 JSON 对象")

	// Duplicate IDs (case-insensitive).
	clearAndType(t, h, tester, "模式 1 参数", "{}")
	catalogClick(t, h, tester, "添加思考模式")
	clearAndType(t, h, tester, "模式 2 ID", "BAD")
	catalogClick(t, h, tester, "保存模型")
	h.assertTexts(tester, "重复")

	// A partially filled row needs an ID.
	clearAndType(t, h, tester, "模式 2 ID", "")
	clearAndType(t, h, tester, "模式 2 名称", "verbose")
	catalogClick(t, h, tester, "保存模型")
	h.assertTexts(tester, "ID 不能为空")

	if got := store.get(contracts.SettingModelCatalogModels); got != original {
		t.Fatalf("failed saves changed the catalog: %q", got)
	}

	// Removing the broken row and saving succeeds.
	catalogClick(t, h, tester, "删除思考模式 2")
	catalogClick(t, h, tester, "保存模型")
	h.assertNoTexts(tester, "自定义思考模式")
	models := catalogStoredModels(t, store)
	if len(models) != 1 || len(models[0].ThinkingModes) != 1 || models[0].ThinkingModes[0].ID != "bad" {
		t.Fatalf("stored catalog = %+v", models)
	}
}

// TestModelCatalogPageOverrideBuiltinThinkingMode covers the documented
// override: a custom mode with a built-in ID replaces the built-in preset
// instead of failing validation.
func TestModelCatalogPageOverrideBuiltinThinkingMode(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"m1","name":"M1","family":"qwen"}]`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelCatalog(t, h)
	catalogClick(t, h, tester, "编辑模型 m1")
	expandCatalogAdvanced(t, h, tester)
	catalogClick(t, h, tester, "添加思考模式")
	clearAndType(t, h, tester, "模式 1 ID", contracts.ThinkingModeQwenOn)
	clearAndType(t, h, tester, "模式 1 名称", "开启并限定 top_k")
	clearAndType(t, h, tester, "模式 1 参数", `{"enable_thinking": true, "top_k": 5}`)
	catalogClick(t, h, tester, "保存模型")

	models := catalogStoredModels(t, store)
	if len(models) != 1 || len(models[0].ThinkingModes) != 1 ||
		models[0].ThinkingModes[0].ID != contracts.ThinkingModeQwenOn {
		t.Fatalf("stored catalog = %+v", models)
	}
	if got := models[0].ThinkingModes[0].Params["top_k"]; got != float64(5) {
		t.Fatalf("top_k = %v, want 5", got)
	}
	h.assertTexts(tester, "系列：千问 · 思考：跟随系列默认 · 自定义模式 1")
}

// TestModelCatalogPageRemoveDefaultCustomMode covers dependency handling in the
// custom-mode editor: removing the mode a model defaults to falls back to the
// family default instead of saving a dangling ID.
func TestModelCatalogPageRemoveDefaultCustomMode(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"m1","name":"M1","family":"qwen","defaultThinking":"speed","thinkingModes":[{"id":"speed","nameZh":"极速","params":{"enable_thinking":false}}]}]`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelCatalog(t, h)
	catalogClick(t, h, tester, "编辑模型 m1")
	h.assertTexts(tester, "极速")
	expandCatalogAdvanced(t, h, tester)

	catalogClick(t, h, tester, "删除思考模式 1")
	h.assertTexts(tester, "跟随系列默认", "还没有自定义模式。")
	catalogClick(t, h, tester, "保存模型")

	models := catalogStoredModels(t, store)
	if len(models) != 1 {
		t.Fatalf("stored catalog = %+v", models)
	}
	if got := models[0].DefaultThinking; got != "" {
		t.Fatalf("default thinking = %q, want empty", got)
	}
	if len(models[0].ThinkingModes) != 0 {
		t.Fatalf("thinking modes = %+v, want none", models[0].ThinkingModes)
	}
	h.assertTexts(tester, "系列：千问 · 思考：跟随系列默认")
}

// TestModelCatalogPageThinkingEnglish covers the English copy of the row
// metadata, the editor and the thinking-mode selects.
func TestModelCatalogPageThinkingEnglish(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"deepseek-chat","name":"DeepSeek Chat","defaultThinking":"reasoning-medium","temperature":0.3,"topP":0.8,"maxTokens":512,"thinkingModes":[{"id":"fast","nameZh":"Fast","nameEn":"Fast"}]}]`,
	})
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Model")

	h.assertTexts(tester,
		"Family: DeepSeek · Thinking: Medium · 1 custom mode",
		"Generation: temperature 0.3 · Top P 0.8 · 512 tokens")

	// The add form opens from one button and closes without writing.
	h.click(tester, "Add model")
	h.assertTexts(tester, "Name input", "Model ID input", "Model family menu", "Cancel", "Add")
	h.click(tester, "Cancel")
	h.assertNoTexts(tester, "Model ID input")

	h.click(tester, "Edit model deepseek-chat")
	h.assertTexts(tester, "Edit model", "Model name", "Thinking family",
		"Default thinking", "Generation & custom modes",
		"Generation: temperature 0.3 · Top P 0.8 · 512 tokens · 1 custom mode")
	catalogAssertNoExactTexts(t, tester, "Generation", "Custom thinking modes",
		"Temperature 0–2", "Add thinking mode")
	h.click(tester, "Generation & custom modes")
	h.assertTexts(tester, "Generation", "Temperature 0–2", "Top P 0–1",
		"Max output tokens", "Custom thinking modes", "Add thinking mode",
		"Remove thinking mode 1")
	h.click(tester, "Thinking family menu")
	h.assertTexts(tester, "Qwen", "DeepSeek", "Other", "Custom")
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	h.click(tester, "Cancel")
	h.assertNoTexts(tester, "Custom thinking modes")
}

// --- generation parameters -------------------------------------------------

// TestModelCatalogPageEditGenerationParams covers the Generation group: values
// save through writeCatalogModels, the row shows them, and reopening the editor
// prefills them (saving again keeps them).
func TestModelCatalogPageEditGenerationParams(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"m1","name":"M1","family":"qwen"}]`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelCatalog(t, h)
	catalogClick(t, h, tester, "编辑模型 m1")
	catalogAssertNoExactTexts(t, tester, "生成参数", "温度 0–2")
	expandCatalogAdvanced(t, h, tester)
	h.assertTexts(tester, "生成参数", "温度 0–2", "Top P 0–1", "最大输出 tokens")

	catalogClick(t, h, tester, "使用默认温度")
	catalogClick(t, h, tester, "使用默认 Top P")
	catalogClick(t, h, tester, "使用默认 max tokens")
	typeNumberInput(t, h, tester, "温度", "0.7")
	typeNumberInput(t, h, tester, "Top P", "0.9")
	typeNumberInput(t, h, tester, "最大输出 tokens", "512")
	catalogClick(t, h, tester, "保存模型")

	h.assertNoTexts(tester, "生成参数")
	h.assertTexts(tester, "生成：温度 0.7 · Top P 0.9 · 512 tokens")
	assertGeneration := func(models []contracts.ModelInfo) {
		t.Helper()
		if len(models) != 1 {
			t.Fatalf("stored catalog = %+v", models)
		}
		model := models[0]
		if model.Temperature == nil || *model.Temperature != 0.7 {
			t.Fatalf("temperature = %v, want 0.7", model.Temperature)
		}
		if model.TopP == nil || *model.TopP != 0.9 {
			t.Fatalf("topP = %v, want 0.9", model.TopP)
		}
		if model.MaxTokens != 512 {
			t.Fatalf("maxTokens = %d, want 512", model.MaxTokens)
		}
	}
	assertGeneration(catalogStoredModels(t, store))

	// Reopening prefills the saved values (the collapsed summary shows them) and
	// saving again keeps them.
	catalogClick(t, h, tester, "编辑模型 m1")
	h.assertTexts(tester, "生成：温度 0.7 · Top P 0.9 · 512 tokens")
	catalogClick(t, h, tester, "保存模型")
	assertGeneration(catalogStoredModels(t, store))
}

// TestModelCatalogPageGenerationBoundaries covers the inclusive boundaries.
func TestModelCatalogPageGenerationBoundaries(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"m1","name":"M1","family":"qwen"}]`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelCatalog(t, h)
	catalogClick(t, h, tester, "编辑模型 m1")
	expandCatalogAdvanced(t, h, tester)
	catalogClick(t, h, tester, "使用默认温度")
	catalogClick(t, h, tester, "使用默认 Top P")
	catalogClick(t, h, tester, "使用默认 max tokens")
	typeNumberInput(t, h, tester, "温度", "0")
	typeNumberInput(t, h, tester, "Top P", "1")
	typeNumberInput(t, h, tester, "最大输出 tokens", "1")
	catalogClick(t, h, tester, "保存模型")

	models := catalogStoredModels(t, store)
	if len(models) != 1 || models[0].Temperature == nil || *models[0].Temperature != 0 {
		t.Fatalf("stored catalog = %+v", models)
	}
	if models[0].TopP == nil || *models[0].TopP != 1 || models[0].MaxTokens != 1 {
		t.Fatalf("stored catalog = %+v", models)
	}
	h.assertTexts(tester, "生成：温度 0 · Top P 1 · 1 tokens")

	catalogClick(t, h, tester, "编辑模型 m1")
	expandCatalogAdvanced(t, h, tester)
	typeNumberInput(t, h, tester, "温度", "2")
	catalogClick(t, h, tester, "保存模型")
	models = catalogStoredModels(t, store)
	if models[0].Temperature == nil || *models[0].Temperature != 2 {
		t.Fatalf("temperature = %v, want 2", models[0].Temperature)
	}
}

// TestModelCatalogPageGenerationOutOfRange covers the official ui.NumberInput
// rule for the optional generation parameters: a typed value outside its range
// is ignored (the modal keeps the last valid value), so the old inline
// "must be a number / in range" errors no longer exist; the "use the provider
// default" switch is what clears a value.
func TestModelCatalogPageGenerationOutOfRange(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"m1","name":"M1","family":"qwen"}]`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelCatalog(t, h)
	catalogClick(t, h, tester, "编辑模型 m1")
	expandCatalogAdvanced(t, h, tester)

	catalogClick(t, h, tester, "使用默认温度")
	catalogClick(t, h, tester, "使用默认 Top P")
	catalogClick(t, h, tester, "使用默认 max tokens")
	typeNumberInput(t, h, tester, "温度", "2.5")         // above the 0–2 range: ignored
	typeNumberInput(t, h, tester, "Top P", "1.2")      // above the 0–1 range: ignored
	typeNumberInput(t, h, tester, "最大输出 tokens", "-5") // below the 1 minimum: ignored
	catalogClick(t, h, tester, "保存模型")

	models := catalogStoredModels(t, store)
	if len(models) != 1 || models[0].Temperature == nil || *models[0].Temperature != 0.7 ||
		models[0].TopP == nil || *models[0].TopP != 0.9 || models[0].MaxTokens != 2048 {
		t.Fatalf("stored catalog = %+v, want the last valid 0.7/0.9/2048", models)
	}
	h.assertTexts(tester, "生成：温度 0.7 · Top P 0.9 · 2048 tokens")
	h.assertNoTexts(tester, "必须在 0–2 之间", "必须在 0–1 之间", "必须大于 0", "必须是数字")

	// The provider-default switch clears a value (nil / 0) while the other
	// values stay.
	catalogClick(t, h, tester, "编辑模型 m1")
	expandCatalogAdvanced(t, h, tester)
	catalogClick(t, h, tester, "使用默认温度")
	catalogClick(t, h, tester, "保存模型")
	models = catalogStoredModels(t, store)
	if models[0].Temperature != nil || models[0].TopP == nil || *models[0].TopP != 0.9 || models[0].MaxTokens != 2048 {
		t.Fatalf("stored catalog = %+v, want nil temperature and kept Top P/tokens", models)
	}
}

// TestModelCatalogPageGenerationCleared covers clearing every field: the row's
// generation line disappears and the catalog stores no values.
func TestModelCatalogPageGenerationCleared(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"m1","name":"M1","family":"qwen","temperature":0.3,"topP":0.8,"maxTokens":512}]`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelCatalog(t, h)
	h.assertTexts(tester, "生成：温度 0.3 · Top P 0.8 · 512 tokens")

	catalogClick(t, h, tester, "编辑模型 m1")
	expandCatalogAdvanced(t, h, tester)
	catalogClick(t, h, tester, "使用默认温度")
	catalogClick(t, h, tester, "使用默认 Top P")
	catalogClick(t, h, tester, "使用默认 max tokens")
	catalogClick(t, h, tester, "保存模型")

	models := catalogStoredModels(t, store)
	if len(models) != 1 || models[0].Temperature != nil || models[0].TopP != nil {
		t.Fatalf("stored catalog = %+v", models)
	}
	if models[0].MaxTokens != 0 {
		t.Fatalf("maxTokens = %d, want 0", models[0].MaxTokens)
	}
	h.assertNoTexts(tester, "生成：")
}

// TestModelCatalogPageGenerationSanitizedSummary covers out-of-range values
// already in the settings row: the row summary shows the clamped effective
// values the catalog module will use.
func TestModelCatalogPageGenerationSanitizedSummary(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"m1","name":"M1","family":"qwen","temperature":2.5,"topP":1.4,"maxTokens":-3}]`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelCatalog(t, h)
	h.assertTexts(tester, "生成：温度 2 · Top P 1")
}

// TestModelCatalogPageAddModal covers the collapsed add flow: the page keeps
// the list, one button opens the form in a native modal, and cancel leaves the
// catalog untouched.
func TestModelCatalogPageAddModal(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelCatalog(t, h)
	h.assertTexts(tester, "添加模型", "恢复默认")
	h.assertNoTexts(tester, "新增模型", "名称 输入", "模型 ID 输入", "模型系列菜单")

	openCatalogAddModal(t, h, tester)
	h.assertTexts(tester, "名称 输入", "模型 ID 输入", "备注 输入",
		"上下文窗口 输入", "模型系列菜单", "取消", "添加")
	h.click(tester, "取消")
	h.assertNoTexts(tester, "新增模型", "名称 输入")
	if got := store.get(contracts.SettingModelCatalogModels); got != "" {
		t.Fatalf("cancel wrote the catalog: %q", got)
	}
}

// TestModelCatalogPageAdvancedCollapsed covers the editor's progressive
// disclosure: the advanced group starts collapsed behind a one-line summary,
// expanding reveals the existing fields, and saving round-trips the values.
func TestModelCatalogPageAdvancedCollapsed(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"m1","name":"M1","family":"qwen","temperature":0.4,"topP":0.9,"maxTokens":2048,"thinkingModes":[{"id":"fast","nameZh":"极速","params":{"enable_thinking":false}}]}]`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelCatalog(t, h)
	catalogClick(t, h, tester, "编辑模型 m1")

	const summary = "生成：温度 0.4 · Top P 0.9 · 2048 tokens · 自定义模式 1"
	h.assertTexts(tester, "编辑模型", "生成与自定义模式", summary)
	catalogAssertNoExactTexts(t, tester, "生成参数", "温度 0–2", "自定义思考模式",
		"添加思考模式", "删除思考模式 1")

	expandCatalogAdvanced(t, h, tester)
	h.assertTexts(tester, "生成参数", "温度 0–2", "Top P 0–1", "最大输出 tokens",
		"自定义思考模式", "删除思考模式 1")
	catalogAssertNoExactTexts(t, tester, summary)

	catalogClick(t, h, tester, "保存模型")
	h.assertNoTexts(tester, "生成参数", "自定义思考模式")
	catalogAssertNoExactTexts(t, tester, "编辑模型")
	h.assertTexts(tester, "生成：温度 0.4 · Top P 0.9 · 2048 tokens")

	models := catalogStoredModels(t, store)
	if len(models) != 1 || models[0].Temperature == nil || *models[0].Temperature != 0.4 {
		t.Fatalf("stored catalog = %+v", models)
	}
	if models[0].TopP == nil || *models[0].TopP != 0.9 || models[0].MaxTokens != 2048 {
		t.Fatalf("stored catalog = %+v", models)
	}
	if len(models[0].ThinkingModes) != 1 {
		t.Fatalf("thinking modes = %+v", models[0].ThinkingModes)
	}
}
