package settings_test

// Frame tests for the model settings page after the multi-provider migration:
// the model list is the only place a model (and its provider connection) is
// configured, list edits apply immediately, one row is the default model and its
// connection is mirrored into chat.model/chat.baseURL/chat.apiKey, and the page
// footer Save owns only the prompt/runtime drafts.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// --- helpers ---------------------------------------------------------------

// modelCatalogRow encodes one modelcatalog.models settings row.
func modelCatalogRow(t *testing.T, models ...contracts.ModelInfo) string {
	t.Helper()
	raw, err := contracts.EncodeModelInfoList(models)
	if err != nil {
		t.Fatalf("encode model list: %v", err)
	}
	return raw
}

// openModelPage opens the merged 模型 page and returns its tester.
func openModelPage(t *testing.T, h *harness) *ui.Tester {
	t.Helper()
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	tester.Frame()
	return tester
}

// openAddModal opens the add-model dialog from the list's action row.
func openAddModal(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	h.click(tester, "添加模型")
	tester.Frame()
	h.assertTexts(tester, "服务商菜单", "模型 ID 输入", "Base URL 输入", "API Key 输入")
}

// openEditModal opens the editor of one model row.
func openEditModal(t *testing.T, h *harness, tester *ui.Tester, id string) {
	t.Helper()
	h.click(tester, "编辑模型 "+id)
	tester.Frame()
	h.assertTexts(tester, "编辑模型", "保存模型")
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

// countExactText counts labels exactly equal to text.
func countExactText(tester *ui.Tester, text string) int {
	count := 0
	for _, value := range tester.Texts() {
		if value == text {
			count++
		}
	}
	return count
}

// expandCatalogAdvanced opens the editor's generation/custom-mode disclosure so
// the generation and custom-thinking fields become visible.
func expandCatalogAdvanced(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	// "温度 0–2" only renders inside the expanded group; "生成参数" alone is
	// also a substring of the collapsed summary.
	if catalogExactText(tester, "温度 0–2") {
		return
	}
	h.click(tester, "生成与自定义模式")
	tester.Frame()
	h.assertTexts(tester, "生成参数", "温度 0–2", "自定义思考模式")
}

// scrollDialogBody scrolls the open dialog's bounded form body down by dy so a
// field below the fold becomes clickable. The editor body is capped at
// design.DialogBodyMax, so an expanded advanced group scrolls inside the modal.
func scrollDialogBody(t *testing.T, tester *ui.Tester, dy float32) {
	t.Helper()
	tester.Scroll(600, 400, 0, dy)
	tester.Frame()
}

// requireCheckButton asserts one test-connection button state.
func requireCheckButton(t *testing.T, tester *ui.Tester, label string, disabled bool) {
	t.Helper()
	info, ok := tester.AccessByRole(ui.RoleButton, label)
	if !ok {
		t.Fatalf("frame has no button named %q; texts = %v", label, tester.Texts())
	}
	if info.Disabled != disabled {
		t.Fatalf("button %q disabled = %v, want %v", label, info.Disabled, disabled)
	}
}

// deepseekFixture is one fully configured model row.
func deepseekFixture() contracts.ModelInfo {
	return contracts.ModelInfo{
		ID: "deepseek-chat", Name: "DeepSeek Chat",
		BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-deepseek",
		ContextWindow: 64000,
	}
}

// --- page structure --------------------------------------------------------

func TestModelPageEmptyStateHasNoBuiltinsAndNoRestore(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)

	h.assertTexts(tester, "模型列表", "还没有模型", "添加模型", "提示词", "高级选项")
	h.assertNoTexts(tester, "恢复默认", "快速预设", "连接检查", "恢复默认值")
	// The list is empty: nothing is written on render.
	if got := store.get(contracts.SettingModelCatalogModels); got != "" {
		t.Fatalf("rendering the empty list wrote the catalog: %q", got)
	}
	if got := store.get(contracts.SettingsChatModel); got != "" {
		t.Fatalf("rendering the empty list wrote the default model mirror: %q", got)
	}
}

