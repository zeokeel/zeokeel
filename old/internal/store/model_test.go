package store

import (
	"errors"
	"strings"
	"testing"
)

func TestModelCRUDAndDefault(t *testing.T) {
	s := openTestStore(t)

	first, err := s.CreateModel(Model{Name: "Alpha", BaseURL: "https://alpha.test", APIKey: "sk-alpha"})
	if err != nil {
		t.Fatalf("CreateModel(first): %v", err)
	}
	if first.ID == "" {
		t.Fatal("CreateModel generated no id")
	}
	if !first.Default {
		t.Fatal("first model should become the default")
	}
	if id, err := s.DefaultModelID(); err != nil || id != first.ID {
		t.Fatalf("DefaultModelID = %q, %v; want %q", id, err, first.ID)
	}
	// Marking the first model default syncs provider.*; an empty wire model
	// falls back to the display name.
	if got, err := s.GetSetting(ProviderModelKey); err != nil || got != "Alpha" {
		t.Fatalf("provider.model = %q, %v; want Alpha", got, err)
	}
	if got, err := s.GetSetting(ProviderAPIKeyKey); err != nil || got != "sk-alpha" {
		t.Fatalf("provider.api_key = %q, %v", got, err)
	}
	if got, err := s.GetSetting(ProviderBaseURLKey); err != nil || got != "https://alpha.test" {
		t.Fatalf("provider.base_url = %q, %v", got, err)
	}

	second, err := s.CreateModel(Model{
		Name: "Beta", Model: "beta-1", BaseURL: "https://beta.test",
		APIKey: "sk-beta", ReasoningEffort: "High", ContextWindow: 1000000,
	})
	if err != nil {
		t.Fatalf("CreateModel(second): %v", err)
	}
	if second.Default {
		t.Fatal("second model must not steal the default")
	}

	// Every documented settings key exists for the row.
	for _, want := range []struct{ key, value string }{
		{modelKey(first.ID, "name"), "Alpha"},
		{modelKey(first.ID, "base_url"), "https://alpha.test"},
		{modelKey(first.ID, "api_key"), "sk-alpha"},
		{modelKey(second.ID, "name"), "Beta"},
		{modelKey(second.ID, "model"), "beta-1"},
		{modelKey(second.ID, "reasoning_effort"), "High"},
		{modelKey(second.ID, "context_window"), "1000000"},
	} {
		if got, err := s.GetSetting(want.key); err != nil || got != want.value {
			t.Errorf("setting %q = %q, %v; want %q", want.key, got, err, want.value)
		}
	}

	list, err := s.ListModels()
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(list) != 2 || list[0].Name != "Alpha" || list[1].Name != "Beta" {
		t.Fatalf("ListModels = %+v", list)
	}
	if !list[0].Default || list[1].Default {
		t.Fatalf("default flags = %v/%v", list[0].Default, list[1].Default)
	}

	// Update the second row and make it the default; provider.* follows it.
	second.Name = "Beta Pro"
	second.Model = "beta-2"
	second.ReasoningEffort = "Medium"
	second.ContextWindow = 200000
	updated, err := s.UpdateModel(*second)
	if err != nil {
		t.Fatalf("UpdateModel: %v", err)
	}
	if updated.Name != "Beta Pro" || updated.Model != "beta-2" || updated.ContextWindow != 200000 {
		t.Fatalf("UpdateModel = %+v", updated)
	}
	// Clearing the context window deletes its settings row instead of
	// persisting a zero.
	updated.ContextWindow = 0
	if _, err := s.UpdateModel(*updated); err != nil {
		t.Fatalf("UpdateModel(clear context): %v", err)
	}
	if _, err := s.GetSetting(modelKey(second.ID, "context_window")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("context_window row after clear = %v, want ErrNotFound", err)
	}
	if err := s.SetDefaultModel(second.ID); err != nil {
		t.Fatalf("SetDefaultModel: %v", err)
	}
	def, err := s.DefaultModel()
	if err != nil {
		t.Fatalf("DefaultModel: %v", err)
	}
	if def.ID != second.ID || !def.Default {
		t.Fatalf("DefaultModel = %+v", def)
	}
	if got, err := s.GetSetting(ProviderModelKey); err != nil || got != "beta-2" {
		t.Fatalf("provider.model = %q, %v; want beta-2", got, err)
	}
	if got, err := s.GetSetting(ProviderReasoningEffortKey); err != nil || got != "Medium" {
		t.Fatalf("provider.reasoning_effort = %q, %v; want Medium", got, err)
	}

	// Editing the default keeps provider.* in sync.
	def.Name = "Beta Max"
	def.Model = "beta-3"
	if _, err := s.UpdateModel(*def); err != nil {
		t.Fatalf("UpdateModel(default): %v", err)
	}
	if got, err := s.GetSetting(ProviderModelKey); err != nil || got != "beta-3" {
		t.Fatalf("provider.model after default edit = %q, %v; want beta-3", got, err)
	}

	// Deleting the default clears it and the provider settings.
	if err := s.DeleteModel(second.ID); err != nil {
		t.Fatalf("DeleteModel(default): %v", err)
	}
	if _, err := s.DefaultModelID(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DefaultModelID after delete = %v, want ErrNotFound", err)
	}
	if _, err := s.GetSetting(ProviderModelKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("provider.model after default delete = %v, want ErrNotFound", err)
	}
	if _, err := s.GetModel(second.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetModel(deleted) = %v, want ErrNotFound", err)
	}
	if list, err := s.ListModels(); err != nil || len(list) != 1 {
		t.Fatalf("ListModels after delete = %+v, %v", list, err)
	}
}

func TestModelValidationAndPersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if _, err := s.CreateModel(Model{}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty name error = %v, want ErrInvalidArgument", err)
	}
	if _, err := s.CreateModel(Model{ID: "bad.id", Name: "x"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid id error = %v, want ErrInvalidArgument", err)
	}
	created, err := s.CreateModel(Model{Name: "Persisted", Model: "p-1"})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if _, err := s.CreateModel(Model{ID: created.ID, Name: "dupe"}); err == nil {
		t.Fatal("CreateModel accepted a duplicate id")
	}
	if err := s.SetDefaultModel("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetDefaultModel(missing) = %v, want ErrNotFound", err)
	}
	if err := s.DeleteModel("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteModel(missing) = %v, want ErrNotFound", err)
	}
	if _, err := s.UpdateModel(Model{ID: "missing", Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateModel(missing) = %v, want ErrNotFound", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	list, err := reopened.ListModels()
	if err != nil {
		t.Fatalf("ListModels after reopen: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Persisted" || !list[0].Default {
		t.Fatalf("models after reopen = %+v", list)
	}
	if got, err := reopened.GetSetting(ModelDefaultKey); err != nil || got != created.ID {
		t.Fatalf("model.default after reopen = %q, %v", got, err)
	}
}

func TestModelListIgnoresUnrelatedSettings(t *testing.T) {
	s := openTestStore(t)
	created, err := s.CreateModel(Model{Name: "Only"})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	for key, value := range map[string]string{
		"ui.theme":          "dark",
		"models":            `[{"name":"json"}]`,
		"modelx.thing.name": "not a model",
	} {
		if err := s.SetSetting(key, value); err != nil {
			t.Fatalf("SetSetting(%q): %v", key, err)
		}
	}
	list, err := s.ListModels()
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("ListModels = %+v, want only %q", list, created.ID)
	}
	if strings.Contains(modelDetailForTest(list[0]), "json") {
		t.Fatalf("JSON models setting leaked into the list: %+v", list[0])
	}
}

// modelDetailForTest renders a model the way the shell's row does, to assert
// that unrelated settings do not leak into a row.
func modelDetailForTest(m Model) string {
	return m.Name + m.BaseURL + m.Model + m.ReasoningEffort
}

// TestModelContextWindowValidationAndTolerance pins the capacity column rules:
// negative values are rejected on write, while a hand-edited malformed or
// non-positive settings row reads as unknown instead of hiding the model.
func TestModelContextWindowValidationAndTolerance(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.CreateModel(Model{Name: "Negative", ContextWindow: -1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("CreateModel(negative context) = %v, want ErrInvalidArgument", err)
	}
	created, err := s.CreateModel(Model{Name: "Tolerant"})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if err := s.SetSetting(modelKey(created.ID, "context_window"), "not-a-number"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	got, err := s.GetModel(created.ID)
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if got.ContextWindow != 0 {
		t.Fatalf("malformed context_window read as %d, want 0", got.ContextWindow)
	}
	if err := s.SetSetting(modelKey(created.ID, "context_window"), "0"); err != nil {
		t.Fatalf("SetSetting(0): %v", err)
	}
	if got, err = s.GetModel(created.ID); err != nil || got.ContextWindow != 0 {
		t.Fatalf("zero context_window read as %d, %v; want 0", got.ContextWindow, err)
	}
	// A stored value survives the ListModels path too.
	if err := s.SetSetting(modelKey(created.ID, "context_window"), "1048576"); err != nil {
		t.Fatalf("SetSetting(1048576): %v", err)
	}
	list, err := s.ListModels()
	if err != nil || len(list) != 1 || list[0].ContextWindow != 1048576 {
		t.Fatalf("ListModels context window = %+v, %v", list, err)
	}
}
