package session_test

// Tests for the composer's compact two-level model menu: the top level shows the
// model and reasoning-level entries (each with its current value as a trailing
// label) and the real choices live one level down. Model defaults are per model
// (modelcatalog.models rows); conversation strengths are per (conversation,
// model).

import (
	"testing"

	"github.com/egoist/mygo/ui"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// settingsModelCatalog is a contracts.ModelCatalogService backed by the
// modelcatalog.models settings row, like the production modelcatalog module: a
// write through the menu is visible on the next frame.
type settingsModelCatalog struct{ settings *permissionSettings }

func (c settingsModelCatalog) Models() []contracts.ModelInfo {
	models, err := contracts.LoadModelCatalog(c.settings)
	if err != nil {
		return nil
	}
	return contracts.SanitizeModelInfoList(models)
}

// modelCatalogSettings is a settings store whose modelcatalog.models row holds
// models.
func modelCatalogSettings(t *testing.T, models []contracts.ModelInfo) *permissionSettings {
	t.Helper()
	encoded, err := contracts.EncodeModelInfoList(models)
	if err != nil {
		t.Fatalf("encode model catalog: %v", err)
	}
	return newPermissionSettings(map[string]string{
		contracts.SettingModelCatalogModels: encoded,
	})
}

// newModelThinkingHarness builds the conversation harness with a model switcher,
// a settings store and the optional model catalogue service.
func newModelThinkingHarness(t *testing.T, chat *modelChat, settings *permissionSettings,
	catalog contracts.ModelCatalogService) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	if settings != nil {
		provide[contracts.SettingsService](t, ctx, contracts.KeySettings, settings)
	}
	if catalog != nil {
		provide(t, ctx, contracts.KeyModelCatalog, catalog)
	}
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat.fakeChat}
}

// modelThinkingChat is a model-switching conversation fake using modelID.
func modelThinkingChat(modelID string) *modelChat {
	chat := newModelChat()
	chat.config = contracts.ChatConfig{Model: modelID}
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "模型会话"}, true)
	return chat
}

// switchSessionModel points the active conversation at another model, the way
// the model submenu does.
func switchSessionModel(chat *modelChat, sessionID, modelID string) {
	chat.mu.Lock()
	chatSession := chat.sessions[sessionID]
	chatSession.Model = modelID
	chat.sessions[sessionID] = chatSession
	chat.mu.Unlock()
}

