package automation

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, spec string) Schedule {
	t.Helper()
	s, err := Parse(spec)
	if err != nil {
		t.Fatalf("Parse(%q): %v", spec, err)
	}
	return s
}

func TestParseValid(t *testing.T) {
	cases := []struct {
		spec  string
		kind  Kind
		every int
		hour  int
		min   int
	}{
		{"*/5 * * * *", KindEveryMinute, 5, 0, 0},
		{"*/1 * * * *", KindEveryMinute, 1, 0, 0},
		{"*/59 * * * *", KindEveryMinute, 59, 0, 0},
		{"0 9 * * *", KindDaily, 0, 9, 0},
		{"30 23 * * *", KindDaily, 0, 23, 30},
		{"@hourly", KindHourly, 0, 0, 0},
		{"@daily", KindDaily, 0, 0, 0},
		{"@midnight", KindDaily, 0, 0, 0},
		{" @DAILY ", KindDaily, 0, 0, 0},
	}
	for _, tc := range cases {
		s := mustParse(t, tc.spec)
		if s.Kind() != tc.kind {
			t.Errorf("%q kind = %v, want %v", tc.spec, s.Kind(), tc.kind)
		}
		if got := s.Every(); got != tc.every {
			t.Errorf("%q every = %d, want %d", tc.spec, got, tc.every)
		}
		if tc.kind == KindDaily {
			hour, minute, ok := s.HourMinute()
			if !ok || hour != tc.hour || minute != tc.min {
				t.Errorf("%q hm = %d:%d,%v; want %d:%d", tc.spec, hour, minute, ok, tc.hour, tc.min)
			}
		}
	}
}

func TestParseInvalid(t *testing.T) {
	for _, spec := range []string{
		"",
		"   ",
		"0 9 * *",
		"0 9 * * * *",
		"* * * * *",
		"0 * * * *",
		"*/0 * * * *",
		"*/60 * * * *",
		"*/x * * * *",
		"*/5 1 * * *",
		"1,2 * * * *",
		"10-20 * * * *",
		"60 9 * * *",
		"0 24 * * *",
		"0 9 1 * *",
		"@weekly",
		"@every 5m",
	} {
		if _, err := Parse(spec); err == nil {
			t.Errorf("Parse(%q) accepted an invalid expression", spec)
		}
	}
}

func TestNextRunEveryMinutes(t *testing.T) {
	cases := []struct {
		spec string
		from string
		want string
	}{
		{"*/5 * * * *", "2026-11-04T10:03:20Z", "2026-11-04T10:05:00Z"},
		{"*/5 * * * *", "2026-11-04T10:05:00Z", "2026-11-04T10:10:00Z"},
		{"*/1 * * * *", "2026-11-04T10:05:30Z", "2026-11-04T10:06:00Z"},
		{"*/15 * * * *", "2026-11-04T10:59:00Z", "2026-11-04T11:00:00Z"},
		{"*/15 * * * *", "2026-11-04T10:45:00Z", "2026-11-04T11:00:00Z"},
	}
	for _, tc := range cases {
		from := mustTime(t, tc.from)
		got, err := NextRun(tc.spec, from)
		if err != nil {
			t.Fatalf("NextRun(%q): %v", tc.spec, err)
		}
		if want := mustTime(t, tc.want); !got.Equal(want) {
			t.Errorf("NextRun(%q, %v) = %v, want %v", tc.spec, from, got, want)
		}
		if !got.After(from) {
			t.Errorf("NextRun(%q, %v) = %v, not strictly after", tc.spec, from, got)
		}
	}
}

func TestNextRunDailyHourly(t *testing.T) {
	cases := []struct {
		spec string
		from string
		want string
	}{
		{"0 9 * * *", "2026-11-04T08:00:00Z", "2026-11-04T09:00:00Z"},
		{"0 9 * * *", "2026-11-04T09:00:00Z", "2026-11-05T09:00:00Z"},
		{"0 9 * * *", "2026-11-04T10:00:00Z", "2026-11-05T09:00:00Z"},
		{"30 23 * * *", "2026-11-04T23:31:00Z", "2026-11-05T23:30:00Z"},
		{"@hourly", "2026-11-04T10:00:00Z", "2026-11-04T11:00:00Z"},
		{"@hourly", "2026-11-04T10:30:00Z", "2026-11-04T11:00:00Z"},
		{"@daily", "2026-11-04T00:00:00Z", "2026-11-05T00:00:00Z"},
		{"@daily", "2026-11-04T12:00:00Z", "2026-11-05T00:00:00Z"},
	}
	for _, tc := range cases {
		from := mustTime(t, tc.from)
		got, err := NextRun(tc.spec, from)
		if err != nil {
			t.Fatalf("NextRun(%q): %v", tc.spec, err)
		}
		if want := mustTime(t, tc.want); !got.Equal(want) {
			t.Errorf("NextRun(%q, %v) = %v, want %v", tc.spec, from, got, want)
		}
	}
}

func TestNextRunKeepsLocation(t *testing.T) {
	zone := time.FixedZone("UTC+8", 8*60*60)
	from := time.Date(2026, 11, 4, 8, 30, 0, 0, zone)
	got, err := NextRun("0 9 * * *", from)
	if err != nil {
		t.Fatalf("NextRun: %v", err)
	}
	if got.Location() != zone {
		t.Fatalf("NextRun location = %v, want %v", got.Location(), zone)
	}
	if got.Hour() != 9 || got.Day() != 4 {
		t.Fatalf("NextRun = %v, want 09:00 on the same local day", got)
	}
}

func TestDescribe(t *testing.T) {
	for _, tc := range []struct{ spec, want string }{
		{"*/1 * * * *", "Every minute"},
		{"*/15 * * * *", "Every 15 minutes"},
		{"@hourly", "Every hour"},
		{"0 9 * * *", "Daily at 09:00"},
		{"bogus", "bogus"},
	} {
		if got := Describe(tc.spec); got != tc.want {
			t.Errorf("Describe(%q) = %q, want %q", tc.spec, got, tc.want)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := Validate("*/5 * * * *"); err != nil {
		t.Fatalf("Validate valid: %v", err)
	}
	if err := Validate("nope"); err == nil {
		t.Fatal("Validate accepted an invalid expression")
	}
	if _, err := NextRun("nope", time.Now()); err == nil {
		t.Fatal("NextRun accepted an invalid expression")
	}
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed.UTC()
}
