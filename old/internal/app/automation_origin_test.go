package app

import (
	"context"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// TestRunAutomationTaskDeliversToOriginSession is the FP-07 app regression: a
// fired task whose origin session still exists submits into that session
// instead of creating a new one, and the run records the origin session id.
func TestRunAutomationTaskDeliversToOriginSession(t *testing.T) {
	a, st := newSettingsTestApp(t)
	ctx := context.Background()
	origin, err := a.services.CreateSession(ctx, "origin conversation")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	before, err := st.ListSessions("")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	task, err := st.CreateAutomation(store.Automation{
		Title: "back to origin", Prompt: "continue where we left off", Cron: "@daily",
		Enabled: true, OriginSessionID: origin,
	})
	if err != nil {
		t.Fatalf("CreateAutomation: %v", err)
	}
	a.runAutomationTask(a.services, *task)

	after, err := st.ListSessions("")
	if err != nil {
		t.Fatalf("ListSessions after: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("session count changed %d -> %d; the origin session should have been reused", len(before), len(after))
	}
	run, err := st.LatestAutomationRun(task.ID)
	if err != nil {
		t.Fatalf("LatestAutomationRun: %v", err)
	}
	if run.SessionID != origin {
		t.Fatalf("run session = %q, want the origin session %q", run.SessionID, origin)
	}
	// The controller has no agent in this harness, so Submit fails and the run
	// must be recorded failed rather than left running.
	if run.Status != store.AutomationRunFailed || run.Error == "" {
		t.Fatalf("run = %+v, want failed with an error", run)
	}
}

// TestRunAutomationTaskCreatesSessionWhenOriginMissing covers the fallback: a
// deleted origin session means a fresh session is created and its id recorded;
// when the submit then fails, that empty session is rolled back again (P3).
func TestRunAutomationTaskCreatesSessionWhenOriginMissing(t *testing.T) {
	a, st := newSettingsTestApp(t)
	before, err := st.ListSessions("")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	task, err := st.CreateAutomation(store.Automation{
		Title: "orphan", Prompt: "p", Cron: "@daily", Enabled: true, OriginSessionID: "deleted-session-id",
	})
	if err != nil {
		t.Fatalf("CreateAutomation: %v", err)
	}
	a.runAutomationTask(a.services, *task)
	after, err := st.ListSessions("")
	if err != nil {
		t.Fatalf("ListSessions after: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("session count %d -> %d; the failed run's empty session should be rolled back", len(before), len(after))
	}
	run, err := st.LatestAutomationRun(task.ID)
	if err != nil {
		t.Fatalf("LatestAutomationRun: %v", err)
	}
	if run.SessionID == "" || run.SessionID == "deleted-session-id" {
		t.Fatalf("run session = %q, want the newly created (and rolled back) session", run.SessionID)
	}
	if _, serr := st.GetSession(run.SessionID); serr == nil {
		t.Fatalf("empty automation session %s was not rolled back", run.SessionID)
	}
}

// TestFastTurnFinishesRunTrackedLater covers the Submit/CreateAutomationRun
// race: a turn that finishes before its run row is tracked still finishes that
// run instead of leaving it "running" forever.
func TestFastTurnFinishesRunTrackedLater(t *testing.T) {
	a, st := newSettingsTestApp(t)
	task, err := st.CreateAutomation(store.Automation{Title: "fast", Prompt: "p", Cron: "@daily", Enabled: true})
	if err != nil {
		t.Fatalf("CreateAutomation: %v", err)
	}
	a.markAutomationPending("fast-session")
	a.observeTurnFinished(session.TurnOutcome{SessionID: "fast-session", TurnID: "t1", HasOutput: true})
	run, err := st.CreateAutomationRun(task.ID, "fast-session")
	if err != nil {
		t.Fatalf("CreateAutomationRun: %v", err)
	}
	a.trackAutomationRun("fast-session", run.ID)

	finished, err := st.LatestAutomationRun(task.ID)
	if err != nil {
		t.Fatalf("LatestAutomationRun: %v", err)
	}
	if finished.Status != store.AutomationRunCompleted || finished.FinishedAt == nil {
		t.Fatalf("run = %+v, want completed with a finish time", finished)
	}
}