// assertMenuLabels fails unless the menu currently shown has exactly want as its
// labels ("-" is a separator).
func assertMenuLabels(t *testing.T, tester *ui.Tester, want ...string) {
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

// menuHasLabel reports whether the open menu has an item with this label.
func menuHasLabel(tester *ui.Tester, label string) bool {
	for _, got := range tester.Menu() {
		if got == label {
			return true
		}
	}
	return false
}

// closeMenus closes an open submenu and its parent menu.
func closeMenus(t *testing.T, tester *ui.Tester) {
	t.Helper()
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
}

// openChipMenu opens the composer's model chip menu.
func openChipMenu(t *testing.T, h *harness, tester *ui.Tester, chip string) {
	t.Helper()
	h.click(tester, chip)
}

// openModelSubmenu opens the 模型 entry of the already-open chip menu.
func openModelSubmenu(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	h.clickExact(tester, "模型")
}

// openReasoningLevelMenu opens the 推理等级 entry of the already-open chip menu.
func openReasoningLevelMenu(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	h.clickExact(tester, "推理等级")
}

func TestModelChipMenuCompactEntries(t *testing.T) {
	settings := modelCatalogSettings(t, []contracts.ModelInfo{{
		ID: "qwen3.8", Name: "Qwen 3.8", Family: contracts.ModelFamilyQwen,
		DefaultThinking: contracts.ThinkingModeQwenDeep,
	}})
	h := newModelThinkingHarness(t, modelThinkingChat("qwen3.8"), settings,
		settingsModelCatalog{settings})
	tester := h.tester()

	openChipMenu(t, h, tester, "qwen3.8")
	// Exactly two compact entries plus the settings link: nothing is stacked.
	assertMenuLabels(t, tester, "模型", "推理等级", "-", "打开模型设置…")
	// Each entry carries its current value as the trailing label.
	h.assertTexts(tester, "Qwen 3.8", "深度思考")
	h.assertNoTexts(tester, "选择模型", "思考强度：")
}

func TestModelSubmenuGroupsByProviderAndSwitches(t *testing.T) {
	settings := modelCatalogSettings(t, []contracts.ModelInfo{
		{ID: "deepseek-chat", Name: "DeepSeek Chat", BaseURL: "https://api.deepseek.com/v1",
			Note: "快速", ContextWindow: 128000},
		{ID: "local-model", Name: "Local Model", Family: contracts.ModelFamilyQwen},
		{ID: "gpt-4o", Name: "GPT-4o", BaseURL: "https://api.openai.com/v1",
			Note: "旗舰", ContextWindow: 128000},
	})
	chat := modelThinkingChat("deepseek-chat")
	h := newModelThinkingHarness(t, chat, settings, settingsModelCatalog{settings})
	tester := h.tester()

	openChipMenu(t, h, tester, "deepseek-chat")
	h.assertTexts(tester, "DeepSeek Chat")
	openModelSubmenu(t, h, tester)
	// Models keep the catalog order, grouped by their provider; rows without a
	// known endpoint fall into the custom-service group.
	h.assertTexts(tester, "DeepSeek", "DeepSeek Chat", "自定义服务", "Local Model", "OpenAI", "GPT-4o")
	deepseekHeader, ok := tester.AccessByRole(ui.RoleMenuItem, "DeepSeek")
	if !ok || !deepseekHeader.Disabled {
		t.Fatalf("provider group header = %+v/%v, want a disabled row", deepseekHeader, ok)
	}
	customHeader, ok := tester.AccessByRole(ui.RoleMenuItem, "自定义服务")
	if !ok || !customHeader.Disabled {
		t.Fatalf("custom-service group header = %+v/%v, want a disabled row", customHeader, ok)
	}
	openaiHeader, ok := tester.AccessByRole(ui.RoleMenuItem, "OpenAI")
	if !ok || !openaiHeader.Disabled {
		t.Fatalf("OpenAI group header = %+v/%v, want a disabled row", openaiHeader, ok)
	}
	// The group headers appear in catalog order above their rows.
	if !(deepseekHeader.Bounds.Y < customHeader.Bounds.Y && customHeader.Bounds.Y < openaiHeader.Bounds.Y) {
		t.Fatalf("provider group order wrong: DeepSeek Y=%.1f, 自定义服务 Y=%.1f, OpenAI Y=%.1f",
			deepseekHeader.Bounds.Y, customHeader.Bounds.Y, openaiHeader.Bounds.Y)
	}
	// The note is the row's subtitle and the context window its trailing value.
	h.assertTexts(tester, "快速", "旗舰", "128k")
	if info, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "DeepSeek Chat"); !ok || !info.Checked {
		t.Fatalf("current-model radio = %+v/%v, want the checked radio", info, ok)
	}

	// Choosing a model switches the conversation model.
	h.clickExact(tester, "GPT-4o")
	tester.Frame()
	calls := chat.calls()
	if len(calls) != 1 || calls[0].sessionID != "sess-1" || calls[0].model != "gpt-4o" {
		t.Fatalf("SetSessionModel calls = %+v, want sess-1/gpt-4o", calls)
	}
	openChipMenu(t, h, tester, "gpt-4o")
	h.assertTexts(tester, "GPT-4o")
}

func TestModelSubmenuEmptyCatalog(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newModelThinkingHarness(t, modelThinkingChat("gpt-4o-mini"), settings, nil)
	tester := h.tester()

	openChipMenu(t, h, tester, "gpt-4o-mini")
	openModelSubmenu(t, h, tester)
	h.assertTexts(tester, "还没有模型")
	// The empty submenu is not selectable; the settings link stays on the top
	// menu.
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	h.clickExact(tester, "打开模型设置…")
	if !h.ui.Navigator().SettingsOpen() {
		t.Fatal("empty model menu did not open the model settings page")
	}
}

