package contracts

import (
	"context"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// AutomationTask is one scheduled automation: a prompt run in a fresh session
// when its schedule fires (the old shell's Automation screen).
type AutomationTask struct {
	ID     string
	Name   string
	Prompt string
	// Schedule is the canonical schedule string (cron subset or typed selector
	// forms such as "every 60s" / "daily 09:00:00 Asia/Shanghai").
	Schedule string
	Enabled  bool

	CreatedAt time.Time
	UpdatedAt time.Time
	// LastRun is when the task last fired (zero when never).
	LastRun time.Time
	// NextRun is the next planned firing (zero when not enabled/unschedulable).
	NextRun time.Time
	// LastError is the last execution error ("" when the last run succeeded).
	LastError string
	// RunHistory lists the most recent finished runs, newest first (the
	// automation module keeps at most ten).
	RunHistory []AutomationRunRecord `json:"runHistory,omitempty"`
	// Model optionally overrides the chat model for this task's runs ("" keeps
	// the session default).
	Model string `json:"model,omitempty"`
}

// Automation run statuses.
const (
	// AutomationRunSuccess marks a run whose prompt was sent successfully.
	AutomationRunSuccess = "success"
	// AutomationRunFailure marks a run that failed (see Summary).
	AutomationRunFailure = "error"
)

// AutomationRunRecord is one finished automation run (memory + persisted in
// automations.json; old files without the field load with an empty history).
type AutomationRunRecord struct {
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	// Status is AutomationRunSuccess or AutomationRunFailure.
	Status string `json:"status"`
	// Summary is the short run outcome (truncated to 200 runes).
	Summary string `json:"summary,omitempty"`
}

// AutomationService is the product's persisted automation surface: CRUD for
// scheduled tasks, an immediate run, and change notifications for the UI.
//
// The backend owns schedule evaluation and execution: when a task fires it
// creates a new chat session and sends the task prompt.
type AutomationService interface {
	// List returns the tasks ordered by creation time.
	List(ctx context.Context) ([]AutomationTask, error)
	// Create registers a task (ID generated when empty, NextRun computed).
	Create(ctx context.Context, task AutomationTask) (AutomationTask, error)
	// Update replaces an existing task (schedule/enabled changes recompute
	// NextRun).
	Update(ctx context.Context, task AutomationTask) (AutomationTask, error)
	// Delete removes a task (unknown IDs are a no-op).
	Delete(ctx context.Context, id string) error
	// RunNow executes a task immediately and reschedules it.
	RunNow(ctx context.Context, id string) error
	// Subscribe receives a notification whenever the task set changed or a
	// run finished. The returned function unsubscribes.
	Subscribe(fn func()) (func(), error)
}

// KeyAutomation is provided by the automation backend module and consumed by
// the Automation screen (and the toolkit's schedule_* tools).
var KeyAutomation = cordis.NewServiceKey[AutomationService]("zeokeel/automation")
