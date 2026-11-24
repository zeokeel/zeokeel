package contracts

// Tests for the automation schedule parser (legacy forms + the DSH typed
// selectors the Rig schedule tools store) and the run record's JSON
// compatibility.

import (
	"encoding/json"
	"testing"
	"time"
)

// shanghai returns the Asia/Shanghai location or skips when no tzdata exists.
func shanghai(t *testing.T) *time.Location {
	t.Helper()
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("time zone data unavailable: %v", err)
	}
	return location
}

func TestParseAutomationScheduleLegacyForms(t *testing.T) {
	valid := []struct {
		spec      string
		kind      AutomationScheduleKind
		canonical string
		every     int
	}{
		{spec: "*/15 * * * *", kind: AutomationScheduleEveryMinutes, canonical: "*/15 * * * *", every: 15},
		{spec: "*/1 * * * *", kind: AutomationScheduleEveryMinutes, canonical: "*/1 * * * *", every: 1},
		{spec: "30 9 * * *", kind: AutomationScheduleDaily, canonical: "30 9 * * *"},
		{spec: "@hourly", kind: AutomationScheduleHourly, canonical: "@hourly"},
		{spec: "@daily", kind: AutomationScheduleDaily, canonical: "@daily"},
		{spec: "@midnight", kind: AutomationScheduleDaily, canonical: "@daily"},
		{spec: "every 30s", kind: AutomationScheduleEverySeconds, canonical: "every 30s"},
		{spec: "EVERY 5S", kind: AutomationScheduleEverySeconds, canonical: "every 5s"},
		{spec: "after 90s", kind: AutomationScheduleOnceAfter, canonical: "after 90s"},
	}
	for _, testCase := range valid {
		schedule, err := ParseAutomationSchedule(testCase.spec)
		if err != nil {
			t.Fatalf("Parse(%q): %v", testCase.spec, err)
		}
		if schedule.Kind() != testCase.kind {
			t.Fatalf("Parse(%q) kind = %v, want %v", testCase.spec, schedule.Kind(), testCase.kind)
		}
		if schedule.String() != testCase.canonical {
			t.Fatalf("Parse(%q) canonical = %q, want %q", testCase.spec, schedule.String(), testCase.canonical)
		}
		if testCase.kind == AutomationScheduleEveryMinutes && schedule.EveryMinutes() != testCase.every {
			t.Fatalf("EveryMinutes(%q) = %d, want %d", testCase.spec, schedule.EveryMinutes(), testCase.every)
		}
		if err := ValidateAutomationSchedule(testCase.spec); err != nil {
			t.Fatalf("Validate(%q): %v", testCase.spec, err)
		}
	}
}

func TestParseAutomationScheduleSelectors(t *testing.T) {
	valid := []struct {
		spec      string
		kind      AutomationScheduleKind
		canonical string
	}{
		{spec: "daily 09:00:00 Asia/Shanghai", kind: AutomationScheduleDaily, canonical: "daily 09:00:00.000 Asia/Shanghai"},
		{spec: "daily 23:00:00.5 UTC", kind: AutomationScheduleDaily, canonical: "daily 23:00:00.500 UTC"},
		{spec: "DAILY 09:00:00", kind: AutomationScheduleDaily, canonical: "daily 09:00:00.000 Asia/Shanghai"},
		{spec: "weekly 09:00:00 Asia/Shanghai 1,3", kind: AutomationScheduleWeekly, canonical: "weekly 09:00:00.000 Asia/Shanghai 1,3"},
		{spec: "weekly 09:00:00.000 UTC 5,1,3", kind: AutomationScheduleWeekly, canonical: "weekly 09:00:00.000 UTC 1,3,5"},
		{spec: "weekly 09:00:00 Mon,wed,fri", kind: AutomationScheduleWeekly, canonical: "weekly 09:00:00.000 Asia/Shanghai 1,3,5"},
		{spec: "cron 0 9 * * 1-5 Asia/Shanghai", kind: AutomationScheduleCron, canonical: "cron 0 9 * * 1-5 Asia/Shanghai"},
		{spec: "cron 0-59/15 * * * * UTC", kind: AutomationScheduleCron, canonical: "cron 0-45/15 * * * * UTC"},
		{spec: "cron 0 0 * * 1,3,5 UTC", kind: AutomationScheduleCron, canonical: "cron 0 0 * * 1-5/2 UTC"},
		{spec: "cron 0 0 * * 7 UTC", kind: AutomationScheduleCron, canonical: "cron 0 0 * * 0 UTC"},
		{spec: "cron */1 * * * * UTC", kind: AutomationScheduleCron, canonical: "cron * * * * * UTC"},
		{spec: "cron 1,2,3,10,11 * * * * UTC", kind: AutomationScheduleCron, canonical: "cron 1-3,10-11 * * * * UTC"},
		{spec: "cron 0 0 1,15 * * UTC", kind: AutomationScheduleCron, canonical: "cron 0 0 1-15/14 * * UTC"},
		{spec: "cron 0 9 * * 1-5", kind: AutomationScheduleCron, canonical: "cron 0 9 * * 1-5 Asia/Shanghai"},
		{spec: "at 2026-12-01T09:30:00Z", kind: AutomationScheduleOnceAt, canonical: "at 2026-12-01T09:30:00.000Z"},
		{spec: "at 2026-12-01T17:30:00+08:00", kind: AutomationScheduleOnceAt, canonical: "at 2026-12-01T09:30:00.000Z"},
	}
	for _, testCase := range valid {
		schedule, err := ParseAutomationSchedule(testCase.spec)
		if err != nil {
			t.Fatalf("Parse(%q): %v", testCase.spec, err)
		}
		if schedule.Kind() != testCase.kind {
			t.Fatalf("Parse(%q) kind = %v, want %v", testCase.spec, schedule.Kind(), testCase.kind)
		}
		if schedule.String() != testCase.canonical {
			t.Fatalf("Parse(%q) canonical = %q, want %q", testCase.spec, schedule.String(), testCase.canonical)
		}
	}
}

