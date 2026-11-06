package shell

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The retired product features must never come back into the shell by
// accident. The forbidden words are assembled from parts on purpose: a plain
// source grep for the retired names has to stay empty, so the guard itself
// must not reintroduce the strings.
func retiredFeatureWords() (modeWord, inputWord, spokenWord string) {
	return "p" + "tc", "vo" + "ice", "\u8bed\u97f3"
}

// TestShellCatalogHasNoRetiredFeatureKeys checks the string catalog: no label,
// hint or plugin row still mentions a retired mode or feature.
func TestShellCatalogHasNoRetiredFeatureKeys(t *testing.T) {
	modeWord, inputWord, spokenWord := retiredFeatureWords()
	for key, translation := range translations {
		haystack := strings.ToLower(key + "\n" + translation.en + "\n" + translation.zh)
		for _, word := range []string{modeWord, inputWord, spokenWord} {
			if strings.Contains(haystack, word) {
				t.Errorf("catalog entry %q still mentions a retired feature: %q", key, translation.en)
			}
		}
	}
}

// TestShellModeListHasNoRetiredMode checks the code-side lists the pickers and
// settings pages render: the supported modes are exactly standard, minimal and
// creator.
func TestShellModeListHasNoRetiredMode(t *testing.T) {
	modeWord, _, _ := retiredFeatureWords()
	options := agentModeOptions()
	if len(options) != 3 {
		t.Fatalf("mode picker has %d entries, want 3", len(options))
	}
	for _, option := range options {
		if strings.EqualFold(option.Mode, modeWord) {
			t.Fatalf("mode picker still offers a retired mode: %+v", option)
		}
		if strings.Contains(strings.ToLower(option.Label+"\n"+option.Hint), modeWord) {
			t.Fatalf("mode picker still labels a retired mode: %+v", option)
		}
	}
	for _, mode := range []string{"standard", "minimal", "creator"} {
		if !supportedAgentMode(mode) {
			t.Fatalf("supported mode %q is not offered", mode)
		}
	}
	if supportedAgentMode(modeWord) || supportedAgentMode(modeWord+"-mode") {
		t.Fatal("the retired mode is still accepted as a preset")
	}
}

// TestShellPluginListHasNoRetiredPlugin checks the Plugins page data: no
// definition, label mapping or rendered row mentions the retired input
// feature.
func TestShellPluginListHasNoRetiredPlugin(t *testing.T) {
	_, inputWord, spokenWord := retiredFeatureWords()
	check := func(where, text string) {
		t.Helper()
		lower := strings.ToLower(text)
		if strings.Contains(lower, inputWord) || strings.Contains(text, spokenWord) {
			t.Fatalf("%s still mentions the retired feature: %q", where, text)
		}
	}
	for _, def := range experimentalPluginDefs {
		check("experimental plugin", def.name+def.titleEN+def.titleZH+def.descEN+def.descZH)
	}
	for label, key := range pluginRowNameKeys {
		check("plugin name mapping", label+key)
	}
	for desc, key := range pluginRowDescKeys {
		check("plugin description mapping", desc+key)
	}
	if _, ok := pluginDetailForKey(inputWord + "_input"); ok {
		t.Fatal("plugin details still have a spec for the retired plugin")
	}
	if spec, ok := pluginDetailForName("Vo" + "ice input"); ok {
		t.Fatalf("plugin details still handle the retired plugin: %+v", spec)
	}

	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Plugins
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	for _, text := range tt.Texts() {
		check("Plugins page", text)
	}
}

// TestShellModePickerHasNoRetiredMode is the rendered check: the Home mode
// menu shows only the supported presets.
func TestShellModePickerHasNoRetiredMode(t *testing.T) {
	modeWord, _, _ := retiredFeatureWords()
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Standard mode"); err != nil {
		t.Fatalf("open mode picker: %v", err)
	}
	tt.Frame()
	for _, text := range tt.Texts() {
		if strings.Contains(strings.ToLower(text), modeWord) {
			t.Fatalf("rendered mode picker still shows a retired mode: %q", text)
		}
	}
	for _, want := range []string{"Minimal mode", "Creator mode"} {
		if !tt.HasText(want) {
			t.Fatalf("mode picker lost %q: %q", want, tt.Texts())
		}
	}
}
