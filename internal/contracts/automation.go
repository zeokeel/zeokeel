package contracts

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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
	// WorkspaceID optionally binds each run's fresh session to a workspace
	// ("" keeps the current workspace behavior).
	WorkspaceID string `json:"workspaceId,omitempty"`
	// Permission optionally overrides the run session's permission mode
	// (ask/workspace/full/never; "" keeps the global setting).
	Permission string `json:"permission,omitempty"`
}

// SessionWorkspaceSwitcher is the optional chat half that moves one session to
// a workspace (the automation module applies task.WorkspaceID through it).
type SessionWorkspaceSwitcher interface {
	SetSessionWorkspace(sessionID, workspaceID string) error
}

// SessionPermissionSwitcher is the optional chat half that overrides one
// session's permission mode (ask/workspace/full/never).
type SessionPermissionSwitcher interface {
	SetSessionPermission(sessionID, mode string) error
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

// AutomationScheduleKind is the shape of a parsed automation schedule.
type AutomationScheduleKind int

const (
	// AutomationScheduleEveryMinutes fires every N minutes (*/N * * * *).
	AutomationScheduleEveryMinutes AutomationScheduleKind = iota
	// AutomationScheduleDaily fires once per day at a fixed hour and minute.
	AutomationScheduleDaily
	// AutomationScheduleHourly fires every hour at minute 0 (@hourly).
	AutomationScheduleHourly
	// AutomationScheduleEverySeconds fires every fixed number of seconds.
	AutomationScheduleEverySeconds
	// AutomationScheduleOnceAfter fires once after a delay (@after <N>s).
	AutomationScheduleOnceAfter
)

// AutomationSchedule is one parsed automation schedule expression. The parser is
// pure and lives here so the settings/automation UI can validate a draft and
// preview its next run with exactly the rules the backend executes (one parser,
// no drift).
type AutomationSchedule struct {
	spec    string
	kind    AutomationScheduleKind
	every   int // minutes for AutomationScheduleEveryMinutes
	minute  int // daily
	hour    int // daily
	seconds int // every/after
}

// ParseAutomationSchedule validates spec and returns its parsed schedule.
//
// Supported expressions:
//
//	*/N * * * *   every N minutes (1 <= N <= 59)
//	M H * * *     daily at hour H and minute M
//	@hourly       every hour at minute 0
//	@daily        every day at midnight (@midnight is an alias)
//	every <N>s    every N seconds
//	after <N>s    once, N seconds after scheduling
func ParseAutomationSchedule(spec string) (AutomationSchedule, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return AutomationSchedule{}, fmt.Errorf("empty schedule expression")
	}
	lower := strings.ToLower(trimmed)
	switch lower {
	case "@hourly":
		return AutomationSchedule{spec: "@hourly", kind: AutomationScheduleHourly}, nil
	case "@daily", "@midnight":
		return AutomationSchedule{spec: "@daily", kind: AutomationScheduleDaily}, nil
	}
	fields := strings.Fields(lower)
	if len(fields) == 2 {
		switch fields[0] {
		case "every":
			seconds, err := parseAutomationDelay(fields[1], "every")
			if err != nil {
				return AutomationSchedule{}, fmt.Errorf("schedule %q: %w", spec, err)
			}
			return AutomationSchedule{spec: fmt.Sprintf("every %ds", seconds), kind: AutomationScheduleEverySeconds, seconds: seconds}, nil
		case "after":
			seconds, err := parseAutomationDelay(fields[1], "after")
			if err != nil {
				return AutomationSchedule{}, fmt.Errorf("schedule %q: %w", spec, err)
			}
			return AutomationSchedule{spec: fmt.Sprintf("after %ds", seconds), kind: AutomationScheduleOnceAfter, seconds: seconds}, nil
		}
	}
	parts := strings.Fields(trimmed)
	if len(parts) != 5 {
		return AutomationSchedule{}, fmt.Errorf(
			"schedule %q: want 5 cron fields, @hourly, @daily, every <N>s or after <N>s", spec)
	}
	for index := 2; index < 5; index++ {
		if parts[index] != "*" {
			return AutomationSchedule{}, fmt.Errorf("cron %q: field %d must be *", spec, index+1)
		}
	}
	minuteField, hourField := parts[0], parts[1]
	if strings.HasPrefix(minuteField, "*/") {
		if hourField != "*" {
			return AutomationSchedule{}, fmt.Errorf("cron %q: */N needs * for hours", spec)
		}
		raw := strings.TrimPrefix(minuteField, "*/")
		interval, err := strconv.Atoi(raw)
		if err != nil || interval < 1 || interval > 59 {
			return AutomationSchedule{}, fmt.Errorf("cron %q: interval must be 1..59", spec)
		}
		return AutomationSchedule{spec: strings.TrimSpace(spec), kind: AutomationScheduleEveryMinutes, every: interval}, nil
	}
	minute, err := parseAutomationCronField(minuteField, 0, 59, "minute")
	if err != nil {
		return AutomationSchedule{}, fmt.Errorf("cron %q: %w", spec, err)
	}
	hour, err := parseAutomationCronField(hourField, 0, 23, "hour")
	if err != nil {
		return AutomationSchedule{}, fmt.Errorf("cron %q: %w", spec, err)
	}
	return AutomationSchedule{spec: strings.TrimSpace(spec), kind: AutomationScheduleDaily, minute: minute, hour: hour}, nil
}

