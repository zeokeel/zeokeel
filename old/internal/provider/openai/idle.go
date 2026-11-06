package openai

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// idleReadWatcher aborts a streamed response when no bytes arrive for idle.
// It mirrors DSH's streamIdleTimeoutMs: the timer only runs while a body read
// is outstanding, so local processing between chunks never counts as idle.
type idleReadWatcher struct {
	r      io.Reader
	idle   time.Duration
	cancel context.CancelFunc

	mu       sync.Mutex
	timer    *time.Timer
	timedOut bool
}

func newIdleReadWatcher(r io.Reader, idle time.Duration, cancel context.CancelFunc) *idleReadWatcher {
	w := &idleReadWatcher{r: r, idle: idle, cancel: cancel}
	w.timer = time.AfterFunc(idle, w.fire)
	return w
}

func (w *idleReadWatcher) fire() {
	w.mu.Lock()
	w.timedOut = true
	w.mu.Unlock()
	w.cancel()
}

// Read implements io.Reader. A timeout is reported as an error containing
// "stream idle timeout" so the retry policy can classify it.
func (w *idleReadWatcher) Read(p []byte) (int, error) {
	n, err := w.r.Read(p)
	w.mu.Lock()
	timedOut := w.timedOut
	if !timedOut && n > 0 {
		w.timer.Reset(w.idle)
	}
	w.mu.Unlock()
	if timedOut {
		return n, fmt.Errorf("openai: stream idle timeout after %s", w.idle)
	}
	return n, err
}

// TimedOut reports whether the idle interval elapsed.
func (w *idleReadWatcher) TimedOut() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.timedOut
}

// TimeoutError returns the classifying error used by the retry policy.
func (w *idleReadWatcher) TimeoutError() error {
	return fmt.Errorf("openai: stream idle timeout after %s", w.idle)
}

// stop releases the watchdog timer.
func (w *idleReadWatcher) stop() {
	w.mu.Lock()
	w.timer.Stop()
	w.mu.Unlock()
}
