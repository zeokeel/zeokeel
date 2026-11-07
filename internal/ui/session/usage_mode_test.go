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
	h.assertTexts(tester, "用时 3.2s · 1,234 tokens · 缓存 200")
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
	h.assertTexts(tester, "Took 3.2s · 1,234 tokens · cached 200")
}

// --- message copy -----------------------------------------------------------

func TestAssistantCopyWritesClipboard(t *testing.T) {
	chat := usageChat(contracts.ChatMessage{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "可复制的回答"})
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "复制")

	h.click(tester, "复制")
	if got := tester.Clipboard(); got != "可复制的回答" {
		t.Fatalf("clipboard = %q, want the assistant text", got)
	}
	h.assertTexts(tester, "已复制")
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
	h.assertTexts(tester, "普通")
	if got := textsContaining(tester.Texts(), "计划模式"); got != 0 {
		t.Fatalf("plan badge rendered in normal mode; texts = %v", tester.Texts())
	}

	h.click(tester, "普通")
	h.assertTexts(tester, "普通模式", "计划模式")
	h.click(tester, "计划模式")
	if got := settings.get(contracts.SettingAgentMode); got != contracts.AgentModePlan {
		t.Fatalf("stored agent mode = %q, want plan", got)
	}
	h.assertTexts(tester, "计划模式")

	h.click(tester, "计划")
	h.click(tester, "普通模式")
	if got := settings.get(contracts.SettingAgentMode); got != contracts.AgentModeNormal {
		t.Fatalf("stored agent mode = %q, want normal", got)
	}
	if got := textsContaining(tester.Texts(), "计划模式"); got != 0 {
		t.Fatalf("plan badge still rendered after switching back; texts = %v", tester.Texts())
	}
}

func TestAgentModeHiddenWithoutSettings(t *testing.T) {
	h := newPermissionHarness(t, permissionChat(), nil)
	tester := h.tester()
	if got := exactTextCount(tester.Texts(), "普通"); got != 0 {
		t.Fatalf("mode selector rendered without a settings store; texts = %v", tester.Texts())
	}
	if got := textsContaining(tester.Texts(), "计划模式"); got != 0 {
		t.Fatalf("plan mode badge rendered without a settings store; texts = %v", tester.Texts())
	}
}

func TestAgentModeEnglishCopy(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Normal")

	h.click(tester, "Normal")
	h.assertTexts(tester, "Normal mode", "Plan mode")
	h.click(tester, "Plan mode")
	if got := settings.get(contracts.SettingAgentMode); got != contracts.AgentModePlan {
		t.Fatalf("stored agent mode = %q, want plan", got)
	}
	h.assertTexts(tester, "Plan mode")
}
