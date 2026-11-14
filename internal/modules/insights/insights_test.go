package insights

// Unit tests for the cross-session aggregation: window and day bucketing in a
// fixed time zone, the model_call/usage fallback rules, priced vs unpriced
// cost, empty data, bounded reads (truncation), and the result cache. The
// store and settings are fakes: the aggregation contract is what is under test,
// not SQLite.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// --- fakes -----------------------------------------------------------------

type fakeStore struct {
	contracts.Store // embedded nil: only the two read methods are used

	metas  []contracts.SessionMeta
	events map[string][]rig.SessionEvent

	sessionsCalls int
	lastCalls     int
	sessionsErr   error
	lastErr       error
}

func (f *fakeStore) Sessions(context.Context) ([]contracts.SessionMeta, error) {
	f.sessionsCalls++
	if f.sessionsErr != nil {
		return nil, f.sessionsErr
	}
	return append([]contracts.SessionMeta(nil), f.metas...), nil
}

func (f *fakeStore) Last(_ context.Context, sessionID string, limit int) ([]rig.SessionEvent, error) {
	f.lastCalls++
	if f.lastErr != nil {
		return nil, f.lastErr
	}
	events := f.events[sessionID]
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	return append([]rig.SessionEvent(nil), events...), nil
}

type fakeSettings struct{ values map[string]string }

func (f *fakeSettings) Get(key, fallback string) string {
	if value, ok := f.values[key]; ok {
		return value
	}
	return fallback
}
func (f *fakeSettings) Set(key, value string) error { f.values[key] = value; return nil }
func (f *fakeSettings) All() map[string]string      { return f.values }

type fakeWorkspace struct {
	contracts.WorkspaceService
	list []contracts.Workspace
}

func (f *fakeWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	return append([]contracts.Workspace(nil), f.list...), nil
}

type testLogger struct{}

func (testLogger) Printf(string, ...any) {}

// --- event fixtures --------------------------------------------------------

func modelCallEvent(at time.Time, model string, prompt, cached, completion, total int) rig.SessionEvent {
	payload, _ := json.Marshal(map[string]any{
		"iteration":        1,
		"model":            model,
		"promptTokens":     prompt,
		"cachedTokens":     cached,
		"completionTokens": completion,
		"totalTokens":      total,
		"contextTokens":    prompt,
	})
	return rig.SessionEvent{Type: eventModelCall, Payload: payload, Time: at}
}

func usageEvent(at time.Time, prompt, completion, cached, total, iterations int) rig.SessionEvent {
	payload, _ := json.Marshal(map[string]any{
		"promptTokens":     prompt,
		"completionTokens": completion,
		"totalTokens":      total,
		"cachedTokens":     cached,
		"iterations":       iterations,
	})
	return rig.SessionEvent{Type: eventUsage, Payload: payload, Time: at}
}

func runStartEvent(at time.Time, model string) rig.SessionEvent {
	payload, _ := json.Marshal(map[string]any{"model": model})
	return rig.SessionEvent{Type: eventRunStart, Payload: payload, Time: at}
}

func plainEvent(at time.Time, eventType string) rig.SessionEvent {
	return rig.SessionEvent{Type: eventType, Payload: []byte(`{}`), Time: at}
}

// --- fixtures --------------------------------------------------------------

func shanghai(t *testing.T) *time.Location {
	t.Helper()
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load Asia/Shanghai: %v", err)
	}
	return location
}

// testService builds the service with an injected clock, location and pricing
// catalog. now is the fixed "today" instant (local 2026-11-15 10:00).
func testService(t *testing.T, store contracts.Store, catalogJSON string) (*service, time.Time, *time.Location) {
	t.Helper()
	location := shanghai(t)
	settings := &fakeSettings{values: map[string]string{}}
	if catalogJSON != "" {
		settings.values[contracts.SettingModelCatalogModels] = catalogJSON
	}
	service, err := newService(store, settings, nil, testLogger{}, Config{})
	if err != nil {
		t.Fatalf("newService: %v", err)
	}
	service.location = location
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, location)
	service.now = func() time.Time { return now }
	return service, now, location
}

const pricedCatalog = `[
	{"id":"model-a","inputPricePerMillion":1,"cachedInputPricePerMillion":0.5,"outputPricePerMillion":2},
	{"id":"model-legacy","inputPricePerMillion":0.5,"outputPricePerMillion":1}
]`

