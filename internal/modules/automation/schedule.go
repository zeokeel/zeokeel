package automation

// Schedule parsing for the automation backend now delegates to the pure parser
// in internal/contracts: the UI validates and previews drafts with exactly the
// rules the backend executes (one parser, no drift). The local names below are
// kept so the service's call sites and tests read the same as before.
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
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Kind is the shape of a parsed schedule.
type Kind = contracts.AutomationScheduleKind

const (
	// KindEveryMinute fires every N minutes.
	KindEveryMinute = contracts.AutomationScheduleEveryMinutes
	// KindDaily fires once per day at a fixed hour and minute.
	KindDaily = contracts.AutomationScheduleDaily
	// KindHourly fires every hour at minute 0.
	KindHourly = contracts.AutomationScheduleHourly
	// KindEvery fires every fixed number of seconds.
	KindEvery = contracts.AutomationScheduleEverySeconds
	// KindAfter fires once after a delay and then disables the task.
	KindAfter = contracts.AutomationScheduleOnceAfter
)

// Schedule is a parsed schedule expression.
type Schedule = contracts.AutomationSchedule

// Parse validates spec and returns its Schedule.
func Parse(spec string) (Schedule, error) {
	schedule, err := contracts.ParseAutomationSchedule(spec)
	if err != nil {
		return Schedule{}, fmt.Errorf("automation: %w", err)
	}
	return schedule, nil
}

// Validate reports whether spec is a supported schedule expression.
func Validate(spec string) error {
	return contracts.ValidateAutomationSchedule(spec)
}

// NextRun parses spec and returns its first run strictly after from.
func NextRun(spec string, from time.Time) (time.Time, error) {
	schedule, err := Parse(spec)
	if err != nil {
		return time.Time{}, err
	}
	return schedule.Next(from), nil
}

// Describe renders a short stable English label for a schedule; invalid
// schedules are returned unchanged. Callers that localize copy build their own
// label from the parsed Schedule (the settings UI does).
func Describe(spec string) string {
	schedule, err := Parse(spec)
	if err != nil {
		return spec
	}
	switch schedule.Kind() {
	case KindEveryMinute:
		if schedule.EveryMinutes() == 1 {
			return "Every minute"
		}
		return fmt.Sprintf("Every %d minutes", schedule.EveryMinutes())
	case KindHourly:
		return "Every hour"
	case KindDaily:
		hour, minute, _ := schedule.HourMinute()
		return fmt.Sprintf("Daily at %02d:%02d", hour, minute)
	case KindEvery:
		if schedule.IntervalSeconds() == 1 {
			return "Every second"
		}
		return fmt.Sprintf("Every %d seconds", schedule.IntervalSeconds())
	case KindAfter:
		if schedule.IntervalSeconds() == 1 {
			return "Once after 1 second"
		}
		return fmt.Sprintf("Once after %d seconds", schedule.IntervalSeconds())
	default:
		return spec
	}
}
