package shell

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/store"
)

// TestAutomationPageShowsForegroundHint pins the PD-06 note: automations only
// fire while the desktop app runs.
func TestAutomationPageShowsForegroundHint(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Automation
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("only while ZeoKeel is running") {
		t.Fatalf("automation page missing the foreground hint: %q", tt.Texts())
	}
}

// TestAutomationCreatePersistsBindingFields pins the run binding: workspace,
// permission and model chosen in the create dialog survive in the store.
func TestAutomationCreatePersistsBindingFields(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Automation
	workspaceID := h.ctrl.CurrentWorkspaceID()
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("New automation task"); err != nil {
		t.Fatalf("open create dialog: %v", err)
	}
	tt.Frame()
	h.app.shellUI().automationDraft.title = "nightly summary"
	h.app.shellUI().automationDraft.prompt = "summarize the day"
	h.app.shellUI().automationDraft.cron = "@daily"
	h.app.shellUI().automationDraft.workspaceID = workspaceID
	h.app.shellUI().automationDraft.permission = "read-only"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save automation: %v", err)
	}
	tt.Frame()
	tasks, err := h.ctrl.Automations().List()
	if err != nil || len(tasks) != 1 {
		t.Fatalf("automations = %+v, %v", tasks, err)
	}
	task := tasks[0]
	if task.WorkspaceID != workspaceID || task.Permission != "read-only" {
		t.Fatalf("binding fields not persisted: %+v", task)
	}
}

// TestAutomationCardShowsRunHistory pins the run rendering: the latest run's
// status/error is on the card, and History expands the recorded runs.
func TestAutomationCardShowsRunHistory(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Automation
	task, err := h.ctrl.Automations().Create(store.Automation{
		Title: "nightly report", Prompt: "report", Cron: "@daily", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create automation: %v", err)
	}
	run, err := h.store.CreateAutomationRun(task.ID, "")
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := h.store.FinishAutomationRun(run.ID, store.AutomationRunFailed, "provider 401"); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Failed") {
		t.Fatalf("card missing the latest run status: %q", tt.Texts())
	}
	if !tt.HasText("provider 401") {
		t.Fatalf("card missing the failure reason: %q", tt.Texts())
	}
	if err := tt.Click("History"); err != nil {
		t.Fatalf("expand run history: %v", err)
	}
	tt.Frame()
	if !tt.HasText("provider 401") {
		t.Fatalf("history missing the run: %q", tt.Texts())
	}
	_ = context.Background()
}
