package modelcatalog

import (
	"net/url"
	"strings"
	"testing"
)

// TestCatalogProvidersAreWellFormed pins the invariants the Add model dialog
// relies on: stable unique ids, usable endpoints, unique model ids per
// provider, and effort lists that only name known levels with a valid default.
func TestCatalogProvidersAreWellFormed(t *testing.T) {
	seenProviders := map[string]bool{}
	for _, provider := range Providers() {
		if provider.ID == "" || provider.Name == "" {
			t.Fatalf("provider with empty id or name: %+v", provider)
		}
		if seenProviders[provider.ID] {
			t.Fatalf("duplicate provider id %q", provider.ID)
		}
		seenProviders[provider.ID] = true
		u, err := url.Parse(provider.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			t.Fatalf("provider %q has unusable base URL %q (%v)", provider.ID, provider.BaseURL, err)
		}
		if strings.HasSuffix(provider.BaseURL, "/") {
			t.Fatalf("provider %q base URL has a trailing slash: %q", provider.ID, provider.BaseURL)
		}
		if len(provider.Models) == 0 {
			t.Fatalf("provider %q has no models", provider.ID)
		}
		seenModels := map[string]bool{}
		for _, model := range provider.Models {
			if model.ID == "" {
				t.Fatalf("provider %q has a model with an empty id", provider.ID)
			}
			if seenModels[model.ID] {
				t.Fatalf("provider %q has duplicate model id %q", provider.ID, model.ID)
			}
			seenModels[model.ID] = true
			if model.ContextWindow < 0 || model.MaxTokens < 0 {
				t.Fatalf("provider %q model %q has a negative capacity: %+v", provider.ID, model.ID, model)
			}
			if model.ContextWindow > 0 && model.MaxTokens > model.ContextWindow {
				t.Fatalf("provider %q model %q has maxTokens > contextWindow", provider.ID, model.ID)
			}
			lastIndex := -1
			for _, effort := range model.Efforts {
				if !ValidEffort(effort) {
					t.Fatalf("provider %q model %q names unknown effort %q", provider.ID, model.ID, effort)
				}
				index := effortIndex(effort)
				if index <= lastIndex {
					t.Fatalf("provider %q model %q efforts are not ascending: %v", provider.ID, model.ID, model.Efforts)
				}
				lastIndex = index
			}
			if len(model.Efforts) == 0 && model.DefaultEffort != "" {
				t.Fatalf("provider %q model %q has a default effort but no efforts", provider.ID, model.ID)
			}
			if model.DefaultEffort != "" && !contains(model.Efforts, model.DefaultEffort) {
				t.Fatalf("provider %q model %q default effort %q is not in its efforts", provider.ID, model.ID, model.DefaultEffort)
			}
		}
	}
}

// TestCatalogDeepSeekIsBuiltIn pins the framework-parity DeepSeek defaults:
// two published models, both with the 1M context window, and the effort levels
// its OpenAI-compatible route accepts.
func TestCatalogDeepSeekIsBuiltIn(t *testing.T) {
	provider, ok := LookupProvider("deepseek")
	if !ok {
		t.Fatal("catalog has no DeepSeek provider")
	}
	if provider.BaseURL != "https://api.deepseek.com" || !provider.KeyRequired {
		t.Fatalf("DeepSeek provider = %+v", provider)
	}
	flash, ok := LookupModel("deepseek", "deepseek-flash")
	if !ok {
		t.Fatal("catalog has no deepseek-flash model")
	}
	if flash.Name != "DeepSeek V4.1 Flash" || flash.ContextWindow != 1000000 || flash.MaxTokens != 384000 {
		t.Fatalf("deepseek-flash = %+v", flash)
	}
	if got, want := strings.Join(flash.Efforts, ","), "off,low,high,max"; got != want {
		t.Fatalf("deepseek-flash efforts = %q, want %q", got, want)
	}
	if flash.DefaultEffort != EffortHigh {
		t.Fatalf("deepseek-flash default effort = %q", flash.DefaultEffort)
	}
	if _, ok := LookupModel("deepseek", "deepseek-v4-pro"); !ok {
		t.Fatal("catalog has no deepseek-v4-pro model")
	}
}

// TestLookupHelpers covers the lookups the dialog and the composer use.
func TestLookupHelpers(t *testing.T) {
	if _, ok := LookupProvider("nope"); ok {
		t.Fatal("LookupProvider found an unknown provider")
	}
	if _, ok := LookupModel("deepseek", "nope"); ok {
		t.Fatal("LookupModel found an unknown model")
	}
	if provider, ok := ProviderForBaseURL("https://api.openai.com/v1/"); !ok || provider.ID != "openai" {
		t.Fatalf("ProviderForBaseURL openai trailing slash = %+v, %v", provider, ok)
	}
	if _, ok := ProviderForBaseURL("https://gateway.example.test"); ok {
		t.Fatal("ProviderForBaseURL matched a custom endpoint")
	}
	if _, ok := ProviderForBaseURL(""); ok {
		t.Fatal("ProviderForBaseURL matched an empty endpoint")
	}

	if efforts, known := EffortsFor("deepseek", "deepseek-v4-pro"); !known || strings.Join(efforts, ",") != "off,high,max" {
		t.Fatalf("EffortsFor deepseek-v4-pro = %v, %v", efforts, known)
	}
	if _, known := EffortsFor("deepseek", "some-custom-model"); known {
		t.Fatal("EffortsFor claimed to know a custom model")
	}

	for _, effort := range AllEfforts() {
		if !ValidEffort(effort) {
			t.Fatalf("AllEfforts contains invalid effort %q", effort)
		}
		if EffortLabel(effort) == effort {
			t.Fatalf("EffortLabel(%q) did not capitalize", effort)
		}
	}
	if ValidEffort("") || ValidEffort("High") {
		t.Fatal("ValidEffort accepted an empty or mis-cased value")
	}
	if EffortLabel("") != "" {
		t.Fatal("EffortLabel of empty id is not empty")
	}
	if EffortLabel("xhigh") != "Xhigh" {
		t.Fatalf("EffortLabel(xhigh) = %q", EffortLabel("xhigh"))
	}
}

// effortIndex is the persisted order of an effort id, -1 when unknown.
func effortIndex(id string) int {
	for i, effort := range allEfforts {
		if effort == id {
			return i
		}
	}
	return -1
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
