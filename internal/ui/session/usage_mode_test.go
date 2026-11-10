package session_test

// TDD tests (RED first) for the assistant usage footer, the per-message copy
// action and the agent plan-mode badge/menu.

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// usageChat is a fake chat with one active session holding the given assistant
// messages (only those, so the tests' ⋯ entry is unambiguous).
func usageChat(assistant ...contracts.ChatMessage) *fakeChat {
	chat := newFakeChat()
	messages := append([]contracts.ChatMessage(nil), assistant...)
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "用量测试", Messages: messages}, true)
	return chat
}

// textsContaining counts text nodes containing fragment.
func textsContaining(testerTexts []string, fragment string) int {
	count := 0
	for _, text := range testerTexts {
		if strings.Contains(text, fragment) {
			count++
		}
	}
	return count
}

// --- compact usage meta row ------------------------------------------------

func TestAssistantMetaRowShowsCompactUsage(t *testing.T) {
	chat := usageChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "你好呀",
		Usage: &contracts.MessageUsage{DurationMS: 3200, TotalTokens: 1234, CachedTokens: 200},
	})
	h := newHarness(t, chat)
	tester := h.tester()
	// The usage summary is part of the compact meta row; the breakdown lives in
	// the ⋯ menu's 用量明细 submenu.
	h.assertTexts(tester, "用时 3.2s · 1.2K tokens", "消息操作")
	h.click(tester, "消息操作")
	h.assertTexts(tester, "用量明细")
	h.click(tester, "用量明细")
	h.assertTexts(tester, "时长", "3.2s", "输入 tokens", "输出 tokens", "缓存 tokens", "200")
}

// TestAssistantMetaRowUsesCompactUnits covers the user's report: the meta row
// must show 173.6K (and 8.4M-style units), never comma-grouped 173,635. The
// expanded token rows use the same units; no raw count survives anywhere.
func TestAssistantMetaRowUsesCompactUnits(t *testing.T) {
	chat := usageChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "长回答",
		Usage: &contracts.MessageUsage{
			DurationMS: 358800, PromptTokens: 171737, CompletionTokens: 1898,
			TotalTokens: 173635, CachedTokens: 92892,
		},
	})
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "用时 358.8s · 173.6K tokens")
	h.assertNoTexts(tester, "173,635")
	h.click(tester, "消息操作")
	h.click(tester, "用量明细")
	h.assertTexts(tester, "171.7K", "1.9K", "92.9K")
	h.assertNoTexts(tester, "171,737", "1,898", "92,892")
}

func TestAssistantMetaRowDurationFormats(t *testing.T) {
	chat := usageChat(
		contracts.ChatMessage{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "一",
			Usage: &contracts.MessageUsage{DurationMS: 850, TotalTokens: 12}},
		contracts.ChatMessage{ID: "a2", Role: contracts.ChatRoleAssistant, Text: "二"},
	)
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "用时 850ms · 12 tokens")
	// The message without Usage gets no usage summary.
	if got := textsContaining(tester.Texts(), "用时"); got != 1 {
		t.Fatalf("usage summaries = %d, want 1; texts = %v", got, tester.Texts())
	}
}

func TestAssistantMetaRowEnglishCopy(t *testing.T) {
	chat := usageChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "hi",
		Usage: &contracts.MessageUsage{DurationMS: 3200, TotalTokens: 1234, CachedTokens: 200},
	})
	h := newHarness(t, chat)
	tester := h.tester()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Took 3.2s · 1.2K tokens")
	h.click(tester, "Message actions")
	h.click(tester, "Usage details")
	h.assertTexts(tester, "Cached tokens", "200")
}

// --- message copy -----------------------------------------------------------

func TestAssistantCopyWritesClipboard(t *testing.T) {
	chat := usageChat(contracts.ChatMessage{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "可复制的回答"})
	h := newHarness(t, chat)
	tester := h.tester()
	// The copy action lives in the ⋯ menu of the compact meta row.
	h.assertNoTexts(tester, "复制消息")
	h.click(tester, "消息操作")
	h.assertTexts(tester, "复制消息")
	h.click(tester, "复制消息")
	if got := tester.Clipboard(); got != "可复制的回答" {
		t.Fatalf("clipboard = %q, want the assistant text", got)
	}
	// The confirmation is the transient toast, never a resident badge.
	h.assertNoTexts(tester, "已复制")
}

func TestAssistantCopyHiddenForEmptyText(t *testing.T) {
	chat := usageChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "",
		Tools: []contracts.ToolCall{{ID: "t1", Name: "bash", Status: "running"}},
	})
	h := newHarness(t, chat)
	tester := h.tester()
	// The empty assistant turn has no actions of its own (the user message's
	// actions are a different meta row).
	if got := textsContaining(tester.Texts(), "复制消息"); got != 0 {
		t.Fatalf("assistant copy action rendered for an empty message; texts = %v", tester.Texts())
	}
	if _, ok := tester.Find("消息操作"); ok {
		t.Fatalf("assistant actions entry rendered for an empty message; texts = %v", tester.Texts())
	}
}

// --- plan mode --------------------------------------------------------------

func TestAgentModePlanBadgeAndToggle(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	// Normal mode keeps the quiet session-menu label; plan mode owns the label
	// (and the warning colour) when it is active.
	h.assertTexts(tester, "工作区可写")
	if got := textsContaining(tester.Texts(), "计划模式"); got != 0 {
		t.Fatalf("plan state rendered outside the session menu; texts = %v", tester.Texts())
	}

	h.openSessionMenu(tester)
	h.assertTexts(tester, "对话模式", "普通模式", "计划模式")
	h.clickExact(tester, "计划模式")
	if got := settings.get(contracts.SettingAgentMode); got != contracts.AgentModePlan {
		t.Fatalf("stored agent mode = %q, want plan", got)
	}
	h.assertTexts(tester, "计划模式")

	h.openSessionMenu(tester)
	h.clickExact(tester, "普通模式")
	if got := settings.get(contracts.SettingAgentMode); got != contracts.AgentModeNormal {
		t.Fatalf("stored agent mode = %q, want normal", got)
	}
	h.assertTexts(tester, "工作区可写")
	if got := textsContaining(tester.Texts(), "计划模式"); got != 0 {
		t.Fatalf("plan state still rendered after switching back; texts = %v", tester.Texts())
	}
}

func TestAgentModeHiddenWithoutSettings(t *testing.T) {
	h := newPermissionHarness(t, permissionChat(), nil)
	tester := h.tester()
	if got := exactTextCount(tester.Texts(), "普通"); got != 0 {
		t.Fatalf("mode control rendered without a settings store; texts = %v", tester.Texts())
	}
	if got := textsContaining(tester.Texts(), "计划模式"); got != 0 {
		t.Fatalf("plan mode state rendered without a settings store; texts = %v", tester.Texts())
	}
	// The session menu still carries the built-in presets, but no mode group.
	h.openSessionMenu(tester)
	h.assertNoTexts(tester, "对话模式", "普通模式", "计划模式")
}

func TestAgentModeEnglishCopy(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Workspace")

	h.openSessionMenu(tester)
	h.assertTexts(tester, "Conversation mode", "Normal mode", "Plan mode")
	h.clickExact(tester, "Plan mode")
	if got := settings.get(contracts.SettingAgentMode); got != contracts.AgentModePlan {
		t.Fatalf("stored agent mode = %q, want plan", got)
	}
	h.assertTexts(tester, "Plan mode")
}
