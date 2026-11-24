package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// The insights screen (R24) fetches /api/insights?range=7|30; the gateway answered
// 404, so the screen showed its error state. The backend aggregates by model and by
// workspace — there is no per-tool breakdown — so the payload exposes byModel and
// the screen plots that instead of a column the backend cannot fill.

type fakeInsights struct {
	result contracts.Insights
	got    contracts.InsightsQuery
}

func (f *fakeInsights) Insights(_ context.Context, query contracts.InsightsQuery) (contracts.Insights, error) {
	f.got = query
	return f.result, nil
}

func newInsightsGateway(t *testing.T, service *fakeInsights) *runningGateway {
	t.Helper()
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	root := testRoot(t, chat)
	if service != nil {
		if _, err := root.Provide(contracts.KeyInsights, contracts.InsightsService(service)); err != nil {
			t.Fatalf("provide insights: %v", err)
		}
	}
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Provide(root); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	return serveGateway(t, gateway)
}

func TestInsightsServesTotalsDaysAndModels(t *testing.T) {
	day := time.Date(2026, 11, 24, 0, 0, 0, 0, time.Local)
	service := &fakeInsights{result: contracts.Insights{
		Days: 30,
		Totals: contracts.InsightsTotals{
			Turns: 12, PromptTokens: 900, CompletionTokens: 300, ToolCalls: 7, Sessions: 3,
		},
		Daily:  []contracts.InsightsDay{{Day: day, Turns: 4, ToolCalls: 2}},
		Models: []contracts.InsightsModel{{Model: "qwen3.8-flash-next", Calls: 5, TotalTokens: 1200}},
	}}
	gw := newInsightsGateway(t, service)
	defer gw.stop(t)

	status, body := filesGet(t, gw, "/api/insights?range=30", true)
	if status != http.StatusOK {
		t.Fatalf("GET /api/insights = %d %q", status, body)
	}
	var payload insightsDTO
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode: %v (%q)", err, body)
	}
	if service.got.Days != 30 {
		t.Fatalf("service got days = %d, want 30", service.got.Days)
	}
	if payload.Range != 30 {
		t.Fatalf("range = %d", payload.Range)
	}
	if payload.Totals.Sessions != 3 || payload.Totals.Turns != 12 ||
		payload.Totals.InputTokens != 900 || payload.Totals.OutputTokens != 300 ||
		payload.Totals.ToolCalls != 7 {
		t.Fatalf("totals = %+v", payload.Totals)
	}
	if len(payload.ByDay) != 1 || payload.ByDay[0].Day != "2026-11-24" || payload.ByDay[0].Turns != 4 {
		t.Fatalf("byDay = %+v", payload.ByDay)
	}
	if len(payload.ByModel) != 1 || payload.ByModel[0].Model != "qwen3.8-flash-next" ||
		payload.ByModel[0].TotalTokens != 1200 {
		t.Fatalf("byModel = %+v", payload.ByModel)
	}
	// The tool split is not a thing the backend tracks; it must not be invented.
	if jsonBody := body; containsField(jsonBody, "byTool") {
		t.Fatalf("payload invents a byTool column: %q", body)
	}
}

func TestInsightsDefaultsToSevenDays(t *testing.T) {
	service := &fakeInsights{result: contracts.Insights{Days: 7}}
	gw := newInsightsGateway(t, service)
	defer gw.stop(t)
	if status, body := filesGet(t, gw, "/api/insights", true); status != http.StatusOK {
		t.Fatalf("GET /api/insights = %d %q", status, body)
	}
	if service.got.Days != 7 {
		t.Fatalf("default days = %d, want 7", service.got.Days)
	}
}

func TestInsightsWithoutServiceIsEmpty(t *testing.T) {
	gw := newInsightsGateway(t, nil)
	defer gw.stop(t)
	status, body := filesGet(t, gw, "/api/insights?range=7", true)
	if status != http.StatusOK {
		t.Fatalf("GET = %d %q", status, body)
	}
	var payload insightsDTO
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode: %v (%q)", err, body)
	}
	if payload.ByDay == nil || payload.ByModel == nil {
		t.Fatalf("empty answer must carry empty arrays: %q", body)
	}
}

func TestInsightsNeedsTheToken(t *testing.T) {
	gw := newInsightsGateway(t, &fakeInsights{})
	defer gw.stop(t)
	if status, _ := filesGet(t, gw, "/api/insights?range=7", false); status != http.StatusUnauthorized {
		t.Fatalf("without a token = %d, want 401", status)
	}
}

// containsField reports whether a JSON body mentions the key.
func containsField(body, key string) bool {
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		return false
	}
	_, ok := decoded[key]
	return ok
}
