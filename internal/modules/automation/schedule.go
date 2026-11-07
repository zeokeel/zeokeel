package automation

// Schedule parsing for the automation backend: a small, pure-Go cron subset
// ported from the old shell's automation package, extended with the typed
// "every <N>s" (fixed interval) and "after <N>s" (one-shot) forms.
//
// Supported expressions:
//
//	*/N * * * *   every N minutes (1 <= N <= 59)
//	M H * * *     daily at hour H and minute M
//	@hourly       every hour at minute 0
//	@daily        every day at midnight
//	every <N>s    every N seconds
//	after <N>s    once, N seconds after scheduling
//
// Everything else is rejected with a readable error. Times are computed in the
// reference time's location; the service keeps persisted instants in UTC.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Kind is the shape of a parsed schedule.
type Kind int

const (
	// KindEveryMinute fires every N minutes.
	KindEveryMinute Kind = iota
	// KindDaily fires once per day at a fixed hour and minute.
	KindDaily
	// KindHourly fires every hour at minute 0.
	KindHourly
	// KindEvery fires every fixed number of seconds.
	KindEvery
	// KindAfter fires once after a delay and then disables the task.
	KindAfter
)

// Schedule is a parsed schedule expression.
type Schedule struct {
	spec    string
	kind    Kind
	every   int // minutes for KindEveryMinute
	minute  int // KindDaily
	hour    int // KindDaily
	seconds int // KindEvery / KindAfter
}

// String returns the canonical expression.
func (s Schedule) String() string { return s.spec }

// Kind reports the schedule's shape.
func (s Schedule) Kind() Kind { return s.kind }

// Every returns the minute interval for KindEveryMinute, and 0 otherwise.
func (s Schedule) Every() int {
	if s.kind == KindEveryMinute {
		return s.every
	}
	return 0
}

// HourMinute returns the daily hour and minute, and false for other kinds.
func (s Schedule) HourMinute() (hour, minute int, ok bool) {
	if s.kind != KindDaily {
		return 0, 0, false
	}
	return s.hour, s.minute, true
}

// Parse validates spec and returns its Schedule.
func Parse(spec string) (Schedule, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return Schedule{}, fmt.Errorf("automation: empty schedule expression")
	}
	lower := strings.ToLower(trimmed)
	switch lower {
	case "@hourly":
		return Schedule{spec: "@hourly", kind: KindHourly}, nil
	case "@daily", "@midnight":
		return Schedule{spec: "@daily", kind: KindDaily}, nil
	}
	fields := strings.Fields(lower)
	if len(fields) == 2 {
		switch fields[0] {
		case "every":
			seconds, err := parseDelay(fields[1], "every")
			if err != nil {
				return Schedule{}, fmt.Errorf("automation: schedule %q: %w", spec, err)
			}
			return Schedule{spec: fmt.Sprintf("every %ds", seconds), kind: KindEvery, seconds: seconds}, nil
		case "after":
			seconds, err := parseDelay(fields[1], "after")
			if err != nil {
				return Schedule{}, fmt.Errorf("automation: schedule %q: %w", spec, err)
			}
			return Schedule{spec: fmt.Sprintf("after %ds", seconds), kind: KindAfter, seconds: seconds}, nil
		}
	}
	parts := strings.Fields(trimmed)
	if len(parts) != 5 {
		return Schedule{}, fmt.Errorf("automation: schedule %q: want 5 cron fields, @hourly, @daily, every <N>s or after <N>s", spec)
	}
	for index := 2; index < 5; index++ {
		if parts[index] != "*" {
			return Schedule{}, fmt.Errorf("automation: cron %q: field %d must be *", spec, index+1)
		}
	}
	minuteField, hourField := parts[0], parts[1]
	if strings.HasPrefix(minuteField, "*/") {
		if hourField != "*" {
			return Schedule{}, fmt.Errorf("automation: cron %q: */N needs * for hours", spec)
		}
		raw := strings.TrimPrefix(minuteField, "*/")
		interval, err := strconv.Atoi(raw)
		if err != nil || interval < 1 || interval > 59 {
			return Schedule{}, fmt.Errorf("automation: cron %q: interval must be 1..59", spec)
		}
		return Schedule{spec: strings.TrimSpace(spec), kind: KindEveryMinute, every: interval}, nil
	}
	minute, err := parseField(minuteField, 0, 59, "minute")
	if err != nil {
		return Schedule{}, fmt.Errorf("automation: cron %q: %w", spec, err)
	}
	hour, err := parseField(hourField, 0, 23, "hour")
	if err != nil {
		return Schedule{}, fmt.Errorf("automation: cron %q: %w", spec, err)
	}
	return Schedule{spec: strings.TrimSpace(spec), kind: KindDaily, minute: minute, hour: hour}, nil
}

