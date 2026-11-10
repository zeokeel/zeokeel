package session_test

// Tests for the composer's @session reference group and the per-model thinking
// strength section of the session menu.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// exactTextCount counts exactly equal text nodes (Tester.HasText is substring).
func exactTextCount(texts []string, want string) int {
	count := 0
	for _, text := range texts {
		if text == want {
			count++
		}
	}
	return count
}

// sessionRefChat is a fake chat with one active session and two others.
func sessionRefChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "当前会话"}, true)
	chat.addSession(contracts.ChatSession{ID: "sess-2", Title: "研究笔记"}, false)
	chat.addSession(contracts.ChatSession{ID: "sess-3", Title: "发布计划"}, false)
	return chat
}

// --- @session mention group -------------------------------------------------

func TestMentionSessionGroupListsOtherSessions(t *testing.T) {
	h := newWorkspaceHarness(t, sessionRefChat(), nil)
	tester := h.tester()
	focusMentionComposer(t, h, tester)
	tester.Type("@")
	tester.Frame()

	h.assertTexts(tester, "会话", "研究笔记", "发布计划", "sess-2", "sess-3")
	// The active session is not offered as a reference.
	if got := exactTextCount(tester.Texts(), "sess-1"); got != 0 {
		t.Fatalf("active session id appears %d times in the mention menu; texts = %v", got, tester.Texts())
	}
}

func TestMentionSessionGroupFiltersByQuery(t *testing.T) {
	h := newWorkspaceHarness(t, sessionRefChat(), nil)
	tester := h.tester()
	focusMentionComposer(t, h, tester)
	tester.Type("@研究")
	tester.Frame()

	h.assertTexts(tester, "研究笔记", "sess-2")
	h.assertNoTexts(tester, "发布计划")
}

func TestMentionSessionGroupHiddenWithoutOtherSessions(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "唯一会话"}, true)
	h := newWorkspaceHarness(t, chat, nil)
	tester := h.tester()
	focusMentionComposer(t, h, tester)
	tester.Type("@")
	tester.Frame()

	if got := exactTextCount(tester.Texts(), "会话"); got != 0 {
		t.Fatalf("empty session group is still rendered (%d headers); texts = %v", got, tester.Texts())
	}
}

func TestMentionSessionReferenceIsInsertedAndSent(t *testing.T) {
	chat := sessionRefChat()
	h := newWorkspaceHarness(t, chat, nil)
	tester := h.tester()
	focusMentionComposer(t, h, tester)
	tester.Type("@研究")
	tester.Frame()

	h.click(tester, "研究笔记")
	h.assertNoTexts(tester, "工作区文件", "发布计划")
	h.click(tester, "发送")
	tester.Frame()

	sends := chat.sends()
	want := contracts.FormatSessionReference("sess-2")
	if len(sends) != 1 || !strings.Contains(sends[0].prompt, want) {
		t.Fatalf("Send calls = %+v, want a prompt containing %q", sends, want)
	}
}

// --- thinking strength (per conversation and model) -------------------------

// thinkingCatalog is a contracts.ModelCatalogService with a fixed list.
type thinkingCatalog struct{ models []contracts.ModelInfo }

func (c *thinkingCatalog) Models() []contracts.ModelInfo {
	return append([]contracts.ModelInfo(nil), c.models...)
}

// thinkingChat is a fake chat whose configured default model is model.
func thinkingChat(model string) *fakeChat {
	chat := permissionChat()
	chat.config = contracts.ChatConfig{Model: model}
	return chat
}

// qwenCatalog offers one Qwen-family model, the family whose modes differ most
// from the legacy reasoning_effort list.
func qwenCatalog() *thinkingCatalog {
	return &thinkingCatalog{models: []contracts.ModelInfo{{
		ID: "qwen3.8-flash-next", Name: "Qwen 3.8", Family: contracts.ModelFamilyQwen,
	}}}
}

