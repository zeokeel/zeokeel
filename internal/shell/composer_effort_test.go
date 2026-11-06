package shell

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/store"
)

// TestComposerReasoningEffortMenuApplies pins the composer's reasoning-strength
// control: the model chip opens a menu listing the selected model's published
// levels, choosing one persists the session's own choice (on top of the menu
// closing), "Use model default" clears it, and a model without published levels
// offers no empty control.
func TestComposerReasoningEffortMenuApplies(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	model, err := h.ctrl.Models().Create(store.Model{
		Name: "DeepSeek V4.1 Flash", Model: "deepseek-flash",
		BaseURL: "https://api.deepseek.com", ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	// The chip shows the effective (model default) strength.
	if !tt.HasText("high") {
		t.Fatalf("badge does not show the effective reasoning effort: %v", tt.Texts())
	}
	if err := tt.Click(modelChipLabel(h, model)); err != nil {
		t.Fatalf("open model menu: %v", err)
	}
	tt.Frame()
	nodes := testerAccessNodes(t, tt)
	for _, label := range []string{"Use model default", "Off", "Low", "High", "Max"} {
		assertMenuRow(t, nodes, accessRoleMenuItemRadio, label, true)
	}
	if states := assertMenuRow(t, nodes, accessRoleMenuItemRadio, "High", true); states&accessCheckedState == 0 {
		t.Fatal("the model's default level is not exposed as checked")
	}

	// Choosing Max writes only the session's choice and closes the menu.
	if err := tt.Click("Max"); err != nil {
		t.Fatalf("click Max: %v", err)
	}
	tt.Frame()
	if h.app.liveMenus().modelOpen {
		t.Fatal("the model menu stayed open after choosing a level")
	}
	id := h.ctrl.CurrentSessionID()
	if got := h.ctrl.SessionReasoningEffort(id); got != "max" {
		t.Fatalf("session reasoning effort = %q, want max", got)
	}
	if !tt.HasText("max") {
		t.Fatalf("badge does not show the chosen level: %v", tt.Texts())
	}

	// Re-opening and choosing "Use model default" clears the explicit choice.
	label := "Select model, current " + model.Name + ", reasoning effort max"
	if err := tt.Click(label); err != nil {
		t.Fatalf("open model menu (#2): %v", err)
	}
	tt.Frame()
	if err := tt.Click("Use model default"); err != nil {
		t.Fatalf("click Model default: %v", err)
	}
	tt.Frame()
	if got := h.ctrl.SessionReasoningEffort(id); got != "" {
		t.Fatalf("session reasoning effort after clear = %q, want empty", got)
	}
	if !tt.HasText("high") {
		t.Fatalf("badge did not fall back to the model's default level: %v", tt.Texts())
	}

	// A model whose entry publishes no levels (OpenAI GPT-4.1) hides the
	// section instead of offering a control that cannot do anything.
	plain, err := h.ctrl.Models().Create(store.Model{
		Name: "GPT-4.1", Model: "gpt-4.1", BaseURL: "https://api.openai.com/v1",
	})
	if err != nil {
		t.Fatalf("create plain model: %v", err)
	}
	if err := h.ctrl.SetSessionModel(id, plain.ID); err != nil {
		t.Fatalf("SetSessionModel: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Select model, current GPT-4.1"); err != nil {
		t.Fatalf("open model menu (#3): %v", err)
	}
	tt.Frame()
	if tt.HasText("Use model default") || tt.HasText("Reasoning effort") {
		t.Fatalf("non-reasoning model still offers an effort section: %v", tt.Texts())
	}
}

// TestHomeComposerReasoningEffortStagesForNextSession pins the Home composer's
// staged strength: choosing a level before the session exists records it on the
// next session the Home composer creates and never touches the global default.
func TestHomeComposerReasoningEffortStagesForNextSession(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	model, err := h.ctrl.Models().Create(store.Model{
		Name: "DeepSeek V4.1 Flash", Model: "deepseek-flash",
		BaseURL: "https://api.deepseek.com", ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click(modelChipLabel(h, model)); err != nil {
		t.Fatalf("open Home model menu: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Max"); err != nil {
		t.Fatalf("click Max: %v", err)
	}
	tt.Frame()
	if got := h.app.homeEffort; got != "max" {
		t.Fatalf("staged Home effort = %q, want max", got)
	}
	if got := h.ctrl.SessionReasoningEffort(h.ctrl.CurrentSessionID()); got != "" {
		t.Fatalf("Home staging wrote the last session's effort: %q", got)
	}

	// The staged choice lands on the session the Home composer creates.
	id, err := h.ctrl.CreateSession(context.Background(), "home staged")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	h.app.applyHomeComposerChoices(id)
	if got := h.ctrl.SessionReasoningEffort(id); got != "max" {
		t.Fatalf("new session effort = %q, want the staged max", got)
	}
	// The global default model keeps its own configured effort.
	stored, err := h.ctrl.Models().Get(model.ID)
	if err != nil || stored.ReasoningEffort != "high" {
		t.Fatalf("stored model effort = %+v, %v; want untouched high", stored, err)
	}
}
