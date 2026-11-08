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

// --- thinking strength (per-model) -----------------------------------------

// thinkingCatalog is a contracts.ModelCatalogService with a fixed list.
type thinkingCatalog struct{ models []contracts.ModelInfo }

func (c *thinkingCatalog) Models() []contracts.ModelInfo {
	return append([]contracts.ModelInfo(nil), c.models...)
}

// newThinkingHarness builds the session harness with the optional model
// catalogue the thinking section reads; the catalogue is an optional service,
// so it can be provided after the module is active.
func newThinkingHarness(t *testing.T, chat *fakeChat, settings *permissionSettings,
	catalog contracts.ModelCatalogService) *harness {
	t.Helper()
	h := newPermissionHarness(t, chat, settings)
	if catalog != nil {
		provide(t, h.ctx, contracts.KeyModelCatalog, catalog)
	}
	return h
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
	h := newThinkingHarness(t, thinkingChat("qwen3.8-flash-next"), settings, qwenCatalog())
	tester := h.tester()

	h.openSessionMenu(tester)
	h.assertTexts(tester, "思考强度：默认", "默认", "关闭思考", "思考开启", "深度思考")

	// Picking a mode stores the new per-conversation key (the agent reads it for
	// the next run, so no restart is needed).
	h.clickExact(tester, "深度思考")
	if got := settings.get(contracts.SessionThinkingSettingKey("sess-1")); got != contracts.ThinkingModeQwenDeep {
		t.Fatalf("stored thinking mode = %q, want %q", got, contracts.ThinkingModeQwenDeep)
	}
	h.openSessionMenu(tester)
	h.assertTexts(tester, "思考强度：深度思考")

	// The legacy per-session reasoning key is not written by the new menu.
	if got := settings.get(contracts.SessionEffortSettingKey("sess-1")); got != "" {
		t.Fatalf("legacy effort key = %q, want empty", got)
	}
}

func TestThinkingMenuCustomModelMode(t *testing.T) {
	catalog := qwenCatalog()
	catalog.models[0].ThinkingModes = []contracts.ThinkingMode{{
		ID: "turbo", NameZH: "极速思考", NameEN: "Turbo", Params: map[string]any{"enable_thinking": true},
	}}
	settings := newPermissionSettings(nil)
	h := newThinkingHarness(t, thinkingChat("qwen3.8-flash-next"), settings, catalog)
	tester := h.tester()

	h.openSessionMenu(tester)
	// Built-ins and the user mode are offered together.
	h.assertTexts(tester, "关闭思考", "极速思考")
	h.clickExact(tester, "极速思考")
	if got := settings.get(contracts.SessionThinkingSettingKey("sess-1")); got != "turbo" {
		t.Fatalf("stored thinking mode = %q, want turbo", got)
	}
}

func TestThinkingMenuModelDefaultAndSessionOverride(t *testing.T) {
	catalog := qwenCatalog()
	catalog.models[0].DefaultThinking = contracts.ThinkingModeQwenDeep
	settings := newPermissionSettings(nil)
	h := newThinkingHarness(t, thinkingChat("qwen3.8-flash-next"), settings, catalog)
	tester := h.tester()

	// With no session choice the model's configured default is effective.
	h.openSessionMenu(tester)
	h.assertTexts(tester, "思考强度：深度思考")

	// Close the open menu before changing the stored state from the test.
	tester.Key(0, ui.KeyEscape)
	tester.Frame()

	// An explicit per-conversation mode wins over the model default.
	settings.set(contracts.SessionThinkingSettingKey("sess-1"), contracts.ThinkingModeQwenOff)
	tester.Frame()
	h.openSessionMenu(tester)
	h.assertTexts(tester, "思考强度：关闭思考")
}

