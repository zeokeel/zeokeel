package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// mustCreateAutomation creates one automation task for the run tests.
func mustCreateAutomation(t *testing.T, s *Store, title string) *Automation {
	t.Helper()
	a, err := s.CreateAutomation(Automation{Title: title, Prompt: "do the task", Cron: "@daily", Enabled: true})
	if err != nil {
		t.Fatalf("CreateAutomation(%q): %v", title, err)
	}
	return a
}

// TestAutomationBindingFieldsRoundTrip covers the PD-06 columns: workspace,
// access mode, model and origin session survive create/get/list/update.
func TestAutomationBindingFieldsRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "bound", t.TempDir())
	origin, err := s.CreateSession(ws.ID, "origin session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	created, err := s.CreateAutomation(Automation{
		Title:           "bound task",
		Prompt:          "summarize",
		Cron:            "0 * * * *",
		Enabled:         true,
		WorkspaceID:     ws.ID,
		Permission:      "read-only",
		Model:           "model-1",
		OriginSessionID: origin.ID,
	})
	if err != nil {
		t.Fatalf("CreateAutomation: %v", err)
	}
	loaded, err := s.GetAutomation(created.ID)
	if err != nil {
		t.Fatalf("GetAutomation: %v", err)
	}
	if loaded.WorkspaceID != ws.ID || loaded.Permission != "read-only" || loaded.Model != "model-1" || loaded.OriginSessionID != origin.ID {
		t.Fatalf("binding fields = %+v, want workspace/permission/model/origin", loaded)
	}
	list, err := s.ListAutomations()
	if err != nil || len(list) != 1 {
		t.Fatalf("ListAutomations = %v, %v", list, err)
	}
	if list[0].WorkspaceID != ws.ID || list[0].Model != "model-1" {
		t.Fatalf("listed binding fields = %+v", list[0])
	}

	loaded.Permission = "full"
	loaded.Model = "model-2"
	updated, err := s.UpdateAutomation(*loaded)
	if err != nil {
		t.Fatalf("UpdateAutomation: %v", err)
	}
	if updated.Permission != "full" || updated.Model != "model-2" {
		t.Fatalf("updated binding fields = %+v", updated)
	}
	reloaded, err := s.GetAutomation(created.ID)
	if err != nil || reloaded.Permission != "full" || reloaded.Model != "model-2" || reloaded.WorkspaceID != ws.ID {
		t.Fatalf("reloaded binding fields = %+v, %v", reloaded, err)
	}
}

