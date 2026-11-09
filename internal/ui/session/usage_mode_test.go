package session_test

// TDD tests (RED first) for the assistant usage footer, the per-message copy
// action and the agent plan-mode badge/menu.

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// usageChat is a fake chat with one active session holding the given assistant
// messages (plus a user prompt so the composer renders).
func usageChat(assistant ...contracts.ChatMessage) *fakeChat {
	chat := newFakeChat()
	messages := []contracts.ChatMessage{{ID: "u1", Role: contracts.ChatRoleUser, Text: "你好"}}
	messages = append(messages, assistant...)
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

// --- usage footer -----------------------------------------------------------

func TestAssistantUsageFooterRenders(t *testing.T) {
	chat := usageChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "你好呀",
		Usage: &contracts.MessageUsage{DurationMS: 3200, TotalTokens: 1234, CachedTokens: 200},
	})
	h := newHarness(t, chat)
	tester := h.tester()
	// The summary line is compact and muted; the breakdown expands on click.
	h.assertTexts(tester, "用时 3.2s · 1,234 tokens")
	h.click(tester, "用时 3.2s · 1,234 tokens")
	h.assertTexts(tester, "缓存 tokens", "200")
}

func TestAssistantUsageDurationFormats(t *testing.T) {
	chat := usageChat(
		contracts.ChatMessage{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "一",
			Usage: &contracts.MessageUsage{DurationMS: 850, TotalTokens: 12}},
		contracts.ChatMessage{ID: "a2", Role: contracts.ChatRoleAssistant, Text: "二"},
	)
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "用时 850ms · 12 tokens")
	// The message without Usage gets no footer.
	if got := textsContaining(tester.Texts(), "用时"); got != 1 {
		t.Fatalf("usage footers = %d, want 1; texts = %v", got, tester.Texts())
	}
}

func TestAssistantUsageEnglishCopy(t *testing.T) {
	chat := usageChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "hi",
		Usage: &contracts.MessageUsage{DurationMS: 3200, TotalTokens: 1234, CachedTokens: 200},
	})
	h := newHarness(t, chat)
	tester := h.tester()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Took 3.2s · 1,234 tokens")
	h.click(tester, "Took 3.2s · 1,234 tokens")
	h.assertTexts(tester, "Cached tokens", "200")
}

// --- message copy -----------------------------------------------------------

func TestAssistantCopyWritesClipboard(t *testing.T) {
	chat := usageChat(contracts.ChatMessage{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "可复制的回答"})
	h := newHarness(t, chat)
	tester := h.tester()
	// H15: copy is resident under the turn (no hover or pin needed).
	h.assertTexts(tester, "复制消息")
	h.click(tester, "复制消息")
	if got := tester.Clipboard(); got != "可复制的回答" {
		t.Fatalf("clipboard = %q, want the assistant text", got)
	}
	// The copied state is the check glyph plus tooltip, never a resident badge.
	h.assertNoTexts(tester, "已复制")
}

func TestAssistantCopyHiddenForEmptyText(t *testing.T) {
	chat := usageChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "",
		Tools: []contracts.ToolCall{{ID: "t1", Name: "bash", Status: "running"}},
	})
	h := newHarness(t, chat)
	tester := h.tester()
	if got := textsContaining(tester.Texts(), "复制"); got != 0 {
		t.Fatalf("copy action rendered for an empty message; texts = %v", tester.Texts())
	}
}

// --- plan mode --------------------------------------------------------------

func TestAgentModePlanBadgeAndToggle(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	// Normal mode keeps the quiet session-menu label; plan mode owns the label
	// (and the warning colour) when it is active.
	h.assertTexts(tester, "工作区权限")
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
	h.assertTexts(tester, "工作区权限")
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