func TestThinkingMenuQwenFamilyModes(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newModelThinkingHarness(t, modelThinkingChat("qwen3.8-flash-next"), settings, qwenCatalog())
	tester := h.tester()

	// The chip opens a compact two-entry menu; the level lives one level down.
	openChipMenu(t, h, tester, "qwen3.8-flash-next")
	assertMenuLabels(t, tester, "模型", "推理等级", "-", "打开模型设置…")
	h.assertTexts(tester, "Qwen 3.8")
	openReasoningLevelMenu(t, h, tester)
	h.assertTexts(tester, "跟随模型默认（系列默认）", "默认", "关闭思考", "思考开启", "深度思考")
	// With no explicit override for this (conversation, model) the follow row
	// is checked; the model itself has no configured default, so it names the
	// family default.
	if info, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "跟随模型默认（系列默认）"); !ok || !info.Checked {
		t.Fatalf("follow-model radio = %+v/%v, want the checked radio", info, ok)
	}

	// Picking a mode stores the (conversation, model) row (the agent reads it
	// for the next run, so no restart is needed).
	h.clickExact(tester, "深度思考")
	key := contracts.SessionModelThinkingSettingKey("sess-1", "qwen3.8-flash-next")
	if got := settings.get(key); got != contracts.ThinkingModeQwenDeep {
		t.Fatalf("stored thinking mode = %q, want %q", got, contracts.ThinkingModeQwenDeep)
	}
	// The legacy conversation-wide rows are cleared so they cannot decide
	// another model's requests.
	if got := settings.get(contracts.SessionThinkingSettingKey("sess-1")); got != "" {
		t.Fatalf("legacy thinking key = %q, want cleared", got)
	}
	if got := settings.get(contracts.SessionEffortSettingKey("sess-1")); got != "" {
		t.Fatalf("legacy effort key = %q, want cleared", got)
	}

	// Reopening shows the new level as the 推理等级 row's trailing value.
	openChipMenu(t, h, tester, "qwen3.8-flash-next")
	h.assertTexts(tester, "深度思考")
	openReasoningLevelMenu(t, h, tester)
	if info, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "深度思考"); !ok || !info.Checked {
		t.Fatalf("deep-thinking radio = %+v/%v, want the checked radio", info, ok)
	}
}

func TestThinkingMenuCustomModelMode(t *testing.T) {
	catalog := qwenCatalog()
	catalog.models[0].ThinkingModes = []contracts.ThinkingMode{{
		ID: "turbo", NameZH: "极速思考", NameEN: "Turbo", Params: map[string]any{"enable_thinking": true},
	}}
	settings := newPermissionSettings(nil)
	h := newModelThinkingHarness(t, modelThinkingChat("qwen3.8-flash-next"), settings, catalog)
	tester := h.tester()

	openChipMenu(t, h, tester, "qwen3.8-flash-next")
	openReasoningLevelMenu(t, h, tester)
	// Built-ins and the user mode are offered together.
	h.assertTexts(tester, "关闭思考", "极速思考")
	h.clickExact(tester, "极速思考")
	key := contracts.SessionModelThinkingSettingKey("sess-1", "qwen3.8-flash-next")
	if got := settings.get(key); got != "turbo" {
		t.Fatalf("stored thinking mode = %q, want turbo", got)
	}
}

func TestThinkingMenuModelDefaultAndSessionOverride(t *testing.T) {
	catalog := qwenCatalog()
	catalog.models[0].DefaultThinking = contracts.ThinkingModeQwenDeep
	settings := newPermissionSettings(nil)
	h := newModelThinkingHarness(t, modelThinkingChat("qwen3.8-flash-next"), settings, catalog)
	tester := h.tester()

	// With no explicit override the model's configured default is effective and
	// the level row's trailing value names it.
	openChipMenu(t, h, tester, "qwen3.8-flash-next")
	h.assertTexts(tester, "深度思考")
	openReasoningLevelMenu(t, h, tester)
	h.assertTexts(tester, "跟随模型默认（深度思考）")
	if info, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "跟随模型默认（深度思考）"); !ok || !info.Checked {
		t.Fatalf("follow-model radio = %+v/%v, want the checked radio", info, ok)
	}
	// The mode rows mark the *explicit* override, so the model default itself is
	// not marked as an explicit choice.
	if info, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "深度思考"); !ok || info.Checked {
		t.Fatalf("deep-thinking radio = %+v/%v, want unchecked while not overridden", info, ok)
	}

	// Close the menus before changing the stored state from the test.
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.Key(0, ui.KeyEscape)
	tester.Frame()

	// An explicit (conversation, model) mode wins over the model default.
	settings.set(contracts.SessionModelThinkingSettingKey("sess-1", "qwen3.8-flash-next"),
		contracts.ThinkingModeQwenOff)
	tester.Frame()
	openChipMenu(t, h, tester, "qwen3.8-flash-next")
	h.assertTexts(tester, "关闭思考")
	openReasoningLevelMenu(t, h, tester)
	if info, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "跟随模型默认（深度思考）"); !ok || info.Checked {
		t.Fatalf("follow-model radio = %+v/%v, want unchecked after an override", info, ok)
	}
	if info, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "关闭思考"); !ok || !info.Checked {
		t.Fatalf("thinking-off radio = %+v/%v, want the checked radio", info, ok)
	}
}