func TestAutomationScheduleAccessors(t *testing.T) {
	daily, err := ParseAutomationSchedule("daily 09:05:07.250 Asia/Shanghai")
	if err != nil {
		t.Fatalf("Parse daily: %v", err)
	}
	hour, minute, second, millis, ok := daily.TimeOfDay()
	if !ok || hour != 9 || minute != 5 || second != 7 || millis != 250 {
		t.Fatalf("TimeOfDay = %d:%d:%d.%d/%v", hour, minute, second, millis, ok)
	}
	if daily.TimeZone() != "Asia/Shanghai" {
		t.Fatalf("TimeZone = %q", daily.TimeZone())
	}
	week, err := ParseAutomationSchedule("weekly 09:00:00 UTC 3,1")
	if err != nil {
		t.Fatalf("Parse weekly: %v", err)
	}
	if got := week.Weekdays(); len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("Weekdays = %v", got)
	}
	cron, err := ParseAutomationSchedule("cron */15 9-17 * * 1-5 Asia/Shanghai")
	if err != nil {
		t.Fatalf("Parse cron: %v", err)
	}
	if cron.CronExpression() != "*/15 9-17 * * 1-5" {
		t.Fatalf("CronExpression = %q", cron.CronExpression())
	}
	at, err := ParseAutomationSchedule("at 2026-12-01T09:30:00.000Z")
	if err != nil {
		t.Fatalf("Parse at: %v", err)
	}
	instant, ok := at.At()
	if !ok || !instant.Equal(time.Date(2026, 12, 1, 9, 30, 0, 0, time.UTC)) {
		t.Fatalf("At = %s/%v", instant, ok)
	}
}

