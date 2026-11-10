package rightpanel

// Internal conformance tests for the inspector's mygo v0.8.3 migration: the
// official diff viewer cache (editor.ParsePatch/SetPatch, docs/ui/diff-viewer.md)
// and the Log pane's event cache (P1: the durable store is not read from
// rendering). They live in the package so they can drive the unexported state
// helpers directly; the frame-level behaviour is covered by the external
// conformance tests.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui/editor"
	"github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// sampleUnifiedDiff is a `git diff` style patch of one file: the changer emits
// this shape (a/ b/ headers, an index line, unified hunks).
const sampleUnifiedDiff = "diff --git a/main.go b/main.go\n" +
	"index 83b414d..1b1a2f0 100644\n" +
	"--- a/main.go\n" +
	"+++ b/main.go\n" +
	"@@ -1,3 +1,3 @@\n" +
	"-old line\n" +
	"+new line\n" +
	" context line\n" +
	" unchanged tail\n"

func TestDiffViewerParsesAndCachesUnifiedPatch(t *testing.T) {
	st := &state{diffs: make(map[string]*diffView)}
	change := contracts.FileChange{Path: "main.go", Status: "M", Diff: sampleUnifiedDiff}

	first, err := st.diffViewFor(change)
	if err != nil || first == nil {
		t.Fatalf("diffViewFor = %v, %v; want the official viewer", first, err)
	}
	if first.state.Layout != editor.DiffUnified {
		t.Fatalf("viewer layout = %v, want unified", first.state.Layout)
	}
	if !first.state.FitContent {
		t.Fatal("a short diff should take its natural height (FitContent)")
	}
	// SetPatch made the texts of the hunks: the official viewer, not a
	// hand-split string, decides what the lines are.
	if got := first.state.New.String(); !strings.Contains(got, "new line") || !strings.Contains(got, "context line") {
		t.Fatalf("viewer New text = %q, want the added and context lines", got)
	}
	if got := first.state.Old.String(); !strings.Contains(got, "old line") || !strings.Contains(got, "context line") {
		t.Fatalf("viewer Old text = %q, want the deleted and context lines", got)
	}
	if first.lines != 4 {
		t.Fatalf("viewer lines = %d, want the four hunk lines", first.lines)
	}

	// The viewer is cached per file: the same DiffState (scroll, folds, focus)
	// is reused across frames.
	second, err := st.diffViewFor(change)
	if err != nil || second != first {
		t.Fatalf("diffViewFor returned %p, %v; want the cached %p", second, err, first)
	}
}

func TestDiffViewerBoundsLongDiffs(t *testing.T) {
	var builder strings.Builder
	builder.WriteString("--- a/big.go\n+++ b/big.go\n@@ -0,0 +1,30 @@\n")
	for index := 0; index < 30; index++ {
		fmt.Fprintf(&builder, "+line%d\n", index)
	}
	st := &state{diffs: make(map[string]*diffView)}
	view, err := st.diffViewFor(contracts.FileChange{Path: "big.go", Status: "M", Diff: builder.String()})
	if err != nil || view == nil {
		t.Fatalf("diffViewFor = %v, %v", view, err)
	}
	if view.state.FitContent {
		t.Fatal("a long diff should scroll inside a bounded viewer, not stretch the pane")
	}
	if view.lines <= diffFitLines {
		t.Fatalf("viewer lines = %d, want more than diffFitLines (%d)", view.lines, diffFitLines)
	}
}

func TestDiffViewerFallsBackForUnparsablePatch(t *testing.T) {
	st := &state{}
	// The old Changes test fixtures feed headerless fragments: ParsePatch reads
	// no file from them, so the renderer must use the bounded text fallback
	// instead of an empty viewer.
	view, err := st.diffViewFor(contracts.FileChange{Path: "big.go", Status: "M",
		Diff: "+line1\n+line2\n+line3\n"})
	if view != nil {
		t.Fatalf("diffViewFor built %v from a headerless fragment, want the fallback", view)
	}
	if err == nil {
		t.Fatal("diffViewFor accepted a headerless fragment")
	}
}

