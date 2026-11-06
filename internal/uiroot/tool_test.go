package uiroot

import (
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestRenderToolOrderAndFallback covers UIRuntime.RenderTool: renderers are
// built in Order/ID order, the call is passed through, an unmatched tool
// reports false so the caller can draw a fallback, and a panicking renderer is
// contained without losing the others.
func TestRenderToolOrderAndFallback(t *testing.T) {
	r := newTestRoot(t)
	var toolName atomic.Value // string, the call the screen builds this frame
	toolName.Store("shell")
	var matched atomic.Bool
	var lastCall atomic.Pointer[contracts.ToolCall]

	mustScreen(t, r, contracts.ScreenSpec{ID: "tools", Title: "工具", Order: 10,
		Build: func(c *ui.Context) {
			call := contracts.ToolCall{ID: "call-1", Name: toolName.Load().(string), Status: "ok", Summary: "summary-1"}
			ok := r.Runtime().RenderTool(c, call)
			matched.Store(ok)
			if !ok {
				ui.Text(c, "tool-fallback")
			}
		}})

	mustToolRenderer(t, r, contracts.ToolRenderer{Tool: "shell", ID: "z", Order: 20,
		Build: func(c *ui.Context, call contracts.ToolCall) { ui.Text(c, "tool-z") }})
	mustToolRenderer(t, r, contracts.ToolRenderer{Tool: "shell", ID: "a", Order: 10,
		Build: func(c *ui.Context, call contracts.ToolCall) {
			cp := call
			lastCall.Store(&cp)
			ui.Text(c, "tool-a")
		}})
	mustToolRenderer(t, r, contracts.ToolRenderer{Tool: "shell", ID: "a2", Order: 10,
		Build: func(c *ui.Context, call contracts.ToolCall) { ui.Text(c, "tool-a2") }})

	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	if !matched.Load() {
		t.Fatal("RenderTool reported no renderer matched the shell tool")
	}
	requireTextBefore(t, tt, "tool-a", "tool-a2", "tool-z")
	requireNoText(t, tt, "tool-fallback")
	if got := lastCall.Load(); got == nil {
		t.Fatal("the renderer did not receive the tool call")
	} else if got.ID != "call-1" || got.Summary != "summary-1" || got.Status != "ok" {
		t.Fatalf("renderer received %+v, want the call built by the screen", *got)
	}

	// An unmatched tool reports false and the caller's fallback renders.
	toolName.Store("unknown-tool")
	tt.Frame()
	if matched.Load() {
		t.Fatal("RenderTool reported a match for an unregistered tool")
	}
	requireHasText(t, tt, "tool-fallback")
	requireNoText(t, tt, "tool-a")
	requireNoText(t, tt, "tool-z")

	// A panicking renderer is contained; its neighbours still render.
	mustToolRenderer(t, r, contracts.ToolRenderer{Tool: "shell", ID: "panic", Order: 15,
		Build: func(*ui.Context, contracts.ToolCall) { panic("tool renderer boom") }})
	toolName.Store("shell")
	tt.Frame() // must not panic
	if !matched.Load() {
		t.Fatal("RenderTool reported no match after a panicking renderer was added")
	}
	requireTextBefore(t, tt, "tool-a", "tool-a2", "tool-z")
	requireNoText(t, tt, "tool-fallback")
}

// TestRenderToolRegistrationValidation covers the tool renderer registry's
// validation, identity and unregister behavior.
func TestRenderToolRegistrationValidation(t *testing.T) {
	r := newTestRoot(t)
	rt := r.Runtime()

	if _, err := rt.RegisterToolRenderer(contracts.ToolRenderer{Tool: "", ID: "x", Build: func(*ui.Context, contracts.ToolCall) {}}); err == nil {
		t.Fatal("tool renderer with an empty Tool was accepted")
	} else {
		requireContains(t, err, "Tool and ID")
	}
	if _, err := rt.RegisterToolRenderer(contracts.ToolRenderer{Tool: "shell", ID: "", Build: func(*ui.Context, contracts.ToolCall) {}}); err == nil {
		t.Fatal("tool renderer with an empty ID was accepted")
	} else {
		requireContains(t, err, "Tool and ID")
	}
	if _, err := rt.RegisterToolRenderer(contracts.ToolRenderer{Tool: "shell", ID: "no-build"}); err == nil {
		t.Fatal("tool renderer with nil Build was accepted")
	} else {
		requireContains(t, err, "nil Build")
	}

	var matched atomic.Bool
	mustScreen(t, r, contracts.ScreenSpec{ID: "tools", Title: "工具", Order: 10,
		Build: func(c *ui.Context) {
			matched.Store(rt.RenderTool(c, contracts.ToolCall{Name: "shell"}))
		}})
	unregister := mustToolRenderer(t, r, textToolRenderer("shell", "card", "tool-card", 10))
	// The same ID under a different tool is a different renderer.
	mustToolRenderer(t, r, textToolRenderer("other", "card", "other-card", 10))
	if _, err := rt.RegisterToolRenderer(textToolRenderer("shell", "card", "dup-card", 20)); err == nil {
		t.Fatal("duplicate tool renderer ID for one tool was accepted")
	} else {
		requireContains(t, err, "duplicate tool renderer")
	}

	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	if !matched.Load() {
		t.Fatal("the registered renderer did not match")
	}
	requireHasText(t, tt, "tool-card")

	unregister()
	tt.Frame()
	if matched.Load() {
		t.Fatal("RenderTool still matched after the renderer was unregistered")
	}
	requireNoText(t, tt, "tool-card")

	// Idempotent unregister: a stale call must not remove a reused ID.
	if _, err := rt.RegisterToolRenderer(textToolRenderer("shell", "card", "tool-card-new", 10)); err != nil {
		t.Fatalf("re-registering tool renderer card: %v", err)
	}
	unregister()
	tt.Frame()
	if !matched.Load() {
		t.Fatal("the re-registered renderer did not match after a stale unregister call")
	}
	requireHasText(t, tt, "tool-card-new")
}
