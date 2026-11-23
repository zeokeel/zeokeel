package host

// Tests for the OS notification sink that are testable without a desktop
// session: the delivery timeout is bounded, so a hidden authorization prompt
// cannot leave a delivery goroutine parked.

import (
	"testing"
	"time"
)

func TestOSNotificationTimeoutIsBounded(t *testing.T) {
	if osNotificationTimeout <= 0 || osNotificationTimeout > time.Minute {
		t.Fatalf("osNotificationTimeout = %v, want a short positive bound", osNotificationTimeout)
	}
}