func TestNextAutomationRunWallClock(t *testing.T) {
	location := shanghai(t)
	// 2026-01-01 is a Thursday; 2026-01-05 is the next Monday.
	cases := []struct {
		spec   string
		from   time.Time
		expect time.Time
	}{
		{
			spec:   "daily 09:00:00.000 Asia/Shanghai",
			from:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			expect: time.Date(2026, 1, 1, 9, 0, 0, 0, location),
		},
		{
			spec:   "daily 09:00:00.000 Asia/Shanghai",
			from:   time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC),
			expect: time.Date(2026, 1, 2, 9, 0, 0, 0, location),
		},
		{
			// Legacy "M H * * *" is evaluated in the default wall clock too.
			spec:   "30 9 * * *",
			from:   time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC),
			expect: time.Date(2026, 1, 2, 9, 30, 0, 0, location),
		},
		{
			// Wednesday 10:00 local -> the next selected weekday is Monday.
			spec:   "weekly 09:00:00.000 Asia/Shanghai 1,3",
			from:   time.Date(2026, 1, 7, 10, 0, 0, 0, location),
			expect: time.Date(2026, 1, 12, 9, 0, 0, 0, location),
		},
		{
			// From Wednesday 08:00 local, the same Wednesday 09:00 is next.
			spec:   "weekly 09:00:00.000 Asia/Shanghai 1,3",
			from:   time.Date(2026, 1, 7, 8, 0, 0, 0, location),
			expect: time.Date(2026, 1, 7, 9, 0, 0, 0, location),
		},
		{
			spec:   "cron 0 9 * * 1-5 Asia/Shanghai",
			from:   time.Date(2026, 1, 9, 9, 30, 0, 0, location),
			expect: time.Date(2026, 1, 12, 9, 0, 0, 0, location),
		},
		{
			// Neither day field starts with '*': either matches (the 1st or a
			// Monday).
			spec:   "cron 0 0 1 * 1 UTC",
			from:   time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
			expect: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC),
		},
		{
			// A star day-of-week forces the day-of-month to match too.
			spec:   "cron 0 0 * * 1 UTC",
			from:   time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
			expect: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC),
		},
		{
			spec:   "cron 0 0 15 * * UTC",
			from:   time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
			expect: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		},
		{
			// @daily is evaluated at local midnight.
			spec:   "@daily",
			from:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			expect: time.Date(2026, 1, 1, 16, 0, 0, 0, time.UTC),
		},
	}
	for _, testCase := range cases {
		next, err := NextAutomationRun(testCase.spec, testCase.from)
		if err != nil {
			t.Fatalf("NextAutomationRun(%q): %v", testCase.spec, err)
		}
		if !next.Equal(testCase.expect) {
			t.Fatalf("NextAutomationRun(%q, %s) = %s, want %s",
				testCase.spec, testCase.from, next, testCase.expect)
		}
	}
}

// TestNextAutomationRunFractionalSeconds pins millisecond wall-clock precision
// (the DSH selector stores HH:MM:SS.mmm).
func TestNextAutomationRunFractionalSeconds(t *testing.T) {
	from := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	next, err := NextAutomationRun("daily 09:00:00.500 UTC", from)
	if err != nil {
		t.Fatalf("NextAutomationRun: %v", err)
	}
	if want := time.Date(2026, 1, 1, 9, 0, 0, 500*int(time.Millisecond), time.UTC); !next.Equal(want) {
		t.Fatalf("NextAutomationRun = %s, want %s", next, want)
	}
}

func TestNextAutomationRunIntervals(t *testing.T) {
	from := time.Date(2026, 1, 1, 10, 3, 30, 0, time.UTC)
	cases := []struct {
		spec   string
		expect time.Time
	}{
		{spec: "*/15 * * * *", expect: time.Date(2026, 1, 1, 10, 15, 0, 0, time.UTC)},
		{spec: "*/1 * * * *", expect: time.Date(2026, 1, 1, 10, 4, 0, 0, time.UTC)},
		{spec: "@hourly", expect: time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)},
		{spec: "every 30s", expect: time.Date(2026, 1, 1, 10, 4, 0, 0, time.UTC)},
		{spec: "after 90s", expect: time.Date(2026, 1, 1, 10, 5, 0, 0, time.UTC)},
		{spec: "at 2026-12-01T09:30:00.000Z", expect: time.Date(2026, 12, 1, 9, 30, 0, 0, time.UTC)},
	}
	for _, testCase := range cases {
		next, err := NextAutomationRun(testCase.spec, from)
		if err != nil {
			t.Fatalf("NextAutomationRun(%q): %v", testCase.spec, err)
		}
		if !next.Equal(testCase.expect) {
			t.Fatalf("NextAutomationRun(%q) = %s, want %s", testCase.spec, next, testCase.expect)
		}
	}
}

// TestNextAutomationRunDaylightSaving pins the DSH DST rules: a nonexistent
// local time is skipped and an ambiguous one uses the earlier instant, once.
func TestNextAutomationRunDaylightSaving(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("time zone data unavailable: %v", err)
	}
	// 2026-03-08 02:30 does not exist in New York: skip to the next day.
	next, err := NextAutomationRun("daily 02:30:00.000 America/New_York",
		time.Date(2026, 3, 8, 0, 0, 0, 0, newYork))
	if err != nil {
		t.Fatalf("NextAutomationRun (gap): %v", err)
	}
	if want := time.Date(2026, 3, 9, 2, 30, 0, 0, newYork); !next.Equal(want) {
		t.Fatalf("DST gap next = %s, want %s", next, want)
	}
	// 2026-11-01 01:30 is ambiguous (EDT then EST): the earlier instant wins.
	next, err = NextAutomationRun("daily 01:30:00.000 America/New_York",
		time.Date(2026, 11, 1, 0, 0, 0, 0, newYork))
	if err != nil {
		t.Fatalf("NextAutomationRun (overlap): %v", err)
	}
	if !next.Equal(time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)) {
		t.Fatalf("DST overlap next = %s, want the earlier EDT instant", next.UTC())
	}
}

