package insights_test

// Headless frame tests for the Insights screen: the registered screen/slot
// entry, the asynchronous load/empty/error states, the KPI row, the daily/model/
// workspace tables, the no-price rule, the 7/30-day switch, the refresh action
// and the click-to-open session row.

import (
	"context"
	"errors"
	"image"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/ui/insights"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// --- fakes -----------------------------------------------------------------

type fakeInsights struct {
	mu      sync.Mutex
	queries []contracts.InsightsQuery
	result  contracts.Insights
	err     error
	// gate blocks the aggregation until it is closed (the loading-state test).
	gate chan struct{}
}

func (f *fakeInsights) Insights(_ context.Context, query contracts.InsightsQuery) (contracts.Insights, error) {
	f.mu.Lock()
	f.queries = append(f.queries, query)
	result, err, gate := f.result, f.err, f.gate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return result, err
}

func (f *fakeInsights) setResult(result contracts.Insights) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.result = result
	f.err = nil
}

func (f *fakeInsights) setError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeInsights) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queries)
}

func (f *fakeInsights) lastQuery() contracts.InsightsQuery {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queries) == 0 {
		return contracts.InsightsQuery{}
	}
	return f.queries[len(f.queries)-1]
}

type fakeChat struct {
	contracts.ChatService
	mu     sync.Mutex
	active []string
	err    error
}

func (f *fakeChat) SetActiveSession(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.active = append(f.active, sessionID)
	return nil
}

func (f *fakeChat) activeIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.active...)
}

// --- fixtures --------------------------------------------------------------

func localDay(day int) time.Time {
	return time.Date(2026, 11, day, 0, 0, 0, 0, time.Local)
}

// sampleInsights is a realistic aggregate: three active days, two models, one
// workspace and three conversations, fully priced.
func sampleInsights() contracts.Insights {
	generated := time.Date(2026, 11, 15, 10, 30, 0, 0, time.Local)
	daily := make([]contracts.InsightsDay, 7)
	for index := range daily {
		day := localDay(9 + index)
		row := contracts.InsightsDay{Day: day}
		switch index {
		case 4: // 11-13
			row = contracts.InsightsDay{Day: day, Turns: 1, Steps: 2, ToolCalls: 3,
				PromptTokens: 100_000, CompletionTokens: 20_000, TotalTokens: 120_000,
				Sessions: 1, CostUSD: 0.25, CostKnown: true}
		case 5: // 11-14
			row = contracts.InsightsDay{Day: day, Turns: 2, Steps: 3, ToolCalls: 4,
				PromptTokens: 300_000, CompletionTokens: 80_000, TotalTokens: 380_000,
				Sessions: 2, CostUSD: 0.9, CostKnown: true}
		case 6: // 11-15
			row = contracts.InsightsDay{Day: day, Turns: 3, Steps: 5, ToolCalls: 5,
				PromptTokens: 1_100_000, CompletionTokens: 400_000, TotalTokens: 1_500_000,
				Sessions: 3, CostUSD: 2.1, CostKnown: true}
		}
		daily[index] = row
	}
	return contracts.Insights{
		Days:        7,
		From:        localDay(9),
		To:          localDay(16),
		GeneratedAt: generated,
		Totals: contracts.InsightsTotals{
			Turns: 6, Steps: 10, PromptTokens: 1_500_000, CompletionTokens: 500_000,
			CachedTokens: 200_000, TotalTokens: 2_000_000, ToolCalls: 12, Sessions: 3,
			Compactions: 1, Spills: 2, CostUSD: 3.25, CostKnown: true,
			PricedCalls: 10, UnpricedCalls: 0,
		},
		Daily: daily,
		Models: []contracts.InsightsModel{
			{Model: "model-a", Calls: 6, PromptTokens: 1_200_000, CompletionTokens: 400_000,
				CachedTokens: 200_000, TotalTokens: 1_600_000, CostUSD: 2.5, CostKnown: true},
			{Model: "model-b", Calls: 4, PromptTokens: 300_000, CompletionTokens: 100_000,
				TotalTokens: 400_000, CostUSD: 0.75, CostKnown: true},
		},
		Workspaces: []contracts.InsightsWorkspace{
			{WorkspaceID: "ws1", Name: "ZeoKeel", Sessions: 2, TotalTokens: 1_700_000,
				CostUSD: 3, CostKnown: true},
			{WorkspaceID: "", Sessions: 1, TotalTokens: 300_000, CostUSD: 0.25, CostKnown: true},
		},
		Sessions: []contracts.InsightsSession{
			{SessionID: "s1", Title: "发布检查", WorkspaceID: "ws1", Model: "model-a",
				UpdatedAt: generated, TotalTokens: 1_500_000, Turns: 3, Steps: 5, ToolCalls: 5,
				CostUSD: 2.5, CostKnown: true},
			{SessionID: "s2", Title: "市场调研", WorkspaceID: "ws1", Model: "model-b",
				UpdatedAt: generated, TotalTokens: 300_000, Turns: 2, Steps: 3, ToolCalls: 4,
				CostUSD: 0.6, CostKnown: true},
			{SessionID: "s3", Title: "临时问题", WorkspaceID: "", Model: "model-b",
				UpdatedAt: generated, TotalTokens: 200_000, Turns: 1, Steps: 2, ToolCalls: 3,
				CostUSD: 0.15, CostKnown: true},
		},
	}
}

