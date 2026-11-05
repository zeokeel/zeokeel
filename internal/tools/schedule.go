package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/automation"
	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/store"
)

// ScheduleConfig configures the schedule CRUD tools. Store is required at call
// time. Now is optional and defaults to time.Now; tests inject a fake clock.
type ScheduleConfig struct {
	Store *store.Store
	Now   func() time.Time
}

const (
	// scheduleMaxTitle mirrors the reference tool's title bound.
	scheduleMaxTitle = 120
	// scheduleMaxOutput caps one schedule tool result (64 KiB).
	scheduleMaxOutput = DefaultMaxOutputBytes
)

const scheduleCreateDescription = `Create a persisted automation task that runs a prompt on a cron schedule.

Call it with a JSON object:
  {"title":"Morning digest","prompt":"Summarize the repository changes","cron":"@daily"}
  {"title":"Ping","prompt":"Check the build","cron":"*/15 * * * *","enabled":false}

"title" (at most 120 characters), "prompt" and "cron" are required.
"enabled" is optional and defaults to true. Supported cron expressions are the
automation subset: "*/N * * * *", "M H * * *", "@hourly", "@daily" (and
"@midnight"). Hour/minute values are the machine's local wall-clock time:
"@daily" fires at local midnight, "@hourly" at the top of each local hour.

The tool validates the expression, computes the next run time and persists the
task through the automation store. It returns
{"kind":"schedule","action":"create","schedule":{id,title,prompt,cron,enabled,
next_run,...}}.`

const scheduleListDescription = `List every persisted automation task.

Call it with an empty JSON object:
  {}

The tool returns {"kind":"schedule","action":"list","count":N,
"schedules":[{id,title,prompt,cron,enabled,last_run,next_run,created_at}]}
ordered by creation time.`

const scheduleUpdateDescription = `Update an existing automation task in place.

Call it with a JSON object:
  {"id":"<id>","title":"New title"}
  {"id":"<id>","cron":"0 9 * * *","enabled":true}

"id" is required and at least one of "title", "prompt", "cron" or "enabled"
must be present. A changed cron expression is validated and the next run time
is recomputed; disabling clears it. The tool returns the updated task as
{"kind":"schedule","action":"update","schedule":{...}}.`

const scheduleDeleteDescription = `Delete a persisted automation task.

Call it with a JSON object:
  {"id":"<id>"}

The tool returns {"kind":"schedule","action":"delete","id":...,"deleted":true}.
An unknown id is reported as an error.`

// scheduleResult is the JSON envelope for create, update and delete.
type scheduleResult struct {
	Kind     string            `json:"kind"`
	Action   string            `json:"action"`
	Schedule *store.Automation `json:"schedule,omitempty"`
	ID       string            `json:"id,omitempty"`
	Deleted  bool              `json:"deleted,omitempty"`
}

// scheduleListResult is the JSON envelope for schedule_list. Schedules is
// always present so an empty list is unambiguous.
type scheduleListResult struct {
	Kind      string             `json:"kind"`
	Action    string             `json:"action"`
	Count     int                `json:"count"`
	Schedules []store.Automation `json:"schedules"`
}

// NewScheduleTools returns the four automation CRUD tools.
func NewScheduleTools(cfg ScheduleConfig) []runtime.Tool {
	now := cfg.Now
	return []runtime.Tool{
		&scheduleCreateTool{st: cfg.Store, now: now},
		&scheduleListTool{st: cfg.Store},
		&scheduleUpdateTool{st: cfg.Store, now: now},
		&scheduleDeleteTool{st: cfg.Store},
	}
}

// scheduleToolBase carries the shared store and clock.
type scheduleToolBase struct {
	st  *store.Store
	now func() time.Time
}

// requireStore returns a clear failure for a tool constructed without a store.
func (b *scheduleToolBase) requireStore(name string) error {
	if b.st == nil {
		return fmt.Errorf("%s: no store configured", name)
	}
	return nil
}

// clock returns the configured clock, defaulting to time.Now in the machine's
// local time zone. Cron expressions are wall-clock expressions (@daily means
// local midnight), so the schedule is computed against local time and only the
// persisted instant is normalized to UTC.
func (b *scheduleToolBase) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// nextRunUTC computes the next run of cron after the clock's wall-clock time
// and returns it as a UTC instant, which is what the store persists.
func (b *scheduleToolBase) nextRunUTC(cron string) (time.Time, error) {
	next, err := automation.NextRun(cron, b.clock())
	if err != nil {
		return time.Time{}, err
	}
	return next.UTC(), nil
}

// scheduleCreateTool implements schedule_create.
type scheduleCreateTool struct {
	scheduleToolBase
}

func (t *scheduleCreateTool) Name() string        { return "schedule_create" }
func (t *scheduleCreateTool) Description() string { return scheduleCreateDescription }

