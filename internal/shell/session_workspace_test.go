package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestSessionHeaderShowsWorkspace pins the new visible workspace indicator: the
// session header names the workspace the agent works in (accessible label
// "Workspace: <name>", absolute root in the tooltip), and the copy follows the
// UI language. The chip is the session screen's obvious way to confirm "the AI
// is in the workspace"; before it, only the right sidebar's Files breadcrumb
// showed the root.
func TestSessionHeaderShowsWorkspace(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	r, ok := tt.Find("Workspace: test")
	if !ok {
		t.Fatalf("session header has no workspace chip; texts: %v", tt.Texts())
	}
	if r.W <= 0 || r.X < 0 || r.X+r.W > 1600 {
		t.Fatalf("workspace chip geometry out of window: %+v", r)
	}
	if !tt.HasText("test") {
		t.Fatalf("workspace chip does not show the workspace name: %v", tt.Texts())
	}

	// The chip follows the UI language (Chinese is the product default).
	h.app.Language = LangZH
	tt.Frame()
	if _, ok := tt.Find("工作区：test"); !ok {
		t.Fatalf("Chinese workspace chip missing; texts: %v", tt.Texts())
	}
}

// TestSessionHeaderWorkspaceChipNarrowWindow keeps the indicator from
// overflowing the header at the reference narrow sizes; the existing title,
// mode chip and actions must stay in place.
func TestSessionHeaderWorkspaceChipNarrowWindow(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	for _, size := range [][2]int{{1600, 1000}, {1200, 800}, {900, 600}} {
		tt := ui.NewTester(h.app.View, size[0], size[1])
		tt.Frame()
		if r, ok := tt.Find("Workspace: test"); ok {
			if r.X < 0 || r.X+r.W > float32(size[0]) {
				t.Fatalf("%dx%d: workspace chip out of window: %+v", size[0], size[1], r)
			}
		} else if size[0] >= 1200 {
			t.Fatalf("%dx%d: workspace chip missing at a wide size: %v", size[0], size[1], tt.Texts())
		}
		if !tt.HasText("Chat") {
			t.Fatalf("%dx%d: header tabs lost: %v", size[0], size[1], tt.Texts())
		}
	}
}