func TestModelPageRowsShowConnectionAndDefault(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: modelCatalogRow(t,
			deepseekFixture(),
			contracts.ModelInfo{
				ID: "qwen-max", Name: "Qwen Max",
				BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-qwen",
			},
		),
		// The second row is the configured default.
		contracts.SettingsChatModel:   "qwen-max",
		contracts.SettingsChatBaseURL: "https://api.deepseek.com/v1",
		contracts.SettingsChatAPIKey:  "sk-qwen",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)

	h.assertTexts(tester,
		"模型列表", "DeepSeek Chat", "deepseek-chat",
		"Qwen Max", "qwen-max",
		"DeepSeek", "密钥已设置", "上下文 64k",
		"系列：DeepSeek · 思考：跟随系列默认",
		// The current section reads the default row from the list.
		"默认模型", "qwen-max",
	)
	if got := countExactText(tester, "默认"); got != 1 {
		t.Fatalf("default badges = %d, want 1 (only the chat.model row); texts = %v", got, tester.Texts())
	}
	// Keys are never rendered in clear text.
	h.assertNoTexts(tester, "sk-deepseek", "sk-qwen")
	// The default model is mirrored from the row, including the key.
	if got := store.get(contracts.SettingsChatAPIKey); got != "sk-qwen" {
		t.Fatalf("default mirror key = %q", got)
	}
}

func TestModelPageMalformedCatalogReportsAndCanBeRewritten(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `{not json`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	h.assertTexts(tester, "模型配置无法解析", "添加模型")

	// Adding a model rewrites the broken row instead of blocking the user.
	openAddModal(t, h, tester)
	clearAndType(t, h, tester, "模型 ID 输入", "m1")
	clearAndType(t, h, tester, "Base URL 输入", "https://api.example.com/v1")
	h.click(tester, "添加")
	tester.Frame()
	models := catalogStoredModels(t, store)
	if len(models) != 1 || models[0].ID != "m1" {
		t.Fatalf("rewritten catalog = %+v", models)
	}
}

// --- add flow --------------------------------------------------------------

func TestModelAddFromProviderPresetWritesModelAndMirror(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	openAddModal(t, h, tester)

	// The provider selector starts on the custom entry; choosing a preset fills
	// the endpoint and, while the ID is empty, its example model.
	h.click(tester, "服务商菜单")
	h.click(tester, "OpenAI")
	tester.Frame()
	if got := inputValue(t, tester, "模型 ID 输入"); got != "gpt-4o-mini" {
		t.Fatalf("preset model ID = %q, want gpt-4o-mini", got)
	}
	if got := inputValue(t, tester, "Base URL 输入"); got != "https://api.openai.com/v1" {
		t.Fatalf("preset base URL = %q", got)
	}
	clearAndType(t, h, tester, "名称 输入", "GPT Mini")
	clearAndType(t, h, tester, "API Key 输入", "sk-openai")
	typeNumberInput(t, h, tester, "上下文窗口", "128000")
	h.assertTexts(tester, "设为默认模型", "第一个模型会自动设为默认。")
	h.click(tester, "添加")
	tester.Frame()

	// The dialog closed and the list shows the new row immediately.
	h.assertNoTexts(tester, "服务商菜单")
	h.assertTexts(tester, "GPT Mini", "gpt-4o-mini", "OpenAI",
		"https://api.openai.com/v1", "密钥已设置", "上下文 128k")
	models := catalogStoredModels(t, store)
	if len(models) != 1 {
		t.Fatalf("stored catalog = %+v", models)
	}
	model := models[0]
	if model.ID != "gpt-4o-mini" || model.Name != "GPT Mini" ||
		model.BaseURL != "https://api.openai.com/v1" || model.APIKey != "sk-openai" ||
		model.ContextWindow != 128000 || model.Family != contracts.ModelFamilyOther {
		t.Fatalf("stored model = %+v", model)
	}
	// The first model is the default: its connection is mirrored.
	if got := store.get(contracts.SettingsChatModel); got != "gpt-4o-mini" {
		t.Fatalf("mirror model = %q", got)
	}
	if got := store.get(contracts.SettingsChatBaseURL); got != "https://api.openai.com/v1" {
		t.Fatalf("mirror base URL = %q", got)
	}
	if got := store.get(contracts.SettingsChatAPIKey); got != "sk-openai" {
		t.Fatalf("mirror API key = %q", got)
	}
}