func (t *scheduleCreateTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if err := scheduleContext(ctx); err != nil {
		return "", err
	}
	if err := t.requireStore(t.Name()); err != nil {
		return "", err
	}
	var in struct {
		Title   string `json:"title"`
		Prompt  string `json:"prompt"`
		Cron    string `json:"cron"`
		Enabled *bool  `json:"enabled"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	title, prompt, cron, err := validateScheduleInput(in.Title, in.Prompt, in.Cron)
	if err != nil {
		return "", fmt.Errorf("%s: %w", t.Name(), err)
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	task := store.Automation{
		Title:   title,
		Prompt:  prompt,
		Cron:    cron,
		Enabled: enabled,
	}
	if enabled {
		next, err := t.nextRunUTC(cron)
		if err != nil {
			return "", fmt.Errorf("%s: %w", t.Name(), err)
		}
		task.NextRun = &next
	}
	created, err := t.st.CreateAutomation(task)
	if err != nil {
		return "", fmt.Errorf("%s: %w", t.Name(), err)
	}
	return boundedToolJSON(scheduleResult{
		Kind:     "schedule",
		Action:   "create",
		Schedule: created,
	}, scheduleMaxOutput, t.Name())
}

// scheduleListTool implements schedule_list.
type scheduleListTool struct {
	scheduleToolBase
}

func (t *scheduleListTool) Name() string        { return "schedule_list" }
func (t *scheduleListTool) Description() string { return scheduleListDescription }

func (t *scheduleListTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if err := scheduleContext(ctx); err != nil {
		return "", err
	}
	if err := t.requireStore(t.Name()); err != nil {
		return "", err
	}
	var in struct{}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	tasks, err := t.st.ListAutomations()
	if err != nil {
		return "", fmt.Errorf("%s: %w", t.Name(), err)
	}
	if tasks == nil {
		tasks = []store.Automation{}
	}
	return boundedToolJSON(scheduleListResult{
		Kind:      "schedule",
		Action:    "list",
		Count:     len(tasks),
		Schedules: tasks,
	}, scheduleMaxOutput, t.Name())
}

// scheduleUpdateTool implements schedule_update.
type scheduleUpdateTool struct {
	scheduleToolBase
}

func (t *scheduleUpdateTool) Name() string        { return "schedule_update" }
func (t *scheduleUpdateTool) Description() string { return scheduleUpdateDescription }

func (t *scheduleUpdateTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if err := scheduleContext(ctx); err != nil {
		return "", err
	}
	if err := t.requireStore(t.Name()); err != nil {
		return "", err
	}
	var in struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Prompt  string `json:"prompt"`
		Cron    string `json:"cron"`
		Enabled *bool  `json:"enabled"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return "", errors.New(`schedule_update: "id" is required`)
	}
	if strings.TrimSpace(in.Title) == "" && strings.TrimSpace(in.Prompt) == "" &&
		strings.TrimSpace(in.Cron) == "" && in.Enabled == nil {
		return "", errors.New(`schedule_update: needs at least one of "title", "prompt", "cron" or "enabled"`)
	}
	existing, err := t.st.GetAutomation(id)
	if err != nil {
		return "", fmt.Errorf("%s: %w", t.Name(), err)
	}
	task := *existing
	cronChanged := false
	if title := strings.TrimSpace(in.Title); title != "" {
		task.Title = title
	}
	if prompt := strings.TrimSpace(in.Prompt); prompt != "" {
		task.Prompt = prompt
	}
	if cron := strings.TrimSpace(in.Cron); cron != "" {
		task.Cron = cron
		cronChanged = true
	}
	enabledChanged := false
	if in.Enabled != nil {
		task.Enabled = *in.Enabled
		enabledChanged = true
	}
	task.Title = strings.TrimSpace(task.Title)
	task.Prompt = strings.TrimSpace(task.Prompt)
	task.Cron = strings.TrimSpace(task.Cron)
	if err := validateScheduleStrings(task.Title, task.Prompt, task.Cron); err != nil {
		return "", fmt.Errorf("%s: %w", t.Name(), err)
	}
	if task.Enabled && (cronChanged || enabledChanged || task.NextRun == nil) {
		next, err := t.nextRunUTC(task.Cron)
		if err != nil {
			return "", fmt.Errorf("%s: %w", t.Name(), err)
		}
		task.NextRun = &next
	}
	if !task.Enabled {
		task.NextRun = nil
	}
	updated, err := t.st.UpdateAutomation(task)
	if err != nil {
		return "", fmt.Errorf("%s: %w", t.Name(), err)
	}
	return boundedToolJSON(scheduleResult{
		Kind:     "schedule",
		Action:   "update",
		Schedule: updated,
	}, scheduleMaxOutput, t.Name())
}

// scheduleDeleteTool implements schedule_delete.
type scheduleDeleteTool struct {
	scheduleToolBase
}

func (t *scheduleDeleteTool) Name() string        { return "schedule_delete" }
func (t *scheduleDeleteTool) Description() string { return scheduleDeleteDescription }

func (t *scheduleDeleteTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if err := scheduleContext(ctx); err != nil {
		return "", err
	}
	if err := t.requireStore(t.Name()); err != nil {
		return "", err
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return "", errors.New(`schedule_delete: "id" is required`)
	}
	if err := t.st.DeleteAutomation(id); err != nil {
		return "", fmt.Errorf("%s: %w", t.Name(), err)
	}
	return boundedToolJSON(scheduleResult{
		Kind:    "schedule",
		Action:  "delete",
		ID:      id,
		Deleted: true,
	}, scheduleMaxOutput, t.Name())
}

// validateScheduleInput trims and validates create arguments.
func validateScheduleInput(title, prompt, cron string) (string, string, string, error) {
	title = strings.TrimSpace(title)
	prompt = strings.TrimSpace(prompt)
	cron = strings.TrimSpace(cron)
	if err := validateScheduleStrings(title, prompt, cron); err != nil {
		return "", "", "", err
	}
	return title, prompt, cron, nil
}

// validateScheduleStrings applies the shared field and cron rules.
func validateScheduleStrings(title, prompt, cron string) error {
	switch {
	case title == "":
		return errors.New(`"title" is required`)
	case len([]rune(title)) > scheduleMaxTitle:
		return fmt.Errorf(`"title" exceeds %d characters`, scheduleMaxTitle)
	case prompt == "":
		return errors.New(`"prompt" is required`)
	case cron == "":
		return errors.New(`"cron" is required`)
	}
	if err := automation.Validate(cron); err != nil {
		return fmt.Errorf("invalid cron %q: %w", cron, err)
	}
	return nil
}

// scheduleContext normalizes and checks a tool context.
func scheduleContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