func TestModelChipMenuHiddenEntriesWithoutSettings(t *testing.T) {
	h := newModelThinkingHarness(t, modelThinkingChat("gpt-4o-mini"), nil, catalogModels())
	tester := h.tester()

	// Without a settings store there is nowhere to persist a level, so the
	// reasoning entry is hidden while the model entry stays.
	openChipMenu(t, h, tester, "gpt-4o-mini")
	assertMenuLabels(t, tester, "模型", "-", "打开模型设置…")
}

func TestReasoningLevelWritesSessionOverride(t *testing.T) {
	settings := modelCatalogSettings(t, []contracts.ModelInfo{{
		ID: "qwen3.8", Name: "Qwen 3.8", Family: contracts.ModelFamilyQwen,
		DefaultThinking: contracts.ThinkingModeQwenDeep,
	}})
	h := newModelThinkingHarness(t, modelThinkingChat("qwen3.8"), settings,
		settingsModelCatalog{settings})
	tester := h.tester()

	openChipMenu(t, h, tester, "qwen3.8")
	openReasoningLevelMenu(t, h, tester)
	h.assertTexts(tester, "跟随模型默认（深度思考）", "默认", "关闭思考", "思考开启", "深度思考")
	// No explicit override yet: the follow row is the checked radio even though
	// the effective value is the model default.
	if info, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "跟随模型默认（深度思考）"); !ok || !info.Checked {
		t.Fatalf("follow-model radio = %+v/%v, want the checked radio", info, ok)
	}
	if info, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "关闭思考"); !ok || info.Checked {
		t.Fatalf("thinking-off radio = %+v/%v, want unchecked", info, ok)
	}

	h.clickExact(tester, "关闭思考")
	key := contracts.SessionModelThinkingSettingKey("sess-1", "qwen3.8")
	if got := settings.get(key); got != contracts.ThinkingModeQwenOff {
		t.Fatalf("per-model override = %q, want qwen-off", got)
	}
	// The legacy conversation-wide rows are cleared.
	if got := settings.get(contracts.SessionThinkingSettingKey("sess-1")); got != "" {
		t.Fatalf("legacy thinking row = %q, want cleared", got)
	}
	if got := settings.get(contracts.SessionEffortSettingKey("sess-1")); got != "" {
		t.Fatalf("legacy effort row = %q, want cleared", got)
	}

	// The reopened menu shows the override as trailing value and radio.
	openChipMenu(t, h, tester, "qwen3.8")
	h.assertTexts(tester, "关闭思考")
	openReasoningLevelMenu(t, h, tester)
	if info, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "关闭思考"); !ok || !info.Checked {
		t.Fatalf("thinking-off radio = %+v/%v, want the checked radio", info, ok)
	}
}

func TestReasoningLevelPerModelIndependence(t *testing.T) {
	settings := modelCatalogSettings(t, []contracts.ModelInfo{
		{ID: "qwen-a", Name: "Qwen A", Family: contracts.ModelFamilyQwen},
		{ID: "qwen-b", Name: "Qwen B", Family: contracts.ModelFamilyQwen,
			DefaultThinking: contracts.ThinkingModeQwenOff},
	})
	chat := modelThinkingChat("qwen-a")
	h := newModelThinkingHarness(t, chat, settings, settingsModelCatalog{settings})
	tester := h.tester()

	// Give model A a conversation override.
	openChipMenu(t, h, tester, "qwen-a")
	openReasoningLevelMenu(t, h, tester)
	h.clickExact(tester, "深度思考")
	if got := settings.get(contracts.SessionModelThinkingSettingKey("sess-1", "qwen-a")); got != contracts.ThinkingModeQwenDeep {
		t.Fatalf("model A override = %q, want qwen-deep", got)
	}

	// Switching to model B uses B's own default, not A's override.
	switchSessionModel(chat, "sess-1", "qwen-b")
	tester.Frame()
	openChipMenu(t, h, tester, "qwen-b")
	h.assertTexts(tester, "Qwen B", "关闭思考")
	openReasoningLevelMenu(t, h, tester)
	if info, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "跟随模型默认（关闭思考）"); !ok || !info.Checked {
		t.Fatalf("model B follow radio = %+v/%v, want the checked radio", info, ok)
	}
	if got := settings.get(contracts.SessionModelThinkingSettingKey("sess-1", "qwen-b")); got != "" {
		t.Fatalf("model B override = %q, want empty", got)
	}

	// Switching back to A restores A's own override.
	closeMenus(t, tester)
	switchSessionModel(chat, "sess-1", "qwen-a")
	tester.Frame()
	openChipMenu(t, h, tester, "qwen-a")
	h.assertTexts(tester, "深度思考")
	openReasoningLevelMenu(t, h, tester)
	if info, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "深度思考"); !ok || !info.Checked {
		t.Fatalf("model A deep radio = %+v/%v, want the checked radio", info, ok)
	}
}

