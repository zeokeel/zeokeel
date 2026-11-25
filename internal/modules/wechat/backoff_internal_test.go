package wechat

// Internal TDD for the backoff schedule: Run must not hammer the platform, and
// the schedule itself (2s doubling to 1m) is a documented contract, so it is
// tested directly instead of by sleeping for minutes.

import (
	"testing"
	"time"
)

func TestBackoffDelaySchedule(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, BackoffInitial},
		{-3, BackoffInitial},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
		{5, 32 * time.Second},
		{6, BackoffMax},
		{7, BackoffMax},
		{1000, BackoffMax},
	}
	for _, testCase := range cases {
		if got := backoffDelay(testCase.attempt); got != testCase.want {
			t.Errorf("backoffDelay(%d) = %v, want %v", testCase.attempt, got, testCase.want)
		}
	}
}

func TestBackoffConstants(t *testing.T) {
	if BackoffInitial != 2*time.Second {
		t.Fatalf("BackoffInitial = %v, want 2s", BackoffInitial)
	}
	if BackoffMax != time.Minute {
		t.Fatalf("BackoffMax = %v, want 1m", BackoffMax)
	}
	if KeepaliveWarnAfter != 20*time.Hour {
		t.Fatalf("KeepaliveWarnAfter = %v, want 20h", KeepaliveWarnAfter)
	}
	if KeepaliveWarnAfter >= 24*time.Hour {
		t.Fatalf("KeepaliveWarnAfter = %v leaves no room before the 24h window closes", KeepaliveWarnAfter)
	}
}
