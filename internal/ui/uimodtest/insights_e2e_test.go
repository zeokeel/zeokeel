package uimodtest

// Real-backend Insights E2E: the desktop assembly's session store and agent
// write real durable events for real turns; the Insights screen aggregates them
// through contracts.KeyInsights and renders the KPI row, the daily/model tables
// and the conversation leaderboard. The priced variant also proves the cost is
// computed from the model catalog, and the unpriced variant proves a missing
// price hides the cost instead of rendering zero.

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// insightsProduct boots the desktop product over an explicit data dir and opens
// one conversation that runs `turns` priced turns.
func insightsProduct(t *testing.T, settings *memSettings, title string, turns int) (*interactionProduct, string) {
	t.Helper()
	root, uiRoot, loader := bootDesktop(t, t.TempDir(), settings)
	t.Cleanup(func() {
		_ = loader.Close()
		_ = root.DisposeAll()
	})
	chat := requireChat(t, root)
	sessionID, err := chat.NewSession(title)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for turn := 0; turn < turns; turn++ {
		if err := chat.Send(sessionID, "跑一轮对话并回报结果"); err != nil {
			t.Fatalf("Send turn %d: %v", turn, err)
		}
		waitIdle(t, chat, sessionID, 10*time.Second)
	}
	tester := ui.NewTester(uiRoot.View, 1280, 800)
	tester.Frame()
	return &interactionProduct{root: root, uiRoot: uiRoot, tester: tester, loader: loader, chat: chat}, sessionID
}

// openInsights pushes the Insights screen and waits for the aggregate's first
// KPI label (the aggregation loads on a background goroutine).
func openInsights(t *testing.T, product *interactionProduct) {
	t.Helper()
	if err := product.uiRoot.Navigator().Push("insights"); err != nil {
		t.Fatalf("Push(insights): %v", err)
	}
	product.tester.Frame()
	if !frameUntil(product.tester, "tokens", 10*time.Second) {
		t.Fatalf("insights KPI never rendered; texts = %v", product.tester.Texts())
	}
}

// clickInsightsRow clicks a leaderboard row that may sit below the scroll fold:
// it scrolls the page down until the click lands.
func clickInsightsRow(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	for attempt := 0; attempt < 30; attempt++ {
		if err := tester.Click(label); err == nil {
			tester.Frame()
			return
		}
		tester.Scroll(600, 400, 0, 200)
		tester.Frame()
	}
	t.Fatalf("insights row %q never became clickable; texts = %v", label, tester.Texts())
}

// TestInsightsAggregatesRealTurnsE2E drives two priced turns through the real
// agent and asserts the cross-session aggregate: 3M tokens, $4.00, 2 turns,
// 2 steps, one session, plus the leaderboard row.
func TestInsightsAggregatesRealTurnsE2E(t *testing.T) {
	server := pricedUsageServer(t)
	t.Cleanup(server.Close)
	settings := &memSettings{values: map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
		contracts.SettingModelCatalogModels: `[{"id":"test-model","name":"test-model","baseURL":"` +
			server.URL + `","contextWindow":128000,` +
			`"inputPricePerMillion":1,"cachedInputPricePerMillion":0.5,"outputPricePerMillion":2}]`,
	}}
	product, sessionID := insightsProduct(t, settings, "洞察 E2E 会话", 2)
	openInsights(t, product)

	for _, want := range []string{
		"洞察", "tokens", "3M", "成本", "$4.00", "会话", "工具调用",
		"按天", "按模型", "会话排行", "洞察 E2E 会话",
	} {
		if !product.tester.HasText(want) {
			t.Fatalf("insights frame lacks %q; texts = %v", want, product.tester.Texts())
		}
	}

	// The leaderboard row opens the conversation it names.
	clickInsightsRow(t, product.tester, "洞察 E2E 会话")
	if current := product.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("current screen = %q, want session", current)
	}
	if active := product.chat.ActiveSession(); active != sessionID {
		t.Fatalf("active session = %q, want %q", active, sessionID)
	}
}

// TestInsightsUnpricedModelsHideCostE2E proves the no-fake-cost rule against the
// real catalog: without pricing the cost KPI and cost columns never render.
func TestInsightsUnpricedModelsHideCostE2E(t *testing.T) {
	server := pricedUsageServer(t)
	t.Cleanup(server.Close)
	settings := &memSettings{values: map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
		contracts.SettingModelCatalogModels: `[{"id":"test-model","name":"test-model","baseURL":"` +
			server.URL + `","contextWindow":128000}]`,
	}}
	product, _ := insightsProduct(t, settings, "无价模型会话", 1)
	openInsights(t, product)

	if !product.tester.HasText("tokens") || !product.tester.HasText("会话") {
		t.Fatalf("insights KPI missing; texts = %v", product.tester.Texts())
	}
	for _, unwanted := range []string{"成本", "$0", "$"} {
		if product.tester.HasText(unwanted) {
			t.Fatalf("unpriced aggregate still shows %q; texts = %v", unwanted, product.tester.Texts())
		}
	}
}
