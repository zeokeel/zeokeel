package host

// Tests for the desktop notification helpers that are testable without a
// desktop session: the click-to-front handler tolerates the startup window
// being nil, and the delivery timeout is bounded so a hidden authorization
// prompt cannot leave a delivery goroutine parked.

import (
	"testing"
	"time"
)

func TestNotifyWindowFrontNilWindowIsSafe(t *testing.T) {
	notifyWindowFront(nil) // must not panic during startup
}

func TestOSNotificationTimeoutIsBounded(t *testing.T) {
	if osNotificationTimeout <= 0 || osNotificationTimeout > time.Minute {
		t.Fatalf("osNotificationTimeout = %v, want a short positive bound", osNotificationTimeout)
	}
}
