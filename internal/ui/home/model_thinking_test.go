package home_test

// Tests for the Home composer's compact two-level model menu: the top level
// shows 模型 (the default model for new conversations) and 推理等级 (that model's
// DefaultThinking), each with its current value one level down.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/home"
)

// homeCatalogSettings is a contracts.ModelCatalogService backed by the
// modelcatalog.models settings row, like the production modelcatalog module.
type homeCatalogSettings struct{ settings contracts.SettingsService }

func (c homeCatalogSettings) Models() []contracts.ModelInfo {
	models, err := contracts.LoadModelCatalog(c.settings)
	if err != nil {
		return nil
	}
	return contracts.SanitizeModelInfoList(models)
}

// homeModelSettings is a settings store whose modelcatalog.models row holds
// models.
func homeModelSettings(t *testing.T, models []contracts.ModelInfo) *homeSettings {
	t.Helper()
	encoded, err := contracts.EncodeModelInfoList(models)
	if err != nil {
		t.Fatalf("encode model catalog: %v", err)
	}
	return newHomeSettings(map[string]string{
		contracts.SettingModelCatalogModels: encoded,
	})
}

// assertHomeMenuLabels fails unless the menu currently shown has exactly want as
// its labels ("-" is a separator).
func assertHomeMenuLabels(t *testing.T, tester *ui.Tester, want ...string) {
	t.Helper()
	got := tester.Menu()
	if len(got) != len(want) {
		t.Fatalf("menu labels = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("menu labels = %v, want %v", got, want)
		}
	}
}

// TestHomeModelMenuEmptyCatalogDeepLinksModelPage pins the empty-catalog
// fallback: the old "providers" page was removed, so the row must open the
// model page (a wrong page ID silently falls back to whichever page is first).
func TestHomeModelMenuEmptyCatalogDeepLinksModelPage(t *testing.T) {
	settings := newHomeSettings(nil)
	chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "test-model"}}}
	h := newComposerHarness(t, chat, settings, nil)
	unregister, err := h.ui.Runtime().RegisterSettingsPage(contracts.SettingsPageSpec{
		ID: "other", Section: "general", Title: "其他设置", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "其他设置页正文") },
	})
	if err != nil {
		t.Fatalf("register settings page: %v", err)
	}
	t.Cleanup(unregister)
	unregisterModel, err := h.ui.Runtime().RegisterSettingsPage(contracts.SettingsPageSpec{
		ID: "model", Section: "general", Title: "模型设置", Order: 20,
		Build: func(c *ui.Context) { ui.Text(c, "模型设置页正文") },
	})
	if err != nil {
		t.Fatalf("register model settings page: %v", err)
	}
	t.Cleanup(unregisterModel)
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()

	clickHome(t, tester, "test-model")
	h.assertTexts(tester, "打开模型设置…")
	clickHome(t, tester, "打开模型设置…")
	h.assertTexts(tester, "模型设置页正文")
	h.assertNoTexts(tester, "其他设置页正文")
}

// TestHomeModelMenuCompactEntries covers the two-level top menu: 模型 (trailing
// the default model) and 推理等级 (trailing that model's default thinking).
func TestHomeModelMenuCompactEntries(t *testing.T) {
	settings := homeModelSettings(t, []contracts.ModelInfo{
		{ID: "qwen3.8", Name: "Qwen 3.8", Family: contracts.ModelFamilyQwen,
			DefaultThinking: contracts.ThinkingModeQwenDeep},
	})
	chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "qwen3.8"}}}
	h := newComposerHarness(t, chat, settings, nil)
	provide(t, h.ctx, contracts.KeyModelCatalog, contracts.ModelCatalogService(homeCatalogSettings{settings}))
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()

	clickHome(t, tester, "qwen3.8")
	assertHomeMenuLabels(t, tester, "模型", "推理等级")
	// The top level only names the current values; the choices are one level
	// down, so nothing is stacked here.
	h.assertTexts(tester, "Qwen 3.8", "深度思考")
	h.assertNoTexts(tester, "思考强度：", "选择模型")

	// 模型 opens the default-model list grouped by provider.
	clickHome(t, tester, "模型")
	h.assertTexts(tester, "自定义服务", "Qwen 3.8")
}

// TestHomeModelMenuChoosesDefaultModel switches the default model for new
// conversations from the grouped 模型 submenu.
func TestHomeModelMenuChoosesDefaultModel(t *testing.T) {
	encoded, err := contracts.EncodeModelInfoList([]contracts.ModelInfo{
		{ID: "deepseek-chat", Name: "DeepSeek Chat", BaseURL: "https://api.deepseek.com/v1"},
		{ID: "qwen3.8", Name: "Qwen 3.8", Family: contracts.ModelFamilyQwen},
	})
	if err != nil {
		t.Fatalf("encode model catalog: %v", err)
	}
	settings := newHomeSettings(map[string]string{
		contracts.SettingsChatModel:         "deepseek-chat",
		contracts.SettingModelCatalogModels: encoded,
	})
	chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "deepseek-chat"}}}
	h := newComposerHarness(t, chat, settings, nil)
	provide(t, h.ctx, contracts.KeyModelCatalog, contracts.ModelCatalogService(homeCatalogSettings{settings}))
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()

	clickHome(t, tester, "deepseek-chat")
	assertHomeMenuLabels(t, tester, "模型", "推理等级")
	clickHome(t, tester, "模型")
	h.assertTexts(tester, "DeepSeek", "DeepSeek Chat", "自定义服务", "Qwen 3.8")
	clickHome(t, tester, "Qwen 3.8")
	if got := settings.Get(contracts.SettingsChatModel, ""); got != "qwen3.8" {
		t.Fatalf("default model = %q, want qwen3.8", got)
	}
}

// TestHomeReasoningLevelWritesModelDefault writes the default model's
// DefaultThinking from its own 推理等级 submenu.
func TestHomeReasoningLevelWritesModelDefault(t *testing.T) {
	settings := homeModelSettings(t, []contracts.ModelInfo{{
		ID: "qwen3.8", Name: "Qwen 3.8", Family: contracts.ModelFamilyQwen,
	}})
	chat := &composerChat{fakeChat: &fakeChat{config: contracts.ChatConfig{Model: "qwen3.8"}}}
	h := newComposerHarness(t, chat, settings, nil)
	provide(t, h.ctx, contracts.KeyModelCatalog, contracts.ModelCatalogService(homeCatalogSettings{settings}))
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()

	clickHome(t, tester, "qwen3.8")
	h.assertTexts(tester, "系列默认")
	clickHome(t, tester, "推理等级")
	h.assertTexts(tester, "系列默认", "关闭思考", "深度思考")
	clickHome(t, tester, "深度思考")

	models, err := contracts.LoadModelCatalog(settings)
	if err != nil {
		t.Fatalf("decode model catalog: %v", err)
	}
	if len(models) != 1 || models[0].DefaultThinking != contracts.ThinkingModeQwenDeep {
		t.Fatalf("stored models = %+v, want qwen-deep as the model default", models)
	}

	// The reopened trailing value names the new default; 系列默认 clears it.
	clickHome(t, tester, "qwen3.8")
	h.assertTexts(tester, "深度思考")
	clickHome(t, tester, "推理等级")
	clickHome(t, tester, "系列默认")
	models, err = contracts.LoadModelCatalog(settings)
	if err != nil {
		t.Fatalf("decode model catalog: %v", err)
	}
	if models[0].DefaultThinking != "" {
		t.Fatalf("model default = %q, want cleared after choosing 系列默认", models[0].DefaultThinking)
	}
}