// parseDelay parses the "<N>s" argument of every/after.
func parseDelay(field, kind string) (int, error) {
	if !strings.HasSuffix(field, "s") {
		return 0, fmt.Errorf("%s needs a whole number of seconds like %q", kind, "30s")
	}
	seconds, err := strconv.Atoi(strings.TrimSuffix(field, "s"))
	if err != nil || seconds < 1 {
		return 0, fmt.Errorf("%s interval must be a positive whole number of seconds", kind)
	}
	return seconds, nil
}

// parseField parses one numeric cron field within [lo, hi].
func parseField(field string, lo, hi int, name string) (int, error) {
	value, err := strconv.Atoi(field)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a number", name, field)
	}
	if value < lo || value > hi {
		return 0, fmt.Errorf("%s %d out of range %d..%d", name, value, lo, hi)
	}
	return value, nil
}

// Validate reports whether spec is a supported schedule expression.
func Validate(spec string) error {
	_, err := Parse(spec)
	return err
}

// Next returns the first instant strictly after from that matches the schedule,
// in from's location (zero for the zero Schedule).
func (s Schedule) Next(from time.Time) time.Time {
	switch s.kind {
	case KindEveryMinute:
		next := from.Truncate(time.Minute).Add(time.Minute)
		for next.Minute()%s.every != 0 {
			next = next.Add(time.Minute)
		}
		return next
	case KindHourly:
		return time.Date(from.Year(), from.Month(), from.Day(), from.Hour(), 0, 0, 0, from.Location()).Add(time.Hour)
	case KindDaily:
		next := time.Date(from.Year(), from.Month(), from.Day(), s.hour, s.minute, 0, 0, from.Location())
		if !next.After(from) {
			next = next.AddDate(0, 0, 1)
		}
		return next
	case KindEvery, KindAfter:
		return from.Add(time.Duration(s.seconds) * time.Second)
	default:
		return time.Time{}
	}
}

// NextRun parses spec and returns its first run strictly after from.
func NextRun(spec string, from time.Time) (time.Time, error) {
	schedule, err := Parse(spec)
	if err != nil {
		return time.Time{}, err
	}
	return schedule.Next(from), nil
}

// Describe renders a short human label for a schedule; invalid schedules are
// returned unchanged.
func Describe(spec string) string {
	schedule, err := Parse(spec)
	if err != nil {
		return spec
	}
	switch schedule.kind {
	case KindEveryMinute:
		if schedule.every == 1 {
			return "Every minute"
		}
		return fmt.Sprintf("Every %d minutes", schedule.every)
	case KindHourly:
		return "Every hour"
	case KindDaily:
		return fmt.Sprintf("Daily at %02d:%02d", schedule.hour, schedule.minute)
	case KindEvery:
		if schedule.seconds == 1 {
			return "Every second"
		}
		return fmt.Sprintf("Every %d seconds", schedule.seconds)
	case KindAfter:
		if schedule.seconds == 1 {
			return "Once after 1 second"
		}
		return fmt.Sprintf("Once after %d seconds", schedule.seconds)
	default:
		return spec
	}
}