func approx(t *testing.T, want, got float64) {
	t.Helper()
	if diff := want - got; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("cost = %v, want %v", got, want)
	}
}

// --- tests -----------------------------------------------------------------

// TestAggregatesWindowTotalsAndDimensions covers the whole fold: totals, the
// per-day series, per-model rows, per-workspace rows and the session
// leaderboard, including the legacy usage fallback and one unpriced model.
func TestAggregatesWindowTotalsAndDimensions(t *testing.T) {
	location := shanghai(t)
	at15 := func(hour int) time.Time { return time.Date(2026, 11, 15, hour, 0, 0, 0, location) }
	at14 := func(hour int) time.Time { return time.Date(2026, 11, 14, hour, 0, 0, 0, location) }
	at13 := func(hour int) time.Time { return time.Date(2026, 11, 13, hour, 0, 0, 0, location) }

	store := &fakeStore{
		metas: []contracts.SessionMeta{
			{ID: "s1", Title: "发布检查", WorkspaceID: "ws1", UpdatedAt: at15(9)},
			{ID: "s2", Title: "旧版会话", WorkspaceID: "", UpdatedAt: at14(23)},
			{ID: "s3", Title: "无价模型会话", WorkspaceID: "ws2", UpdatedAt: at13(12)},
		},
		events: map[string][]rig.SessionEvent{
			"s1": {
				runStartEvent(at15(9), "model-a"),
				modelCallEvent(at15(9).Add(time.Minute), "model-a", 1000, 400, 200, 1200),
				modelCallEvent(at15(9).Add(2*time.Minute), "model-a", 500, 0, 100, 600),
				plainEvent(at15(9).Add(3*time.Minute), eventToolCall),
				// The turn's cumulative usage event: tokens come from the
				// model_call events above, so this one only bumps turns.
				usageEvent(at15(9).Add(4*time.Minute), 1500, 300, 400, 1800, 2),
				plainEvent(at15(9).Add(5*time.Minute), eventCompact),
			},
			"s2": {
				runStartEvent(at14(23), "model-legacy"),
				usageEvent(at14(23).Add(time.Minute), 3000, 400, 0, 3400, 2),
				plainEvent(at14(23).Add(2*time.Minute), eventToolCall),
			},
			"s3": {
				modelCallEvent(at13(12), "model-b", 100, 0, 50, 150),
			},
		},
	}
	service, _, _ := testService(t, store, pricedCatalog)
	service.workspace = &fakeWorkspace{list: []contracts.Workspace{
		{ID: "ws1", Name: "ZeoKeel"},
		{ID: "ws2", Name: "发布仓库"},
	}}

	result, err := service.Insights(context.Background(), contracts.InsightsQuery{Days: 7})
	if err != nil {
		t.Fatalf("Insights: %v", err)
	}
	if result.Days != 7 || len(result.Daily) != 7 {
		t.Fatalf("Days = %d, daily = %d, want 7/7", result.Days, len(result.Daily))
	}
	if want := time.Date(2026, 11, 9, 0, 0, 0, 0, location); !result.From.Equal(want) {
		t.Fatalf("From = %v, want %v", result.From, want)
	}
	if want := time.Date(2026, 11, 16, 0, 0, 0, 0, location); !result.To.Equal(want) {
		t.Fatalf("To = %v, want %v", result.To, want)
	}

	totals := result.Totals
	checks := []struct {
		name string
		got  int
		want int
	}{
		{"turns", totals.Turns, 2},
		{"steps", totals.Steps, 5}, // 2 model_call + 2 legacy iterations + 1 model_call
		{"prompt", totals.PromptTokens, 4600},
		{"completion", totals.CompletionTokens, 750},
		{"cached", totals.CachedTokens, 400},
		{"total", totals.TotalTokens, 5350},
		{"toolCalls", totals.ToolCalls, 2},
		{"sessions", totals.Sessions, 3},
		{"compactions", totals.Compactions, 1},
		{"pricedCalls", totals.PricedCalls, 3},
		{"unpricedCalls", totals.UnpricedCalls, 1},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Fatalf("totals.%s = %d, want %d", check.name, check.got, check.want)
		}
	}
	if totals.CostKnown {
		t.Fatal("totals.CostKnown = true with an unpriced model in the window")
	}
	// s1: 600 uncached·1 + 400 cached·0.5 + 200 out·2 = 0.0012; then
	// 500·1 + 0 + 100·2 = 0.0007. s2: 3000·0.5 + 400·1 = 0.0019.
	approx(t, 0.0038, totals.CostUSD)

	// Day rows are oldest first and include the empty days.
	if result.Daily[6].Day.Format("2006-01-02") != "2026-11-15" {
		t.Fatalf("last day = %v, want 2026-11-15", result.Daily[6].Day)
	}
	if result.Daily[0].Day.Format("2006-01-02") != "2026-11-09" {
		t.Fatalf("first day = %v, want 2026-11-09", result.Daily[0].Day)
	}
	day15 := result.Daily[6]
	if day15.TotalTokens != 1800 || day15.Turns != 1 || day15.Steps != 2 ||
		day15.ToolCalls != 1 || day15.Sessions != 1 || !day15.CostKnown {
		t.Fatalf("day 11-15 = %+v, want 1800 tokens/1 turn/2 steps/1 tool/1 session priced", day15)
	}
	approx(t, 0.0019, day15.CostUSD)
	day14 := result.Daily[5]
	if day14.TotalTokens != 3400 || day14.Sessions != 1 || !day14.CostKnown {
		t.Fatalf("day 11-14 = %+v, want 3400 tokens/1 session priced", day14)
	}
	approx(t, 0.0019, day14.CostUSD)
	day13 := result.Daily[4]
	if day13.TotalTokens != 150 || day13.Sessions != 1 || day13.CostKnown {
		t.Fatalf("day 11-13 = %+v, want 150 tokens/1 session/unpriced", day13)
	}
	if result.Daily[0].TotalTokens != 0 || result.Daily[0].Sessions != 0 {
		t.Fatalf("empty day = %+v, want zeros", result.Daily[0])
	}

	// Model rows: sorted by total tokens; the unpriced model reports no cost.
	if len(result.Models) != 3 {
		t.Fatalf("models = %+v, want 3 rows", result.Models)
	}
	models := map[string]contracts.InsightsModel{}
	for _, row := range result.Models {
		models[row.Model] = row
	}
	if row := models["model-a"]; row.Calls != 2 || row.TotalTokens != 1800 || !row.CostKnown {
		t.Fatalf("model-a row = %+v, want 2 calls/1800 tokens/priced", row)
	}
	if row := models["model-legacy"]; row.Calls != 2 || row.TotalTokens != 3400 || !row.CostKnown {
		t.Fatalf("model-legacy row = %+v, want 2 calls/3400 tokens/priced (usage iterations)", row)
	}
	if row := models["model-b"]; row.CostKnown || row.CostUSD != 0 {
		t.Fatalf("model-b row = %+v, want no cost", row)
	}
	if result.Models[0].Model != "model-legacy" {
		t.Fatalf("model order = %+v, want model-legacy first", result.Models)
	}

	// Workspace rows: names resolved by the backend, unassociated sessions
	// grouped under the empty ID.
	if len(result.Workspaces) != 3 {
		t.Fatalf("workspaces = %+v, want 3 rows", result.Workspaces)
	}
	workspaces := map[string]contracts.InsightsWorkspace{}
	for _, row := range result.Workspaces {
		workspaces[row.WorkspaceID] = row
	}
	if row := workspaces["ws1"]; row.Name != "ZeoKeel" || row.Sessions != 1 || row.TotalTokens != 1800 {
		t.Fatalf("workspace ws1 = %+v", row)
	}
	if row := workspaces["ws2"]; row.Name != "发布仓库" || row.Sessions != 1 || row.TotalTokens != 150 {
		t.Fatalf("workspace ws2 = %+v", row)
	}
	if row := workspaces[""]; row.Name != "" || row.Sessions != 1 || row.TotalTokens != 3400 {
		t.Fatalf("unassociated workspace = %+v", row)
	}

	// Session leaderboard: busiest first, model attributed per session.
	if len(result.Sessions) != 3 {
		t.Fatalf("sessions = %+v, want 3 rows", result.Sessions)
	}
	if result.Sessions[0].SessionID != "s2" || result.Sessions[0].Model != "model-legacy" ||
		result.Sessions[0].TotalTokens != 3400 {
		t.Fatalf("leaderboard[0] = %+v, want s2/model-legacy/3400", result.Sessions[0])
	}
	if s1 := result.Sessions[1]; s1.SessionID != "s1" || s1.Model != "model-a" ||
		s1.Turns != 1 || s1.Steps != 2 || s1.ToolCalls != 1 || s1.TotalTokens != 1800 {
		t.Fatalf("leaderboard[1] = %+v, want s1/model-a/1 turn/2 steps/1 tool/1800", s1)
	}
	if !result.Sessions[1].CostKnown {
		t.Fatal("s1 should be fully priced")
	}
	if result.Sessions[2].SessionID != "s3" || result.Sessions[2].CostKnown {
		t.Fatalf("leaderboard[2] = %+v, want s3 unpriced", result.Sessions[2])
	}
	if result.Truncated {
		t.Fatal("Truncated = true without hitting the read bound")
	}
}

