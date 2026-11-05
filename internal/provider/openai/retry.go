package openai

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RetryPolicy configures recovery from transient model-request failures. It
// follows DSH's normal retry mode: bounded attempts, exponential backoff with
// jitter, and a retryable error/status set. ZeoKeel retries at the provider
// request boundary; a failure that happens after visible output was emitted is
// not retried because the transcript cannot un-see the partial answer.
type RetryPolicy struct {
	// MaxRetries is the number of retries after the first attempt. Zero
	// disables retries.
	MaxRetries int
	// InitialDelay is the first backoff delay.
	InitialDelay time.Duration
	// MaxDelay caps the backoff delay (and any Retry-After value).
	MaxDelay time.Duration
	// JitterRatio adds symmetric jitter in [0,1] to each delay.
	JitterRatio float64
}

// DefaultRetryPolicy mirrors DSH's normal mode defaults: five retries after
// the initial attempt, 500ms..10s exponential backoff and 10% jitter.
var DefaultRetryPolicy = RetryPolicy{
	MaxRetries:   5,
	InitialDelay: 500 * time.Millisecond,
	MaxDelay:     10 * time.Second,
	JitterRatio:  0.1,
}

// DefaultStreamIdleTimeout is DSH's streamIdleTimeoutMs default (300,000ms):
// the longest a single streamed read may stay silent before the request is
// considered failed. It is an idle timeout, not a total generation limit.
const DefaultStreamIdleTimeout = 300 * time.Second

// normalize fills sensible values for an explicit-but-partial policy.
func (p RetryPolicy) normalize() RetryPolicy {
	if p.InitialDelay <= 0 {
		p.InitialDelay = DefaultRetryPolicy.InitialDelay
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = DefaultRetryPolicy.MaxDelay
	}
	if p.MaxDelay < p.InitialDelay {
		p.MaxDelay = p.InitialDelay
	}
	if p.JitterRatio < 0 {
		p.JitterRatio = 0
	}
	if p.JitterRatio > 1 {
		p.JitterRatio = 1
	}
	if p.MaxRetries < 0 {
		p.MaxRetries = 0
	}
	return p
}

// retryableStatus reports HTTP statuses that are transient. 401/403/400 and
// other client errors are not retried.
func retryableStatus(code int) bool {
	switch {
	case code == http.StatusRequestTimeout, code == http.StatusTooEarly,
		code == http.StatusTooManyRequests:
		return true
	case code >= 500 && code <= 599:
		return true
	default:
		return false
	}
}

// retryableStreamError reports whether a read-phase failure that happened
// before any visible chunk is worth retrying: transport failures, idle
// timeouts, unexpected EOFs and empty responses.
func retryableStreamError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"timeout", "timed out", "idle", "connection reset", "connection refused",
		"broken pipe", "unexpected eof", "eof", "temporary failure", "transport",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// delayFor computes the exponential backoff for one retry, honoring a
// provider Retry-After header when it is valid and within MaxDelay.
func (p Provider) delayFor(attempt int, retryAfter string) time.Duration {
	if d := parseRetryAfter(retryAfter); d > 0 {
		if d > p.retry.MaxDelay {
			return p.retry.MaxDelay
		}
		return d
	}
	delay := p.retry.InitialDelay
	for i := 0; i < attempt && delay < p.retry.MaxDelay; i++ {
		delay *= 2
	}
	if delay > p.retry.MaxDelay {
		delay = p.retry.MaxDelay
	}
	if p.retry.JitterRatio > 0 {
		factor := 1 + p.retry.JitterRatio*(2*rand.Float64()-1)
		delay = time.Duration(float64(delay) * factor)
	}
	if delay < 0 {
		return 0
	}
	return delay
}

// parseRetryAfter parses the two legal Retry-After forms: delay-seconds or an
// HTTP date. Empty/invalid values return 0.
func parseRetryAfter(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(raw); err == nil {
		if d := time.Until(at); d > 0 {
			return d
		}
	}
	return 0
}

// sleepCtx waits for d or until ctx is done. It reports whether the wait
// completed normally.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// logRetry records one scheduled retry. Retries are provider-internal; the
// model sees only the final stream, matching DSH's invisible retry events.
func logRetry(attempt, max int, delay time.Duration, err error) {
	if err == nil {
		err = fmt.Errorf("empty response")
	}
	log.Printf("zeokeel: provider retry %d/%d in %s: %v", attempt, max, delay, err)
}

// requestFailure wraps a request-phase error without losing its cause.
func requestFailure(err error) error {
	return fmt.Errorf("openai: chat completions: %w", err)
}
