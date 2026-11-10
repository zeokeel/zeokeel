package session_test

// Tests for the context-window suffix in the assistant usage footer.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func budgetChat(model string) *modelChat {
	chat := newModelChat()
	chat.config = contracts.ChatConfig{Model: "configured-model"}
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "预算测试", Model: model,
		Messages: []contracts.ChatMessage{
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "你好呀",
				Usage: &contracts.MessageUsage{DurationMS: 1200, TotalTokens: 460}},
		},
	}, true)
	return chat
}

// openUsageDetails opens the message-actions menu and its 用量明细 submenu.
func openUsageDetails(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	h.click(tester, "消息操作")
	h.click(tester, "用量明细")
}

func TestUsageDetailShowsContextWindowBudget(t *testing.T) {
	chat := budgetChat("gpt-4o")
	catalog := &fakeCatalog{models: []contracts.ModelInfo{
		{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128000},
	}}
	h := newModelHarness(t, chat, catalog)
	tester := h.tester()
	h.assertTexts(tester, "用时 1.2s · 460 tokens")
	openUsageDetails(t, h, tester)
	h.assertTexts(tester, "上下文预算", "128k")
	// The label already names the budget: the value must not repeat it.
	h.assertNoTexts(tester, "预算 128k")
}

// TestUsageDetailNoDuplicateBudget pins the label/value split of the usage
// detail: one 上下文预算 label and the bare size next to it.
func TestUsageDetailNoDuplicateBudget(t *testing.T) {
	chat := budgetChat("gpt-4o")
	catalog := &fakeCatalog{models: []contracts.ModelInfo{
		{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128000},
	}}
	h := newModelHarness(t, chat, catalog)
	tester := h.tester()
	openUsageDetails(t, h, tester)
	for _, text := range tester.Texts() {
		if strings.Contains(text, "预算 128k") || strings.Contains(text, "预算 预算") {
			t.Fatalf("duplicated budget copy in the detail: %q", text)
		}
	}
	if !tester.HasText("上下文预算") {
		t.Fatalf("context-budget row missing; texts = %v", tester.Texts())
	}
	h.assertTexts(tester, "128k")
}

func TestUsageDetailContextWindowEnglish(t *testing.T) {
	chat := budgetChat("gpt-4o")
	catalog := &fakeCatalog{models: []contracts.ModelInfo{
		{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128000},
	}}
	h := newModelHarness(t, chat, catalog)
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "Took 1.2s · 460 tokens")
	h.click(tester, "Message actions")
	h.click(tester, "Usage details")
	h.assertTexts(tester, "Context budget", "128k")
	h.assertNoTexts(tester, "128k context")
}

func TestUsageDetailBudgetHiddenWithoutCatalog(t *testing.T) {
	chat := budgetChat("gpt-4o")
	h := newModelHarness(t, chat, nil)
	tester := h.tester()
	h.assertTexts(tester, "460 tokens")
	h.click(tester, "消息操作")
	h.click(tester, "用量明细")
	h.assertTexts(tester, "输入 tokens", "输出 tokens")
	h.assertNoTexts(tester, "上下文预算", "128k context")
}

func TestUsageDetailBudgetHiddenForUnknownModelOrZero(t *testing.T) {
	chat := budgetChat("unknown-model")
	catalog := &fakeCatalog{models: []contracts.ModelInfo{
		{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128000},
	}}
	h := newModelHarness(t, chat, catalog)
	tester := h.tester()
	openUsageDetails(t, h, tester)
	h.assertNoTexts(tester, "上下文预算")

	zeroChat := budgetChat("gpt-4o")
	zeroCatalog := &fakeCatalog{models: []contracts.ModelInfo{
		{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 0},
	}}
	h = newModelHarness(t, zeroChat, zeroCatalog)
	tester = h.tester()
	openUsageDetails(t, h, tester)
	h.assertNoTexts(tester, "上下文预算")
}

func TestUsageDetailBudgetUsesConfiguredModelFallback(t *testing.T) {
	chat := budgetChat("")
	catalog := &fakeCatalog{models: []contracts.ModelInfo{
		{ID: "configured-model", Name: "Configured", ContextWindow: 64000},
	}}
	h := newModelHarness(t, chat, catalog)
	tester := h.tester()
	h.assertTexts(tester, "用时 1.2s · 460 tokens")
	openUsageDetails(t, h, tester)
	h.assertTexts(tester, "上下文预算", "64k")
	h.assertNoTexts(tester, "预算 64k")
}