func TestModelAddCustomServiceKeepsTypedEndpoint(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	openAddModal(t, h, tester)

	// The custom entry (the selector's default) keeps the typed endpoint and
	// does not invent a model ID.
	clearAndType(t, h, tester, "模型 ID 输入", "local-llm")
	clearAndType(t, h, tester, "Base URL 输入", "http://127.0.0.1:8080/v1/")
	clearAndType(t, h, tester, "API Key 输入", "sk-local")
	h.click(tester, "添加")
	tester.Frame()

	h.assertTexts(tester, "local-llm", "自定义服务", "http://127.0.0.1:8080/v1", "密钥已设置")
	models := catalogStoredModels(t, store)
	if len(models) != 1 || models[0].BaseURL != "http://127.0.0.1:8080/v1" ||
		models[0].APIKey != "sk-local" {
		t.Fatalf("stored model = %+v", models)
	}
}

func TestModelAddValidation(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: modelCatalogRow(t,
			contracts.ModelInfo{ID: "m1", BaseURL: "https://api.example.com/v1"}),
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	openAddModal(t, h, tester)

	// Required model ID.
	h.click(tester, "添加")
	tester.Frame()
	h.assertTexts(tester, "模型 ID 不能为空")

	// Required Base URL.
	clearAndType(t, h, tester, "模型 ID 输入", "new-model")
	h.click(tester, "添加")
	tester.Frame()
	h.assertTexts(tester, "Base URL 不能为空")

	// Duplicate IDs are rejected case-insensitively and write nothing.
	clearAndType(t, h, tester, "模型 ID 输入", "M1")
	clearAndType(t, h, tester, "Base URL 输入", "https://api.example.com/v1")
	h.click(tester, "添加")
	tester.Frame()
	h.assertTexts(tester, "模型 ID 已存在")
	models := catalogStoredModels(t, store)
	if len(models) != 1 || models[0].ID != "m1" {
		t.Fatalf("failed adds changed the catalog: %+v", models)
	}
	// The dialog stays open so the user can fix the draft.
	h.assertTexts(tester, "服务商菜单")
}

func TestModelAddInheritsKeyForSameEndpoint(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: modelCatalogRow(t,
			contracts.ModelInfo{ID: "m1", BaseURL: "https://api.example.com/v1", APIKey: "sk-shared"}),
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	openAddModal(t, h, tester)
	clearAndType(t, h, tester, "模型 ID 输入", "m2")
	clearAndType(t, h, tester, "Base URL 输入", "https://api.example.com/v1/")
	// API key left blank: it inherits the key of the model on the same endpoint.
	h.click(tester, "添加")
	tester.Frame()
	models := catalogStoredModels(t, store)
	if len(models) != 2 || models[1].APIKey != "sk-shared" {
		t.Fatalf("inherited catalog = %+v", models)
	}
	h.assertTexts(tester, "m2", "密钥已设置")
}

func TestModelAddSetAsDefaultUpdatesMirror(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: modelCatalogRow(t,
			contracts.ModelInfo{ID: "m1", Name: "M1", BaseURL: "https://api.example.com/v1", APIKey: "sk-m1"}),
		contracts.SettingsChatModel:   "m1",
		contracts.SettingsChatBaseURL: "https://api.example.com/v1",
		contracts.SettingsChatAPIKey:  "sk-m1",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	openAddModal(t, h, tester)
	clearAndType(t, h, tester, "模型 ID 输入", "m2")
	clearAndType(t, h, tester, "Base URL 输入", "https://other.example/v1")
	clearAndType(t, h, tester, "API Key 输入", "sk-m2")
	h.click(tester, "设为默认模型")
	tester.Frame()
	h.click(tester, "添加")
	tester.Frame()

	h.assertTexts(tester, "已添加模型")
	if got := countExactText(tester, "默认"); got != 1 {
		t.Fatalf("default badges = %d, want 1; texts = %v", got, tester.Texts())
	}
	if got := store.get(contracts.SettingsChatModel); got != "m2" {
		t.Fatalf("mirror model = %q, want m2", got)
	}
	if got := store.get(contracts.SettingsChatBaseURL); got != "https://other.example/v1" {
		t.Fatalf("mirror base URL = %q", got)
	}
	if got := store.get(contracts.SettingsChatAPIKey); got != "sk-m2" {
		t.Fatalf("mirror API key = %q", got)
	}
}

// --- edit flow -------------------------------------------------------------