// TestWindowBoundariesAreLocalMidnights proves the day bucketing uses the
// configured zone: exactly-midnight events belong to the new day, events before
// the window start / after today are excluded.
func TestWindowBoundariesAreLocalMidnights(t *testing.T) {
	location := shanghai(t)
	at := func(day, hour, minute, second int) time.Time {
		return time.Date(2026, 11, day, hour, minute, second, 0, location)
	}
	store := &fakeStore{
		metas: []contracts.SessionMeta{{ID: "s1", UpdatedAt: at(15, 10, 0, 0)}},
		events: map[string][]rig.SessionEvent{
			"s1": {
				modelCallEvent(at(8, 23, 59, 59), "model-a", 1, 0, 0, 1), // before the window
				modelCallEvent(at(9, 0, 0, 0), "model-a", 10, 0, 0, 10),  // first in-window instant
				modelCallEvent(at(15, 23, 59, 59), "model-a", 100, 0, 0, 100),
				modelCallEvent(at(16, 0, 0, 0), "model-a", 1000, 0, 0, 1000), // after today
			},
		},
	}
	service, _, _ := testService(t, store, "")
	result, err := service.Insights(context.Background(), contracts.InsightsQuery{Days: 7})
	if err != nil {
		t.Fatalf("Insights: %v", err)
	}
	if got, want := result.Totals.TotalTokens, 110; got != want {
		t.Fatalf("TotalTokens = %d, want %d (only the in-window events)", got, want)
	}
	if got, want := result.Totals.Steps, 2; got != want {
		t.Fatalf("Steps = %d, want %d", got, want)
	}
	if got, want := result.Daily[0].TotalTokens, 10; got != want {
		t.Fatalf("first day tokens = %d, want %d (the 00:00 event)", got, want)
	}
	if got, want := result.Daily[6].TotalTokens, 100; got != want {
		t.Fatalf("last day tokens = %d, want %d (23:59:59 stays on its day)", got, want)
	}
}

