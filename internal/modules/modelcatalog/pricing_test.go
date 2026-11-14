package modelcatalog

import (
	"math"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestConfiguredPricesReachTheCatalog pins the optional per-million price fields:
// the assembly baseline passes them through and the sanitizer keeps/clamps them.
func TestConfiguredPricesReachTheCatalog(t *testing.T) {
	svc := newService(Config{Models: []Model{{
		ID:                         "priced",
		ContextWindow:              128000,
		InputPricePerMillion:       2.5,
		CachedInputPricePerMillion: 0.5,
		OutputPricePerMillion:      10,
	}}}, &fakeLogger{})
	models := svc.Models()
	if len(models) != 1 {
		t.Fatalf("models = %d, want 1", len(models))
	}
	model := models[0]
	if model.InputPricePerMillion != 2.5 || model.CachedInputPricePerMillion != 0.5 ||
		model.OutputPricePerMillion != 10 {
		t.Fatalf("model prices = %+v, want 2.5/0.5/10", model)
	}
	cost, ok := model.Cost(1_000_000, 0, 1_000_000)
	if !ok || math.Abs(cost-12.5) > 1e-9 {
		t.Fatalf("Cost = %v (ok=%v), want 12.5", cost, ok)
	}
}

// TestSettingsPriceRoundTrip pins that the user-edited settings row decodes the
// price fields (the same JSON tags the catalog editor will write).
func TestSettingsPriceRoundTrip(t *testing.T) {
	raw := `[{"id":"priced","inputPricePerMillion":2,"cachedInputPricePerMillion":1,"outputPricePerMillion":8}]`
	models, err := contracts.ParseModelInfoList(raw)
	if err != nil {
		t.Fatalf("ParseModelInfoList: %v", err)
	}
	if len(models) != 1 || models[0].OutputPricePerMillion != 8 {
		t.Fatalf("decoded models = %+v, want the output price", models)
	}
}
