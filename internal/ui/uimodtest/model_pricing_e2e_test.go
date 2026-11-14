package uimodtest

// Real-backend pricing E2E: editing a model's per-million prices in
// Settings → Model makes the next run's model calls priced, so the session
// Context tab shows the cost column and total; clearing the prices and
// restarting recomputes the fold at the current (empty) prices and hides them
// again.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// pricedUsageServer streams answers with provider usage so model_call rows have
// real token counts to price.
func pricedUsageServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"完成\"}}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1000000,\"completion_tokens\":500000,\"total_tokens\":1500000,\"prompt_cache_hit_tokens\":0}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

// pricingProduct boots the product over an explicit data dir and opens the
// session screen.
func pricingProduct(t *testing.T, dataDir string, settings *memSettings) *interactionProduct {
	t.Helper()
	root, uiRoot, loader := bootDesktop(t, dataDir, settings)
	chat := requireChat(t, root)
	if err := uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	tester := ui.NewTester(uiRoot.View, 1280, 800)
	tester.Frame()
	return &interactionProduct{root: root, uiRoot: uiRoot, tester: tester, loader: loader, chat: chat}
}

// closeProduct tears one booted product down (nil-safe, so a test can restart
// the same data dir and let a captured-cleanup close whichever app is current).
func closeProduct(product *interactionProduct) {
	if product == nil {
		return
	}
	if product.loader != nil {
		_ = product.loader.Close()
	}
	_ = product.root.DisposeAll()
}

// focusLabel makes a control visible in the open dialog's body if its scroll
// position hides it, then focuses it.
func focusLabel(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	// The editor body is a bounded Scroll and the pricing block sits below its
	// fold at this window size: walk the keyboard focus to the input instead of
	// clicking a clipped (zero-size) rect. Focusing a field scrolls it into view.
	for attempt := 0; attempt < 160; attempt++ {
		if tester.Focused(label) {
			tester.Frame()
			return
		}
		tester.Key(0, ui.KeyTab)
		tester.Frame()
	}
	t.Fatalf("control %q never took the keyboard focus; texts = %v", label, tester.Texts())
}

// setPriceField replaces one price input's value.
func setPriceField(t *testing.T, tester *ui.Tester, label, value string) {
	t.Helper()
	focusLabel(t, tester, label)
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	if value != "" {
		tester.Type(value)
	}
	tester.Frame()
}

// editModelPrices opens Settings → Model → edit the seeded test-model and saves
// the given per-million prices ("" clears).
func editModelPrices(t *testing.T, product *interactionProduct, input, cached, output string) {
	t.Helper()
	if err := product.uiRoot.OpenSettingsPage("model"); err != nil {
		t.Fatalf("open model settings: %v", err)
	}
	product.tester.Frame()
	product.tester.Frame()
	for _, want := range []string{"模型列表", "编辑模型 test-model"} {
		if !product.tester.HasText(want) {
			t.Fatalf("model settings lacks %q; texts = %v", want, product.tester.Texts())
		}
	}
	if err := product.tester.Click("编辑模型 test-model"); err != nil {
		t.Fatalf("open model editor: %v", err)
	}
	product.tester.Frame()
	if err := product.tester.Click("生成、价格与自定义模式"); err != nil {
		t.Fatalf("expand advanced group: %v", err)
	}
	product.tester.Frame()
	product.tester.Scroll(640, 420, 0, 320)
	product.tester.Frame()
	setPriceField(t, product.tester, "输入价格 / 1M 输入", input)
	setPriceField(t, product.tester, "缓存输入 / 1M 输入", cached)
	setPriceField(t, product.tester, "输出价格 / 1M 输入", output)
	if err := product.tester.Click("保存模型"); err != nil {
		t.Fatalf("save model: %v", err)
	}
	product.tester.Frame()
	if product.tester.HasText("价格（可选）") {
		t.Fatalf("editor stayed open after save; texts = %v", product.tester.Texts())
	}
	product.uiRoot.Navigator().CloseSettings()
	product.tester.Frame()
}

// runPricedTurn sends one prompt and waits for the run to settle.
func runPricedTurn(t *testing.T, product *interactionProduct, prompt string) string {
	t.Helper()
	chat := requireChat(t, product.root)
	sessionID := chat.ActiveSession()
	if err := chat.Send(sessionID, prompt); err != nil {
		t.Fatalf("Send(%q): %v", prompt, err)
	}
	waitIdle(t, chat, sessionID, 10*time.Second)
	product.tester.Frame()
	return sessionID
}

// openContextTab clicks the Context tab and waits for its pane.
func openContextTab(t *testing.T, product *interactionProduct) {
	t.Helper()
	if err := product.uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	product.tester.Frame()
	if err := product.tester.Click("上下文"); err != nil {
		t.Fatalf("click Context tab: %v; texts = %v", err, product.tester.Texts())
	}
	product.tester.Frame()
	product.tester.Frame()
}

func TestModelPricingDrivesContextCostE2E(t *testing.T) {
	server := pricedUsageServer(t)
	t.Cleanup(server.Close)
	dataDir := t.TempDir()
	settings := &memSettings{values: map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
		contracts.SettingModelCatalogModels: `[{"id":"test-model","name":"test-model","baseURL":"` +
			server.URL + `","contextWindow":128000}]`,
	}}
	product := pricingProduct(t, dataDir, settings)
	t.Cleanup(func() { closeProduct(product) })
	chat := requireChat(t, product.root)
	sessionID, err := chat.NewSession("定价会话")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	runPricedTurn(t, product, "第一轮（未定价）")

	// Prices are configured through the real settings UI; $1/M input, $0.5/M
	// cached, $2/M output, so the next call costs exactly $2.
	editModelPrices(t, product, "1", "0.5", "2")
	savedModels, loadErr := contracts.LoadModelCatalog(settings)
	if loadErr != nil || len(savedModels) != 1 || !savedModels[0].HasPricing() {
		t.Fatalf("prices not persisted by the editor: %+v (err %v)", savedModels, loadErr)
	}
	runPricedTurn(t, product, "第二轮（已定价）")

	openContextTab(t, product)
	for _, want := range []string{"概览", "累计成本", "成本", "$2.00"} {
		if !product.tester.HasText(want) {
			t.Fatalf("priced context lacks %q; texts = %v", want, product.tester.Texts())
		}
	}

	// Clearing the prices and restarting recomputes the fold at the current
	// (unpriced) catalog, so the costs disappear instead of staying stale.
	editModelPrices(t, product, "", "", "")
	closeProduct(product)
	product = pricingProduct(t, dataDir, settings)
	t.Cleanup(func() { closeProduct(product) })
	restarted := product
	if err := requireChat(t, restarted.root).SetActiveSession(sessionID); err != nil {
		t.Fatalf("activate restored session: %v", err)
	}
	openContextTab(t, restarted)
	if restarted.tester.HasText("累计成本") || restarted.tester.HasText("成本") {
		t.Fatalf("cleared prices still show cost; texts = %v", restarted.tester.Texts())
	}
	for _, want := range []string{"概览", "模型调用", "上下文用量"} {
		if !restarted.tester.HasText(want) {
			t.Fatalf("restored context lacks %q; texts = %v", want, restarted.tester.Texts())
		}
	}
}