func TestReasoningFollowClearsAllOverrideRows(t *testing.T) {
	settings := modelCatalogSettings(t, []contracts.ModelInfo{{
		ID: "qwen3.8", Name: "Qwen 3.8", Family: contracts.ModelFamilyQwen,
		DefaultThinking: contracts.ThinkingModeQwenDeep,
	}})
	modelKey := contracts.SessionModelThinkingSettingKey("sess-1", "qwen3.8")
	legacyThinkingKey := contracts.SessionThinkingSettingKey("sess-1")
	legacyEffortKey := contracts.SessionEffortSettingKey("sess-1")
	// A legacy conversation carries all three rows at once; the per-model row
	// wins in the effective value.
	settings.set(modelKey, contracts.ThinkingModeQwenOff)
	settings.set(legacyThinkingKey, contracts.ThinkingModeQwenOn)
	settings.set(legacyEffortKey, contracts.SessionEffortHigh)
	h := newModelThinkingHarness(t, modelThinkingChat("qwen3.8"), settings,
		settingsModelCatalog{settings})
	tester := h.tester()

	openChipMenu(t, h, tester, "qwen3.8")
	h.assertTexts(tester, "关闭思考")
	openReasoningLevelMenu(t, h, tester)
	// "Follow the model default" clears every override row (the per-model row
	// plus the legacy conversation-wide thinking and effort rows).
	h.clickExact(tester, "跟随模型默认（深度思考）")
	for _, key := range contracts.SessionThinkingOverrideKeys("sess-1", "qwen3.8") {
		if got := settings.get(key); got != "" {
			t.Fatalf("override row %q = %q, want cleared", key, got)
		}
	}
	openChipMenu(t, h, tester, "qwen3.8")
	h.assertTexts(tester, "深度思考")
}

func TestVeryCompactSessionMenuReasoningFallback(t *testing.T) {
	settings := modelCatalogSettings(t, []contracts.ModelInfo{{
		ID: "qwen3.8", Name: "Qwen 3.8", Family: contracts.ModelFamilyQwen,
		DefaultThinking: contracts.ThinkingModeQwenDeep,
	}})
	h := newModelThinkingHarness(t, modelThinkingChat("qwen3.8"), settings,
		settingsModelCatalog{settings})
	tester := h.tester()
	tester.SetSize(704, 720)
	for i := 0; i < 3; i++ {
		tester.Frame()
	}

	// The model chip is folded away at this width, so the session menu carries
	// the same 推理等级 entry.
	h.openSessionMenu(tester)
	h.assertTexts(tester, "模型：qwen3.8", "推理等级", "打开模型设置…")
	if !menuHasLabel(tester, "推理等级") {
		t.Fatalf("very narrow session menu lacks the 推理等级 fallback; labels = %v", tester.Menu())
	}
	h.clickExact(tester, "推理等级")
	h.assertTexts(tester, "跟随模型默认（深度思考）", "关闭思考")
	h.clickExact(tester, "关闭思考")
	if got := settings.get(contracts.SessionModelThinkingSettingKey("sess-1", "qwen3.8")); got != contracts.ThinkingModeQwenOff {
		t.Fatalf("per-model override = %q, want qwen-off", got)
	}
}
