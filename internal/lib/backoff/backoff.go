// Package backoff is the channel adapters' single retry schedule. The wechat and
// email adapters each carried a private copy of it (2s doubling, capped at 1m,
// plus an identical ctx-aware sleep); the copies drifted only in name, so they
// converged here. The schedule is a documented contract — an outage must not turn
// into a request flood — and it is tested directly (see backoff_test.go) instead
// of by sleeping for minutes.
package backoff

import (
	"context"
	"time"
)

// Initial and Max bound the retry schedule: the first transient failure waits
// Initial, every further failure doubles the wait, and Max is the ceiling
// (2s, 4s, 8s … 1m).
const (
	Initial = 2 * time.Second
	Max     = time.Minute
)

// Delay returns how long to wait before retry number attempt (1 is the first
// retry). A non-positive attempt yields Initial, so a caller that forgot to
// count still backs off rather than spinning.
func Delay(attempt int) time.Duration {
	if attempt < 1 {
		return Initial
	}
	delay := Initial
	for i := 1; i < attempt; i++ {
		if delay >= Max/2 {
			return Max
		}
		delay *= 2
	}
	if delay > Max {
		return Max
	}
	return delay
}

// Sleep waits d, returning false when ctx was canceled (or already canceled)
// first. A non-positive d only checks ctx, so an elapsed deadline is reported
// without a timer.
func Sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