func TestModelEditConnectionAndAPIKey(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: modelCatalogRow(t,
			contracts.ModelInfo{ID: "m1", Name: "M1", BaseURL: "https://api.example.com/v1", APIKey: "sk-old"}),
		contracts.SettingsChatModel: "m1",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	openEditModal(t, h, tester, "m1")

	h.assertTexts(tester, "m1", "不可修改", "服务商菜单", "API Key", "清除已保存密钥",
		"思考系列菜单", "默认思考菜单", "设为默认模型")
	clearAndType(t, h, tester, "模型名称 输入", "M1 Renamed")
	clearAndType(t, h, tester, "Base URL 输入", "https://api.openai.com/v1")
	tester.Frame()
	// The provider selector follows the typed endpoint.
	h.assertTexts(tester, "OpenAI")
	clearAndType(t, h, tester, "API Key 输入", "sk-new")
	typeNumberInput(t, h, tester, "上下文窗口", "128000")
	h.click(tester, "保存模型")
	tester.Frame()

	h.assertNoTexts(tester, "保存模型")
	h.assertTexts(tester, "M1 Renamed", "OpenAI", "https://api.openai.com/v1",
		"密钥已设置", "上下文 128k")
	models := catalogStoredModels(t, store)
	if len(models) != 1 {
		t.Fatalf("stored catalog = %+v", models)
	}
	model := models[0]
	if model.Name != "M1 Renamed" || model.BaseURL != "https://api.openai.com/v1" ||
		model.APIKey != "sk-new" || model.ContextWindow != 128000 {
		t.Fatalf("stored model = %+v", model)
	}
	// The edited row is the default: its mirror follows the connection.
	if got := store.get(contracts.SettingsChatBaseURL); got != "https://api.openai.com/v1" {
		t.Fatalf("mirror base URL = %q", got)
	}
	if got := store.get(contracts.SettingsChatAPIKey); got != "sk-new" {
		t.Fatalf("mirror API key = %q", got)
	}
}

func TestModelEditBlankKeyKeepsStoredAndClearRemoves(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: modelCatalogRow(t,
			contracts.ModelInfo{ID: "m1", BaseURL: "https://api.example.com/v1", APIKey: "sk-stored"}),
		contracts.SettingsChatModel:   "m1",
		contracts.SettingsChatBaseURL: "https://api.example.com/v1",
		contracts.SettingsChatAPIKey:  "sk-stored",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)

	// A blank key field keeps the stored credential.
	openEditModal(t, h, tester, "m1")
	clearAndType(t, h, tester, "模型名称 输入", "Kept")
	h.click(tester, "保存模型")
	tester.Frame()
	if got := catalogStoredModels(t, store)[0].APIKey; got != "sk-stored" {
		t.Fatalf("blank key replaced the stored one: %q", got)
	}

	// The explicit clear action removes it (and the default mirror follows).
	openEditModal(t, h, tester, "m1")
	h.click(tester, "清除已保存密钥")
	tester.Frame()
	h.assertTexts(tester, "保存后删除该模型的密钥。")
	h.click(tester, "保存模型")
	tester.Frame()
	if got := catalogStoredModels(t, store)[0].APIKey; got != "" {
		t.Fatalf("cleared key = %q, want empty", got)
	}
	if got := store.get(contracts.SettingsChatAPIKey); got != "" {
		t.Fatalf("mirror API key after clear = %q, want empty", got)
	}
	h.assertTexts(tester, "密钥未设置")
}

func TestModelEditSetAsDefault(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: modelCatalogRow(t,
			contracts.ModelInfo{ID: "m1", Name: "M1", BaseURL: "https://one.example/v1", APIKey: "sk-1"},
			contracts.ModelInfo{ID: "m2", Name: "M2", BaseURL: "https://two.example/v1", APIKey: "sk-2"},
		),
		contracts.SettingsChatModel:   "m1",
		contracts.SettingsChatBaseURL: "https://one.example/v1",
		contracts.SettingsChatAPIKey:  "sk-1",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	openEditModal(t, h, tester, "m2")
	h.click(tester, "设为默认模型")
	tester.Frame()
	h.click(tester, "保存模型")
	tester.Frame()

	if got := store.get(contracts.SettingsChatModel); got != "m2" {
		t.Fatalf("mirror model = %q, want m2", got)
	}
	if got := store.get(contracts.SettingsChatBaseURL); got != "https://two.example/v1" {
		t.Fatalf("mirror base URL = %q", got)
	}
	if got := store.get(contracts.SettingsChatAPIKey); got != "sk-2" {
		t.Fatalf("mirror API key = %q", got)
	}
	if got := countExactText(tester, "默认"); got != 1 {
		t.Fatalf("default badges = %d, want 1", got)
	}
}