func TestParseAutomationScheduleRejectsInvalid(t *testing.T) {
	invalid := []string{
		"", "   ",
		"*/0 * * * *", "*/60 * * * *",
		"60 1 * * *", "1 24 * * *", "1 2 3 4", "1 2 3 4 5 6",
		"every 0s", "every -5s", "every 5m", "every", "after 0s", "after x",
		"foo", "@weekly", "@yearly",
		"daily", "daily 24:00:00 UTC", "daily 10:00 UTC", "daily 09:00:00 Mars/Olympus",
		"weekly 09:00:00 UTC", "weekly 09:00:00 UTC 0", "weekly 09:00:00 UTC 8",
		"weekly 09:00:00 UTC 1,x",
		"cron", "cron * * * * UTC", "cron 60 * * * * UTC", "cron 10-5 * * * * UTC",
		"cron 5/2 * * * * UTC", "cron 0 9 * * MON UTC", "cron 0 9 ? * * UTC",
		"cron 0 9 L * * UTC", "cron 0 9 * * 8 UTC", "cron 0 9 * * 1 UTC extra",
		"at tomorrow", "at 2026-12-01T09:30:00", "at",
	}
	for _, spec := range invalid {
		if _, err := ParseAutomationSchedule(spec); err == nil {
			t.Fatalf("ParseAutomationSchedule(%q) accepted an invalid schedule", spec)
		}
		if err := ValidateAutomationSchedule(spec); err == nil {
			t.Fatalf("ValidateAutomationSchedule(%q) accepted an invalid schedule", spec)
		}
	}
}

func TestAutomationRunRecordJSONCompatibility(t *testing.T) {
	legacy := `{"startedAt":"2026-01-01T00:00:00Z","finishedAt":"2026-01-01T00:00:05Z",` +
		`"status":"success","summary":"ran in chat session sess-1"}`
	var record AutomationRunRecord
	if err := json.Unmarshal([]byte(legacy), &record); err != nil {
		t.Fatalf("decode legacy record: %v", err)
	}
	if record.Status != AutomationRunSuccess || record.CompletedAt.IsZero() {
		t.Fatalf("legacy record = %+v", record)
	}
	if record.SessionID != "" {
		t.Fatalf("legacy record gained a session id: %+v", record)
	}
	if got := AutomationRunSessionID(record.Summary); got != "sess-1" {
		t.Fatalf("AutomationRunSessionID = %q", got)
	}

	current := AutomationRunRecord{
		ID: "run-1", Status: AutomationRunDelivered,
		StartedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		DeliveredAt: time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC),
		SessionID:   "sess-2",
	}
	data, err := json.Marshal(current)
	if err != nil {
		t.Fatalf("encode record: %v", err)
	}
	var roundTrip AutomationRunRecord
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if roundTrip.ID != "run-1" || roundTrip.SessionID != "sess-2" || roundTrip.Status != AutomationRunDelivered {
		t.Fatalf("round trip = %+v", roundTrip)
	}
	if roundTrip.Terminal() {
		t.Fatal("delivered record reports terminal")
	}
	roundTrip.Status = AutomationRunInterrupted
	if !roundTrip.Terminal() {
		t.Fatal("interrupted record does not report terminal")
	}
}

// TestAutomationScheduleRejectsNegativeClockFields covers AUDIT-2026-11-24 B-16:
// only the upper bound was checked, so "daily -1:00:00" validated as a schedule
// whose Next() could never be computed.
func TestAutomationScheduleRejectsNegativeClockFields(t *testing.T) {
	invalid := []string{
		"daily -1:00:00",
		"daily 09:-30:00",
		"daily 09:30:-5",
		"weekly -1:00:00 Asia/Shanghai 1",
	}
	for _, schedule := range invalid {
		if err := ValidateAutomationSchedule(schedule); err == nil {
			t.Fatalf("ValidateAutomationSchedule(%q) = nil error, want a clock-time error", schedule)
		}
	}
	// The boundaries that must stay legal.
	valid := []string{"daily 00:00:00", "daily 23:59:59", "every 90s"}
	for _, schedule := range valid {
		if err := ValidateAutomationSchedule(schedule); err != nil {
			t.Fatalf("ValidateAutomationSchedule(%q) = %v, want it accepted", schedule, err)
		}
	}
}
