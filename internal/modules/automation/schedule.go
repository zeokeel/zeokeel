package automation

// Schedule parsing for the automation backend now delegates to the pure parser
// in internal/contracts: the UI validates and previews drafts with exactly the
// rules the backend executes (one parser, no drift). The local names below are
// kept so the service's call sites and tests read the same as before.
//
// Supported expressions (canonical forms match the DSH selectors the Rig
// schedule tools store):
//
//	*/N * * * *                          every N minutes (1 <= N <= 59, legacy)
//	M H * * *                            daily at hour H and minute M (legacy)
//	@hourly                              every hour at minute 0
//	@daily                               every day at local midnight
//	every <N>s                           every N seconds
//	after <N>s                           once, N seconds after scheduling
//	at <RFC3339>                         once at an absolute instant
//	daily HH:MM:SS[.fff] [zone]          daily wall-clock time
//	weekly HH:MM:SS[.fff] [zone] <days>  weekly wall-clock time (ISO 1..7)
//	cron <five fields> [zone]            five-field Vixie cron
//
// Everything else is rejected with a readable error. Wall-clock expressions are
// evaluated in Asia/Shanghai unless they name an IANA time zone; the service
// keeps persisted instants in UTC.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Kind is the shape of a parsed schedule.
type Kind = contracts.AutomationScheduleKind

const (
	// KindEveryMinute fires every N minutes.
	KindEveryMinute = contracts.AutomationScheduleEveryMinutes
	// KindDaily fires once per day at a fixed wall-clock time.
	KindDaily = contracts.AutomationScheduleDaily
	// KindHourly fires every hour at minute 0.
	KindHourly = contracts.AutomationScheduleHourly
	// KindEvery fires every fixed number of seconds.
	KindEvery = contracts.AutomationScheduleEverySeconds
	// KindAfter fires once after a delay and then disables the task.
	KindAfter = contracts.AutomationScheduleOnceAfter
	// KindWeekly fires on selected ISO weekdays at a wall-clock time.
	KindWeekly = contracts.AutomationScheduleWeekly
	// KindCron fires by a five-field Vixie cron expression.
	KindCron = contracts.AutomationScheduleCron
	// KindAt fires once at an absolute instant and then disables the task.
	KindAt = contracts.AutomationScheduleOnceAt
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
		hour, minute, second, _, _ := schedule.TimeOfDay()
		return withScheduleZone(schedule, "Daily at "+formatDescribeClock(hour, minute, second))
	case KindWeekly:
		hour, minute, second, _, _ := schedule.TimeOfDay()
		names := make([]string, 0, len(schedule.Weekdays()))
		for _, weekday := range schedule.Weekdays() {
			names = append(names, describeWeekday(weekday))
		}
		label := fmt.Sprintf("Weekly on %s at %s", strings.Join(names, ", "), formatDescribeClock(hour, minute, second))
		return withScheduleZone(schedule, label)
	case KindCron:
		return withScheduleZone(schedule, "Cron "+schedule.CronExpression())
	case KindAt:
		if instant, ok := schedule.At(); ok {
			return "Once at " + instant.UTC().Format("2006-01-02 15:04:05 UTC")
		}
		return spec
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

// withScheduleZone appends the explicit time zone when it differs from the
// product default (legacy interval labels stay zone-free).
func withScheduleZone(schedule Schedule, label string) string {
	zone := schedule.TimeZone()
	if zone == "" || zone == contracts.AutomationDefaultTimeZone {
		return label
	}
	return label + " (" + zone + ")"
}

// formatDescribeClock renders HH:MM, adding seconds only when set.
func formatDescribeClock(hour, minute, second int) string {
	if second == 0 {
		return fmt.Sprintf("%02d:%02d", hour, minute)
	}
	return fmt.Sprintf("%02d:%02d:%02d", hour, minute, second)
}

// describeWeekday renders one ISO weekday (Monday=1) as a short English name.
func describeWeekday(weekday int) string {
	names := [...]string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	if weekday < 1 || weekday > 7 {
		return strconv.Itoa(weekday)
	}
	return names[weekday-1]
}
