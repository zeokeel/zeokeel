package shell

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/modelcatalog"
	"github.com/zeokeel/zeokeel/internal/store"
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

	// Add the first model. The dialog opens on the built-in DeepSeek
	// configuration, so every prefilled field is selected before typing; the
	// Name field is auto-focused. Tab walks the other fields.
	if err := tt.Click("Add model"); err != nil {
		t.Fatalf("click Add model: %v", err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("My Model")
	tt.Key(0, ui.KeyTab)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("https://alpha.test")
	tt.Key(0, ui.KeyTab)
	tt.Type("sk-alpha")
	tt.Key(0, ui.KeyTab)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("alpha-1")
	tt.Key(0, ui.KeyTab)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("200000")
	tt.Key(0, ui.KeyTab)
	tt.Key(ui.Cmd, ui.KeyA)
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
		first.APIKey != "sk-alpha" || first.Model != "alpha-1" ||
		first.ReasoningEffort != "High" || first.ContextWindow != 200000 {
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
		"model." + first.ID + ".context_window":   "200000",
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
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Second")
	// Leave this row's wire fields empty so it exercises the name fallback
	// (the dialog opened with the built-in DeepSeek configuration).
	tt.Key(0, ui.KeyTab)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	tt.Key(0, ui.KeyTab)
	tt.Key(0, ui.KeyTab)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
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

	// Delete the edited model; the confirmation must be accepted first (PD-18)
	// and cancelling it must leave the store untouched.
	if err := tt.Click("Delete"); err != nil {
		t.Fatalf("click Delete: %v", err)
	}
	if !tt.HasText("Delete model?") {
		t.Fatalf("delete confirmation missing: %q", tt.Texts())
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("cancel delete: %v", err)
	}
	if models, err = h.ctrl.Models().List(); err != nil || len(models) != 2 {
		t.Fatalf("cancelling the delete changed the models: %+v, %v", models, err)
	}
	if err := tt.Click("Delete"); err != nil {
		t.Fatalf("click Delete (#2): %v", err)
	}
	if err := tt.Click("Delete model"); err != nil {
		t.Fatalf("confirm Delete: %v", err)
	}
	models, err = h.ctrl.Models().List()
	if err != nil || len(models) != 1 || models[0].ID != second.ID {
		t.Fatalf("models after delete = %+v, %v", models, err)
	}
	if _, err := h.store.GetSetting("model." + first.ID + ".name"); err == nil {
		t.Fatal("deleted model's name key survived")
	}

	// Deleting the default clears the selection, also through the
	// confirmation.
	if err := tt.Click("Delete"); err != nil {
		t.Fatalf("click Delete (default): %v", err)
	}
	if err := tt.Click("Delete model"); err != nil {
		t.Fatalf("confirm Delete (default): %v", err)
	}
	if _, err := h.ctrl.Models().DefaultID(); err == nil {
		t.Fatal("default survived deleting the default model")
	}
	if got := h.app.DefaultModelLabel(); got != "No model" {
		t.Fatalf("DefaultModelLabel with no default = %q, want No model", got)
	}
}

// TestModelProviderPresetsAndConnection pins PD-02: the Add model dialog
// offers provider presets that fill Base URL and Model, and a "Test
// connection" probe with a local validation state and an async outcome. The
// probe is stubbed so the test never touches a network.
func TestModelProviderPresetsAndConnection(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Key(ui.Cmd, ui.KeyComma)
	if err := tt.Click("Models"); err != nil {
		t.Fatalf("click Models nav: %v", err)
	}
	if err := tt.Click("Add model"); err != nil {
		t.Fatalf("click Add model: %v", err)
	}
	if !tt.HasText("Provider") {
		t.Fatalf("model dialog has no provider row: %q", tt.Texts())
	}
	if !tt.HasText("Test connection") {
		t.Fatalf("model dialog has no connection test: %q", tt.Texts())
	}

	// Validation runs before any request: empty Base URL/Model reports what is
	// missing instead of probing "". The dialog opens preconfigured, so clear
	// the live draft (the button and handler read it directly).
	called := make(chan struct{}, 1)
	oldProbe := probeModelConnection
	probeModelConnection = func(context.Context, string, string) error {
		called <- struct{}{}
		return nil
	}
	t.Cleanup(func() { probeModelConnection = oldProbe })
	live := h.app.shellUI()
	live.modelDraft.baseURL = ""
	live.modelDraft.model = ""
	if err := tt.Click("Test connection"); err != nil {
		t.Fatalf("click Test connection: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Fill in Base URL and Model") {
		t.Fatalf("connection test without fields did not validate: %q", tt.Texts())
	}
	select {
	case <-called:
		t.Fatal("connection test probed an empty endpoint")
	default:
	}

	// Picking a built-in provider fills the endpoint and its first published
	// model, including that model's context window and default effort.
	openai, ok := modelcatalog.LookupProvider("openai")
	if !ok {
		t.Fatal("catalog has no OpenAI provider")
	}
	h.app.applyProviderPreset(openai)
	draft := h.app.shellUI().modelDraft
	if draft.baseURL != "https://api.openai.com/v1" || draft.model != "gpt-4.1" {
		t.Fatalf("preset did not fill the draft: %+v", draft)
	}
	if draft.contextWindow != "1047576" || draft.presetModel != "gpt-4.1" {
		t.Fatalf("preset did not fill the model metadata: %+v", draft)
	}
	h.app.shellUI().modelDraft.apiKey = "sk-test"
	tt.Frame()

	// The stub probe reports success, and the dialog paints it.
	if err := tt.Click("Test connection"); err != nil {
		t.Fatalf("click Test connection (#2): %v", err)
	}
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("connection probe did not run")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := h.app.currentModelProbe(); got.message == "Connection OK" {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	tt.Frame()
	if !tt.HasText("Connection OK") {
		t.Fatalf("connection success not painted: %q", tt.Texts())
	}

	// A failing endpoint paints the failure message and the error text.
	probeModelConnection = func(context.Context, string, string) error {
		return errors.New("HTTP 401 Unauthorized")
	}
	if err := tt.Click("Test connection"); err != nil {
		t.Fatalf("click Test connection (#3): %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := h.app.currentModelProbe(); strings.Contains(got.message, "401") {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	tt.Frame()
	if !tt.HasText("Connection failed") || !tt.HasText("HTTP 401 Unauthorized") {
		t.Fatalf("connection failure not painted: %q", tt.Texts())
	}
}

// TestModelPresetMatching keeps the preset row honest when editing a saved
// model: a known Base URL selects its preset, anything else is Custom.
func TestModelPresetMatching(t *testing.T) {
	cases := map[string]string{
		"https://api.deepseek.com":     "deepseek",
		"https://api.deepseek.com/v1":  "custom",
		"https://api.openai.com/v1/":   "openai",
		"http://localhost:11434/v1":    "ollama",
		"https://gateway.example.test": "custom",
	}
	for baseURL, want := range cases {
		if got := presetForBaseURL(baseURL); got != want {
			t.Errorf("presetForBaseURL(%q) = %q, want %q", baseURL, got, want)
		}
	}
}

// TestModelDialogBuiltInCatalogPrefillAndValidation pins the framework-parity
// add flow: choosing DeepSeek fills the endpoint, its default model, the 1M
// context window and that model's default strength, so the user only pastes a
// key; invalid capacities and unpublished levels are rejected before saving.
func TestModelDialogBuiltInCatalogPrefillAndValidation(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Key(ui.Cmd, ui.KeyComma)
	if err := tt.Click("Models"); err != nil {
		t.Fatalf("click Models nav: %v", err)
	}
	if err := tt.Click("Add model"); err != nil {
		t.Fatalf("click Add model: %v", err)
	}
	tt.Frame()
	// The dialog opens on the built-in DeepSeek configuration: only the API
	// key is missing, and the built-in model row is visible.
	if !tt.HasText("Built-in model") {
		t.Fatalf("built-in model row missing on the default DeepSeek setup: %v", tt.Texts())
	}
	if draft := h.app.shellUI().modelDraft; draft.preset != "deepseek" ||
		draft.name != "DeepSeek V4.1 Flash" || draft.baseURL != "https://api.deepseek.com" ||
		draft.model != "deepseek-flash" || draft.contextWindow != "1000000" || draft.effort != "high" {
		t.Fatalf("default add draft = %+v", draft)
	}
	// The custom endpoint leaves the fields alone and hides the built-in model
	// row (the user manages the endpoint by hand).
	h.app.shellUI().modelDraft = modelDraft{preset: customProviderID}
	tt.Frame()
	if tt.HasText("Built-in model") {
		t.Fatalf("custom endpoint shows a built-in model row: %v", tt.Texts())
	}
	if draft := h.app.shellUI().modelDraft; draft.preset != customProviderID || draft.presetModel != "" {
		t.Fatalf("custom draft = %+v", draft)
	}

	// Choosing DeepSeek prefills the whole built-in configuration.
	deepseek, ok := modelcatalog.LookupProvider("deepseek")
	if !ok {
		t.Fatal("catalog has no DeepSeek provider")
	}
	h.app.applyProviderPreset(deepseek)
	tt.Frame()
	if !tt.HasText("Built-in model") {
		t.Fatalf("built-in model row missing after choosing DeepSeek: %v", tt.Texts())
	}
	draft := h.app.shellUI().modelDraft
	if draft.name != "DeepSeek V4.1 Flash" || draft.baseURL != "https://api.deepseek.com" ||
		draft.model != "deepseek-flash" || draft.contextWindow != "1000000" ||
		draft.effort != "high" || draft.presetModel != "deepseek-flash" {
		t.Fatalf("DeepSeek preset = %+v", draft)
	}

	// Save validation is covered directly (typing into every non-focused
	// field would only re-test the native text input): a bad capacity, an
	// unpublished level and a missing name each block the row.
	invalid := draft
	invalid.contextWindow = "many"
	if _, key := modelFromDraft(invalid); key != "models.invalid_context_window" {
		t.Fatalf("bad capacity validation key = %q", key)
	}
	invalid = draft
	invalid.effort = "medium"
	if _, key := modelFromDraft(invalid); key != "models.unsupported_reasoning_effort" {
		t.Fatalf("unpublished effort validation key = %q", key)
	}
	invalid = draft
	invalid.name = ""
	if _, key := modelFromDraft(invalid); key != "models.model_name_is_required" {
		t.Fatalf("missing name validation key = %q", key)
	}
	if valid, key := modelFromDraft(draft); key != "" || valid.ContextWindow != 1000000 {
		t.Fatalf("valid draft = %+v, key %q", valid, key)
	}

	// A published level saves the complete built-in configuration. The live
	// draft is mutated directly (a frame would re-sync the bound text inputs).
	live := h.app.shellUI()
	live.modelDraft.effort = "max"
	live.modelDraft.apiKey = "sk-deepseek"
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("click Save: %v", err)
	}
	models, err := h.ctrl.Models().List()
	if err != nil || len(models) != 1 {
		t.Fatalf("models after save = %+v, %v", models, err)
	}
	saved := models[0]
	if saved.Model != "deepseek-flash" || saved.BaseURL != "https://api.deepseek.com" ||
		saved.ContextWindow != 1000000 || saved.ReasoningEffort != "max" || saved.APIKey != "sk-deepseek" {
		t.Fatalf("saved model = %+v", saved)
	}
}

// TestBeginModelEditRestoresCatalogMetadata keeps the edit dialog honest: the
// provider, built-in model choice and context window are re-selected from the
// saved model instead of showing Custom/empty again.
func TestBeginModelEditRestoresCatalogMetadata(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	saved, err := h.ctrl.Models().Create(store.Model{
		Name: "DeepSeek V4 Pro", Model: "deepseek-v4-pro",
		BaseURL: "https://api.deepseek.com", ReasoningEffort: "high",
		ContextWindow: 1000000,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	h.app.beginModelEdit(*saved)
	draft := h.app.shellUI().modelDraft
	if draft.preset != "deepseek" || draft.presetModel != "deepseek-v4-pro" ||
		draft.contextWindow != "1000000" || draft.effort != "high" {
		t.Fatalf("edit draft = %+v", draft)
	}
}
