package presets_test

// Example user preset fixtures, format smoke test and micro-benchmarks.
//
// The JSON files under testdata/ are the documented user preset shape:
//   "<data dir>/presets/<id>.json"
// They are copied into a temporary data directory here so the loader contract
// (merge, allowlist, persona sections) is exercised against real files rather
// than string literals.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zeokeel/zeokeel/internal/presets"
	"github.com/zeokeel/zeokeel/internal/prompt"
)

// exampleTools is a registry surface wide enough for the example presets.
var exampleTools = []string{
	"read_file", "write_file", "edit_file", "bash", "read_image",
	"terminal_open", "terminal_read", "settings_get", "settings_set",
}

// copyExamples installs testdata/*.json as <dataDir>/presets/*.json.
func copyExamples(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	dst := filepath.Join(dataDir, "presets")
	if err := os.MkdirAll(dst, 0o700); err != nil {
		t.Fatalf("create presets dir: %v", err)
	}
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		body, err := os.ReadFile(filepath.Join("testdata", entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, entry.Name()), body, 0o600); err != nil {
			t.Fatalf("install %s: %v", entry.Name(), err)
		}
	}
	return dataDir
}

// TestUserPresetJSONExamplesLoad pins the documented example shape against the
// real loader: both example files parse, are validated against a known tool
// set, and expose persona sections/tool filtering.
func TestUserPresetJSONExamplesLoad(t *testing.T) {
	dataDir := copyExamples(t)
	reg, err := presets.Load(presets.Options{UserDir: dataDir, KnownTools: exampleTools})
	if err != nil {
		t.Fatalf("Load examples: %v", err)
	}
	if got := len(reg.List()); got != 5 {
		t.Fatalf("List() = %d presets, want 5 (3 built-ins + 2 examples): %+v", got, presetIDs(reg))
	}
	for _, id := range []string{"reviewer", "doc-writer"} {
		if _, ok := reg.Get(id); !ok {
			t.Fatalf("example preset %q not loaded (IDs %v)", id, presetIDs(reg))
		}
	}

	visible, err := reg.VisibleTools("doc-writer", exampleTools)
	if err != nil {
		t.Fatalf("VisibleTools(doc-writer): %v", err)
	}
	want := []string{"read_file", "write_file", "edit_file"}
	if !equalStrings(visible, want) {
		t.Errorf("VisibleTools(doc-writer) = %v, want %v", visible, want)
	}

	sections := reg.PersonaSections("reviewer")
	if len(sections) != 2 {
		t.Fatalf("PersonaSections(reviewer) = %d sections, want 2", len(sections))
	}
	if sections[0].ID != presets.PersonaSectionID || sections[0].Order != prompt.OrderPersonaPrefix {
		t.Errorf("reviewer persona section = %+v, want %s at order %d", sections[0], presets.PersonaSectionID, prompt.OrderPersonaPrefix)
	}
	if sections[1].ID != presets.PromptSectionID || sections[1].Order != presets.OrderPresetPrompt {
		t.Errorf("reviewer instructions section = %+v, want %s at order %d", sections[1], presets.PromptSectionID, presets.OrderPresetPrompt)
	}

	// The examples declare no model: the session keeps the app default.
	if p, _ := reg.Get("doc-writer"); p.Model != "" {
		t.Errorf("doc-writer Model = %q, want empty (app default)", p.Model)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// BenchmarkPersonaSections measures prompt-section construction (called on an
// agent build / session open path).
func BenchmarkPersonaSections(b *testing.B) {
	dataDir := b.TempDir()
	dir := filepath.Join(dataDir, "presets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		b.Fatalf("create presets dir: %v", err)
	}
	body, err := os.ReadFile("testdata/reviewer.json")
	if err != nil {
		b.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reviewer.json"), body, 0o600); err != nil {
		b.Fatalf("install fixture: %v", err)
	}
	reg, err := presets.Load(presets.Options{UserDir: dataDir, KnownTools: exampleTools})
	if err != nil {
		b.Fatalf("Load: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := reg.PersonaSections("reviewer"); len(got) != 2 {
			b.Fatalf("PersonaSections = %d sections, want 2", len(got))
		}
	}
}

// BenchmarkVisibleTools measures allowlist filtering (called on every agent
// build and on plugin/preset rebuilds).
func BenchmarkVisibleTools(b *testing.B) {
	dataDir := b.TempDir()
	dir := filepath.Join(dataDir, "presets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		b.Fatalf("create presets dir: %v", err)
	}
	body, err := os.ReadFile("testdata/doc-writer.json")
	if err != nil {
		b.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "doc-writer.json"), body, 0o600); err != nil {
		b.Fatalf("install fixture: %v", err)
	}
	reg, err := presets.Load(presets.Options{UserDir: dataDir, KnownTools: exampleTools})
	if err != nil {
		b.Fatalf("Load: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := reg.VisibleTools("doc-writer", exampleTools); err != nil {
			b.Fatalf("VisibleTools: %v", err)
		}
	}
}