func TestThinkingMenuLegacyEffortMigration(t *testing.T) {
	// An unknown model id infers the generic reasoning_effort family, so the
	// legacy effort value still resolves to its matching mode.
	settings := newPermissionSettings(map[string]string{
		contracts.SessionEffortSettingKey("sess-1"): contracts.SessionEffortHigh,
	})
	h := newThinkingHarness(t, thinkingChat("mystery-model"), settings, nil)
	tester := h.tester()

	h.openSessionMenu(tester)
	h.assertTexts(tester, "思考强度：高强度", "低强度", "中强度")
	h.clickExact(tester, "中强度")
	if got := settings.get(contracts.SessionThinkingSettingKey("sess-1")); got != contracts.ThinkingModeReasoningMedium {
		t.Fatalf("stored thinking mode = %q, want %q", got, contracts.ThinkingModeReasoningMedium)
	}
	// The legacy value is left in place; the explicit mode wins by contract.
	if got := settings.get(contracts.SessionEffortSettingKey("sess-1")); got != contracts.SessionEffortHigh {
		t.Fatalf("legacy effort key = %q, want untouched high", got)
	}
	h.openSessionMenu(tester)
	h.assertTexts(tester, "思考强度：中强度")
}

func TestThinkingMenuUnknownModelInfersOtherFamily(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newThinkingHarness(t, thinkingChat("totally-unknown-model"), settings, nil)
	tester := h.tester()

	h.openSessionMenu(tester)
	h.assertTexts(tester, "思考强度：默认", "默认", "低强度", "中强度", "高强度")
}

func TestThinkingMenuSessionModelOverrideWins(t *testing.T) {
	catalog := &thinkingCatalog{models: []contracts.ModelInfo{
		{ID: "deepseek-chat", Name: "DeepSeek", Family: contracts.ModelFamilyDeepSeek},
		{ID: "qwen3.8-flash-next", Name: "Qwen", Family: contracts.ModelFamilyQwen},
	}}
	chat := thinkingChat("deepseek-chat")
	// The session model override decides the family, not the configured default.
	session := chat.sessions["sess-1"]
	session.Model = "qwen3.8-flash-next"
	chat.sessions["sess-1"] = session
	h := newThinkingHarness(t, chat, newPermissionSettings(nil), catalog)
	tester := h.tester()

	h.openSessionMenu(tester)
	h.assertTexts(tester, "思考强度：默认", "关闭思考", "思考开启", "深度思考")
}

func TestThinkingMenuCustomFamilyWithoutModesHidden(t *testing.T) {
	catalog := &thinkingCatalog{models: []contracts.ModelInfo{{
		ID: "private-model", Name: "Private", Family: contracts.ModelFamilyCustom,
	}}}
	settings := newPermissionSettings(nil)
	h := newThinkingHarness(t, thinkingChat("private-model"), settings, catalog)
	tester := h.tester()

	h.openSessionMenu(tester)
	h.assertTexts(tester, "插入预设", "打开行为设置…")
	h.assertNoTexts(tester, "思考强度：")
	if got := exactTextCount(tester.Texts(), "默认"); got != 0 {
		t.Fatalf("thinking choices rendered for a custom family without modes; texts = %v", tester.Texts())
	}
}

func TestThinkingMenuEnglishCopy(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newThinkingHarness(t, thinkingChat("qwen3.8-flash-next"), settings, qwenCatalog())
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()

	h.openSessionMenu(tester)
	h.assertTexts(tester, "Thinking: Default", "Default", "Thinking off", "Thinking on", "Deep thinking")
	h.clickExact(tester, "Deep thinking")
	if got := settings.get(contracts.SessionThinkingSettingKey("sess-1")); got != contracts.ThinkingModeQwenDeep {
		t.Fatalf("stored thinking mode = %q, want deep", got)
	}
	h.openSessionMenu(tester)
	h.assertTexts(tester, "Thinking: Deep thinking")
}

func TestThinkingMenuHiddenWithoutSettings(t *testing.T) {
	h := newThinkingHarness(t, thinkingChat("qwen3.8-flash-next"), nil, qwenCatalog())
	tester := h.tester()
	// Without a settings store there is nowhere to persist a mode, so the
	// section stays hidden while the preset group remains.
	if got := exactTextCount(tester.Texts(), "会话设置"); got != 1 {
		t.Fatalf("session menu = %d, want one; texts = %v", got, tester.Texts())
	}
	h.openSessionMenu(tester)
	h.assertTexts(tester, "插入预设")
	if tester.HasText("思考强度：") {
		t.Fatalf("thinking section rendered without a settings store; texts = %v", tester.Texts())
	}
}
