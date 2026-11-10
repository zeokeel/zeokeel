package contracts

import "testing"

func TestModelProviderPresets(t *testing.T) {
	presets := ModelProviderPresets()
	if len(presets) < 5 {
		t.Fatalf("preset count = %d", len(presets))
	}
	for index, preset := range presets {
		if preset.ID == "" || preset.NameZH == "" || preset.NameEN == "" {
			t.Fatalf("preset %d is incomplete: %+v", index, preset)
		}
		if preset.IsCustom() && preset.ExampleModel != "" {
			t.Fatalf("custom preset carries an example model: %+v", preset)
		}
		if !preset.IsCustom() && (preset.BaseURL == "" || preset.ExampleModel == "") {
			t.Fatalf("provider preset %q misses URL/example model: %+v", preset.ID, preset)
		}
	}
	if last := presets[len(presets)-1]; !last.IsCustom() {
		t.Fatalf("last preset is not the custom entry: %+v", last)
	}
	// Displaying the returned slice (or changing it) must not mutate the table.
	presets[0].NameZH = "mutated"
	if ModelProviderPresets()[0].NameZH == "mutated" {
		t.Fatal("ModelProviderPresets shared its backing slice")
	}
}

func TestProviderPresetLookupAndMatch(t *testing.T) {
	preset, ok := ProviderPresetByID("DeepSeek")
	if !ok || preset.ID != "deepseek" || preset.BaseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("lookup = %+v / %v", preset, ok)
	}
	if _, ok := ProviderPresetByID("does-not-exist"); ok {
		t.Fatal("unknown preset id matched")
	}
	matched, ok := MatchProviderPreset("https://API.DeepSeek.com/v1/")
	if !ok || matched.ID != "deepseek" {
		t.Fatalf("match = %+v / %v", matched, ok)
	}
	if _, ok := MatchProviderPreset("https://my-own.example/v1"); ok {
		t.Fatal("unknown URL matched a preset")
	}
	if _, ok := MatchProviderPreset(""); ok {
		t.Fatal("empty URL matched a preset")
	}
	for _, english := range []bool{false, true} {
		labels := map[string]bool{}
		for _, preset := range ModelProviderPresets() {
			label := preset.DisplayName(english)
			if label == "" || labels[label] {
				t.Fatalf("duplicate/empty provider label %q (english=%v)", label, english)
			}
			labels[label] = true
		}
	}
}