// parseAutomationDelay parses the "<N>s" argument of every/after.
func parseAutomationDelay(field, kind string) (int, error) {
	if !strings.HasSuffix(field, "s") {
		return 0, fmt.Errorf("%s needs a whole number of seconds like %q", kind, "30s")
	}
	seconds, err := strconv.Atoi(strings.TrimSuffix(field, "s"))
	if err != nil || seconds < 1 {
		return 0, fmt.Errorf("%s interval must be a positive whole number of seconds", kind)
	}
	return seconds, nil
}

// parseAutomationCronField parses one numeric cron field within [lo, hi].
func parseAutomationCronField(field string, lo, hi int, name string) (int, error) {
	value, err := strconv.Atoi(field)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a number", name, field)
	}
	if value < lo || value > hi {
		return 0, fmt.Errorf("%s %d out of range %d..%d", name, value, lo, hi)
	}
	return value, nil
}

// ValidateAutomationSchedule reports whether spec is a supported schedule.
func ValidateAutomationSchedule(spec string) error {
	_, err := ParseAutomationSchedule(spec)
	return err
}

// String returns the canonical expression.
func (s AutomationSchedule) String() string { return s.spec }

// Kind reports the schedule's shape.
func (s AutomationSchedule) Kind() AutomationScheduleKind { return s.kind }

// Every returns the minute interval for the */N form, and 0 otherwise. It is
// the legacy accessor name the automation backend and its tests use.
func (s AutomationSchedule) Every() int { return s.EveryMinutes() }

// EveryMinutes returns the minute interval for the */N form, and 0 otherwise.
func (s AutomationSchedule) EveryMinutes() int {
	if s.kind == AutomationScheduleEveryMinutes {
		return s.every
	}
	return 0
}

// HourMinute returns the daily hour and minute, and false for other kinds.
func (s AutomationSchedule) HourMinute() (hour, minute int, ok bool) {
	if s.kind != AutomationScheduleDaily {
		return 0, 0, false
	}
	return s.hour, s.minute, true
}

// IntervalSeconds returns the every/after delay in seconds, and 0 otherwise.
func (s AutomationSchedule) IntervalSeconds() int {
	if s.kind == AutomationScheduleEverySeconds || s.kind == AutomationScheduleOnceAfter {
		return s.seconds
	}
	return 0
}

// Next returns the first instant strictly after from that matches the schedule,
// in from's location (zero for the zero schedule).
func (s AutomationSchedule) Next(from time.Time) time.Time {
	switch s.kind {
	case AutomationScheduleEveryMinutes:
		next := from.Truncate(time.Minute).Add(time.Minute)
		for next.Minute()%s.every != 0 {
			next = next.Add(time.Minute)
		}
		return next
	case AutomationScheduleHourly:
		return time.Date(from.Year(), from.Month(), from.Day(), from.Hour(), 0, 0, 0, from.Location()).Add(time.Hour)
	case AutomationScheduleDaily:
		next := time.Date(from.Year(), from.Month(), from.Day(), s.hour, s.minute, 0, 0, from.Location())
		if !next.After(from) {
			next = next.AddDate(0, 0, 1)
		}
		return next
	case AutomationScheduleEverySeconds, AutomationScheduleOnceAfter:
		return from.Add(time.Duration(s.seconds) * time.Second)
	default:
		return time.Time{}
	}
}

// NextAutomationRun parses spec and returns its first run strictly after from.
func NextAutomationRun(spec string, from time.Time) (time.Time, error) {
	schedule, err := ParseAutomationSchedule(spec)
	if err != nil {
		return time.Time{}, err
	}
	return schedule.Next(from), nil
}
