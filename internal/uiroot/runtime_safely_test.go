package uiroot

// Frame test for AR-18 (2026-11 wave D): a panic contained in a queued Post
// callback must be logged with its stack, matching the package's safeBuild /
// runHandlerSafely convention. Before the fix runSafely recovered silently, so
// a half-applied state update left no trace in a developer log.

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestPostPanicIsLoggedWithStack injects a panic into a Post callback, drains it
// through a frame, and asserts the recovered panic is logged with a stack trace
// (and that the UI thread keeps running afterwards).
func TestPostPanicIsLoggedWithStack(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	r := newTestRoot(t)
	mustScreen(t, r, contracts.ScreenSpec{ID: "home", Title: "首页", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "alive") }})
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()

	r.Runtime().Post(func() { panic("post boom AR-18") })
	tt.Frame() // drains the Post; must not panic

	logged := buf.String()
	if !strings.Contains(logged, "post boom AR-18") {
		t.Fatalf("AR-18: the contained Post panic was not logged; log = %q", logged)
	}
	// safeBuild logs debug.Stack(); the stack carries goroutine frames.
	if !strings.Contains(logged, "goroutine") {
		t.Fatalf("AR-18: the logged Post panic has no stack trace; log = %q", logged)
	}
	// The UI thread survives and keeps rendering.
	requireHasText(t, tt, "alive")
}
