package backoff

// The schedule (2s doubling to 1m) is a documented contract — the adapters'
// package comments promise it and a flood on outage is a real failure mode — so
// it is pinned directly rather than by sleeping for minutes.

import (
	"context"
	"testing"
	"time"
)

func TestDelaySchedule(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, Initial},
		{-3, Initial},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
		{5, 32 * time.Second},
		{6, Max},
		{7, Max},
		{1000, Max},
	}
	for _, testCase := range cases {
		if got := Delay(testCase.attempt); got != testCase.want {
			t.Errorf("Delay(%d) = %v, want %v", testCase.attempt, got, testCase.want)
		}
	}
}

func TestDelayBoundsAreTheDocumentedOnes(t *testing.T) {
	if Initial != 2*time.Second {
		t.Fatalf("Initial = %v, want 2s", Initial)
	}
	if Max != time.Minute {
		t.Fatalf("Max = %v, want 1m", Max)
	}
	// Doubling must never jump past the ceiling on the way up.
	for attempt := 1; attempt <= 64; attempt++ {
		if got := Delay(attempt); got > Max {
			t.Fatalf("Delay(%d) = %v, above Max %v", attempt, got, Max)
		}
		if attempt > 1 && Delay(attempt) < Delay(attempt-1) {
			t.Fatalf("Delay(%d) = %v, below Delay(%d) = %v", attempt, Delay(attempt), attempt-1, Delay(attempt-1))
		}
	}
}

func TestSleepReturnsFalseWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Sleep(ctx, time.Hour) {
		t.Fatal("Sleep returned true on a canceled ctx")
	}
	if Sleep(ctx, 0) {
		t.Fatal("Sleep(0) returned true on a canceled ctx")
	}
}

func TestSleepWaitsForShortDelays(t *testing.T) {
	if !Sleep(context.Background(), time.Millisecond) {
		t.Fatal("Sleep returned false with a live ctx")
	}
	if !Sleep(context.Background(), 0) {
		t.Fatal("Sleep(0) returned false with a live ctx")
	}
}
