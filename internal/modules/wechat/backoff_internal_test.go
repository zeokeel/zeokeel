package wechat

// Internal TDD for the two timing contracts the adapter's Run relies on: the
// retry schedule and the keepalive window.

import (
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/lib/backoff"
)

// The retry schedule (2s doubling to 1m) lives in internal/lib/backoff now —
// backoff_test.go pins the whole ladder there. What this test protects is the
// pair of numbers this adapter's doc comment promises: Run must not hammer the
// platform, so the shared schedule it calls must still be 2s doubling to 1m.
func TestRetryScheduleIsTheDocumentedContract(t *testing.T) {
	if backoff.Initial != 2*time.Second {
		t.Fatalf("backoff.Initial = %v, want 2s", backoff.Initial)
	}
	if backoff.Max != time.Minute {
		t.Fatalf("backoff.Max = %v, want 1m", backoff.Max)
	}
	if got := backoff.Delay(1); got != 2*time.Second {
		t.Fatalf("backoff.Delay(1) = %v, want 2s", got)
	}
	if got := backoff.Delay(6); got != time.Minute {
		t.Fatalf("backoff.Delay(6) = %v, want the 1m ceiling", got)
	}
}

func TestKeepaliveWarnAfter(t *testing.T) {
	if KeepaliveWarnAfter != 20*time.Hour {
		t.Fatalf("KeepaliveWarnAfter = %v, want 20h", KeepaliveWarnAfter)
	}
	if KeepaliveWarnAfter >= 24*time.Hour {
		t.Fatalf("KeepaliveWarnAfter = %v leaves no room before the 24h window closes", KeepaliveWarnAfter)
	}
}
