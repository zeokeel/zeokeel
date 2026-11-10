package contracts

import "testing"

// mapSettings is a minimal SettingsService for the contract helpers.
type mapSettings map[string]string

func (m mapSettings) Get(key, def string) string {
	if value, ok := m[key]; ok {
		return value
	}
	return def
}

func (m mapSettings) Set(key, value string) error { m[key] = value; return nil }

func (m mapSettings) All() map[string]string {
	out := make(map[string]string, len(m))
	for key, value := range m {
		out[key] = value
	}
	return out
}

func TestLoadModelCatalog(t *testing.T) {
	if models, err := LoadModelCatalog(nil); err != nil || models != nil {
		t.Fatalf("nil settings = %v / %v", models, err)
	}
	empty := mapSettings{}
	if models, err := LoadModelCatalog(empty); err != nil || models != nil {
		t.Fatalf("unset row = %v / %v", models, err)
	}
	empty[SettingModelCatalogModels] = "not json"
	if _, err := LoadModelCatalog(empty); err == nil {
		t.Fatal("malformed row did not report an error")
	}
	empty[SettingModelCatalogModels] = `[{"id":"qwen3.8-flash-next","baseURL":"https://example.com/v1/","apiKey":" sk-1 "}]`
	models, err := LoadModelCatalog(empty)
	if err != nil || len(models) != 1 || models[0].ID != "qwen3.8-flash-next" {
		t.Fatalf("decoded catalog = %+v / %v", models, err)
	}
}

func TestSanitizeModelInfoList(t *testing.T) {
	temperature := 3.0
	topP := -1.0
	models := SanitizeModelInfoList([]ModelInfo{
		{ID: "  a  ", BaseURL: "https://a.example/v1/", APIKey: " k ", Temperature: &temperature, TopP: &topP, MaxTokens: -5},
		{ID: "a", Name: "duplicate"},
		{ID: ""},
		{ID: "b"},
	})
	if len(models) != 2 {
		t.Fatalf("models = %+v, want two rows", models)
	}
	first := models[0]
	if first.ID != "a" || first.Name != "a" || first.BaseURL != "https://a.example/v1" || first.APIKey != "k" {
		t.Fatalf("sanitized first row = %+v", first)
	}
	if first.Temperature == nil || *first.Temperature != 2 || first.TopP == nil || *first.TopP != 0 || first.MaxTokens != 0 {
		t.Fatalf("sanitized generation = %+v / %+v / %d", first.Temperature, first.TopP, first.MaxTokens)
	}
	if models[1].ID != "b" || models[1].Name != "b" {
		t.Fatalf("second row = %+v", models[1])
	}
}

func TestModelConnection(t *testing.T) {
	models := []ModelInfo{
		{ID: "own", BaseURL: "https://own.example/v1/", APIKey: " own-key "},
		{ID: "partial", BaseURL: "https://partial.example/v1"},
		{ID: "fallback"},
	}
	cases := []struct {
		id, wantURL, wantKey string
	}{
		{"own", "https://own.example/v1", "own-key"},
		{"partial", "https://partial.example/v1", "global-key"},
		{"fallback", "https://global.example/v1", "global-key"},
		{"unknown", "https://global.example/v1", "global-key"},
	}
	for _, tc := range cases {
		url, key := ModelConnection(models, tc.id, "https://global.example/v1/", " global-key ")
		if url != tc.wantURL || key != tc.wantKey {
			t.Fatalf("ModelConnection(%q) = %q / %q, want %q / %q",
				tc.id, url, key, tc.wantURL, tc.wantKey)
		}
	}
}

func TestLegacyConnectionModelInfo(t *testing.T) {
	if _, ok := LegacyConnectionModelInfo(nil); ok {
		t.Fatal("nil settings produced a legacy model")
	}
	settings := mapSettings{}
	if _, ok := LegacyConnectionModelInfo(settings); ok {
		t.Fatal("empty settings produced a legacy model")
	}
	settings[SettingsChatModel] = " qwen3.8-flash-next "
	settings[SettingsChatBaseURL] = "https://ai.example/v1/"
	settings[SettingsChatAPIKey] = " sk-legacy "
	model, ok := LegacyConnectionModelInfo(settings)
	if !ok {
		t.Fatal("configured legacy connection was not found")
	}
	if model.ID != "qwen3.8-flash-next" || model.Name != "qwen3.8-flash-next" ||
		model.BaseURL != "https://ai.example/v1" || model.APIKey != "sk-legacy" {
		t.Fatalf("legacy row = %+v", model)
	}
}

func TestDefaultModelIDAndEntry(t *testing.T) {
	models := []ModelInfo{{ID: "first"}, {ID: "second"}}
	if got := DefaultModelID(models, nil); got != "first" {
		t.Fatalf("first-row default = %q", got)
	}
	settings := mapSettings{SettingsChatModel: "second"}
	if got := DefaultModelID(models, settings); got != "second" {
		t.Fatalf("configured default = %q", got)
	}
	if entry, ok := DefaultModelEntry(models, settings); !ok || entry.ID != "second" {
		t.Fatalf("default entry = %+v / %v", entry, ok)
	}
	// A configured default that is no longer in the list falls back to the
	// first row so the UI always marks a row that exists.
	if got := DefaultModelID(models, mapSettings{SettingsChatModel: "gone"}); got != "first" {
		t.Fatalf("stale default = %q", got)
	}
}