// TestNoDoubleCountWhenModelCallsAndUsageCoexist pins the fallback rule: a
// session with per-call events uses them for tokens, and its usage event only
// counts the turn.
func TestNoDoubleCountWhenModelCallsAndUsageCoexist(t *testing.T) {
	location := shanghai(t)
	when := time.Date(2026, 11, 15, 9, 0, 0, 0, location)
	store := &fakeStore{
		metas: []contracts.SessionMeta{{ID: "s1", UpdatedAt: when}},
		events: map[string][]rig.SessionEvent{
			"s1": {
				runStartEvent(when, "model-a"),
				modelCallEvent(when, "model-a", 100, 0, 10, 110),
				// The same turn's cumulative usage event would double count.
				usageEvent(when.Add(time.Minute), 100, 10, 0, 110, 1),
			},
		},
	}
	service, _, _ := testService(t, store, "")
	result, err := service.Insights(context.Background(), contracts.InsightsQuery{Days: 7})
	if err != nil {
		t.Fatalf("Insights: %v", err)
	}
	if result.Totals.PromptTokens != 100 || result.Totals.CompletionTokens != 10 || result.Totals.TotalTokens != 110 {
		t.Fatalf("totals = %+v, want the model_call tokens only", result.Totals)
	}
	if result.Totals.Turns != 1 || result.Totals.Steps != 1 {
		t.Fatalf("turns/steps = %d/%d, want 1/1", result.Totals.Turns, result.Totals.Steps)
	}
}