// --- delete flow -----------------------------------------------------------

func TestModelDeletePromotesFirstRemainingAndClearsEmptyList(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: modelCatalogRow(t,
			contracts.ModelInfo{ID: "m1", Name: "M1", BaseURL: "https://one.example/v1", APIKey: "sk-1"},
			contracts.ModelInfo{ID: "m2", Name: "M2", BaseURL: "https://two.example/v1", APIKey: "sk-2"},
		),
		contracts.SettingsChatModel:   "m1",
		contracts.SettingsChatBaseURL: "https://one.example/v1",
		contracts.SettingsChatAPIKey:  "sk-1",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)

	// Removing the default row promotes the first remaining row.
	h.click(tester, "删除模型 m1")
	tester.Frame()
	h.assertTexts(tester, "删除模型", "确认删除")
	h.click(tester, "确认删除")
	tester.Frame()
	h.assertTexts(tester, "已删除模型", "m2")
	h.assertNoTexts(tester, "M1")

	models := catalogStoredModels(t, store)
	if len(models) != 1 || models[0].ID != "m2" {
		t.Fatalf("stored catalog after delete = %+v", models)
	}
	if got := store.get(contracts.SettingsChatModel); got != "m2" {
		t.Fatalf("promoted default = %q, want m2", got)
	}
	if got := store.get(contracts.SettingsChatAPIKey); got != "sk-2" {
		t.Fatalf("promoted key = %q", got)
	}

	// Removing the last row clears the list and the default mirror.
	h.click(tester, "删除模型 m2")
	tester.Frame()
	h.click(tester, "确认删除")
	tester.Frame()
	h.assertTexts(tester, "还没有模型")
	if got := store.get(contracts.SettingModelCatalogModels); got != "[]" {
		t.Fatalf("emptied catalog = %q, want []", got)
	}
	if got := store.get(contracts.SettingsChatModel); got != "" {
		t.Fatalf("cleared mirror model = %q", got)
	}
	if got := store.get(contracts.SettingsChatBaseURL); got != "" {
		t.Fatalf("cleared mirror base URL = %q", got)
	}
	if got := store.get(contracts.SettingsChatAPIKey); got != "" {
		t.Fatalf("cleared mirror API key = %q", got)
	}
}

func TestModelDeleteNonDefaultKeepsMirror(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: modelCatalogRow(t,
			contracts.ModelInfo{ID: "m1", BaseURL: "https://one.example/v1"},
			contracts.ModelInfo{ID: "m2", BaseURL: "https://two.example/v1"},
		),
		contracts.SettingsChatModel:   "m1",
		contracts.SettingsChatBaseURL: "https://one.example/v1",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	h.click(tester, "删除模型 m2")
	tester.Frame()
	h.click(tester, "确认删除")
	tester.Frame()
	if got := store.get(contracts.SettingsChatModel); got != "m1" {
		t.Fatalf("deleting a non-default row changed the mirror: %q", got)
	}
	if got := store.get(contracts.SettingsChatBaseURL); got != "https://one.example/v1" {
		t.Fatalf("deleting a non-default row changed the mirror URL: %q", got)
	}
}

// --- default mirror rollback ----------------------------------------------

// TestModelDefaultMirrorRollsBackWhenALaterKeyFails covers AUDIT-P3-15: the
// three mirror keys are written transactionally, so a failed write restores the
// keys already written and the model list is not persisted.
func TestModelDefaultMirrorRollsBackWhenALaterKeyFails(t *testing.T) {
	store := newFakeSettings(nil)
	store.failKey = contracts.SettingsChatBaseURL
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	openAddModal(t, h, tester)
	clearAndType(t, h, tester, "模型 ID 输入", "m1")
	clearAndType(t, h, tester, "Base URL 输入", "https://api.example.com/v1")
	h.click(tester, "添加")
	tester.Frame()

	h.assertTexts(tester, "保存失败", "服务商菜单")
	if got := store.get(contracts.SettingsChatModel); got != "" {
		t.Fatalf("half-written mirror model = %q, want rollback to empty", got)
	}
	if got := store.get(contracts.SettingModelCatalogModels); got != "" {
		t.Fatalf("failed mirror still wrote the list: %q", got)
	}
}