func TestThinkingMenuLegacyEffortMigration(t *testing.T) {
	// An unknown model id infers the generic reasoning_effort family, so the
	// legacy effort value still resolves to its matching mode.
	settings := newPermissionSettings(map[string]string{
		contracts.SessionEffortSettingKey("sess-1"): contracts.SessionEffortHigh,
	})
	h := newModelThinkingHarness(t, modelThinkingChat("mystery-model"), settings, nil)
	tester := h.tester()

	openChipMenu(t, h, tester, "mystery-model")
	// The effective legacy level is the row's trailing value.
	h.assertTexts(tester, "高强度")
	openReasoningLevelMenu(t, h, tester)
	h.assertTexts(tester, "跟随模型默认（系列默认）", "低强度", "中强度")
	h.clickExact(tester, "中强度")
	key := contracts.SessionModelThinkingSettingKey("sess-1", "mystery-model")
	if got := settings.get(key); got != contracts.ThinkingModeReasoningMedium {
		t.Fatalf("stored thinking mode = %q, want %q", got, contracts.ThinkingModeReasoningMedium)
	}
	// Picking a per-model strength migrates the conversation: both legacy
	// conversation-wide rows are cleared.
	if got := settings.get(contracts.SessionEffortSettingKey("sess-1")); got != "" {
		t.Fatalf("legacy effort key = %q, want cleared", got)
	}
	if got := settings.get(contracts.SessionThinkingSettingKey("sess-1")); got != "" {
		t.Fatalf("legacy thinking key = %q, want cleared", got)
	}
	openChipMenu(t, h, tester, "mystery-model")
	h.assertTexts(tester, "中强度")
}

func TestThinkingMenuUnknownModelInfersOtherFamily(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newModelThinkingHarness(t, modelThinkingChat("totally-unknown-model"), settings, nil)
	tester := h.tester()

	openChipMenu(t, h, tester, "totally-unknown-model")
	openReasoningLevelMenu(t, h, tester)
	h.assertTexts(tester, "跟随模型默认（系列默认）", "默认", "低强度", "中强度", "高强度")
}

func TestThinkingMenuSessionModelOverrideWins(t *testing.T) {
	catalog := &thinkingCatalog{models: []contracts.ModelInfo{
		{ID: "deepseek-chat", Name: "DeepSeek", Family: contracts.ModelFamilyDeepSeek},
		{ID: "qwen3.8-flash-next", Name: "Qwen", Family: contracts.ModelFamilyQwen},
	}}
	chat := modelThinkingChat("deepseek-chat")
	// The session model override decides the family, not the configured default.
	switchSessionModel(chat, "sess-1", "qwen3.8-flash-next")
	h := newModelThinkingHarness(t, chat, newPermissionSettings(nil), catalog)
	tester := h.tester()

	openChipMenu(t, h, tester, "qwen3.8-flash-next")
	h.assertTexts(tester, "Qwen")
	openReasoningLevelMenu(t, h, tester)
	h.assertTexts(tester, "跟随模型默认（系列默认）", "关闭思考", "思考开启", "深度思考")
}

func TestThinkingMenuCustomFamilyWithoutModesHidden(t *testing.T) {
	catalog := &thinkingCatalog{models: []contracts.ModelInfo{{
		ID: "private-model", Name: "Private", Family: contracts.ModelFamilyCustom,
	}}}
	settings := newPermissionSettings(nil)
	h := newModelThinkingHarness(t, modelThinkingChat("private-model"), settings, catalog)
	tester := h.tester()

	openChipMenu(t, h, tester, "private-model")
	// A custom family with no user modes has no thinking field to send: the
	// 推理等级 entry is hidden (the model entry stays).
	assertMenuLabels(t, tester, "模型", "-", "打开模型设置…")
}

func TestThinkingMenuEnglishCopy(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newModelThinkingHarness(t, modelThinkingChat("qwen3.8-flash-next"), settings, qwenCatalog())
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()

	openChipMenu(t, h, tester, "qwen3.8-flash-next")
	assertMenuLabels(t, tester, "Model", "Reasoning level", "-", "Open model settings…")
	h.clickExact(tester, "Reasoning level")
	h.assertTexts(tester, "Follow the model default (Family default)", "Default", "Thinking off", "Thinking on", "Deep thinking")
	h.clickExact(tester, "Deep thinking")
	key := contracts.SessionModelThinkingSettingKey("sess-1", "qwen3.8-flash-next")
	if got := settings.get(key); got != contracts.ThinkingModeQwenDeep {
		t.Fatalf("stored thinking mode = %q, want deep", got)
	}
	openChipMenu(t, h, tester, "qwen3.8-flash-next")
	h.assertTexts(tester, "Deep thinking")
}

func TestThinkingMenuHiddenWithoutSettings(t *testing.T) {
	h := newModelThinkingHarness(t, modelThinkingChat("qwen3.8-flash-next"), nil, qwenCatalog())
	tester := h.tester()
	// Without a settings store there is nowhere to persist a level, so the
	// 推理等级 entry stays hidden while the model entry remains.
	openChipMenu(t, h, tester, "qwen3.8-flash-next")
	assertMenuLabels(t, tester, "模型", "-", "打开模型设置…")
}
