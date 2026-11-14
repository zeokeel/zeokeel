package contracts

import (
	"math"
	"testing"
)

func TestModelInfoHasPricingAndCost(t *testing.T) {
	unpriced := ModelInfo{ID: "free"}
	if unpriced.HasPricing() {
		t.Fatal("a model without prices reports HasPricing")
	}
	if _, ok := unpriced.Cost(1000, 0, 1000); ok {
		t.Fatal("a model without prices reported a cost")
	}

	model := ModelInfo{
		ID:                         "priced",
		InputPricePerMillion:       2,
		CachedInputPricePerMillion: 1,
		OutputPricePerMillion:      8,
	}
	// 90000 uncached input @2 + 10000 cached @1 + 1000 output @8, per million.
	cost, ok := model.Cost(100_000, 10_000, 1_000)
	if !ok {
		t.Fatal("priced model reported no cost")
	}
	want := 0.18 + 0.01 + 0.008
	if math.Abs(cost-want) > 1e-9 {
		t.Fatalf("Cost = %v, want %v", cost, want)
	}

	// Cached tokens are a subset of prompt tokens: an over-reported cached count
	// never makes the uncached part negative.
	clamped, ok := model.Cost(1_000, 5_000, 0)
	if !ok {
		t.Fatal("clamped cost reported no cost")
	}
	if wantClamped := 1000.0 / 1_000_000 * 1; math.Abs(clamped-wantClamped) > 1e-12 {
		t.Fatalf("clamped Cost = %v, want %v", clamped, wantClamped)
	}

	// A row that only prices input uses the input price for cached tokens too.
	inputOnly := ModelInfo{ID: "input-only", InputPricePerMillion: 4}
	cost, ok = inputOnly.Cost(1_000_000, 250_000, 0)
	if !ok || math.Abs(cost-4) > 1e-9 {
		t.Fatalf("input-only Cost = %v (ok=%v), want 4", cost, ok)
	}
}

func TestSanitizePrices(t *testing.T) {
	input, cached, output := SanitizePrices(-1, 2, 0)
	if input != 0 || cached != 2 || output != 0 {
		t.Fatalf("SanitizePrices = %v/%v/%v, want 0/2/0", input, cached, output)
	}
	if input, _, _ := SanitizePrices(math.NaN(), 0, 0); input != 0 {
		t.Fatalf("SanitizePrices(NaN) = %v, want 0", input)
	}
	if input, _, _ := SanitizePrices(1e12, 0, 0); input != 1e9 {
		t.Fatalf("SanitizePrices(1e12) = %v, want the 1e9 cap", input)
	}
}

// TestSanitizeModelInfoListKeepsPrices pins that the catalog sanitizer (used for
// both the settings row and the assembly baseline) keeps the price fields.
func TestSanitizeModelInfoListKeepsPrices(t *testing.T) {
	models := SanitizeModelInfoList([]ModelInfo{{
		ID: "m", InputPricePerMillion: 3, CachedInputPricePerMillion: -1, OutputPricePerMillion: 9,
	}})
	if len(models) != 1 {
		t.Fatalf("models = %d, want 1", len(models))
	}
	if models[0].InputPricePerMillion != 3 || models[0].CachedInputPricePerMillion != 0 ||
		models[0].OutputPricePerMillion != 9 {
		t.Fatalf("sanitized prices = %+v, want 3/0/9", models[0])
	}
}