// TestModelListWriteRollsBackMirror covers the other half of the transaction: a
// failed list write restores the mirror keys written before it.
func TestModelListWriteRollsBackMirror(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:   "old-model",
		contracts.SettingsChatBaseURL: "https://old.example/v1",
		contracts.SettingsChatAPIKey:  "sk-old",
	})
	store.failKey = contracts.SettingModelCatalogModels
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	openAddModal(t, h, tester)
	clearAndType(t, h, tester, "模型 ID 输入", "m1")
	clearAndType(t, h, tester, "Base URL 输入", "https://api.example.com/v1")
	h.click(tester, "添加")
	tester.Frame()
	h.assertTexts(tester, "保存失败", "服务商菜单")
	if got := store.get(contracts.SettingsChatModel); got != "old-model" {
		t.Fatalf("mirror not rolled back: model = %q", got)
	}
	if got := store.get(contracts.SettingsChatBaseURL); got != "https://old.example/v1" {
		t.Fatalf("mirror not rolled back: base URL = %q", got)
	}
	if got := store.get(contracts.SettingsChatAPIKey); got != "sk-old" {
		t.Fatalf("mirror not rolled back: key = %q", got)
	}
	if got := store.get(contracts.SettingModelCatalogModels); got != "" {
		t.Fatalf("failed list write persisted a row: %q", got)
	}
}

// --- thinking configuration (unchanged semantics, kept in the editor) ------

func TestModelThinkingSummaryAndEditConfig(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[
			{"id":"qwen-max","name":"Qwen Max","baseURL":"https://dashscope.aliyuncs.com/compatible-mode/v1","family":"qwen","defaultThinking":"qwen-deep","thinkingModes":[{"id":"fast","nameZh":"极速","params":{"enable_thinking":false}}]},
			{"id":"deepseek-reasoner","name":"DeepSeek Reasoner","baseURL":"https://api.deepseek.com/v1","defaultThinking":"reasoning-high"}
		]`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	h.assertTexts(tester,
		"系列：千问 · 思考：深度思考 · 自定义模式 1",
		"系列：DeepSeek · 思考：高强度")

	// The editor saves family/default mode/custom mode together and the row
	// reflects the result immediately.
	openEditModal(t, h, tester, "deepseek-reasoner")
	h.assertTexts(tester, "生成与自定义模式", "未配置生成参数或自定义模式")
	h.click(tester, "思考系列菜单")
	h.click(tester, "千问")
	tester.Frame()
	h.click(tester, "默认思考菜单")
	h.assertTexts(tester, "关闭思考", "思考开启", "深度思考")
	h.click(tester, "深度思考")
	expandCatalogAdvanced(t, h, tester)
	scrollDialogBody(t, tester, 300)
	h.click(tester, "添加思考模式")
	tester.Frame()
	clearAndType(t, h, tester, "模式 1 ID", "speed")
	clearAndType(t, h, tester, "模式 1 名称", "极速")
	clearAndType(t, h, tester, "模式 1 参数",
		`{"enable_thinking": false, "thinking_budget": 1024}`)
	h.click(tester, "保存模型")
	tester.Frame()

	models := catalogStoredModels(t, store)
	if len(models) != 2 {
		t.Fatalf("stored catalog = %+v", models)
	}
	model := models[1]
	if model.Family != contracts.ModelFamilyQwen || model.DefaultThinking != contracts.ThinkingModeQwenDeep {
		t.Fatalf("stored thinking config = %+v", model)
	}
	if len(model.ThinkingModes) != 1 || model.ThinkingModes[0].ID != "speed" {
		t.Fatalf("stored custom modes = %+v", model.ThinkingModes)
	}
	h.assertTexts(tester, "系列：千问 · 思考：深度思考 · 自定义模式 1")
}

func TestModelThinkingFamilyChangeClearsStaleDefault(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"m1","name":"M1","baseURL":"https://api.example.com/v1","family":"other","defaultThinking":"reasoning-high"}]`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	openEditModal(t, h, tester, "m1")
	h.assertTexts(tester, "高强度")
	h.click(tester, "思考系列菜单")
	h.click(tester, "自定义")
	tester.Frame()
	h.assertTexts(tester, "跟随系列默认")
	h.click(tester, "保存模型")
	tester.Frame()
	models := catalogStoredModels(t, store)
	if len(models) != 1 || models[0].Family != contracts.ModelFamilyCustom ||
		models[0].DefaultThinking != "" {
		t.Fatalf("stored catalog = %+v", models)
	}
}

