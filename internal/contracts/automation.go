package contracts

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // 墙钟计划接受 IANA 时区；不依赖宿主操作系统的时区数据库（与 rig 选择器一致）

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// AutomationDefaultTimeZone is the IANA time zone wall-clock schedule
// expressions are evaluated in when the expression does not name one. DSH
// defaults to Asia/Shanghai; the product follows it so a new selector such as
// "weekly 09:00:00 Asia/Shanghai 1,3" fires at the local time the user wrote.
const AutomationDefaultTimeZone = "Asia/Shanghai"

// AutomationTask is one scheduled automation: a prompt delivered into a chat
// session when its schedule fires (the old shell's Automation screen).
type AutomationTask struct {
	ID     string
	Name   string
	Prompt string
	// Schedule is the canonical schedule string (a legacy interval/daily form
	// or a DSH typed selector: daily/weekly/at, or a five-field Vixie cron).
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
	// WorkspaceID optionally binds each run's session to a workspace
	// ("" keeps the current workspace behavior).
	WorkspaceID string `json:"workspaceId,omitempty"`
	// Permission optionally overrides the run session's permission mode
	// (ask/workspace/full/never; "" keeps the global setting).
	Permission string `json:"permission,omitempty"`
	// SessionID is the conversation subsequent runs are delivered to. The first
	// run creates the session, records it here and persists it, so later runs
	// continue the same conversation (DSH's original-session delivery). "" means
	// the next run creates one.
	SessionID string `json:"sessionId,omitempty"`
	// NewSessionPerRun starts every run in a fresh conversation instead of
	// reusing SessionID (the legacy behavior, kept as an explicit option).
	// SessionID still remembers the last created conversation.
	NewSessionPerRun bool `json:"newSessionPerRun,omitempty"`
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

// Automation run statuses. A run is first recorded as delivered (the chat
// service accepted the prompt) and later settles to exactly one terminal
// status when the bound session's run ends. Old registry files used "success"
// and "error" directly, so those tokens stay the terminal success/failure
// spellings.
const (
	// AutomationRunDelivered marks a run whose prompt the chat service accepted
	// and whose bound conversation is still running (or whose outcome is not
	// known yet).
	AutomationRunDelivered = "delivered"
	// AutomationRunSuccess marks a run that settled without an error.
	AutomationRunSuccess = "success"
	// AutomationRunFailure marks a run that failed (delivery failure or a
	// failed agent turn).
	AutomationRunFailure = "error"
	// AutomationRunInterrupted marks a run the user stopped or that a restart
	// cut short: neutral, not a failure.
	AutomationRunInterrupted = "interrupted"
)

// AutomationRunRecord is one automation run (memory + persisted in
// automations.json; old files without the newer fields load with unknown
// values).
//
// The record separates delivery from completion: DeliveredAt is set when
// chat.Send accepted the prompt, CompletedAt and Status when the bound
// session's run settled. A delivered record that never settles keeps Status
// AutomationRunDelivered and a zero CompletedAt (unknown outcome).
type AutomationRunRecord struct {
	// ID identifies the record while the run is in flight; settlement writes
	// back by ID. "" for records written before run settlement existed.
	ID string `json:"id,omitempty"`
	// StartedAt is when the run attempt started (before the session existed).
	StartedAt time.Time `json:"startedAt"`
	// DeliveredAt is when the chat service accepted the prompt (zero when
	// delivery failed).
	DeliveredAt time.Time `json:"deliveredAt,omitempty"`
	// CompletedAt is when the run settled (success/failure/interrupted); zero
	// while the run is still in flight or the outcome is unknown. It is
	// persisted as "completedAt"; old files stored the same instant as
	// "finishedAt" and are read through legacyRunRecord.FinishedAt.
	CompletedAt time.Time `json:"completedAt,omitempty"`
	// Status is AutomationRunDelivered or one of the terminal statuses.
	Status string `json:"status"`
	// Error is the terminal failure/interruption cause ("" for success and for
	// a still-running run).
	Error string `json:"error,omitempty"`
	// Summary is the short run outcome (truncated to 200 runes). The legacy
	// value "ran in chat session <id>" is kept so older readers still find the
	// run conversation.
	Summary string `json:"summary,omitempty"`
	// DurationMS is the wall time from delivery to completion in milliseconds
	// (0 when unknown).
	DurationMS int64 `json:"durationMs,omitempty"`
	// SessionID is the conversation the run was delivered to. "" for records
	// written before the field existed (the legacy summary carries the id).
	SessionID string `json:"sessionId,omitempty"`
}

// Terminal reports whether the record reached a terminal state.
func (r AutomationRunRecord) Terminal() bool {
	switch r.Status {
	case AutomationRunDelivered, "":
		return false
	default:
		return true
	}
}

// UnmarshalJSON reads a run record, accepting the legacy "finishedAt" spelling
// for the completion timestamp so registries written by older versions keep
// loading (a missing value stays zero = unknown).
func (r *AutomationRunRecord) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID          string    `json:"id"`
		StartedAt   time.Time `json:"startedAt"`
		DeliveredAt time.Time `json:"deliveredAt"`
		CompletedAt time.Time `json:"completedAt"`
		FinishedAt  time.Time `json:"finishedAt"`
		Status      string    `json:"status"`
		Error       string    `json:"error"`
		Summary     string    `json:"summary"`
		DurationMS  int64     `json:"durationMs"`
		SessionID   string    `json:"sessionId"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	completedAt := raw.CompletedAt
	if completedAt.IsZero() {
		completedAt = raw.FinishedAt
	}
	*r = AutomationRunRecord{
		ID:          raw.ID,
		StartedAt:   raw.StartedAt,
		DeliveredAt: raw.DeliveredAt,
		CompletedAt: completedAt,
		Status:      raw.Status,
		Error:       raw.Error,
		Summary:     raw.Summary,
		DurationMS:  raw.DurationMS,
		SessionID:   raw.SessionID,
	}
	return nil
}

// AutomationRunSessionID extracts the run conversation id from the legacy run
// summary ("ran in chat session <id>"). Records written by this version carry
// SessionID; the summary fallback keeps older records linkable.
func AutomationRunSessionID(summary string) string {
	trimmed := strings.TrimSpace(summary)
	const prefix = "ran in chat session "
	if len(trimmed) <= len(prefix) || !strings.EqualFold(trimmed[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(trimmed[len(prefix):])
}

// AutomationService is the product's persisted automation surface: CRUD for
// scheduled tasks, an immediate run, and change notifications for the UI.
//
// The backend owns schedule evaluation and execution: when a task fires, the
// next run is delivered into the task's bound conversation (created on the
// first run, or on every run when NewSessionPerRun is set).
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
	// AutomationScheduleDaily fires once per day at a fixed wall-clock time.
	AutomationScheduleDaily
	// AutomationScheduleHourly fires every hour at minute 0 (@hourly).
	AutomationScheduleHourly
	// AutomationScheduleEverySeconds fires every fixed number of seconds.
	AutomationScheduleEverySeconds
	// AutomationScheduleOnceAfter fires once after a delay (@after <N>s).
	AutomationScheduleOnceAfter
	// AutomationScheduleWeekly fires on selected ISO weekdays at a wall-clock
	// time (weekly <time> <zone> <weekdays>, Monday=1 .. Sunday=7).
	AutomationScheduleWeekly
	// AutomationScheduleCron fires by a five-field Vixie cron expression
	// evaluated in a wall-clock time zone.
	AutomationScheduleCron
	// AutomationScheduleOnceAt fires once at an absolute instant (at <RFC3339>).
	AutomationScheduleOnceAt
)

// AutomationSchedule is one parsed automation schedule expression. The parser is
// pure and lives here so the settings/automation UI can validate a draft and
// preview its next run with exactly the rules the backend executes (one parser,
// no drift). Its canonical String() form matches the DSH selector spellings the
// Rig schedule tools produce.
type AutomationSchedule struct {
	spec     string
	kind     AutomationScheduleKind
	every    int // minutes for AutomationScheduleEveryMinutes
	minute   int // wall-clock minute for Daily/Weekly
	hour     int // wall-clock hour for Daily/Weekly
	second   int // wall-clock second for Daily/Weekly
	millis   int // wall-clock milliseconds for Daily/Weekly
	seconds  int // every/after
	at       time.Time
	timezone string
	location *time.Location
	weekdays []int
	cron     *automationCron
}

// ParseAutomationSchedule validates spec and returns its parsed schedule.
//
// Supported expressions:
//
//	*/N * * * *                  every N minutes (1 <= N <= 59, legacy)
//	M H * * *                    daily at hour H and minute M (legacy)
//	@hourly                      every hour at minute 0 (legacy)
//	@daily                       every day at midnight (@midnight is an alias)
//	every <N>s                   every N seconds
//	after <N>s                   once, N seconds after scheduling
//	at <RFC3339>                 once at an absolute instant (DSH typed selector)
//	daily <HH:MM:SS[.fff]> [zone]        daily wall-clock time
//	weekly <HH:MM:SS[.fff]> [zone] [iso weekdays]
//	                             weekly wall-clock time (Monday=1..Sunday=7)
//	cron <five fields> [zone]    five-field Vixie cron (DSH dialect)
//
// The canonical forms are the ones the Rig schedule tools store ("daily
// 09:00:00.000 Asia/Shanghai", "weekly 09:00:00.000 Asia/Shanghai 1,3",
// "cron 0 9 * * 1-5 Asia/Shanghai", "at 2026-12-01T09:30:00.000Z"). The legacy
// forms stay accepted verbatim. Wall-clock expressions without a zone use
// AutomationDefaultTimeZone.
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
		return newAutomationDailySchedule("@daily", 0, 0, 0, 0, AutomationDefaultTimeZone)
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 2 {
		switch strings.ToLower(fields[0]) {
		case "every":
			seconds, err := parseAutomationDelay(strings.ToLower(fields[1]), "every")
			if err != nil {
				return AutomationSchedule{}, fmt.Errorf("schedule %q: %w", spec, err)
			}
			return AutomationSchedule{spec: fmt.Sprintf("every %ds", seconds), kind: AutomationScheduleEverySeconds, seconds: seconds}, nil
		case "after":
			seconds, err := parseAutomationDelay(strings.ToLower(fields[1]), "after")
			if err != nil {
				return AutomationSchedule{}, fmt.Errorf("schedule %q: %w", spec, err)
			}
			return AutomationSchedule{spec: fmt.Sprintf("after %ds", seconds), kind: AutomationScheduleOnceAfter, seconds: seconds}, nil
		}
	}
	if len(fields) > 0 {
		switch strings.ToLower(fields[0]) {
		case "at":
			return parseAutomationAtSelector(fields, spec)
		case "daily":
			return parseAutomationDailySelector(fields, spec)
		case "weekly":
			return parseAutomationWeeklySelector(fields, spec)
		case "cron":
			return parseAutomationCronSelector(fields, spec)
		}
	}
	if len(fields) == 5 || len(fields) == 6 {
		return parseAutomationBareCron(fields, spec)
	}
	return AutomationSchedule{}, fmt.Errorf(
		"schedule %q: want a five-field cron expression, daily/weekly/at/cron selector, @hourly, @daily, every <N>s or after <N>s", spec)
}

// parseAutomationAtSelector parses `at <RFC3339>` (the DSH one-shot absolute
// selector). at is kept absolute in UTC.
func parseAutomationAtSelector(fields []string, spec string) (AutomationSchedule, error) {
	if len(fields) != 2 {
		return AutomationSchedule{}, fmt.Errorf("schedule %q: want at <RFC 3339 date-time>", spec)
	}
	instant, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(fields[1]))
	if err != nil {
		return AutomationSchedule{}, fmt.Errorf("schedule %q: at needs an RFC 3339 date-time such as 2026-12-01T09:30:00+08:00", spec)
	}
	canonical := "at " + formatAutomationUTCInstant(instant)
	return AutomationSchedule{spec: canonical, kind: AutomationScheduleOnceAt, at: instant.UTC()}, nil
}

// parseAutomationDailySelector parses `daily <HH:MM:SS[.fff]> [zone]`.
func parseAutomationDailySelector(fields []string, spec string) (AutomationSchedule, error) {
	if len(fields) != 2 && len(fields) != 3 {
		return AutomationSchedule{}, fmt.Errorf("schedule %q: want daily <HH:MM:SS> [time zone]", spec)
	}
	hour, minute, second, millis, err := parseAutomationClock(fields[1])
	if err != nil {
		return AutomationSchedule{}, fmt.Errorf("schedule %q: %w", spec, err)
	}
	zone := AutomationDefaultTimeZone
	if len(fields) == 3 {
		zone = fields[2]
	}
	return newAutomationDailySchedule("", hour, minute, second, millis, zone)
}

// newAutomationDailySchedule builds a daily wall-clock schedule with the
// canonical spelling: the keyword selector when spec is empty, otherwise the
// legacy macro.
func newAutomationDailySchedule(spec string, hour, minute, second, millis int, zone string) (AutomationSchedule, error) {
	location, canonicalZone, err := loadAutomationLocation(zone)
	if err != nil {
		return AutomationSchedule{}, err
	}
	if spec == "" {
		spec = fmt.Sprintf("daily %s %s",
			formatAutomationClock(hour, minute, second, millis), canonicalZone)
	}
	return AutomationSchedule{
		spec: spec, kind: AutomationScheduleDaily,
		hour: hour, minute: minute, second: second, millis: millis,
		timezone: canonicalZone, location: location,
	}, nil
}

// parseAutomationWeeklySelector parses the canonical DSH form
// `weekly <HH:MM:SS[.fff]> [zone] <iso weekdays>` (the zone may be omitted for
// the default Asia/Shanghai).
func parseAutomationWeeklySelector(fields []string, spec string) (AutomationSchedule, error) {
	if len(fields) != 3 && len(fields) != 4 {
		return AutomationSchedule{}, fmt.Errorf("schedule %q: want weekly <HH:MM:SS> [time zone] <ISO weekdays>", spec)
	}
	clockField, zone, weekdayField := fields[1], AutomationDefaultTimeZone, ""
	if len(fields) == 4 {
		zone, weekdayField = fields[2], fields[3]
	} else {
		weekdayField = fields[2]
	}
	hour, minute, second, millis, err := parseAutomationClock(clockField)
	if err != nil {
		return AutomationSchedule{}, fmt.Errorf("schedule %q: %w", spec, err)
	}
	weekdays, err := parseAutomationWeekdays(weekdayField)
	if err != nil {
		return AutomationSchedule{}, fmt.Errorf("schedule %q: %w", spec, err)
	}
	location, canonicalZone, err := loadAutomationLocation(zone)
	if err != nil {
		return AutomationSchedule{}, err
	}
	parts := make([]string, len(weekdays))
	for index, weekday := range weekdays {
		parts[index] = strconv.Itoa(weekday)
	}
	canonical := fmt.Sprintf("weekly %s %s %s",
		formatAutomationClock(hour, minute, second, millis), canonicalZone, strings.Join(parts, ","))
	return AutomationSchedule{
		spec: canonical, kind: AutomationScheduleWeekly,
		hour: hour, minute: minute, second: second, millis: millis,
		timezone: canonicalZone, location: location, weekdays: weekdays,
	}, nil
}

// parseAutomationCronSelector parses `cron <five fields> [zone]`.
func parseAutomationCronSelector(fields []string, spec string) (AutomationSchedule, error) {
	if len(fields) != 6 && len(fields) != 7 {
		return AutomationSchedule{}, fmt.Errorf("schedule %q: want cron <minute hour day-of-month month day-of-week> [time zone]", spec)
	}
	zone := AutomationDefaultTimeZone
	if len(fields) == 7 {
		zone = fields[6]
	}
	location, canonicalZone, err := loadAutomationLocation(zone)
	if err != nil {
		return AutomationSchedule{}, err
	}
	cron, err := parseAutomationCron(fields[1:6])
	if err != nil {
		return AutomationSchedule{}, fmt.Errorf("schedule %q: %w", spec, err)
	}
	canonical := fmt.Sprintf("cron %s %s", cron.expression, canonicalZone)
	return AutomationSchedule{
		spec: canonical, kind: AutomationScheduleCron,
		timezone: canonicalZone, location: location, cron: cron,
	}, nil
}

// parseAutomationBareCron parses a bare five-field expression (the legacy form),
// optionally followed by a time zone. The two legacy shapes keep their old
// kinds so the UI keeps labelling them as before; every other expression is a
// full Vixie cron schedule.
func parseAutomationBareCron(fields []string, spec string) (AutomationSchedule, error) {
	zone := AutomationDefaultTimeZone
	if len(fields) == 6 {
		zone = fields[5]
	}
	location, canonicalZone, err := loadAutomationLocation(zone)
	if err != nil {
		return AutomationSchedule{}, err
	}
	minuteField, hourField := fields[0], fields[1]
	restStar := fields[2] == "*" && fields[3] == "*" && fields[4] == "*"
	if len(fields) == 5 && restStar && strings.HasPrefix(minuteField, "*/") && hourField == "*" {
		raw := strings.TrimPrefix(minuteField, "*/")
		interval, convErr := strconv.Atoi(raw)
		if convErr != nil || interval < 1 || interval > 59 {
			return AutomationSchedule{}, fmt.Errorf("schedule %q: interval must be 1..59", spec)
		}
		return AutomationSchedule{spec: fmt.Sprintf("*/%d * * * *", interval), kind: AutomationScheduleEveryMinutes, every: interval}, nil
	}
	cron, err := parseAutomationCron(fields[:5])
	if err != nil {
		return AutomationSchedule{}, fmt.Errorf("schedule %q: %w", spec, err)
	}
	if restStar && isAutomationSingleNumber(minuteField) && isAutomationSingleNumber(hourField) {
		minute, _ := strconv.Atoi(minuteField)
		hour, _ := strconv.Atoi(hourField)
		if len(fields) == 5 && canonicalZone == AutomationDefaultTimeZone {
			return AutomationSchedule{
				spec: fmt.Sprintf("%d %d * * *", minute, hour),
				kind: AutomationScheduleDaily,
				hour: hour, minute: minute,
				timezone: canonicalZone, location: location,
			}, nil
		}
		return newAutomationDailySchedule("", hour, minute, 0, 0, canonicalZone)
	}
	canonical := fmt.Sprintf("cron %s %s", cron.expression, canonicalZone)
	return AutomationSchedule{
		spec: canonical, kind: AutomationScheduleCron,
		timezone: canonicalZone, location: location, cron: cron,
	}, nil
}

// isAutomationSingleNumber reports whether a cron field is one plain number
// (the legacy daily/daily-minute shape needs it).
func isAutomationSingleNumber(field string) bool {
	if field == "" || strings.ContainsAny(field, "*-/,") {
		return false
	}
	_, err := strconv.Atoi(field)
	return err == nil
}

// parseAutomationClock parses HH:MM:SS with optional one to three fractional
// digits (the DSH wall-clock time shape).
func parseAutomationClock(text string) (hour, minute, second, millis int, err error) {
	clock := strings.TrimSpace(text)
	whole := clock
	fraction := ""
	if dot := strings.IndexByte(clock, '.'); dot >= 0 {
		whole, fraction = clock[:dot], clock[dot+1:]
		if fraction == "" || len(fraction) > 3 {
			return 0, 0, 0, 0, fmt.Errorf("time %q must be HH:MM:SS with optional one to three fractional digits", clock)
		}
		for len(fraction) < 3 {
			fraction += "0"
		}
		parsed, convErr := strconv.Atoi(fraction)
		if convErr != nil {
			return 0, 0, 0, 0, fmt.Errorf("time %q has an invalid fraction", clock)
		}
		millis = parsed
	}
	parts := strings.Split(whole, ":")
	if len(parts) != 3 {
		return 0, 0, 0, 0, fmt.Errorf("time %q must be HH:MM:SS with optional one to three fractional digits", clock)
	}
	values := make([]int, 3)
	for index, part := range parts {
		if part == "" {
			return 0, 0, 0, 0, fmt.Errorf("time %q must be HH:MM:SS", clock)
		}
		value, convErr := strconv.Atoi(part)
		if convErr != nil {
			return 0, 0, 0, 0, fmt.Errorf("time %q must be HH:MM:SS", clock)
		}
		values[index] = value
	}
	hour, minute, second = values[0], values[1], values[2]
	if hour > 23 || minute > 59 || second > 59 {
		return 0, 0, 0, 0, fmt.Errorf("time %q is not a real 24-hour clock time", clock)
	}
	return hour, minute, second, millis, nil
}

// formatAutomationClock renders the canonical DSH wall-clock time (three
// fractional digits).
func formatAutomationClock(hour, minute, second, millis int) string {
	return fmt.Sprintf("%02d:%02d:%02d.%03d", hour, minute, second, millis)
}

// formatAutomationUTCInstant renders the canonical DSH absolute instant (UTC,
// millisecond precision).
func formatAutomationUTCInstant(instant time.Time) string {
	return instant.UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

// loadAutomationLocation validates and loads a DSH wall-clock time zone: UTC or
// an IANA Area/Location name.
func loadAutomationLocation(zone string) (*time.Location, string, error) {
	zone = strings.TrimSpace(zone)
	if zone == "" {
		zone = AutomationDefaultTimeZone
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return nil, "", fmt.Errorf("time zone %q is not a known IANA time zone", zone)
	}
	if location == time.UTC || location.String() == "UTC" {
		return time.UTC, "UTC", nil
	}
	return location, location.String(), nil
}

// parseAutomationWeekdays parses a comma-separated ISO weekday list (Monday=1
// .. Sunday=7). Three-letter and full English names are accepted as input; the
// canonical schedule stores numbers.
func parseAutomationWeekdays(text string) ([]int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("weekday list must not be empty")
	}
	seen := make(map[int]bool, 7)
	for _, raw := range strings.Split(text, ",") {
		item := strings.ToLower(strings.TrimSpace(raw))
		if item == "" {
			return nil, fmt.Errorf("weekday list must not contain an empty item")
		}
		weekday := 0
		if number, err := strconv.Atoi(item); err == nil {
			weekday = number
		} else if mapped, ok := automationWeekdayNames[item]; ok {
			weekday = mapped
		} else {
			return nil, fmt.Errorf("%q is not an ISO weekday (1-7) or a weekday name", item)
		}
		if weekday < 1 || weekday > 7 {
			return nil, fmt.Errorf("weekday %d is outside 1..7 (Monday=1, Sunday=7)", weekday)
		}
		seen[weekday] = true
	}
	weekdays := make([]int, 0, len(seen))
	for weekday := 1; weekday <= 7; weekday++ {
		if seen[weekday] {
			weekdays = append(weekdays, weekday)
		}
	}
	return weekdays, nil
}

// automationWeekdayNames maps weekday names to ISO numbers (Monday=1).
var automationWeekdayNames = map[string]int{
	"mon": 1, "monday": 1,
	"tue": 2, "tuesday": 2,
	"wed": 3, "wednesday": 3,
	"thu": 4, "thursday": 4,
	"fri": 5, "friday": 5,
	"sat": 6, "saturday": 6,
	"sun": 7, "sunday": 7,
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

// HourMinute returns the legacy daily hour and minute, and false for other
// kinds.
func (s AutomationSchedule) HourMinute() (hour, minute int, ok bool) {
	if s.kind != AutomationScheduleDaily {
		return 0, 0, false
	}
	return s.hour, s.minute, true
}

// TimeOfDay returns the wall-clock time of a daily/weekly schedule.
func (s AutomationSchedule) TimeOfDay() (hour, minute, second, millis int, ok bool) {
	if s.kind != AutomationScheduleDaily && s.kind != AutomationScheduleWeekly {
		return 0, 0, 0, 0, false
	}
	return s.hour, s.minute, s.second, s.millis, true
}

// Weekdays returns the ISO weekdays of a weekly schedule (Monday=1..Sunday=7,
// sorted, never nil for a weekly schedule).
func (s AutomationSchedule) Weekdays() []int {
	return append([]int(nil), s.weekdays...)
}

// TimeZone returns the IANA time zone wall-clock schedules evaluate in ("" for
// interval and one-shot-at schedules).
func (s AutomationSchedule) TimeZone() string { return s.timezone }

// CronExpression returns the canonical five-field expression of a cron
// schedule ("" for other kinds).
func (s AutomationSchedule) CronExpression() string {
	if s.kind != AutomationScheduleCron || s.cron == nil {
		return ""
	}
	return s.cron.expression
}

// At returns the absolute instant of a once-at schedule.
func (s AutomationSchedule) At() (time.Time, bool) {
	if s.kind != AutomationScheduleOnceAt {
		return time.Time{}, false
	}
	return s.at, true
}

// IntervalSeconds returns the every/after delay in seconds, and 0 otherwise.
func (s AutomationSchedule) IntervalSeconds() int {
	if s.kind == AutomationScheduleEverySeconds || s.kind == AutomationScheduleOnceAfter {
		return s.seconds
	}
	return 0
}

// Next returns the first instant strictly after from that matches the schedule,
// or zero when the schedule has no upcoming occurrence. Daily/weekly/cron
// instants are evaluated on the schedule's wall clock (AutomationDefaultTimeZone
// unless the expression names one): nonexistent local times (DST gaps) are
// skipped and ambiguous ones use the earlier instant, exactly once per date.
// A once-at schedule returns its absolute instant (even when it is already due,
// so a run missed while the host was off catches up once after a restart).
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
	case AutomationScheduleDaily, AutomationScheduleWeekly, AutomationScheduleCron:
		return s.nextWallClock(from)
	case AutomationScheduleEverySeconds, AutomationScheduleOnceAfter:
		return from.Add(time.Duration(s.seconds) * time.Second)
	case AutomationScheduleOnceAt:
		return s.at
	default:
		return time.Time{}
	}
}

// nextWallClock finds the first wall-clock occurrence strictly after from.
func (s AutomationSchedule) nextWallClock(from time.Time) time.Time {
	location := s.location
	if location == nil {
		loaded, _, err := loadAutomationLocation(s.timezone)
		if err != nil {
			return time.Time{}
		}
		location = loaded
	}
	local := from.In(location)
	// Anchor at local noon so AddDate never crosses a DST transition.
	day := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, location)
	// Nine years cover every leap-year shape (Feb 29 can be eight years apart).
	const maxDays = 366*9 + 31
	for offset := 0; offset <= maxDays; offset++ {
		current := day.AddDate(0, 0, offset)
		year, month, dayOfMonth := current.Date()
		weekday := time.Date(year, month, dayOfMonth, 0, 0, 0, 0, time.UTC).Weekday()
		if !s.matchesLocalDate(weekday, month, dayOfMonth) {
			continue
		}
		if next := s.firstWallInstant(year, month, dayOfMonth, from, location); !next.IsZero() {
			return next
		}
	}
	return time.Time{}
}

// matchesLocalDate reports whether a local date matches the wall-clock rule.
func (s AutomationSchedule) matchesLocalDate(weekday time.Weekday, month time.Month, dayOfMonth int) bool {
	switch s.kind {
	case AutomationScheduleDaily:
		return true
	case AutomationScheduleWeekly:
		iso := ((int(weekday) + 6) % 7) + 1
		for _, selected := range s.weekdays {
			if selected == iso {
				return true
			}
		}
		return false
	case AutomationScheduleCron:
		return s.cron != nil && s.cron.matchesDate(weekday, month, dayOfMonth)
	default:
		return false
	}
}

// firstWallInstant returns the first matching instant on one local date that is
// strictly after from (zero when the date has none).
func (s AutomationSchedule) firstWallInstant(year int, month time.Month, dayOfMonth int, from time.Time, location *time.Location) time.Time {
	if s.kind == AutomationScheduleCron {
		for hour := 0; hour < 24; hour++ {
			if !s.cron.hours[hour] {
				continue
			}
			for minute := 0; minute < 60; minute++ {
				if !s.cron.minutes[minute] {
					continue
				}
				candidate := automationWallInstant(year, month, dayOfMonth, hour, minute, 0, 0, location)
				if !candidate.IsZero() && candidate.After(from) {
					return candidate
				}
			}
		}
		return time.Time{}
	}
	candidate := automationWallInstant(year, month, dayOfMonth, s.hour, s.minute, s.second, s.millis, location)
	if candidate.IsZero() || !candidate.After(from) {
		return time.Time{}
	}
	return candidate
}

// automationWallInstant resolves one wall-clock time in location. A time that
// does not exist (DST gap) returns the zero time so the caller skips that date;
// an ambiguous time resolves to the earlier instant (Go's time.Date behavior),
// so each wall clock fires once per date.
func automationWallInstant(year int, month time.Month, dayOfMonth, hour, minute, second, millis int, location *time.Location) time.Time {
	instant := time.Date(year, month, dayOfMonth, hour, minute, second, millis*int(time.Millisecond), location)
	if instant.Year() != year || instant.Month() != month || instant.Day() != dayOfMonth ||
		instant.Hour() != hour || instant.Minute() != minute || instant.Second() != second ||
		instant.Nanosecond() != millis*int(time.Millisecond) {
		return time.Time{}
	}
	return instant
}

// NextAutomationRun parses spec and returns its first run strictly after from.
func NextAutomationRun(spec string, from time.Time) (time.Time, error) {
	schedule, err := ParseAutomationSchedule(spec)
	if err != nil {
		return time.Time{}, err
	}
	return schedule.Next(from), nil
}

// --- five-field Vixie cron (DSH dialect) -----------------------------------

// automationCron is one parsed five-field Vixie expression.
type automationCron struct {
	expression  string
	minutes     [60]bool
	hours       [24]bool
	daysOfMonth [32]bool
	months      [13]bool
	daysOfWeek  [7]bool // Sunday=0 .. Saturday=6 (input value 7 folds onto 0)
	domStar     bool
	dowStar     bool
}

// matchesDate applies the Vixie day rule: when either the day-of-month or the
// day-of-week field starts with '*', both fields must match; when neither does,
// either may match.
func (c *automationCron) matchesDate(weekday time.Weekday, month time.Month, dayOfMonth int) bool {
	if month < 1 || month > 12 || !c.months[int(month)] {
		return false
	}
	domMatch := dayOfMonth >= 1 && dayOfMonth <= 31 && c.daysOfMonth[dayOfMonth]
	dowMatch := c.daysOfWeek[int(weekday)%7]
	switch {
	case c.domStar && c.dowStar:
		return true
	case c.domStar:
		return dowMatch
	case c.dowStar:
		return domMatch
	default:
		return domMatch || dowMatch
	}
}

// parseAutomationCron parses the five fields of a Vixie expression, rejecting
// the names/L/W/#/question-mark/seconds-field extensions DSH rejects.
func parseAutomationCron(fields []string) (*automationCron, error) {
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron needs exactly five fields (minute hour day-of-month month day-of-week)")
	}
	minutes, minuteStar, err := parseAutomationCronField(fields[0], 0, 59, "minute")
	if err != nil {
		return nil, err
	}
	hours, hourStar, err := parseAutomationCronField(fields[1], 0, 23, "hour")
	if err != nil {
		return nil, err
	}
	daysOfMonth, domStar, err := parseAutomationCronField(fields[2], 1, 31, "day-of-month")
	if err != nil {
		return nil, err
	}
	months, monthStar, err := parseAutomationCronField(fields[3], 1, 12, "month")
	if err != nil {
		return nil, err
	}
	daysOfWeekRaw, dowStar, err := parseAutomationCronField(fields[4], 0, 7, "day-of-week")
	if err != nil {
		return nil, err
	}
	cron := &automationCron{domStar: domStar, dowStar: dowStar}
	copy(cron.minutes[:], minutes)
	copy(cron.hours[:], hours)
	copy(cron.daysOfMonth[:], daysOfMonth)
	copy(cron.months[:], months)
	daysOfWeek := make([]bool, 7)
	for value := 0; value <= 7; value++ {
		if daysOfWeekRaw[value] {
			cron.daysOfWeek[value%7] = true
			daysOfWeek[value%7] = true
		}
	}
	cron.expression = strings.Join([]string{
		renderAutomationCronField(minutes, 0, 59, minuteStar),
		renderAutomationCronField(hours, 0, 23, hourStar),
		renderAutomationCronField(daysOfMonth, 1, 31, domStar),
		renderAutomationCronField(months, 1, 12, monthStar),
		renderAutomationCronField(daysOfWeek, 0, 6, dowStar),
	}, " ")
	return cron, nil
}

// parseAutomationCronField expands one cron field into its matching values and
// reports whether the source text starts with '*' (the Vixie star flag).
func parseAutomationCronField(text string, lo, hi int, name string) ([]bool, bool, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, false, fmt.Errorf("cron %s field must not be empty", name)
	}
	star := strings.HasPrefix(text, "*")
	values := make([]bool, hi+1)
	for _, raw := range strings.Split(text, ",") {
		element := strings.TrimSpace(raw)
		if element == "" {
			return nil, false, fmt.Errorf("cron %s field must not contain an empty list element", name)
		}
		step := 1
		rangeText := element
		if slash := strings.IndexByte(element, '/'); slash >= 0 {
			rangeText = strings.TrimSpace(element[:slash])
			stepText := strings.TrimSpace(element[slash+1:])
			parsedStep, convErr := strconv.Atoi(stepText)
			if convErr != nil || parsedStep < 1 {
				return nil, false, fmt.Errorf("cron %s field element %q must be *, a value, a-b, */n, a-b/n, or a comma-separated list of those", name, element)
			}
			step = parsedStep
		}
		start, end := lo, hi
		switch {
		case rangeText == "*":
		case strings.ContainsRune(rangeText, '-'):
			bounds := strings.SplitN(rangeText, "-", 2)
			startValue, err := parseAutomationCronValue(bounds[0], lo, hi, name)
			if err != nil {
				return nil, false, err
			}
			endValue, err := parseAutomationCronValue(bounds[1], lo, hi, name)
			if err != nil {
				return nil, false, err
			}
			if startValue > endValue {
				return nil, false, fmt.Errorf("cron %s field range %d-%d is inverted", name, startValue, endValue)
			}
			start, end = startValue, endValue
		default:
			value, err := parseAutomationCronValue(rangeText, lo, hi, name)
			if err != nil {
				return nil, false, err
			}
			if step != 1 {
				return nil, false, fmt.Errorf("cron %s field element %q must be *, a value, a-b, */n, a-b/n, or a comma-separated list of those", name, element)
			}
			start, end = value, value
		}
		for value := start; value <= end; value += step {
			values[value] = true
		}
	}
	return values, star, nil
}

// parseAutomationCronValue parses one numeric cron value in [lo, hi].
func parseAutomationCronValue(text string, lo, hi int, name string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return 0, fmt.Errorf("cron %s field element %q must be *, a value, a-b, */n, a-b/n, or a comma-separated list of those", name, strings.TrimSpace(text))
	}
	if value < lo || value > hi {
		return 0, fmt.Errorf("cron %s field value %d is outside %d-%d", name, value, lo, hi)
	}
	return value, nil
}

// renderAutomationCronField renders one matching set in the canonical DSH
// spelling. starField reproduces the DSH rule that a source field starting
// with '*' keeps the star shape (widest star step, then the remaining values).
func renderAutomationCronField(values []bool, lo, hi int, starField bool) string {
	matched := make([]int, 0, hi-lo+1)
	for value := lo; value <= hi; value++ {
		if values[value] {
			matched = append(matched, value)
		}
	}
	if starField {
		return renderAutomationStarCronField(matched, lo, hi)
	}
	return encodeAutomationCronField(matched)
}

// renderAutomationStarCronField encodes a source field that started with '*'.
func renderAutomationStarCronField(values []int, lo, hi int) string {
	if len(values) == hi-lo+1 {
		return "*"
	}
	present := make(map[int]bool, len(values))
	for _, value := range values {
		present[value] = true
	}
	bestStep := 0
	bestWalk := []int(nil)
	for step := 1; step <= hi-lo+1; step++ {
		walk := automationStarWalk(lo, hi, step)
		if len(walk) <= len(bestWalk) {
			continue
		}
		contained := true
		for _, value := range walk {
			if !present[value] {
				contained = false
				break
			}
		}
		if contained {
			bestStep = step
			bestWalk = walk
		}
	}
	if bestStep == 0 {
		return encodeAutomationCronField(values)
	}
	walked := make(map[int]bool, len(bestWalk))
	for _, value := range bestWalk {
		walked[value] = true
	}
	remaining := make([]int, 0, len(values))
	for _, value := range values {
		if !walked[value] {
			remaining = append(remaining, value)
		}
	}
	walkText := fmt.Sprintf("*/%d", bestStep)
	if len(remaining) == 0 {
		return walkText
	}
	return walkText + "," + encodeAutomationCronField(remaining)
}

// automationStarWalk returns the ascending values a */step field matches.
func automationStarWalk(lo, hi, step int) []int {
	values := make([]int, 0, (hi-lo)/step+1)
	for value := lo; value <= hi; value += step {
		values = append(values, value)
	}
	return values
}

// encodeAutomationCronField renders a matching set with the shortest
// equivalent comma-separated spelling (never a leading '*').
func encodeAutomationCronField(values []int) string {
	if len(values) == 0 {
		return ""
	}
	if len(values) == 1 {
		return strconv.Itoa(values[0])
	}
	first, last := values[0], values[len(values)-1]
	step := values[1] - first
	uniform := true
	for index := 2; index < len(values); index++ {
		if values[index]-values[index-1] != step {
			uniform = false
			break
		}
	}
	if uniform {
		if step == 1 {
			return fmt.Sprintf("%d-%d", first, last)
		}
		return fmt.Sprintf("%d-%d/%d", first, last, step)
	}
	var builder strings.Builder
	runStart := first
	for index := 1; index < len(values); index++ {
		current, previous := values[index], values[index-1]
		if current == previous+1 {
			continue
		}
		if runStart == previous {
			builder.WriteString(strconv.Itoa(runStart))
		} else {
			fmt.Fprintf(&builder, "%d-%d", runStart, previous)
		}
		builder.WriteByte(',')
		runStart = current
	}
	if runStart == last {
		builder.WriteString(strconv.Itoa(runStart))
	} else {
		fmt.Fprintf(&builder, "%d-%d", runStart, last)
	}
	return builder.String()
}
