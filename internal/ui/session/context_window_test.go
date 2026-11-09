package session_test

// Tests for the context-window suffix in the assistant usage footer.

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func budgetChat(model string) *modelChat {
	chat := newModelChat()
	chat.config = contracts.ChatConfig{Model: "configured-model"}
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "预算测试", Model: model,
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "你好"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "你好呀",
				Usage: &contracts.MessageUsage{DurationMS: 1200, TotalTokens: 460}},
		},
	}, true)
	return chat
}

func TestUsageFooterShowsContextWindowBudget(t *testing.T) {
	chat := budgetChat("gpt-4o")
	catalog := &fakeCatalog{models: []contracts.ModelInfo{
		{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128000},
	}}
	h := newModelHarness(t, chat, catalog)
	tester := h.tester()
	h.assertTexts(tester, "用时 1.2s · 460 tokens")
	h.click(tester, "用时 1.2s · 460 tokens")
	h.assertTexts(tester, "上下文预算", "128k")
	// The label already names the budget: the value must not repeat it.
	h.assertNoTexts(tester, "预算 128k")
}

// TestUsageDetailNoDuplicateBudget pins the label/value split of the expanded
// usage detail: one 上下文预算 label and the bare size next to it.
func TestUsageDetailNoDuplicateBudget(t *testing.T) {
	chat := budgetChat("gpt-4o")
	catalog := &fakeCatalog{models: []contracts.ModelInfo{
		{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128000},
	}}
	h := newModelHarness(t, chat, catalog)
	tester := h.tester()
	h.click(tester, "用时 1.2s · 460 tokens")
	count := 0
	for _, text := range tester.Texts() {
		if text == "上下文预算" {
			count++
		}
		if strings.Contains(text, "预算 128k") || strings.Contains(text, "预算 预算") {
			t.Fatalf("duplicated budget copy in the detail: %q", text)
		}
	}
	if count != 1 {
		t.Fatalf("上下文预算 rows = %d, want 1; texts = %v", count, tester.Texts())
	}
	h.assertTexts(tester, "128k")
}

func TestUsageFooterContextWindowEnglish(t *testing.T) {
	chat := budgetChat("gpt-4o")
	catalog := &fakeCatalog{models: []contracts.ModelInfo{
		{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128000},
	}}
	h := newModelHarness(t, chat, catalog)
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "Took 1.2s · 460 tokens")
	h.click(tester, "Took 1.2s · 460 tokens")
	h.assertTexts(tester, "Context budget", "128k")
	h.assertNoTexts(tester, "128k context")
}

func TestUsageFooterBudgetHiddenWithoutCatalog(t *testing.T) {
	chat := budgetChat("gpt-4o")
	h := newModelHarness(t, chat, nil)
	tester := h.tester()
	h.assertTexts(tester, "460 tokens")
	h.assertNoTexts(tester, "上下文预算", "128k context")
}

func TestUsageFooterBudgetHiddenForUnknownModelOrZero(t *testing.T) {
	chat := budgetChat("unknown-model")
	catalog := &fakeCatalog{models: []contracts.ModelInfo{
		{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128000},
	}}
	h := newModelHarness(t, chat, catalog)
	tester := h.tester()
	h.assertNoTexts(tester, "上下文预算")

	zeroChat := budgetChat("gpt-4o")
	zeroCatalog := &fakeCatalog{models: []contracts.ModelInfo{
		{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 0},
	}}
	h = newModelHarness(t, zeroChat, zeroCatalog)
	tester = h.tester()
	h.assertNoTexts(tester, "上下文预算")
}

func TestUsageFooterBudgetUsesConfiguredModelFallback(t *testing.T) {
	chat := budgetChat("")
	catalog := &fakeCatalog{models: []contracts.ModelInfo{
		{ID: "configured-model", Name: "Configured", ContextWindow: 64000},
	}}
	h := newModelHarness(t, chat, catalog)
	tester := h.tester()
	h.assertTexts(tester, "用时 1.2s · 460 tokens")
	h.click(tester, "用时 1.2s · 460 tokens")
	h.assertTexts(tester, "上下文预算", "64k")
	h.assertNoTexts(tester, "预算 64k")
}