// TestAutomationRunLifecycle covers the run history API: a run is created as
// running, finished once, listed newest-first and looked up as latest.
func TestAutomationRunLifecycle(t *testing.T) {
	s := openTestStore(t)
	task := mustCreateAutomation(t, s, "history task")
	ws := mustWorkspace(t, s, "runs", t.TempDir())
	sess, err := s.CreateSession(ws.ID, "run session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	run, err := s.CreateAutomationRun(task.ID, sess.ID)
	if err != nil {
		t.Fatalf("CreateAutomationRun: %v", err)
	}
	if run.Status != AutomationRunRunning || run.FinishedAt != nil || run.SessionID != sess.ID {
		t.Fatalf("new run = %+v, want running with session and no finish time", run)
	}
	if latest, err := s.LatestAutomationRun(task.ID); err != nil || latest.ID != run.ID {
		t.Fatalf("LatestAutomationRun = %+v, %v", latest, err)
	}

	finished, err := s.FinishAutomationRun(run.ID, AutomationRunFailed, "model request failed")
	if err != nil {
		t.Fatalf("FinishAutomationRun: %v", err)
	}
	if finished.Status != AutomationRunFailed || finished.Error != "model request failed" || finished.FinishedAt == nil {
		t.Fatalf("finished run = %+v, want failed with error and finish time", finished)
	}
	// A second finish does not rewrite the first terminal result (idempotent
	// shutdown/retry paths must not flip a completed run to failed).
	again, err := s.FinishAutomationRun(run.ID, AutomationRunCompleted, "")
	if err != nil {
		t.Fatalf("second FinishAutomationRun: %v", err)
	}
	if again.Status != AutomationRunFailed || again.Error != "model request failed" {
		t.Fatalf("second finish rewrote history: %+v", again)
	}

	// A newer run sorts first.
	second, err := s.CreateAutomationRun(task.ID, "")
	if err != nil {
		t.Fatalf("second CreateAutomationRun: %v", err)
	}
	if _, err := s.FinishAutomationRun(second.ID, AutomationRunCompleted, ""); err != nil {
		t.Fatalf("finish second run: %v", err)
	}
	runs, err := s.ListAutomationRuns(task.ID, 10)
	if err != nil {
		t.Fatalf("ListAutomationRuns: %v", err)
	}
	if len(runs) != 2 || runs[0].ID != second.ID {
		t.Fatalf("runs = %+v, want the newest run first", runs)
	}
	if latest, err := s.LatestAutomationRun(task.ID); err != nil || latest.ID != second.ID {
		t.Fatalf("latest after second run = %+v, %v", latest, err)
	}
}

// TestAutomationRunValidation covers the argument/status errors.
func TestAutomationRunValidation(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.CreateAutomationRun("", ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("CreateAutomationRun empty id = %v", err)
	}
	task := mustCreateAutomation(t, s, "validation task")
	if _, err := s.FinishAutomationRun("missing", AutomationRunCompleted, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FinishAutomationRun missing = %v", err)
	}
	run, err := s.CreateAutomationRun(task.ID, "")
	if err != nil {
		t.Fatalf("CreateAutomationRun: %v", err)
	}
	if _, err := s.FinishAutomationRun(run.ID, "bogus", ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("FinishAutomationRun bogus status = %v", err)
	}
	if _, err := s.LatestAutomationRun(task.ID); err != nil {
		t.Fatalf("LatestAutomationRun: %v", err)
	}
	if _, err := s.LatestAutomationRun(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("LatestAutomationRun empty id = %v", err)
	}
}

// TestAutomationDeleteCascadesRuns pins the FK: deleting the task removes its
// run history instead of leaving orphan rows.
func TestAutomationDeleteCascadesRuns(t *testing.T) {
	s := openTestStore(t)
	task := mustCreateAutomation(t, s, "cascade task")
	if _, err := s.CreateAutomationRun(task.ID, ""); err != nil {
		t.Fatalf("CreateAutomationRun: %v", err)
	}
	if err := s.DeleteAutomation(task.ID); err != nil {
		t.Fatalf("DeleteAutomation: %v", err)
	}
	runs, err := s.ListAutomationRuns(task.ID, 10)
	if err != nil {
		t.Fatalf("ListAutomationRuns after delete: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("run history survived the task delete: %+v", runs)
	}
}

// TestLegacyAutomationsTableMigrates simulates an index created before PD-06
// (old automations columns, no automation_runs table): reopening the store must
// add the columns and the table without losing existing rows.
func TestLegacyAutomationsTableMigrates(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Rewrite the automations table to the pre-PD-06 shape and drop the new
	// table, exactly like an index created by wave-1.
	raw, err := sql.Open("sqlite", dsn(filepath.Join(dir, dbFileName)))
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	ctx := context.Background()
	if _, err := raw.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatalf("disable foreign keys: %v", err)
	}
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS automation_runs`,
		`DROP TABLE IF EXISTS automations`,
		`CREATE TABLE automations (
			id         TEXT PRIMARY KEY,
			title      TEXT NOT NULL,
			prompt     TEXT NOT NULL,
			cron       TEXT NOT NULL,
			enabled    INTEGER NOT NULL,
			last_run   TEXT,
			next_run   TEXT,
			created_at TEXT NOT NULL
		)`,
		`INSERT INTO automations(id, title, prompt, cron, enabled, last_run, next_run, created_at)
		 VALUES ('legacy-task', 'Legacy', 'old prompt', '@daily', 1, NULL, NULL, '2026-01-01T00:00:00Z')`,
	} {
		if _, err := raw.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("prepare legacy index (%s): %v", stmt, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw sqlite: %v", err)
	}

	// Reopen: the additions create automation_runs and the column migration
	// adds the binding columns; the legacy row keeps its data and reads empty
	// binding fields.
	migrated, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen migrated store: %v", err)
	}
	defer migrated.Close() //nolint:errcheck // test cleanup
	legacy, err := migrated.GetAutomation("legacy-task")
	if err != nil {
		t.Fatalf("GetAutomation(legacy): %v", err)
	}
	if legacy.Title != "Legacy" || legacy.Prompt != "old prompt" {
		t.Fatalf("legacy row = %+v, want original data preserved", legacy)
	}
	if legacy.WorkspaceID != "" || legacy.Permission != "" || legacy.Model != "" || legacy.OriginSessionID != "" {
		t.Fatalf("legacy binding fields = %+v, want empty defaults", legacy)
	}
	if _, err := migrated.CreateAutomationRun(legacy.ID, ""); err != nil {
		t.Fatalf("CreateAutomationRun on migrated schema: %v", err)
	}

	// A fresh database (new install) also has every PD-06 column and table.
	fresh, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open fresh store: %v", err)
	}
	defer fresh.Close() //nolint:errcheck // test cleanup
	task := mustCreateAutomation(t, fresh, "fresh task")
	if task.WorkspaceID != "" || task.Model != "" {
		t.Fatalf("fresh automation binding fields = %+v", task)
	}
	if _, err := fresh.CreateAutomationRun(task.ID, ""); err != nil {
		t.Fatalf("fresh CreateAutomationRun: %v", err)
	}
}