func TestModelGenerationRoundTrip(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"m1","name":"M1","baseURL":"https://api.example.com/v1","family":"qwen"}]`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	h.assertNoTexts(tester, "生成：")

	openEditModal(t, h, tester, "m1")
	expandCatalogAdvanced(t, h, tester)
	scrollDialogBody(t, tester, 260)
	h.click(tester, "使用默认温度")
	h.click(tester, "使用默认 Top P")
	h.click(tester, "使用默认 max tokens")
	typeNumberInput(t, h, tester, "温度", "0.7")
	typeNumberInput(t, h, tester, "Top P", "0.9")
	typeNumberInput(t, h, tester, "最大输出 tokens", "512")
	h.click(tester, "保存模型")
	tester.Frame()

	h.assertTexts(tester, "生成：温度 0.7 · Top P 0.9 · 512 tokens")
	models := catalogStoredModels(t, store)
	if len(models) != 1 || models[0].Temperature == nil || *models[0].Temperature != 0.7 ||
		models[0].TopP == nil || *models[0].TopP != 0.9 || models[0].MaxTokens != 512 {
		t.Fatalf("stored generation = %+v", models)
	}

	// Reopening shows the persisted summary and saving keeps the values.
	openEditModal(t, h, tester, "m1")
	h.assertTexts(tester, "生成：温度 0.7 · Top P 0.9 · 512 tokens")
	h.click(tester, "保存模型")
	tester.Frame()
	models = catalogStoredModels(t, store)
	if models[0].Temperature == nil || *models[0].Temperature != 0.7 {
		t.Fatalf("generation lost on reopen = %+v", models)
	}
}

func TestModelEditorValidationKeepsModalOpen(t *testing.T) {
	const original = `[{"id":"m1","name":"M1","baseURL":"https://api.example.com/v1","family":"qwen"}]`
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: original,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	openEditModal(t, h, tester, "m1")
	expandCatalogAdvanced(t, h, tester)
	scrollDialogBody(t, tester, 300)
	h.click(tester, "添加思考模式")
	clearAndType(t, h, tester, "模式 1 ID", "bad")

	// Not JSON at all.
	clearAndType(t, h, tester, "模式 1 参数", "not json")
	h.click(tester, "保存模型")
	tester.Frame()
	h.assertTexts(tester, "不是有效 JSON", "自定义思考模式")

	// Valid JSON but not an object.
	clearAndType(t, h, tester, "模式 1 参数", "[1,2]")
	h.click(tester, "保存模型")
	tester.Frame()
	h.assertTexts(tester, "必须是 JSON 对象")

	if got := store.get(contracts.SettingModelCatalogModels); got != original {
		t.Fatalf("failed saves changed the catalog: %q", got)
	}

	// Removing the broken row and saving succeeds.
	h.click(tester, "删除思考模式 1")
	h.click(tester, "保存模型")
	tester.Frame()
	h.assertNoTexts(tester, "自定义思考模式")
	if got := catalogStoredModels(t, store); len(got) != 1 {
		t.Fatalf("stored catalog = %+v", got)
	}
}

// --- localization ----------------------------------------------------------

func TestModelPageEnglishCopy(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"deepseek-chat","name":"DeepSeek Chat","baseURL":"https://api.deepseek.com/v1","apiKey":"sk-x","contextWindow":64000}]`,
	})
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Model")
	tester.Frame()
	h.assertTexts(tester, "Models", "Add model", "DeepSeek Chat", "deepseek-chat",
		"DeepSeek", "Key set", "64k context", "Default", "Prompt",
		"System prompt", "Save", "Default model")
	h.assertNoTexts(tester, "模型列表", "添加模型", "密钥已设置", "默认")

	h.click(tester, "Add model")
	tester.Frame()
	h.assertTexts(tester, "Provider", "Model ID", "Name", "Base URL", "API Key",
		"Context window", "Model family", "Set as default model", "Test connection",
		"Cancel", "Add")
	h.click(tester, "Cancel")
	tester.Frame()
	h.assertNoTexts(tester, "Provider menu")

	h.click(tester, "Edit model deepseek-chat")
	tester.Frame()
	h.assertTexts(tester, "Edit model", "Model name", "Provider", "Base URL",
		"API Key", "Clear the saved key", "Context window", "Thinking family",
		"Default thinking", "Generation & custom modes", "Set as default model",
		"Save model")
	h.click(tester, "Cancel")
}