// TestUnpricedModelNeverReportsCost covers the no-price rule end to end.
func TestUnpricedModelNeverReportsCost(t *testing.T) {
	location := shanghai(t)
	when := time.Date(2026, 11, 15, 9, 0, 0, 0, location)
	store := &fakeStore{
		metas: []contracts.SessionMeta{{ID: "s1", UpdatedAt: when}},
		events: map[string][]rig.SessionEvent{
			"s1": {modelCallEvent(when, "unpriced", 1_000_000, 0, 500_000, 1_500_000)},
		},
	}
	service, _, _ := testService(t, store, pricedCatalog)
	result, err := service.Insights(context.Background(), contracts.InsightsQuery{Days: 7})
	if err != nil {
		t.Fatalf("Insights: %v", err)
	}
	if result.Totals.CostKnown || result.Totals.CostUSD != 0 || result.Totals.PricedCalls != 0 ||
		result.Totals.UnpricedCalls != 1 {
		t.Fatalf("totals = %+v, want unpriced/zero cost", result.Totals)
	}
	if result.Models[0].CostKnown || result.Models[0].CostUSD != 0 ||
		result.Sessions[0].CostKnown || result.Daily[6].CostKnown {
		t.Fatalf("unpriced rows invented a cost: %+v / %+v / %+v", result.Models[0], result.Sessions[0], result.Daily[6])
	}
}

// TestEmptyStoreIsAnHonestEmptyWindow covers the cold case: no sessions at all.
func TestEmptyStoreIsAnHonestEmptyWindow(t *testing.T) {
	store := &fakeStore{}
	service, _, location := testService(t, store, pricedCatalog)
	result, err := service.Insights(context.Background(), contracts.InsightsQuery{})
	if err != nil {
		t.Fatalf("Insights: %v", err)
	}
	if result.Days != contracts.InsightsDefaultDays || len(result.Daily) != contracts.InsightsDefaultDays {
		t.Fatalf("default window = %d/%d, want 7/7", result.Days, len(result.Daily))
	}
	if result.Totals.Sessions != 0 || result.Totals.TotalTokens != 0 || result.Totals.CostKnown {
		t.Fatalf("empty totals = %+v", result.Totals)
	}
	if len(result.Models) != 0 || len(result.Workspaces) != 0 || len(result.Sessions) != 0 {
		t.Fatalf("empty rows: models %v, workspaces %v, sessions %v",
			result.Models, result.Workspaces, result.Sessions)
	}
	if result.Models == nil || result.Workspaces == nil || result.Sessions == nil {
		t.Fatal("empty rows must be empty slices, not nil")
	}
	for index, day := range result.Daily {
		want := time.Date(2026, 11, 9+index, 0, 0, 0, 0, location)
		if !day.Day.Equal(want) || day.TotalTokens != 0 || day.Sessions != 0 || day.CostKnown {
			t.Fatalf("empty day %d = %+v, want %v zeros", index, day, want)
		}
	}
}

// TestSessionsOutsideWindowAreNotRead proves the bounded read: a session whose
// durable updated time predates the window is skipped entirely.
func TestSessionsOutsideWindowAreNotRead(t *testing.T) {
	location := shanghai(t)
	old := time.Date(2026, 10, 1, 9, 0, 0, 0, location)
	store := &fakeStore{
		metas:  []contracts.SessionMeta{{ID: "old", UpdatedAt: old}},
		events: map[string][]rig.SessionEvent{"old": {modelCallEvent(old, "model-a", 1, 0, 0, 1)}},
	}
	service, _, _ := testService(t, store, "")
	if _, err := service.Insights(context.Background(), contracts.InsightsQuery{Days: 7}); err != nil {
		t.Fatalf("Insights: %v", err)
	}
	if store.lastCalls != 0 {
		t.Fatalf("Last calls = %d, want 0 (an out-of-window session is not read)", store.lastCalls)
	}
}

// TestTruncatedReadIsReported covers the per-session bound: when the newest
// events do not reach the window start, the result flags truncation instead of
// silently under-counting.
func TestTruncatedReadIsReported(t *testing.T) {
	location := shanghai(t)
	mk := func(hour int) time.Time { return time.Date(2026, 11, 15, hour, 0, 0, 0, location) }
	store := &fakeStore{
		metas: []contracts.SessionMeta{{ID: "s1", UpdatedAt: mk(12)}},
		events: map[string][]rig.SessionEvent{
			"s1": {
				modelCallEvent(mk(8), "model-a", 1, 0, 0, 1),
				modelCallEvent(mk(9), "model-a", 1, 0, 0, 1),
				modelCallEvent(mk(10), "model-a", 1, 0, 0, 1),
			},
		},
	}
	service, _, _ := testService(t, store, "")
	// newService clamps the configured bound to a serviceable minimum; tests
	// set it directly to exercise the truncation path.
	service.eventsPerSession = 2
	result, err := service.Insights(context.Background(), contracts.InsightsQuery{Days: 7})
	if err != nil {
		t.Fatalf("Insights: %v", err)
	}
	if !result.Truncated || result.TruncatedSessions != 1 {
		t.Fatalf("truncated = %v/%d, want true/1", result.Truncated, result.TruncatedSessions)
	}
	if result.Totals.Steps != 2 {
		t.Fatalf("steps = %d, want the 2 newest events", result.Totals.Steps)
	}
}

