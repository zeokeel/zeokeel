package modelcatalog

// Tests for the model context-window metadata.

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestConfiguredCatalogContextWindow covers the assembly baseline: the context
// window is passed through and invalid values are normalized to "unknown" (0).
func TestConfiguredCatalogContextWindow(t *testing.T) {
	svc := newService(Config{Models: []Model{
		{ID: "m1", ContextWindow: 32000},
		{ID: "m2", ContextWindow: -5},
	}}, &fakeLogger{})
	models := svc.Models()
	if len(models) != 2 || models[0].ContextWindow != 32000 || models[1].ContextWindow != 0 {
		t.Fatalf("configured context windows = %+v", models)
	}
}

func TestSettingsCatalogContextWindowAndSanitize(t *testing.T) {
	store := newSettingsStore(nil)
	store.setModels(`[
		{"id":"a","name":"A","contextWindow":128000},
		{"id":"b","contextWindow":-5},
		{"id":"c","contextWindow":0},
		{"id":"d"}
	]`)
	svc, _ := modelService(t, Config{}, store)
	models := svc.Models()
	if len(models) != 4 {
		t.Fatalf("models = %+v", models)
	}
	if models[0].ContextWindow != 128000 {
		t.Fatalf("valid context window = %d", models[0].ContextWindow)
	}
	for _, model := range models[1:] {
		if model.ContextWindow != 0 {
			t.Fatalf("invalid context window %s = %d, want 0", model.ID, model.ContextWindow)
		}
	}
}

func TestContextWindowJSONRoundTrip(t *testing.T) {
	encoded, err := contracts.EncodeModelInfoList([]contracts.ModelInfo{
		{ID: "deepseek-chat", Name: "DeepSeek Chat", ContextWindow: 128000},
		{ID: "plain", Name: "Plain"},
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(encoded, `"contextWindow":128000`) {
		t.Fatalf("encoded JSON = %s", encoded)
	}
	parsed, err := contracts.ParseModelInfoList(encoded)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(parsed) != 2 || parsed[0].ContextWindow != 128000 || parsed[1].ContextWindow != 0 {
		t.Fatalf("round-trip = %+v", parsed)
	}
}
