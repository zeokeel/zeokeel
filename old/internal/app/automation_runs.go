package app

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/store"
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

// automationTerminalState is a turn outcome remembered for a session whose
// automation run row was not created yet (the turn finished in the tiny window
// between Submit and CreateAutomationRun).
type automationTerminalState struct {
	status  string
	errText string
}

// automationTerminal maps one TurnOutcome to the run status/error pair.
func automationTerminal(outcome session.TurnOutcome) automationTerminalState {
	switch {
	case outcome.Stopped:
		return automationTerminalState{status: store.AutomationRunStopped}
	case outcome.Failed:
		errText := strings.TrimSpace(outcome.Error)
		if errText == "" {
			errText = "turn failed"
		}
		return automationTerminalState{status: store.AutomationRunFailed, errText: errText}
	default:
		return automationTerminalState{status: store.AutomationRunCompleted}
	}
}

// markAutomationPending records that a turn was submitted for sessionID before
// its run row exists, so a turn that finishes immediately still finishes the
// run (via trackAutomationRun) instead of leaving it "running" forever.
func (a *app) markAutomationPending(sessionID string) {
	a.automationMu.Lock()
	defer a.automationMu.Unlock()
	if a.automationRuns == nil {
		a.automationRuns = make(map[string]string)
	}
	if _, exists := a.automationRuns[sessionID]; !exists {
		a.automationRuns[sessionID] = ""
	}
}

// clearAutomationPending forgets a pending/known run for sessionID after a
// submit that never started a turn.
func (a *app) clearAutomationPending(sessionID string) {
	a.automationMu.Lock()
	defer a.automationMu.Unlock()
	delete(a.automationRuns, sessionID)
	if a.automationTerminals != nil {
		delete(a.automationTerminals, sessionID)
	}
}

// trackAutomationRun attaches the created run id to its session. If the turn
// already finished (a very fast provider), the remembered outcome finishes the
// run immediately.
func (a *app) trackAutomationRun(sessionID, runID string) {
	a.automationMu.Lock()
	if a.automationRuns == nil {
		a.automationRuns = make(map[string]string)
	}
	if terminal, ok := a.automationTerminals[sessionID]; ok {
		delete(a.automationTerminals, sessionID)
		delete(a.automationRuns, sessionID)
		a.automationMu.Unlock()
		a.finishAutomationRun(runID, terminal.status, terminal.errText)
		return
	}
	a.automationRuns[sessionID] = runID
	a.automationMu.Unlock()
}