// --- harness ---------------------------------------------------------------

type harness struct {
	t      *testing.T
	uiRoot *uiroot.Root
	chat   *fakeChat
}

func newHarness(t *testing.T, service contracts.InsightsService, chat *fakeChat) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	if service != nil {
		provide[contracts.InsightsService](t, ctx, contracts.KeyInsights, service)
	}
	if chat != nil {
		provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	}
	// A placeholder conversation screen: the Insights screen navigates to it
	// when a leaderboard row opens (the real session screen is out of scope for
	// this UI module test).
	if _, err := uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID: "session", Title: "对话", Order: 20,
		Build: func(c *ui.Context) { ui.Text(c, "会话屏幕内容") },
	}); err != nil {
		t.Fatalf("register placeholder session screen: %v", err)
	}
	fiber := activate(t, ctx, insights.Module(), insights.Config{})
	_ = fiber
	return &harness{t: t, uiRoot: uiRoot, chat: chat}
}

// tester opens the insights screen and returns one frame tester.
func (h *harness) tester(width, height int) *ui.Tester {
	h.t.Helper()
	if err := h.uiRoot.Navigator().Push(insights.ScreenID); err != nil {
		h.t.Fatalf("Push(insights): %v", err)
	}
	tester := ui.NewTester(h.uiRoot.View, width, height)
	tester.Frame()
	return tester
}

