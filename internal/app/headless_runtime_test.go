package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/state"
)

// newTestHeadlessRuntime builds a HeadlessRuntime over a fresh data directory
// whose provider points at baseURL.
func newTestHeadlessRuntime(t *testing.T, baseURL string) HeadlessRuntime {
	t.Helper()
	dir := t.TempDir()
	writeHeadlessStore(t, dir, baseURL, map[string]string{"provider.retry_max_retries": "0"})
	rt, err := NewHeadlessRuntime(HeadlessRuntimeConfig{DataDir: dir, Logger: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("NewHeadlessRuntime: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := rt.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return rt
}

// waitRuntimeEvent reads events until cond matches or the timeout expires.
func waitRuntimeEvent(t *testing.T, ch <-chan state.Event, timeout time.Duration, cond func(state.Event) bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("runtime event channel closed before the expected event")
			}
			if cond(ev) {
				return
			}
		case <-deadline:
			t.Fatalf("timed out after %v waiting for a runtime event", timeout)
		}
	}
}

// TestHeadlessRuntimePromptAndEvents is the ACP seam acceptance test: a new
// session bound to a cwd runs a prompt and the subscriber sees the assistant
// text followed by the terminal idle status.
func TestHeadlessRuntimePromptAndEvents(t *testing.T) {
	ctx := context.Background()
	srv := sseServer(t, "runtime answer")
	rt := newTestHeadlessRuntime(t, srv.URL+"/v1")

	cwd := t.TempDir()
	sessionID, err := rt.NewSession(ctx, cwd)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// The session is bound to the requested workspace directory.
	hr := rt.(*headlessRuntime)
	sess, err := hr.ctrl.Store().GetSession(sessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	ws, err := hr.ctrl.Store().GetWorkspace(sess.WorkspaceID)
	if err != nil || filepath.Clean(ws.Root) != filepath.Clean(cwd) {
		t.Fatalf("session workspace root = %+v, %v; want %q", ws, err, cwd)
	}

	ch, unsubscribe, err := rt.Subscribe(sessionID)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsubscribe()
	if err := rt.Submit(ctx, sessionID, "say hello"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	var (
		mu       sync.Mutex
		text     strings.Builder
		terminal bool
		reason   state.StatusReason
	)
	waitRuntimeEvent(t, ch, 10*time.Second, func(ev state.Event) bool {
		mu.Lock()
		defer mu.Unlock()
		switch e := ev.(type) {
		case state.Token:
			text.WriteString(e.Text)
		case state.Status:
			if e.Phase == state.PhaseIdle || e.Phase == state.PhaseFailed {
				terminal = true
				reason = e.Reason
				return true
			}
		}
		return false
	})
	mu.Lock()
	gotText, gotReason := text.String(), reason
	mu.Unlock()
	if !terminal {
		t.Fatal("turn did not reach a terminal status")
	}
	if !strings.Contains(gotText, "runtime answer") {
		t.Fatalf("streamed text = %q, want the model answer", gotText)
	}
	if gotReason != state.ReasonNone {
		t.Fatalf("terminal reason = %q, want a completed turn", gotReason)
	}
}

// TestHeadlessRuntimeCancel stops an in-flight prompt: the turn settles as
// stopped (cancelled) instead of completing.
func TestHeadlessRuntimeCancel(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":"working"}}]}`+"\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Keep the stream open until the turn is cancelled.
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	rt := newTestHeadlessRuntime(t, srv.URL+"/v1")
	cwd := t.TempDir()
	sessionID, err := rt.NewSession(ctx, cwd)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	ch, unsubscribe, err := rt.Subscribe(sessionID)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsubscribe()
	if err := rt.Submit(ctx, sessionID, "slow prompt"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	// Wait until the turn is actually streaming, then cancel it.
	waitRuntimeEvent(t, ch, 10*time.Second, func(ev state.Event) bool {
		_, ok := ev.(state.Token)
		return ok
	})
	if err := rt.Cancel(ctx, sessionID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	var stopped bool
	waitRuntimeEvent(t, ch, 10*time.Second, func(ev state.Event) bool {
		status, ok := ev.(state.Status)
		if !ok {
			return false
		}
		if status.Phase == state.PhaseIdle && status.Reason == state.ReasonStopped {
			stopped = true
			return true
		}
		return false
	})
	if !stopped {
		t.Fatal("cancelled turn did not settle as stopped")
	}
	// Cancelling an unknown session is a documented no-op.
	if err := rt.Cancel(ctx, "missing-session"); err != nil {
		t.Fatalf("Cancel(unknown session): %v", err)
	}
}

// TestHeadlessRuntimeAutoDeniesApprovals proves the no-UI approval policy: a
// tool call that needs approval is denied immediately, the turn still settles,
// and nothing runs (no marker file).
func TestHeadlessRuntimeAutoDeniesApprovals(t *testing.T) {
	ctx := context.Background()
	marker := filepath.Join(t.TempDir(), "should-not-exist")
	srv := toolCallServer(t, "bash", fmt.Sprintf(`{"command":"touch %s"}`, marker), "denied as expected")
	rt := newTestHeadlessRuntime(t, srv.URL+"/v1")
	sessionID, err := rt.NewSession(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	ch, unsubscribe, err := rt.Subscribe(sessionID)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsubscribe()
	if err := rt.Submit(ctx, sessionID, "run the command"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitRuntimeEvent(t, ch, 15*time.Second, func(ev state.Event) bool {
		status, ok := ev.(state.Status)
		return ok && (status.Phase == state.PhaseIdle || status.Phase == state.PhaseFailed)
	})
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("headless runtime executed an approval-gated bash call: %s", marker)
	}
}

// TestHeadlessRuntimeSessionsRunInParallel is the RT-04 acceptance test at the
// ACP seam: two sessions with their own agents overlap their model calls.
func TestHeadlessRuntimeSessionsRunInParallel(t *testing.T) {
	ctx := context.Background()
	const latency = 250 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(latency)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeHeadlessSSE(w, "parallel answer")
	}))
	t.Cleanup(srv.Close)
	rt := newTestHeadlessRuntime(t, srv.URL+"/v1")

	first, err := rt.NewSession(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("NewSession(a): %v", err)
	}
	second, err := rt.NewSession(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("NewSession(b): %v", err)
	}
	chA, unsubA, err := rt.Subscribe(first)
	if err != nil {
		t.Fatalf("Subscribe(a): %v", err)
	}
	defer unsubA()
	chB, unsubB, err := rt.Subscribe(second)
	if err != nil {
		t.Fatalf("Subscribe(b): %v", err)
	}
	defer unsubB()

	start := time.Now()
	if err := rt.Submit(ctx, first, "first prompt"); err != nil {
		t.Fatalf("Submit(a): %v", err)
	}
	if err := rt.Submit(ctx, second, "second prompt"); err != nil {
		t.Fatalf("Submit(b): %v", err)
	}
	terminal := func(ch <-chan state.Event) {
		waitRuntimeEvent(t, ch, 15*time.Second, func(ev state.Event) bool {
			status, ok := ev.(state.Status)
			return ok && (status.Phase == state.PhaseIdle || status.Phase == state.PhaseFailed)
		})
	}
	terminal(chA)
	terminal(chB)
	elapsed := time.Since(start)
	if elapsed >= 2*latency {
		t.Fatalf("two sessions' prompts took %v (serial would be ~%v): per-session agents are not running in parallel", elapsed, 2*latency)
	}
}
