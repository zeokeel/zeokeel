package store

import (
	"errors"
	"testing"
	"time"
)

func TestAutomationCRUD(t *testing.T) {
	s := openTestStore(t)

	next := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	created, err := s.CreateAutomation(Automation{
		Title:   "  Morning report  ",
		Prompt:  "Summarize yesterday",
		Cron:    "0 9 * * *",
		Enabled: true,
		NextRun: &next,
	})
	if err != nil {
		t.Fatalf("CreateAutomation: %v", err)
	}
	if created.ID == "" {
		t.Fatal("CreateAutomation generated no id")
	}
	if created.Title != "Morning report" {
		t.Fatalf("title = %q, want trimmed", created.Title)
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("CreateAutomation left CreatedAt zero")
	}
	if created.LastRun != nil {
		t.Fatalf("LastRun = %v, want nil", created.LastRun)
	}
	if created.NextRun == nil || !created.NextRun.Equal(next) {
		t.Fatalf("NextRun = %v, want %v", created.NextRun, next)
	}

	got, err := s.GetAutomation(created.ID)
	if err != nil {
		t.Fatalf("GetAutomation: %v", err)
	}
	if got.ID != created.ID || got.Cron != "0 9 * * *" || !got.Enabled {
		t.Fatalf("GetAutomation = %+v", got)
	}

	list, err := s.ListAutomations()
	if err != nil {
		t.Fatalf("ListAutomations: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("ListAutomations = %+v", list)
	}

	updatedNext := next.Add(24 * time.Hour)
	updated, err := s.UpdateAutomation(Automation{
		ID:      created.ID,
		Title:   "Evening report",
		Prompt:  "Summarize today",
		Cron:    "@daily",
		Enabled: false,
		NextRun: &updatedNext,
	})
	if err != nil {
		t.Fatalf("UpdateAutomation: %v", err)
	}
	if updated.Title != "Evening report" || updated.Cron != "@daily" || updated.Enabled {
		t.Fatalf("UpdateAutomation = %+v", updated)
	}
	if !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("CreatedAt = %v, want preserved %v", updated.CreatedAt, created.CreatedAt)
	}
	if updated.NextRun != nil {
		t.Fatalf("disabled update kept NextRun = %v, want nil", updated.NextRun)
	}

	enabled, err := s.SetAutomationEnabled(created.ID, true)
	if err != nil {
		t.Fatalf("SetAutomationEnabled: %v", err)
	}
	if !enabled.Enabled {
		t.Fatal("SetAutomationEnabled did not enable")
	}

	lastRun := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	afterRun, err := s.MarkAutomationRun(created.ID, lastRun, &updatedNext)
	if err != nil {
		t.Fatalf("MarkAutomationRun: %v", err)
	}
	if afterRun.LastRun == nil || !afterRun.LastRun.Equal(lastRun) {
		t.Fatalf("LastRun = %v, want %v", afterRun.LastRun, lastRun)
	}
	if afterRun.NextRun == nil || !afterRun.NextRun.Equal(updatedNext) {
		t.Fatalf("NextRun = %v, want %v", afterRun.NextRun, updatedNext)
	}

	cleared, err := s.MarkAutomationRun(created.ID, lastRun, nil)
	if err != nil {
		t.Fatalf("MarkAutomationRun(nil next): %v", err)
	}
	if cleared.NextRun != nil {
		t.Fatalf("NextRun = %v, want nil", cleared.NextRun)
	}

	rescheduled, err := s.SetAutomationNextRun(created.ID, updatedNext)
	if err != nil {
		t.Fatalf("SetAutomationNextRun: %v", err)
	}
	if rescheduled.NextRun == nil || !rescheduled.NextRun.Equal(updatedNext) {
		t.Fatalf("NextRun = %v, want %v", rescheduled.NextRun, updatedNext)
	}

	if err := s.DeleteAutomation(created.ID); err != nil {
		t.Fatalf("DeleteAutomation: %v", err)
	}
	if _, err := s.GetAutomation(created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetAutomation after delete error = %v, want ErrNotFound", err)
	}
	if list, err := s.ListAutomations(); err != nil || len(list) != 0 {
		t.Fatalf("ListAutomations after delete = %+v, %v", list, err)
	}
}

func TestAutomationValidationAndNotFound(t *testing.T) {
	s := openTestStore(t)

	if _, err := s.CreateAutomation(Automation{Cron: "@daily"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty title error = %v, want ErrInvalidArgument", err)
	}
	if _, err := s.CreateAutomation(Automation{Title: "x"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty cron error = %v, want ErrInvalidArgument", err)
	}
	if _, err := s.GetAutomation("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetAutomation(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := s.GetAutomation(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("GetAutomation(empty) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := s.UpdateAutomation(Automation{Title: "x", Cron: "@daily"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("UpdateAutomation(no id) error = %v, want ErrInvalidArgument", err)
	}
	if err := s.DeleteAutomation("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteAutomation(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := s.SetAutomationEnabled("missing", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetAutomationEnabled(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := s.MarkAutomationRun("missing", time.Now(), nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("MarkAutomationRun(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := s.SetAutomationNextRun("missing", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetAutomationNextRun(missing) error = %v, want ErrNotFound", err)
	}
}

func TestAutomationPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	next := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	created, err := s.CreateAutomation(Automation{
		Title: "Persisted", Prompt: "p", Cron: "*/15 * * * *", Enabled: true, NextRun: &next,
	})
	if err != nil {
		t.Fatalf("CreateAutomation: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	got, err := reopened.GetAutomation(created.ID)
	if err != nil {
		t.Fatalf("GetAutomation after reopen: %v", err)
	}
	if got.Title != "Persisted" || !got.Enabled || got.Cron != "*/15 * * * *" {
		t.Fatalf("task after reopen = %+v", got)
	}
	if got.NextRun == nil || !got.NextRun.Equal(next) {
		t.Fatalf("NextRun after reopen = %v, want %v", got.NextRun, next)
	}
}

func TestAutomationTableAddedToExistingV1Index(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Simulate an index written before the automations table existed.
	if _, err := s.db.Exec(`DROP TABLE automations`); err != nil {
		t.Fatalf("drop automations: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	ok, err := tableExists(reopened.db, "automations")
	if err != nil || !ok {
		t.Fatalf("automations table after reopen = %v, %v; want true", ok, err)
	}
	if _, err := reopened.CreateAutomation(Automation{Title: "after upgrade", Cron: "@daily"}); err != nil {
		t.Fatalf("CreateAutomation after upgrade: %v", err)
	}
}
