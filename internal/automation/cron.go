// Package automation implements ZeoKeel's task scheduler: a small, pure-Go
// cron subset, next-run computation, and a scheduler that fires persisted
// automation tasks through an OnFire callback.
//
// The supported cron expressions are deliberately narrow:
//
//	*/N * * * *   every N minutes (1 <= N <= 59)
//	M H * * *     daily at hour H and minute M
//	@hourly       every hour at minute 0
//	@daily        every day at midnight
//
// All computations are local to the reference time's location, so callers
// that keep times in UTC get UTC schedules.
package automation

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
)

// Schedule is a parsed cron subset expression.
type Schedule struct {
	spec   string
	kind   Kind
	every  int // minutes, KindEveryMinute
	minute int // KindDaily
	hour   int // KindDaily
}

// String returns the canonical expression, or the original spec.
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

// Parse validates spec and returns its Schedule. It rejects anything outside
// the documented subset, including wildcard hour fields, lists, ranges and
// step values other than "*/N".
func Parse(spec string) (Schedule, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return Schedule{}, fmt.Errorf("automation: empty cron expression")
	}
	lower := strings.ToLower(trimmed)
	switch lower {
	case "@hourly":
		return Schedule{spec: trimmed, kind: KindHourly}, nil
	case "@daily", "@midnight":
		return Schedule{spec: trimmed, kind: KindDaily}, nil
	}

	fields := strings.Fields(trimmed)
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf("automation: cron %q: want 5 fields, got %d", spec, len(fields))
	}
	for i := 2; i < 5; i++ {
		if fields[i] != "*" {
			return Schedule{}, fmt.Errorf("automation: cron %q: field %d must be *", spec, i+1)
		}
	}

	minuteField, hourField := fields[0], fields[1]
	switch {
	case strings.HasPrefix(minuteField, "*/"):
		if hourField != "*" {
			return Schedule{}, fmt.Errorf("automation: cron %q: */N needs * for hours", spec)
		}
		raw := strings.TrimPrefix(minuteField, "*/")
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 59 {
			return Schedule{}, fmt.Errorf("automation: cron %q: interval must be 1..59", spec)
		}
		return Schedule{spec: trimmed, kind: KindEveryMinute, every: n}, nil
	default:
		minute, err := parseRange(minuteField, 0, 59, "minute")
		if err != nil {
			return Schedule{}, fmt.Errorf("automation: cron %q: %w", spec, err)
		}
		hour, err := parseRange(hourField, 0, 23, "hour")
		if err != nil {
			return Schedule{}, fmt.Errorf("automation: cron %q: %w", spec, err)
		}
		return Schedule{spec: trimmed, kind: KindDaily, minute: minute, hour: hour}, nil
	}
}

// Validate reports whether spec is a supported cron expression.
func Validate(spec string) error {
	_, err := Parse(spec)
	return err
}

// parseRange parses a plain decimal cron field within [lo, hi].
func parseRange(field string, lo, hi int, name string) (int, error) {
	n, err := strconv.Atoi(field)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a number", name, field)
	}
	if n < lo || n > hi {
		return 0, fmt.Errorf("%s %d out of range %d..%d", name, n, lo, hi)
	}
	return n, nil
}

// Next returns the first instant strictly after from that matches the
// schedule, in from's location.
func (s Schedule) Next(from time.Time) time.Time {
	switch s.kind {
	case KindEveryMinute:
		next := from.Truncate(time.Minute).Add(time.Minute)
		for next.Minute()%s.every != 0 {
			next = next.Add(time.Minute)
		}
		return next
	case KindHourly:
		next := time.Date(from.Year(), from.Month(), from.Day(), from.Hour(), 0, 0, 0, from.Location()).
			Add(time.Hour)
		return next
	case KindDaily:
		next := time.Date(from.Year(), from.Month(), from.Day(), s.hour, s.minute, 0, 0, from.Location())
		if !next.After(from) {
			next = next.AddDate(0, 0, 1)
		}
		return next
	default:
		// Zero Schedule: never fires.
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

// Describe renders a short human label for a cron expression, used by the
// Automation screen. Invalid schedules are returned unchanged.
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
	default:
		return spec
	}
}