// TestCacheServesFreshResultsAndForceRefreshBypassesIt covers the aggregation
// cache and the explicit refresh.
func TestCacheServesFreshResultsAndForceRefreshBypassesIt(t *testing.T) {
	location := shanghai(t)
	when := time.Date(2026, 11, 15, 9, 0, 0, 0, location)
	store := &fakeStore{
		metas:  []contracts.SessionMeta{{ID: "s1", UpdatedAt: when}},
		events: map[string][]rig.SessionEvent{"s1": {modelCallEvent(when, "model-a", 10, 0, 5, 15)}},
	}
	service, now, _ := testService(t, store, pricedCatalog)
	ctx := context.Background()
	if _, err := service.Insights(ctx, contracts.InsightsQuery{Days: 7}); err != nil {
		t.Fatalf("first Insights: %v", err)
	}
	if store.sessionsCalls != 1 {
		t.Fatalf("sessions calls = %d, want 1", store.sessionsCalls)
	}
	if _, err := service.Insights(ctx, contracts.InsightsQuery{Days: 7, SessionLimit: 8}); err != nil {
		t.Fatalf("cached Insights: %v", err)
	}
	if store.sessionsCalls != 1 {
		t.Fatalf("cached read hit the store: sessions calls = %d, want 1", store.sessionsCalls)
	}
	if _, err := service.Insights(ctx, contracts.InsightsQuery{Days: 7, ForceRefresh: true}); err != nil {
		t.Fatalf("force refresh: %v", err)
	}
	if store.sessionsCalls != 2 {
		t.Fatalf("force refresh did not bypass the cache: sessions calls = %d, want 2", store.sessionsCalls)
	}
	// The 30-day window is a different cache key.
	if _, err := service.Insights(ctx, contracts.InsightsQuery{Days: 30}); err != nil {
		t.Fatalf("month Insights: %v", err)
	}
	if store.sessionsCalls != 3 {
		t.Fatalf("month window reused the week cache: sessions calls = %d, want 3", store.sessionsCalls)
	}
	// Past the TTL the cache expires.
	service.now = func() time.Time { return now.Add(DefaultCacheTTL + time.Second) }
	if _, err := service.Insights(ctx, contracts.InsightsQuery{Days: 7}); err != nil {
		t.Fatalf("expired-cache Insights: %v", err)
	}
	if store.sessionsCalls != 4 {
		t.Fatalf("expired cache did not reload: sessions calls = %d, want 4", store.sessionsCalls)
	}
}

// TestStoreErrorsPropagate keeps a store failure from rendering as zero data.
func TestStoreErrorsPropagate(t *testing.T) {
	store := &fakeStore{sessionsErr: errors.New("disk gone")}
	service, _, _ := testService(t, store, "")
	if _, err := service.Insights(context.Background(), contracts.InsightsQuery{}); err == nil {
		t.Fatal("Insights succeeded with a failing store")
	}
}

// TestQueryNormalization pins the window/leaderboard clamps.
func TestQueryNormalization(t *testing.T) {
	location := shanghai(t)
	store := &fakeStore{metas: []contracts.SessionMeta{{ID: "s1", UpdatedAt: time.Date(2026, 11, 15, 9, 0, 0, 0, location)}}}
	service, _, _ := testService(t, store, "")
	result, err := service.Insights(context.Background(), contracts.InsightsQuery{
		Days: 1000, SessionLimit: 1000,
	})
	if err != nil {
		t.Fatalf("Insights: %v", err)
	}
	if result.Days != contracts.InsightsMaxDays {
		t.Fatalf("Days = %d, want %d", result.Days, contracts.InsightsMaxDays)
	}
	if len(result.Daily) != contracts.InsightsMaxDays {
		t.Fatalf("Daily = %d rows, want %d", len(result.Daily), contracts.InsightsMaxDays)
	}
}
