package regression

// Adversarial audit: the model context-window budget suffix in the assistant
// usage details. The optional model catalogue decides the suffix: a matching
// model renders the formatted budget, an unknown model or a missing catalogue
// renders nothing (the details must never invent a budget).

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
)

// modelCatalogFake exposes a fixed model list to the session screen.
type modelCatalogFake struct{ models []contracts.ModelInfo }

func (c *modelCatalogFake) Models() []contracts.ModelInfo {
	return append([]contracts.ModelInfo(nil), c.models...)
}

// startBudgetSession opens the session screen with one usage-bearing answer.
func startBudgetSession(t *testing.T, sessionModel string, catalog contracts.ModelCatalogService) *ui.Tester {
	t.Helper()
	h := newBareHarness(t)
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-budget", Title: "Budget session", Model: sessionModel,
		// Only the assistant turn: its meta line owns the single 「消息操作」⋯
		// entry this audit opens for the usage details (a user turn's meta line
		// carries the same accessible name).
		Messages: []contracts.ChatMessage{
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "answer",
				Usage:     &contracts.MessageUsage{DurationMS: 120, TotalTokens: 1900},
				CreatedAt: time.Unix(1_700_000_100, 0)},
		}}, true)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	if catalog != nil {
		provide(t, h.ctx, contracts.KeyModelCatalog, catalog)
	}
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	h.push(sessionui.ScreenID)
	tester := h.tester(1280, 800)
	tester.Frame()
	return tester
}

// openUsageDetails opens one assistant meta line's ⋯ menu and its 用量明细
// submenu (the budget lives there, not in a separate footer row).
func openUsageDetails(t *testing.T, tester *ui.Tester) {
	t.Helper()
	if err := tester.Click("消息操作"); err != nil {
		t.Fatalf("open message actions: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if err := tester.Click("用量明细"); err != nil {
		t.Fatalf("open usage details: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
}

// TestContextBudgetSuffix covers the budget suffix: exact thousands, one
// decimal formatting, unknown model and missing catalogue are silent.
func TestContextBudgetSuffix(t *testing.T) {
	t.Run("matching model 128k", func(t *testing.T) {
		catalog := &modelCatalogFake{models: []contracts.ModelInfo{
			{ID: "audit-model", ContextWindow: 128_000},
		}}
		tester := startBudgetSession(t, "audit-model", catalog)
		// The budget lives in the meta line's usage-details submenu.
		openUsageDetails(t, tester)
		requireHasText(t, tester, "上下文预算")
		requireHasText(t, tester, "128k")
	})

	t.Run("non-thousand window", func(t *testing.T) {
		catalog := &modelCatalogFake{models: []contracts.ModelInfo{
			{ID: "audit-model", ContextWindow: 32_768},
		}}
		tester := startBudgetSession(t, "audit-model", catalog)
		openUsageDetails(t, tester)
		requireHasText(t, tester, "上下文预算")
		requireHasText(t, tester, "32.8k")
	})

	t.Run("unknown model", func(t *testing.T) {
		catalog := &modelCatalogFake{models: []contracts.ModelInfo{
			{ID: "other-model", ContextWindow: 128_000},
		}}
		tester := startBudgetSession(t, "audit-model", catalog)
		openUsageDetails(t, tester)
		requireNoText(t, tester, "上下文预算")
	})

	t.Run("no catalogue", func(t *testing.T) {
		tester := startBudgetSession(t, "audit-model", nil)
		openUsageDetails(t, tester)
		requireNoText(t, tester, "上下文预算")
	})
}
