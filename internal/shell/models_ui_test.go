package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// TestModelsSettingsCRUD drives Settings > Models with NewTester: add two
// models, mark the second default, edit and delete through the UI, and checks
// the store keys and the composer badge.
func TestModelsSettingsCRUD(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	tt := ui.NewTester(h.app.View, 1600, 1000)

	// Open Settings, then the Models page.
	tt.Key(ui.Cmd, ui.KeyComma)
	if err := tt.Click("Models"); err != nil {
		t.Fatalf("click Models nav: %v", err)
	}
	if !tt.HasText("Add model") {
		t.Fatalf("models page missing: %q", tt.Texts())
	}

	// Add the first model. Name is auto-focused; Tab walks the other fields.
	if err := tt.Click("Add model"); err != nil {
		t.Fatalf("click Add model: %v", err)
	}
	tt.Type("My Model")
	tt.Key(0, ui.KeyTab)
	tt.Type("https://alpha.test")
	tt.Key(0, ui.KeyTab)
	tt.Type("sk-alpha")
	tt.Key(0, ui.KeyTab)
	tt.Type("alpha-1")
	tt.Key(0, ui.KeyTab)
	tt.Type("High")
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("click Save: %v", err)
	}

	models, err := h.ctrl.Models().List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %+v, want one", models)
	}
	first := models[0]
	if first.Name != "My Model" || first.BaseURL != "https://alpha.test" ||
		first.APIKey != "sk-alpha" || first.Model != "alpha-1" || first.ReasoningEffort != "High" {
		t.Fatalf("stored model = %+v", first)
	}
	if !first.Default {
		t.Fatal("first model is not the default")
	}
	for key, want := range map[string]string{
		"model." + first.ID + ".name":             "My Model",
		"model." + first.ID + ".base_url":         "https://alpha.test",
		"model." + first.ID + ".api_key":          "sk-alpha",
		"model." + first.ID + ".model":            "alpha-1",
		"model." + first.ID + ".reasoning_effort": "High",
		"model.default":                           first.ID,
	} {
		if got, err := h.store.GetSetting(key); err != nil || got != want {
			t.Errorf("setting %q = %q, %v; want %q", key, got, err, want)
		}
	}
	if got := h.app.DefaultModelLabel(); got != "My Model" {
		t.Fatalf("DefaultModelLabel = %q, want My Model", got)
	}

	// The reusable badge renders the selected default's name and effort.
	badge := ui.NewTester(func(c *ui.Context) {
		h.app.ModelBadge(c, design.Light())
	}, 420, 60)
	if !badge.HasText("My Model") {
		t.Fatalf("model badge missing the default name: %q", badge.Texts())
	}
	if !badge.HasText("High") {
		t.Fatalf("model badge missing the reasoning effort: %q", badge.Texts())
	}

	// Add a second model; it must not steal the default.
	if err := tt.Click("Add model"); err != nil {
		t.Fatalf("click Add model (#2): %v", err)
	}
	tt.Type("Second")
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("click Save (#2): %v", err)
	}
	models, err = h.ctrl.Models().List()
	if err != nil || len(models) != 2 {
		t.Fatalf("models after second add = %+v, %v", models, err)
	}
	if models[0].Name != "My Model" || models[1].Name != "Second" {
		t.Fatalf("model order = %q, %q", models[0].Name, models[1].Name)
	}
	if models[1].Default {
		t.Fatal("second model stole the default")
	}

	// Mark the second model default through its row action.
	if err := tt.Click("Set default"); err != nil {
		t.Fatalf("click Set default: %v", err)
	}
	second := models[1]
	if id, err := h.ctrl.Models().DefaultID(); err != nil || id != second.ID {
		t.Fatalf("DefaultID = %q, %v; want %q", id, err, second.ID)
	}
	if got := h.app.DefaultModelLabel(); got != "Second" {
		t.Fatalf("DefaultModelLabel after default change = %q, want Second", got)
	}
	if got, err := h.store.GetSetting("provider.model"); err != nil || got != "Second" {
		t.Fatalf("provider.model = %q, %v; want Second", got, err)
	}

	// Edit the first model. AutoFocus puts the caret in its name field; set
	// the draft explicitly so the assertion is independent of caret position.
	if err := tt.Click("Edit"); err != nil {
		t.Fatalf("click Edit: %v", err)
	}
	if !tt.HasText("Edit model") {
		t.Fatalf("edit dialog missing: %q", tt.Texts())
	}
	h.app.shellUI().modelDraft.name = "Alpha Renamed"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("click Save (edit): %v", err)
	}
	edited, err := h.ctrl.Models().Get(first.ID)
	if err != nil {
		t.Fatalf("Get after edit: %v", err)
	}
	if edited.Name != "Alpha Renamed" {
		t.Fatalf("edited name = %q, want Alpha Renamed", edited.Name)
	}

	// Delete the edited model; the default (second) survives.
	if err := tt.Click("Delete"); err != nil {
		t.Fatalf("click Delete: %v", err)
	}
	models, err = h.ctrl.Models().List()
	if err != nil || len(models) != 1 || models[0].ID != second.ID {
		t.Fatalf("models after delete = %+v, %v", models, err)
	}
	if _, err := h.store.GetSetting("model." + first.ID + ".name"); err == nil {
		t.Fatal("deleted model's name key survived")
	}

	// Deleting the default clears the selection.
	if err := tt.Click("Delete"); err != nil {
		t.Fatalf("click Delete (default): %v", err)
	}
	if _, err := h.ctrl.Models().DefaultID(); err == nil {
		t.Fatal("default survived deleting the default model")
	}
	if got := h.app.DefaultModelLabel(); got != "No model" {
		t.Fatalf("DefaultModelLabel with no default = %q, want No model", got)
	}
}
