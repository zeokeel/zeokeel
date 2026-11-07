package automation

import (
	"strings"
	"testing"
	"time"
)

// TestParseSchedule covers the supported cron subset plus every/after.
func TestParseSchedule(t *testing.T) {
	valid := []struct {
		spec       string
		kind       Kind
		canonical  string
		from       time.Time
		wantNext   time.Time
		every      int
		hourMinute [2]int
	}{
		{
			spec: "*/15 * * * *", kind: KindEveryMinute, canonical: "*/15 * * * *",
			from:     time.Date(2026, 1, 1, 10, 3, 30, 0, time.UTC),
			wantNext: time.Date(2026, 1, 1, 10, 15, 0, 0, time.UTC),
			every:    15,
		},
		{
			spec: "*/1 * * * *", kind: KindEveryMinute, canonical: "*/1 * * * *",
			from:     time.Date(2026, 1, 1, 10, 3, 0, 0, time.UTC),
			wantNext: time.Date(2026, 1, 1, 10, 4, 0, 0, time.UTC),
			every:    1,
		},
		{
			spec: "30 9 * * *", kind: KindDaily, canonical: "30 9 * * *",
			from:       time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC),
			wantNext:   time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC),
			hourMinute: [2]int{9, 30},
		},
		{
			// Exactly on the instant: next is tomorrow.
			spec: "30 9 * * *", kind: KindDaily, canonical: "30 9 * * *",
			from:     time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC),
			wantNext: time.Date(2026, 1, 2, 9, 30, 0, 0, time.UTC),
		},
		{
			spec: "@hourly", kind: KindHourly, canonical: "@hourly",
			from:     time.Date(2026, 1, 1, 10, 30, 45, 0, time.UTC),
			wantNext: time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC),
		},
		{
			spec: "@daily", kind: KindDaily, canonical: "@daily",
			from:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			wantNext: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		},
		{
			spec: "@midnight", kind: KindDaily, canonical: "@daily",
			from:     time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
			wantNext: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		},
		{
			spec: "every 30s", kind: KindEvery, canonical: "every 30s",
			from:     time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
			wantNext: time.Date(2026, 1, 1, 10, 0, 30, 0, time.UTC),
		},
		{
			spec: "EVERY 5S", kind: KindEvery, canonical: "every 5s",
			from:     time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
			wantNext: time.Date(2026, 1, 1, 10, 0, 5, 0, time.UTC),
		},
		{
			spec: "after 90s", kind: KindAfter, canonical: "after 90s",
			from:     time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
			wantNext: time.Date(2026, 1, 1, 10, 1, 30, 0, time.UTC),
		},
	}
	for _, testCase := range valid {
		schedule, err := Parse(testCase.spec)
		if err != nil {
			t.Fatalf("Parse(%q): %v", testCase.spec, err)
		}
		if schedule.Kind() != testCase.kind {
			t.Fatalf("Parse(%q) kind = %v, want %v", testCase.spec, schedule.Kind(), testCase.kind)
		}
		if schedule.String() != testCase.canonical {
			t.Fatalf("Parse(%q) canonical = %q, want %q", testCase.spec, schedule.String(), testCase.canonical)
		}
		if next := schedule.Next(testCase.from); !next.Equal(testCase.wantNext) {
			t.Fatalf("Next(%q, %s) = %s, want %s", testCase.spec, testCase.from, next, testCase.wantNext)
		}
		if next, err := NextRun(testCase.spec, testCase.from); err != nil || !next.Equal(testCase.wantNext) {
			t.Fatalf("NextRun(%q) = %s/%v, want %s", testCase.spec, next, err, testCase.wantNext)
		}
		if testCase.kind == KindEveryMinute && schedule.Every() != testCase.every {
			t.Fatalf("Every(%q) = %d, want %d", testCase.spec, schedule.Every(), testCase.every)
		}
		if testCase.kind == KindDaily {
			hour, minute, ok := schedule.HourMinute()
			if ok && testCase.hourMinute != [2]int{0, 0} && (hour != testCase.hourMinute[0] || minute != testCase.hourMinute[1]) {
				t.Fatalf("HourMinute(%q) = %02d:%02d", testCase.spec, hour, minute)
			}
		}
		if err := Validate(testCase.spec); err != nil {
			t.Fatalf("Validate(%q): %v", testCase.spec, err)
		}
	}
}

func TestParseScheduleRejectsInvalid(t *testing.T) {
	invalid := []string{
		"", "   ",
		"* * * * *", "0 * * * *", "*/0 * * * *", "*/60 * * * *", "*/5 1 * * *",
		"1 2 3 * *", "1 2 * 3 *", "1 2 * * 3", "60 1 * * *", "1 24 * * *",
		"every 0s", "every -5s", "every 5m", "every", "after 0s", "after x",
		"foo", "@weekly", "1 2 3 4", "1 2 3 4 5 6",
	}
	for _, spec := range invalid {
		if _, err := Parse(spec); err == nil {
			t.Fatalf("Parse(%q) accepted an invalid schedule", spec)
		}
		if err := Validate(spec); err == nil {
			t.Fatalf("Validate(%q) accepted an invalid schedule", spec)
		}
	}
}

func TestDescribeSchedule(t *testing.T) {
	cases := map[string]string{
		"*/1 * * * *":  "Every minute",
		"*/15 * * * *": "Every 15 minutes",
		"@hourly":      "Every hour",
		"0 9 * * *":    "Daily at 09:00",
		"every 1s":     "Every second",
		"every 30s":    "Every 30 seconds",
		"after 90s":    "Once after 90 seconds",
		"nonsense":     "nonsense",
	}
	for spec, want := range cases {
		if got := Describe(spec); got != want {
			t.Fatalf("Describe(%q) = %q, want %q", spec, got, want)
		}
	}
}

// TestNextRunKeepsLocation pins that Next is computed in the reference location.
func TestNextRunKeepsLocation(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("time zone data unavailable: %v", err)
	}
	from := time.Date(2026, 1, 1, 8, 0, 0, 0, location)
	next, err := NextRun("0 9 * * *", from)
	if err != nil {
		t.Fatalf("NextRun: %v", err)
	}
	if next.Location() != location || next.Hour() != 9 || next.Minute() != 0 {
		t.Fatalf("NextRun = %s, want 09:00 in %s", next, location)
	}
	if !strings.HasPrefix(Describe("0 9 * * *"), "Daily at") {
		t.Fatalf("Describe = %q", Describe("0 9 * * *"))
	}
}