// --- Log pane cache ---------------------------------------------------------

// countingEventReader is a scripted eventReader that counts the store reads.
type countingEventReader struct {
	mu     sync.Mutex
	calls  int
	events []rig.SessionEvent
	err    error
}

func (r *countingEventReader) Last(_ context.Context, sessionID string, limit int) ([]rig.SessionEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	out := make([]rig.SessionEvent, 0, len(r.events))
	for _, event := range r.events {
		event.SessionID = sessionID
		out = append(out, event)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *countingEventReader) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func TestLogCacheReadsOncePerSession(t *testing.T) {
	reader := &countingEventReader{events: []rig.SessionEvent{
		{Seq: 1, Type: "session_started", Time: time.Unix(1700000000, 0)},
		{Seq: 2, Type: "message_added", Time: time.Unix(1700000001, 0)},
	}}
	base := time.Unix(1700000000, 0)
	st := &state{}

	// First frame of a session reads once (the gate is open: no anchor yet).
	st.syncLogCache(nil, reader, "s1", base)
	if got := reader.callCount(); got != 1 {
		t.Fatalf("store reads = %d after the first load, want 1", got)
	}
	if len(st.logEvents) != 2 || !st.logLoaded || st.logSession != "s1" {
		t.Fatalf("log cache = %d events, loaded %v, session %q", len(st.logEvents), st.logLoaded, st.logSession)
	}

	// Extra frames of the same session reuse the cache: no I/O.
	st.syncLogCache(nil, reader, "s1", base.Add(10*time.Millisecond))
	st.syncLogCache(nil, reader, "s1", base.Add(20*time.Millisecond))
	if got := reader.callCount(); got != 1 {
		t.Fatalf("store reads = %d after extra frames, want 1 (no render-time I/O)", got)
	}

	// The chat notifies per streamed delta: 20 notifications inside one gate
	// window coalesce into at most one store read, which happens when the
	// window opens.
	for index := 0; index < 20; index++ {
		st.logDirty = true // what the chat subscription does
		st.syncLogCache(nil, reader, "s1", base.Add(30*time.Millisecond))
	}
	if got := reader.callCount(); got != 1 {
		t.Fatalf("store reads = %d after 20 notifications inside the window, want 1", got)
	}
	st.syncLogCache(nil, reader, "s1", base.Add(logReloadInterval))
	if got := reader.callCount(); got != 2 {
		t.Fatalf("store reads = %d after the window opened, want 2", got)
	}
	st.syncLogCache(nil, reader, "s1", base.Add(logReloadInterval))
	if got := reader.callCount(); got != 2 {
		t.Fatalf("store reads = %d after a cached frame, want 2", got)
	}

	// A session switch reads the new session's events (the cache is dropped).
	st.syncLogCache(nil, reader, "s2", base.Add(logReloadInterval+10*time.Millisecond))
	if got := reader.callCount(); got != 3 || st.logSession != "s2" {
		t.Fatalf("store reads = %d, session %q after a session switch; want 3, s2", got, st.logSession)
	}
}

// TestLogReloadGate pins the throttle window: a dirty cache waits until the
// interval elapsed, so the notification-per-delta stream cannot trigger a read
// per frame.
func TestLogReloadGate(t *testing.T) {
	base := time.Unix(1700000000, 0)
	st := &state{logLoaded: true, logLoadedAt: base}
	if due, _ := st.logReloadGate(base.Add(100 * time.Millisecond)); due {
		t.Fatal("the gate opened inside the window")
	}
	due, wait := st.logReloadGate(base.Add(100 * time.Millisecond))
	if due || wait != 150*time.Millisecond {
		t.Fatalf("gate = %v, %s inside the window; want closed with a 150ms wait", due, wait)
	}
	if due, wait := st.logReloadGate(base.Add(logReloadInterval)); !due || wait != 0 {
		t.Fatalf("gate = %v, %s at the window edge; want open", due, wait)
	}
	fresh := &state{}
	if due, _ := fresh.logReloadGate(base); !due {
		t.Fatal("a cache that never read must open the gate at once")
	}
}
