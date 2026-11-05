package app

import (
	"context"
	"log"
	"strings"

	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/store"
)

// Automation run history (PD-06).
//
// The scheduler fires a task, the app creates the bound session and submits the
// prompt, and every firing is recorded in the automation_runs table: created as
// running with the session id, then finished when that session's turn ends (or
// immediately when the turn never starts). Failure text comes from the turn's
// terminal error, so the Automation page can explain why a run failed instead
// of only showing a timestamp.

// observeTurnFinished is the app's single controller TurnFinishedHook: it fans
// the outcome out to the desktop notification dispatcher and to the automation
// run history. It runs on the finishing turn's goroutine.
func (a *app) observeTurnFinished(outcome session.TurnOutcome) {
	if a == nil {
		return
	}
	if a.notifier != nil {
		a.notifier.observeTurnFinished(outcome)
	}
	a.finishAutomationForSession(outcome)
}

// trackAutomationRun remembers which run a session's turn reports to.
func (a *app) trackAutomationRun(sessionID, runID string) {
	a.automationMu.Lock()
	defer a.automationMu.Unlock()
	if a.automationRuns == nil {
		a.automationRuns = make(map[string]string)
	}
	a.automationRuns[sessionID] = runID
}

// untrackAutomationRun removes the session's pending run entry and returns it.
func (a *app) untrackAutomationRun(sessionID string) string {
	a.automationMu.Lock()
	defer a.automationMu.Unlock()
	runID := a.automationRuns[sessionID]
	delete(a.automationRuns, sessionID)
	return runID
}

// finishAutomationForSession records a fired automation's terminal state when
// its session's turn finishes.
func (a *app) finishAutomationForSession(outcome session.TurnOutcome) {
	if a == nil || strings.TrimSpace(outcome.SessionID) == "" {
		return
	}
	runID := a.untrackAutomationRun(outcome.SessionID)
	if runID == "" {
		return
	}
	status := store.AutomationRunCompleted
	errText := ""
	switch {
	case outcome.Stopped:
		status = store.AutomationRunStopped
	case outcome.Failed:
		status = store.AutomationRunFailed
		errText = strings.TrimSpace(outcome.Error)
		if errText == "" {
			errText = "turn failed"
		}
	}
	a.finishAutomationRun(runID, status, errText)
}

// finishAutomationRun persists a run's terminal state, logging (not failing)
// on a store error: the run history is diagnostics, never a reason to break the
// scheduler.
func (a *app) finishAutomationRun(runID, status, errText string) {
	if a == nil || strings.TrimSpace(runID) == "" {
		return
	}
	st := a.store()
	if st == nil {
		return
	}
	if _, err := st.FinishAutomationRun(runID, status, errText); err != nil {
		log.Printf("zeokeel: automation: finish run %s: %v", runID, err)
	}
}

// store returns the app's session store, or nil before services are ready.
func (a *app) store() *store.Store {
	if a == nil || a.services == nil {
		return nil
	}
	return a.services.Store()
}

// runAutomationTask executes one fired automation: it creates a session bound
// to the task's workspace, permission and model, records a run, and submits the
// prompt. A failure before the turn starts (no workspace, no model key, a
// rejected submit) finishes the run as failed immediately; otherwise the turn
// finished hook finishes it.
func (a *app) runAutomationTask(ctrl *session.Controller, task store.Automation) {
	if ctrl == nil {
		return
	}
	ctx := context.Background()
	st := ctrl.Store()

	// Create the session in the task's workspace. The workspace is part of the
	// task definition (PD-06), so a task created for another project must not
	// run in whichever workspace happens to be current.
	var (
		sessionID string
		sessErr   error
	)
	if workspaceID := strings.TrimSpace(task.WorkspaceID); workspaceID != "" {
		sessionID, sessErr = ctrl.CreateSessionInWorkspace(ctx, workspaceID, task.Title)
	} else {
		sessionID, sessErr = ctrl.CreateSession(ctx, task.Title)
	}

	// Record the run even when the session could not be created: the history
	// must show that the scheduled task attempted to run and failed.
	run, runErr := st.CreateAutomationRun(task.ID, sessionID)
	if runErr != nil {
		log.Printf("zeokeel: automation %q: record run: %v", task.Title, runErr)
	}
	if sessErr != nil {
		if run != nil {
			a.finishAutomationRun(run.ID, store.AutomationRunFailed, "create session: "+sessErr.Error())
		}
		log.Printf("zeokeel: automation %q: create session: %v", task.Title, sessErr)
		return
	}

	// Apply the run's access mode and model. Both are best effort: an invalid
	// permission or a deleted model must not stop the task from running with
	// the app defaults (the run history still records what happened).
	if permission := strings.TrimSpace(task.Permission); permission != "" {
		ctrl.SetSessionPermission(sessionID, permission)
	}
	if modelID := strings.TrimSpace(task.Model); modelID != "" {
		if err := ctrl.SetSessionModel(sessionID, modelID); err != nil {
			log.Printf("zeokeel: automation %q: bind model %q: %v", task.Title, modelID, err)
		}
	}

	if err := ctrl.Submit(ctx, sessionID, task.Prompt); err != nil {
		// The turn never started, so no TurnFinished hook will fire: finish
		// the run here instead of leaving it "running" forever.
		if run != nil {
			a.finishAutomationRun(run.ID, store.AutomationRunFailed, "submit: "+err.Error())
		}
		log.Printf("zeokeel: automation %q: submit: %v", task.Title, err)
		return
	}
	if run != nil {
		a.trackAutomationRun(sessionID, run.ID)
	}
}