// finishAutomationForSession records a fired automation's terminal state when
// its session's turn finishes.
func (a *app) finishAutomationForSession(outcome session.TurnOutcome) {
	if a == nil || strings.TrimSpace(outcome.SessionID) == "" {
		return
	}
	terminal := automationTerminal(outcome)
	a.automationMu.Lock()
	runID, tracked := a.automationRuns[outcome.SessionID]
	if !tracked {
		// This session's turn was not started by an automation.
		a.automationMu.Unlock()
		return
	}
	if runID == "" {
		// The run row is still being created; remember the outcome and let
		// trackAutomationRun finish it.
		if a.automationTerminals == nil {
			a.automationTerminals = make(map[string]automationTerminalState)
		}
		a.automationTerminals[outcome.SessionID] = terminal
		a.automationMu.Unlock()
		return
	}
	delete(a.automationRuns, outcome.SessionID)
	a.automationMu.Unlock()
	a.finishAutomationRun(runID, terminal.status, terminal.errText)
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

// runAutomationTask executes one fired automation. The scheduler's run is
// delivered back to the session that created the task (FP-07) when that session
// still exists and is free; otherwise a new session is created. The task's
// workspace/permission/model bind the new session only: an existing origin
// session keeps the user's own settings. Every attempt writes an
// automation_runs row.
func (a *app) runAutomationTask(ctrl *session.Controller, task store.Automation) {
	if ctrl == nil {
		return
	}
	ctx := context.Background()
	st := ctrl.Store()

	// submitInto prepares the run row, submits the prompt into sessionID and
	// tracks the run for the terminal-turn hook. It reports false when Submit
	// did not start a turn (the run is already finished as failed then) and
	// returns the submit error so the caller can distinguish a busy session
	// (fall back to a fresh one) from a failure that affects every session.
	submitInto := func(sessionID string) (bool, error) {
		a.markAutomationPending(sessionID)
		run, runErr := st.CreateAutomationRun(task.ID, sessionID)
		if runErr != nil {
			log.Printf("zeokeel: automation %q: record run: %v", task.Title, runErr)
		}
		if err := ctrl.Submit(ctx, sessionID, task.Prompt); err != nil {
			a.clearAutomationPending(sessionID)
			if run != nil {
				a.finishAutomationRun(run.ID, store.AutomationRunFailed, "submit: "+err.Error())
			}
			log.Printf("zeokeel: automation %q: submit: %v", task.Title, err)
			return false, err
		}
		if run != nil {
			a.trackAutomationRun(sessionID, run.ID)
		} else {
			// Without a run row there is nothing to finish; keep the pending
			// marker out of the maps.
			a.clearAutomationPending(sessionID)
		}
		return true, nil
	}

	// Deliver the run back to the session that created the task when possible.
	if origin := strings.TrimSpace(task.OriginSessionID); origin != "" {
		if _, err := st.GetSession(origin); err == nil {
			ok, submitErr := submitInto(origin)
			if ok {
				return
			}
			if !errors.Is(submitErr, session.ErrTurnRunning) {
				// No-Agent / closed-controller errors would hit a new session
				// exactly the same way; the failed run is already recorded.
				return
			}
			// A busy origin session falls through to a fresh session so a
			// scheduled task is not silently skipped.
			log.Printf("zeokeel: automation %q: origin session %s is busy; creating a new session", task.Title, origin)
		} else {
			log.Printf("zeokeel: automation %q: origin session %s no longer exists; creating a new session", task.Title, origin)
		}
	}

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
	if sessErr != nil {
		// Record the attempt even though no session exists: the history must
		// show that the scheduled task ran and failed.
		run, runErr := st.CreateAutomationRun(task.ID, "")
		if runErr != nil {
			log.Printf("zeokeel: automation %q: record run: %v", task.Title, runErr)
		}
		if run != nil {
			a.finishAutomationRun(run.ID, store.AutomationRunFailed, "create session: "+sessErr.Error())
		}
		log.Printf("zeokeel: automation %q: create session: %v", task.Title, sessErr)
		return
	}

	// Apply the run's access mode and model to the new session. Both are best
	// effort: an invalid permission or a deleted model must not stop the task
	// from running with the app defaults. The permission accepts both the UI
	// spellings ("read-only", "workspace", "full", "auto-review") and the
	// persisted session constants ("ask", ...).
	if permission := strings.TrimSpace(task.Permission); permission != "" {
		if mode, perr := headlessPermission(permission); perr == nil {
			ctrl.SetSessionPermission(sessionID, mode)
		} else {
			log.Printf("zeokeel: automation %q: invalid permission %q: %v", task.Title, permission, perr)
		}
	}
	if modelID := strings.TrimSpace(task.Model); modelID != "" {
		if err := ctrl.SetSessionModel(sessionID, modelID); err != nil {
			log.Printf("zeokeel: automation %q: bind model %q: %v", task.Title, modelID, err)
		}
	}
	if ok, submitErr := submitInto(sessionID); !ok {
		// Run-now rollback (P3): the session was created moments ago and never
		// received a turn, so leaving it behind pollutes the session list with
		// an empty conversation. Delete it and keep the failed run record.
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if delErr := ctrl.DeleteSession(rollbackCtx, sessionID); delErr != nil {
			log.Printf("zeokeel: automation %q: roll back empty session %s: %v", task.Title, sessionID, delErr)
		} else {
			log.Printf("zeokeel: automation %q: rolled back empty session %s after %v", task.Title, sessionID, submitErr)
		}
	}
}
