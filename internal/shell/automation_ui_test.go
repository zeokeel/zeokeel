package shell

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/store"
)

// TestAutomationTasksCRUDAndRunNow drives the Automation screen end to end:
// create through the empty state, run now, disable/enable, edit and delete,
// with the store asserted after every step.
func TestAutomationTasksCRUDAndRunNow(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	var ran []string
	h.app.SetAutomationRunNow(func(id string) { ran = append(ran, id) })

	tt := ui.NewTester(h.app.View, 1600, 1000)
	if err := tt.Click("Automation tasks"); err != nil {
		t.Fatalf("click Automation tasks: %v", err)
	}
	if !tt.HasText("No tasks yet. Tasks created in your sessions appear here.") {
		t.Fatalf("empty state missing: %q", tt.Texts())
	}

	// Create through the empty state's outline action.
	if err := tt.Click("New automation task"); err != nil {
		t.Fatalf("click New automation task: %v", err)
	}
	tt.Type("Daily report")
	tt.Key(0, ui.KeyTab)
	tt.Type("Summarize yesterday's changes")
	tt.Key(0, ui.KeyTab)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("*/5 * * * *")
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("click Save: %v", err)
	}

	tasks, err := h.ctrl.Automations().List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v, want one", tasks)
	}
	task := tasks[0]
	if task.Title != "Daily report" || task.Prompt != "Summarize yesterday's changes" ||
		task.Cron != "*/5 * * * *" || !task.Enabled {
		t.Fatalf("stored task = %+v", task)
	}
	if task.NextRun == nil || !task.NextRun.After(time.Now().Add(-time.Minute)) {
		t.Fatalf("NextRun = %v, want a future schedule", task.NextRun)
	}
	if !tt.HasText("Daily report") || !tt.HasText("Every 5 minutes") {
		t.Fatalf("task row missing: %q", tt.Texts())
	}

	// Run now goes through the app's hook.
	if err := tt.Click("Run now"); err != nil {
		t.Fatalf("click Run now: %v", err)
	}
	if len(ran) != 1 || ran[0] != task.ID {
		t.Fatalf("RunNow calls = %v, want [%s]", ran, task.ID)
	}
	if !tt.HasText("Automation started") {
		t.Fatalf("run toast missing: %q", tt.Texts())
	}

	// Disable and enable through the row actions. A disabled task must not
	// keep (or render) a next run that can never fire.
	if err := tt.Click("Disable"); err != nil {
		t.Fatalf("click Disable: %v", err)
	}
	disabled, err := h.ctrl.Automations().Get(task.ID)
	if err != nil || disabled.Enabled {
		t.Fatalf("task after Disable = %+v, %v", disabled, err)
	}
	if disabled.NextRun != nil {
		t.Fatalf("disabled task NextRun = %v, want nil", disabled.NextRun)
	}
	if detail := h.app.automationTaskDetail(*disabled); strings.Contains(detail, "next ") {
		t.Fatalf("disabled task detail still shows a next run: %q", detail)
	}
	if !tt.HasText("Inactive") {
		t.Fatalf("inactive badge missing: %q", tt.Texts())
	}
	if err := tt.Click("Enable"); err != nil {
		t.Fatalf("click Enable: %v", err)
	}
	enabled, err := h.ctrl.Automations().Get(task.ID)
	if err != nil || !enabled.Enabled {
		t.Fatalf("task after Enable = %+v, %v", enabled, err)
	}
	if enabled.NextRun == nil {
		t.Fatal("enabled task has no NextRun")
	}

	// Edit keeps the row identity and updates the fields.
	if err := tt.Click("Edit"); err != nil {
		t.Fatalf("click Edit: %v", err)
	}
	if !tt.HasText("Edit automation task") {
		t.Fatalf("edit dialog missing: %q", tt.Texts())
	}
	h.app.shellUI().automationDraft.title = "Daily report v2"
	h.app.shellUI().automationDraft.prompt = "Summarize today"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("click Save (edit): %v", err)
	}
	edited, err := h.ctrl.Automations().Get(task.ID)
	if err != nil {
		t.Fatalf("Get after edit: %v", err)
	}
	if edited.Title != "Daily report v2" || edited.Prompt != "Summarize today" {
		t.Fatalf("edited task = %+v", edited)
	}
	if len(edited.ID) != len(task.ID) {
		t.Fatalf("edit changed the id: %q -> %q", task.ID, edited.ID)
	}

	// Delete removes the task and returns to the empty state.
	if err := tt.Click("Delete"); err != nil {
		t.Fatalf("click Delete: %v", err)
	}
	if tasks, err := h.ctrl.Automations().List(); err != nil || len(tasks) != 0 {
		t.Fatalf("tasks after delete = %+v, %v", tasks, err)
	}
	if !tt.HasText("No tasks yet. Tasks created in your sessions appear here.") {
		t.Fatalf("empty state missing after delete: %q", tt.Texts())
	}
}

// TestAutomationFiltersAndSearch covers the All / Enabled / Inactive tabs and
// the search field over real tasks.
func TestAutomationFiltersAndSearch(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	if _, err := h.ctrl.Automations().Create(store.Automation{
		Title: "Enabled task", Cron: "@hourly", Enabled: true,
	}); err != nil {
		t.Fatalf("Create enabled: %v", err)
	}
	if _, err := h.ctrl.Automations().Create(store.Automation{
		Title: "Inactive task", Cron: "@daily", Enabled: false,
	}); err != nil {
		t.Fatalf("Create inactive: %v", err)
	}

	tt := ui.NewTester(h.app.View, 1600, 1000)
	if err := tt.Click("Automation tasks"); err != nil {
		t.Fatalf("click Automation tasks: %v", err)
	}
	if !tt.HasText("Enabled task") || !tt.HasText("Inactive task") {
		t.Fatalf("tasks missing on All: %q", tt.Texts())
	}
	if err := tt.Click("Enabled"); err != nil {
		t.Fatalf("click Enabled tab: %v", err)
	}
	if !tt.HasText("Enabled task") || tt.HasText("Inactive task") {
		t.Fatalf("Enabled filter = %q", tt.Texts())
	}
	if err := tt.Click("Inactive"); err != nil {
		t.Fatalf("click Inactive tab: %v", err)
	}
	if tt.HasText("Enabled task") || !tt.HasText("Inactive task") {
		t.Fatalf("Inactive filter = %q", tt.Texts())
	}

	// Search matches the title; typing reaches the live query state. Switch
	// back to All first so the filter does not hide the match.
	if err := tt.Click("All"); err != nil {
		t.Fatalf("click All tab: %v", err)
	}
	tt.Click("Search automation tasks")
	tt.Type("enabled")
	if got := h.app.shellUI().automationQuery; got != "enabled" {
		t.Fatalf("search query = %q, want enabled", got)
	}
	if !tt.HasText("Enabled task") || tt.HasText("Inactive task") {
		t.Fatalf("search result = %q", tt.Texts())
	}
}

// TestAutomationRunNowWithoutHook checks the UI reports the missing runner
// rather than silently doing nothing.
func TestAutomationRunNowWithoutHook(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	if _, err := h.ctrl.Automations().Create(store.Automation{
		Title: "No runner", Cron: "@daily", Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Click("Automation tasks")
	if err := tt.Click("Run now"); err != nil {
		t.Fatalf("click Run now: %v", err)
	}
	if !tt.HasText("No automation runner is configured") {
		t.Fatalf("missing-runner toast missing: %q", tt.Texts())
	}
}