func (h *harness) assertTexts(tester *ui.Tester, wanted ...string) {
	h.t.Helper()
	for _, want := range wanted {
		if !tester.HasText(want) {
			h.t.Fatalf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
}

func (h *harness) assertNoTexts(tester *ui.Tester, unwanted ...string) {
	h.t.Helper()
	for _, text := range unwanted {
		if tester.HasText(text) {
			h.t.Fatalf("frame unexpectedly shows %q; texts = %v", text, tester.Texts())
		}
	}
}

// clickInScroll clicks a control that may sit below the page fold: the screen
// body scrolls, so a failed click walks the page down and retries.
func (h *harness) clickInScroll(tester *ui.Tester, label string) {
	h.t.Helper()
	for attempt := 0; attempt < 20; attempt++ {
		if err := tester.Click(label); err == nil {
			tester.Frame()
			return
		}
		tester.Scroll(600, 400, 0, 200)
		tester.Frame()
	}
	h.t.Fatalf("control %q never became clickable; texts = %v", label, tester.Texts())
}

// waitForText drives frames until the text appears (the aggregate is read on a
// background goroutine and posted to the UI thread).
func (h *harness) waitForText(tester *ui.Tester, text string) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tester.Frame()
		if tester.HasText(text) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("text %q never appeared; texts = %v", text, tester.Texts())
}

func provide[T any](t *testing.T, ctx cordis.Context, key cordis.ServiceKey[T], value T) {
	t.Helper()
	if _, err := ctx.Provide(key, value); err != nil {
		t.Fatalf("provide %s: %v", key.Name(), err)
	}
}

type descriptorPlugin[T any] struct {
	descriptor *module.Descriptor[T]
	config     T
	inject     []string
}

func (p *descriptorPlugin[T]) Name() string { return p.descriptor.ID }
func (p *descriptorPlugin[T]) Inject() []string {
	return p.inject
}
func (p *descriptorPlugin[T]) Activate(ctx cordis.Context, _ cordis.Value) error {
	return p.descriptor.Factory(ctx, p.config)
}

func activate[T any](t *testing.T, ctx cordis.Context, descriptor *module.Descriptor[T], config T) *cordis.Fiber {
	t.Helper()
	plugin := &descriptorPlugin[T]{descriptor: descriptor, config: config}
	plugin.inject = make([]string, 0, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		plugin.inject = append(plugin.inject, requirement.Name)
	}
	fiber, err := ctx.Plugin(plugin, nil)
	if err != nil {
		t.Fatalf("activate %s: %v", descriptor.ID, err)
	}
	if state := fiber.State(); state != cordis.Active {
		t.Fatalf("module %s state = %s (%v), want Active", descriptor.ID, state, fiber.ErrorDetail())
	}
	return fiber
}

// --- tests -----------------------------------------------------------------

// TestScreenShowsAggregate is the main data frame: entry title, KPI row, day/
// model/workspace tables, the session leaderboard and one read only.
func TestScreenShowsAggregate(t *testing.T) {
	service := &fakeInsights{result: sampleInsights()}
	h := newHarness(t, service, &fakeChat{})
	tester := h.tester(1280, 800)
	h.waitForText(tester, "发布检查")

	h.assertTexts(tester, "洞察", "tokens", "2M", "成本", "$3.25", "会话", "工具调用",
		"按天", "按模型", "按工作区", "会话排行", "ZeoKeel", "model-a", "11-15", "12")
	// One aggregation, no per-frame re-read.
	for range 5 {
		tester.Frame()
	}
	if got := service.calls(); got != 1 {
		t.Fatalf("aggregation calls = %d after six frames, want 1 (cached, not per-frame)", got)
	}
	if query := service.lastQuery(); query.Days != contracts.InsightsDaysWeek {
		t.Fatalf("query days = %d, want %d", query.Days, contracts.InsightsDaysWeek)
	}
}

// TestNoPricingHidesCost covers the no-fake-cost rule in the frame.
func TestNoPricingHidesCost(t *testing.T) {
	data := sampleInsights()
	data.Totals.CostUSD = 0
	data.Totals.CostKnown = false
	data.Totals.PricedCalls = 0
	data.Totals.UnpricedCalls = 10
	for index := range data.Daily {
		data.Daily[index].CostUSD = 0
		data.Daily[index].CostKnown = false
	}
	for index := range data.Models {
		data.Models[index].CostUSD = 0
		data.Models[index].CostKnown = false
	}
	for index := range data.Workspaces {
		data.Workspaces[index].CostUSD = 0
		data.Workspaces[index].CostKnown = false
	}
	for index := range data.Sessions {
		data.Sessions[index].CostUSD = 0
		data.Sessions[index].CostKnown = false
	}
	service := &fakeInsights{result: data}
	h := newHarness(t, service, nil)
	tester := h.tester(1280, 800)
	h.waitForText(tester, "发布检查")
	h.assertNoTexts(tester, "成本", "$0", "$")
	h.assertTexts(tester, "tokens", "按模型", "会话")
}

// TestPartialPricingMarksCost covers the partial case: one priced model keeps
// the cost visible, but the total is marked partial.
func TestPartialPricingMarksCost(t *testing.T) {
	data := sampleInsights()
	data.Totals.UnpricedCalls = 3
	data.Totals.CostKnown = false
	data.Models = append(data.Models, contracts.InsightsModel{
		Model: "model-unpriced", Calls: 3, TotalTokens: 500_000, CostKnown: false,
	})
	data.Sessions[2].CostKnown = false
	data.Sessions[2].CostUSD = 0
	service := &fakeInsights{result: data}
	h := newHarness(t, service, nil)
	tester := h.tester(1280, 800)
	h.waitForText(tester, "发布检查")
	h.assertTexts(tester, "成本", "$3.25", "部分模型未定价", "model-unpriced", "—")
}

// TestEmptyAggregateShowsEmptyState covers the cold window.
func TestEmptyAggregateShowsEmptyState(t *testing.T) {
	data := contracts.Insights{
		Days:   7,
		From:   localDay(9),
		To:     localDay(16),
		Daily:  make([]contracts.InsightsDay, 7),
		Models: []contracts.InsightsModel{},
	}
	service := &fakeInsights{result: data}
	h := newHarness(t, service, nil)
	tester := h.tester(1280, 800)
	h.waitForText(tester, "这个时间范围内还没有用量")
	h.assertNoTexts(tester, "成本", "按模型")
}

// TestRangeSwitchReloads covers the 7/30-day switch: the click changes the
// window and triggers a new aggregation.
func TestRangeSwitchReloads(t *testing.T) {
	service := &fakeInsights{result: sampleInsights()}
	h := newHarness(t, service, nil)
	tester := h.tester(1280, 800)
	h.waitForText(tester, "发布检查")
	if err := tester.Click("30 天"); err != nil {
		t.Fatalf("click 30 days: %v; texts = %v", err, tester.Texts())
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tester.Frame()
		if service.calls() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if query := service.lastQuery(); query.Days != contracts.InsightsDaysMonth {
		t.Fatalf("query days = %d, want %d after the switch", query.Days, contracts.InsightsDaysMonth)
	}
}

// TestRefreshButtonForcesAggregation covers the page action.
func TestRefreshButtonForcesAggregation(t *testing.T) {
	service := &fakeInsights{result: sampleInsights()}
	h := newHarness(t, service, nil)
	tester := h.tester(1280, 800)
	h.waitForText(tester, "发布检查")
	if err := tester.Click("刷新"); err != nil {
		t.Fatalf("click refresh: %v; texts = %v", err, tester.Texts())
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tester.Frame()
		if service.calls() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if query := service.lastQuery(); !query.ForceRefresh {
		t.Fatalf("refresh query = %+v, want ForceRefresh", query)
	}
}

// TestSessionRowOpensConversation covers the leaderboard row click: the chat
// service is activated and the session screen opens.
func TestSessionRowOpensConversation(t *testing.T) {
	service := &fakeInsights{result: sampleInsights()}
	chat := &fakeChat{}
	h := newHarness(t, service, chat)
	tester := h.tester(1280, 800)
	h.waitForText(tester, "发布检查")
	h.clickInScroll(tester, "发布检查")
	if got := chat.activeIDs(); len(got) != 1 || got[0] != "s1" {
		t.Fatalf("opened sessions = %v, want [s1]", got)
	}
	if current := h.uiRoot.Navigator().Current(); current != "session" {
		t.Fatalf("current screen = %q, want session", current)
	}
}

// TestErrorStateRetries covers the error + retry path (a failed first load
// shows the error and a retry action; the retry renders the data).
func TestErrorStateRetries(t *testing.T) {
	service := &fakeInsights{err: errors.New("boom")}
	h := newHarness(t, service, nil)
	tester := h.tester(1280, 800)
	h.waitForText(tester, "读取洞察数据失败")
	h.assertTexts(tester, "重试")
	service.setResult(sampleInsights())
	if err := tester.Click("重试"); err != nil {
		t.Fatalf("click retry: %v; texts = %v", err, tester.Texts())
	}
	h.waitForText(tester, "发布检查")
}

// TestLoadingState covers the first-load spinner while the aggregation runs.
func TestLoadingState(t *testing.T) {
	gate := make(chan struct{})
	service := &fakeInsights{result: sampleInsights(), gate: gate}
	h := newHarness(t, service, nil)
	tester := h.tester(1280, 800)
	h.waitForText(tester, "正在汇总用量…")
	close(gate)
	h.waitForText(tester, "发布检查")
}

// TestNoBackendShowsExplanatoryState covers a host without the aggregation
// module (the screen is still registered and says why it is empty).
func TestNoBackendShowsExplanatoryState(t *testing.T) {
	h := newHarness(t, nil, nil)
	tester := h.tester(1280, 800)
	h.assertTexts(tester, "洞察后端未启用")
}

// TestEnglishCopy covers the English strings.
func TestEnglishCopy(t *testing.T) {
	service := &fakeInsights{result: sampleInsights()}
	h := newHarness(t, service, nil)
	h.uiRoot.Locale().SetLanguage("en")
	tester := h.tester(1280, 800)
	h.waitForText(tester, "发布检查")
	h.assertTexts(tester, "Insights", "Sessions", "Cost", "By day", "By model",
		"Top conversations", "Tool calls")
}

// TestNarrowWindowKeepsThePage renders the screen at a narrow window: the
// tables scroll sideways and the KPI row keeps its metrics visible.
func TestNarrowWindowKeepsThePage(t *testing.T) {
	service := &fakeInsights{result: sampleInsights()}
	h := newHarness(t, service, nil)
	tester := h.tester(760, 640)
	h.waitForText(tester, "发布检查")
	h.assertTexts(tester, "tokens", "会话", "按天", "会话排行")
	// The 30-day switch stays reachable in the narrow window.
	if err := tester.Click("30 天"); err != nil {
		t.Fatalf("click 30 days at narrow width: %v; texts = %v", err, tester.Texts())
	}
}

// TestScreenRegisteredWithNavEntry pins the module's host registration: one
// registered screen at its order and its own band contributions.
func TestScreenRegisteredWithNavEntry(t *testing.T) {
	service := &fakeInsights{result: sampleInsights()}
	h := newHarness(t, service, nil)
	screens := h.uiRoot.ScreenLister().Screens()
	found := false
	for _, screen := range screens {
		if screen == insights.ScreenID {
			found = true
		}
	}
	if !found {
		t.Fatalf("registered screens = %v, want %q", screens, insights.ScreenID)
	}
	slots := h.uiRoot.Runtime().Slots()
	for _, want := range []string{contracts.SlotScreenHeader, contracts.SlotScreenActions} {
		ok := false
		for _, slot := range slots {
			if slot == want {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("slots = %v, want %q", slots, want)
		}
	}
}

// --- selected leaderboard row contrast -------------------------------------

// framePixel reads one pixel of the last frame as a token color.
func framePixel(t *testing.T, img *image.RGBA, x, y int) ui.Color {
	t.Helper()
	if x < 0 || y < 0 || x >= img.Rect.Dx() || y >= img.Rect.Dy() {
		t.Fatalf("pixel (%d,%d) is outside the %dx%d frame", x, y, img.Rect.Dx(), img.Rect.Dy())
	}
	c := img.RGBAAt(x, y)
	return ui.Color{R: c.R, G: c.G, B: c.B, A: c.A}
}

// colorNear reports whether two colors are within tolerance per channel (the
// painter composites in 8-bit channels).
func colorNear(got, want ui.Color, tolerance int) bool {
	close := func(a, b uint8) bool {
		d := int(a) - int(b)
		if d < 0 {
			d = -d
		}
		return d <= tolerance
	}
	return close(got.R, want.R) && close(got.G, want.G) && close(got.B, want.B)
}

// colorDistance is the RGB distance between two colors.
func colorDistance(a, b ui.Color) float64 {
	dr := float64(a.R) - float64(b.R)
	dg := float64(a.G) - float64(b.G)
	db := float64(a.B) - float64(b.B)
	return math.Sqrt(dr*dr + dg*dg + db*db)
}

// luminance is WCAG's relative luminance of an opaque color.
func luminance(c ui.Color) float64 {
	linear := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}

// contrastRatio is WCAG's ratio of two opaque colors.
func contrastRatio(a, b ui.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// nearestPixel returns the pixel of the box nearest the wanted color, and its
// distance: a rendered glyph of that color (a full-coverage anti-aliased pixel
// is near it) makes the distance small.
func nearestPixel(t *testing.T, img *image.RGBA, box ui.Rect, wanted ui.Color) (ui.Color, float64) {
	t.Helper()
	best, bestDistance := ui.Color{}, math.Inf(1)
	x0, x1 := int(box.X)+1, int(box.X+box.W)-1
	y0, y1 := int(box.Y)+1, int(box.Y+box.H)-1
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			pixel := framePixel(t, img, x, y)
			if d := colorDistance(pixel, wanted); d < bestDistance {
				best, bestDistance = pixel, d
			}
		}
	}
	return best, bestDistance
}

// focusSessionLeaderboard moves the keyboard focus to the session leaderboard
// with Tab: the leaderboard is the screen's last focus stop, after the refresh
// action, the range switch and the tables.
func focusSessionLeaderboard(t *testing.T, tester *ui.Tester) {
	t.Helper()
	for attempt := 0; attempt < 40; attempt++ {
		if tester.Focused("发布检查") {
			return
		}
		tester.Key(0, ui.KeyTab)
		tester.Frame()
	}
	t.Fatalf("the session leaderboard never took the keyboard focus; texts = %v", tester.Texts())
}

// TestSelectedSessionRowReadable covers the leaderboard's chosen row in both
// themes: mygo paints the row in the accent fill, so its own copy must switch to
// the on-accent foreground (Tokens.AccentText) — the resting label inks measure
// 2.4:1 (light ink on royal blue) and 1.9:1 (dark ink on sky) and are the bug
// this test locks (>= 4.5:1 required for the row's small text).
func TestSelectedSessionRowReadable(t *testing.T) {
	for _, tc := range []struct {
		name string
		dark bool
	}{
		{name: "light"},
		{name: "dark", dark: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeInsights{result: sampleInsights()}
			h := newHarness(t, service, nil)
			tester := h.tester(1280, 800)
			h.waitForText(tester, "发布检查")
			if tc.dark {
				h.uiRoot.Theme().SetDark(true)
				tester.Frame()
				h.waitForText(tester, "发布检查")
			}
			tokens := design.Light()
			if tc.dark {
				tokens = design.Dark()
			}

			// The keys choose the first row without opening it (a click on a
			// row opens the conversation, so the test drives the keyboard).
			focusSessionLeaderboard(t, tester)
			tester.Key(0, ui.KeyDown)
			tester.Frame()
			row, ok := tester.AccessByRole(ui.RoleListItem, "")
			if !ok {
				t.Fatalf("the leaderboard row is not in the accessibility tree; texts = %v", tester.Texts())
			}
			if !row.Selected {
				t.Fatalf("the chosen row does not read as selected; %+v", row)
			}

			img := tester.Image()
			// The row's own background, left of its 8 DIP content padding.
			fill := framePixel(t, img, int(row.Bounds.X)+3, int(row.Bounds.Y+row.Bounds.H/2))
			if !colorNear(fill, tokens.Accent, 3) {
				t.Fatalf("chosen row fill = %+v, want the theme's accent %+v", fill, tokens.Accent)
			}
			// The contract against the actual painted fill: the on-accent ink
			// clears AA for the row's 12-14px text.
			if got := contrastRatio(tokens.AccentText, fill); got < 4.5 {
				t.Fatalf("AccentText on the painted chosen row is %.2f:1, want >= 4.5:1", got)
			}
			// And the frame paints that ink: the pixel nearest AccentText in
			// the row is a full-coverage glyph pixel, not a resting-ink blend.
			ink, inkDistance := nearestPixel(t, img, row.Bounds, tokens.AccentText)
			if inkDistance > 48 {
				t.Fatalf("no pixel of the chosen row is near AccentText %+v (nearest %+v, distance %.1f): the row copy is not on-accent ink",
					tokens.AccentText, ink, inkDistance)
			}
			if got := contrastRatio(ink, fill); got < 4.5 {
				t.Fatalf("the chosen row's rendered ink %+v is %.2f:1 on the row fill, want >= 4.5:1", ink, got)
			}
		})
	}
}

// TestKPIValuesUseThePageTypeScale covers DESIGN.md §5.3 on the Insights screen:
// the KPI row's values are metric values, not a page/dialog H1, so they take a
// page type-scale step (<= TypeM's 24 DIP line) instead of TypeL's 28 DIP line;
// that also brings the screen from five sizes down to the four the rule allows
// while the metric row stays above the 13/12 DIP metadata.
func TestKPIValuesUseThePageTypeScale(t *testing.T) {
	service := &fakeInsights{result: sampleInsights()}
	h := newHarness(t, service, nil)
	tester := h.tester(1280, 800)
	h.waitForText(tester, "发布检查")

	value, ok := tester.Find("2M")
	if !ok {
		t.Fatalf("the tokens KPI value is missing; texts = %v", tester.Texts())
	}
	if value.H >= 28 {
		t.Fatalf("the KPI value's line is %.1f DIP tall, want <= 24 (TypeL's 28 DIP is a settings/dialog H1 role and gives the screen five sizes)",
			value.H)
	}
	if value.H <= 20 {
		t.Fatalf("the KPI value's line is %.1f DIP tall, too close to the metadata: the metric row must stay a level above the 13/12 DIP text",
			value.H)
	}
}
