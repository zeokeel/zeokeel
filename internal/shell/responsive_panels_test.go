package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
	"github.com/zeokeel/zeokeel/internal/tools"
)

// seedDeepSession gives the live session a Deep-mode footprint: a todo list,
// one subagent and one user turn. It is the state that used to push the
// right-aligned user bubble out of the transcript clip.
func seedDeepSession(t *testing.T, h *liveApp) {
	t.Helper()
	h.ctrl.SetTodos([]tools.Todo{
		{ID: 1, Task: "Inspect the layout", Status: tools.TodoCompleted},
		{ID: 2, Task: "Fix the panels", Status: tools.TodoInProgress},
	})
	h.ctrl.SetTeam([]tools.Teammate{{
		CallID: "call-sub", Name: "layout-auditor",
		Task: "audit the narrow layout", Status: tools.SubagentStatusCompleted,
	}})
	h.app.SetSnapshot(&state.Snapshot{
		SessionID: h.ctrl.CurrentSessionID(),
		Phase:     state.PhaseIdle,
		Messages: []state.Message{{
			MsgID: "msg-user", Role: state.RoleUser,
			Text: "please audit the narrow layout",
		}},
	})
}

// TestSessionSidePanelsKeepTranscriptVisible covers the Deep-mode layout: the
// fixed 272px Todo and Team columns used to make an 845px transcript wider
// than its viewport, so the right-aligned user bubble measured W=0. The panels
// now yield (or the transcript shrinks with them) and the docked right sidebar
// keeps its minimum width at the 900px window.
func TestSessionSidePanelsKeepTranscriptVisible(t *testing.T) {
	cases := []struct {
		name      string
		w, h      int
		rightOpen bool
		wantTODO  bool
		wantTeam  bool
	}{
		// 1160px content - 544px panels leaves 616px of transcript.
		{"1440 both panels", 1440, 900, false, true, true},
		// 600px content cannot host either 272px panel beside the
		// conversation, so both collapse and the 845px transcript keeps its
		// docked-panel width.
		{"1600 docked panel", 1600, 1000, true, false, false},
		// 400px content has no room for a side panel at all.
		{"1200 docked panel", 1200, 800, true, false, false},
		// The 900x600 minimum: the panels collapse and the right sidebar
		// stays at its 220px minimum instead of being squeezed to 76px.
		{"900 minimum", 900, 600, true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newLiveApp(t, &shellProvider{})
			h.app.Language = LangEN
			h.app.Screen = Session
			h.app.rightOpen = tc.rightOpen
			seedDeepSession(t, h)
			tt := ui.NewTester(h.app.View, tc.w, tc.h)
			tt.Frame()

			msg, ok := tt.Find("please audit the narrow layout")
			if !ok {
				t.Fatalf("user message missing: %v", tt.Texts())
			}
			if msg.W < 80 || msg.H < 10 {
				t.Fatalf("user message rect = %+v, want a visible bubble (W>=80)", msg)
			}
			if msg.X < 0 || msg.X+msg.W > float32(tc.w) {
				t.Fatalf("user message rect %+v is outside the %dpx window", msg, tc.w)
			}

			_, haveTodos := tt.Find("Todos")
			_, haveTeam := tt.Find("Team")
			if haveTodos != tc.wantTODO || haveTeam != tc.wantTeam {
				t.Fatalf("panels todo=%v team=%v, want todo=%v team=%v; texts: %v",
					haveTodos, haveTeam, tc.wantTODO, tc.wantTeam, tt.Texts())
			}
			if todo, ok := tt.Find("Todos"); ok && todo.X < 280 {
				t.Fatalf("todos panel starts at %v, inside the 280px sidebar", todo.X)
			}

			if tc.rightOpen {
				panel, ok := tt.Find("Right sidebar")
				if !ok {
					t.Fatal("right sidebar missing")
				}
				if panel.W < minRightPanelW {
					t.Fatalf("right sidebar width = %v, want >= %v", panel.W, minRightPanelW)
				}
				if panel.X+panel.W > float32(tc.w)+0.5 {
					t.Fatalf("right sidebar rect %+v runs past the window", panel)
				}
			}
		})
	}
}

// TestAutomationActionsRemainUsableWithDockedPanel covers the task row beside
// the docked right sidebar: the page column is 304px, which used to shrink
// Delete and Edit to W=0 and Run now to 47px. The responsive row now wraps its
// actions onto a second line, keeping every target at least 44px wide and
// clickable at 900, 1200 and 1366.
func TestAutomationActionsRemainUsableWithDockedPanel(t *testing.T) {
	for _, size := range []struct{ w, h int }{{900, 600}, {1200, 800}, {1366, 768}} {
		size := size
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			h := newLiveApp(t, &shellProvider{})
			h.app.Language = LangEN
			const title = "Daily dependency audit report"
			if _, err := h.ctrl.Automations().Create(store.Automation{
				Title: title, Cron: "@hourly", Enabled: true,
			}); err != nil {
				t.Fatal(err)
			}
			h.app.Screen = Automation
			h.app.rightOpen = true
			tt := ui.NewTester(h.app.View, size.w, size.h)
			tt.Frame()

			// The main column starts at x=280 and ends where the docked right
			// sidebar begins; the automation page column is centered inside
			// it, so no action may paint past that boundary. The sidebar is
			// responsive since the brand pass, so read its rect instead of
			// pinning a DSH-era x=632.
			panel, ok := tt.Find("Right sidebar")
			if !ok {
				t.Fatalf("docked right sidebar missing: %v", tt.Texts())
			}
			pageRight := panel.X
			for _, label := range []string{"Run now", "Disable", "Edit", "Delete"} {
				r, ok := tt.Find(label)
				if !ok {
					t.Fatalf("%q action missing: %v", label, tt.Texts())
				}
				if r.W < 44 {
					t.Fatalf("%q width = %v, want >= 44", label, r.W)
				}
				if r.H < 24 {
					t.Fatalf("%q height = %v, want a >=24px target", label, r.H)
				}
				if r.X < 280 || r.X+r.W > pageRight {
					t.Fatalf("%q rect %+v is outside the 280..%v page column", label, r, pageRight)
				}
			}
			if err := tt.Click("Delete"); err != nil {
				t.Fatalf("click Delete: %v", err)
			}
			tt.Frame()
			tasks, err := h.ctrl.Automations().List()
			if err != nil {
				t.Fatal(err)
			}
			for _, task := range tasks {
				if task.Title == title {
					t.Fatalf("Delete left %q in the list", title)
				}
			}
		})
	}
}

// TestLiveSessionUsagePillsStayInsideComposer covers the usage footer beside
// the docked right sidebar: the six detailed pills used to paint up to x=826
// while the 361px composer ended at x=656 and the panel began at 680. Every
// pill must stay within the composer's own width (wrapping onto more lines is
// fine) at 1200x800 and 900x600.
func TestLiveSessionUsagePillsStayInsideComposer(t *testing.T) {
	for _, size := range []struct{ w, h int }{{1200, 800}, {900, 600}} {
		size := size
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			h := newLiveApp(t, &shellProvider{})
			h.app.Language = LangEN
			h.app.Screen = Session
			h.app.rightOpen = true
			snap := &state.Snapshot{
				SessionID: h.ctrl.CurrentSessionID(),
				Phase:     state.PhaseIdle,
				Usage: state.Usage{
					Turns: 3, Steps: 20, In: 1000, Out: 2000, CacheHit: 900,
				},
			}
			h.app.SetSnapshot(snap)
			h.app.liveMenus().lastTurnDuration = 12 * time.Second
			tt := ui.NewTester(h.app.View, size.w, size.h)
			tt.Frame()

			composer, ok := tt.Find("Session composer")
			if !ok {
				t.Fatal("composer missing")
			}
			usage, ok := tt.Find("Session usage")
			if !ok {
				t.Fatal("usage footer missing")
			}
			if usage.W > composer.W+1 {
				t.Fatalf("usage row width = %v, composer width = %v", usage.W, composer.W)
			}
			segments := h.app.usageSegments(snap)
			if len(segments) < 5 {
				t.Fatalf("expected the detailed segments, got %v", segments)
			}
			for _, segment := range segments {
				r, ok := tt.Find(segment)
				if !ok {
					t.Fatalf("usage pill %q missing: %v", segment, tt.Texts())
				}
				if r.X < composer.X-0.5 || r.X+r.W > composer.X+composer.W+0.5 {
					t.Fatalf("usage pill %q rect %+v is outside composer %+v", segment, r, composer)
				}
			}
		})
	}
}

// TestFilesBreadcrumbKeepsReloadAtMinimum covers the 900x600 Files panel: a
// normal two-level path used to push Reload outside the window (x=924, W=0)
// and clip the last segment to W=0. Leading segments collapse into an
// ellipsis menu and Reload stays a >=24px target inside the panel. The path is
// built inside the fixture's temporary workspace; it deliberately does not
// point at the retired docs/baseline tree.
func TestFilesBreadcrumbKeepsReloadAtMinimum(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	if err := os.MkdirAll(filepath.Join(h.dir, "docs", "guides", "screenshots"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.app.Screen = Session
	h.app.rightOpen = true
	h.app.rightTab = filesTab
	h.app.filesDir = "docs/guides/screenshots"

	tt := ui.NewTester(h.app.View, 900, 600)
	tt.Frame()
	panel, ok := tt.Find("Right sidebar")
	if !ok {
		t.Fatal("right sidebar missing")
	}
	reload, ok := tt.Find("Reload")
	if !ok {
		t.Fatalf("Reload missing at 900x600: %v", tt.Texts())
	}
	if reload.W < 24 || reload.H < 24 {
		t.Fatalf("Reload target = %vx%v, want >=24x24", reload.W, reload.H)
	}
	if reload.X < panel.X || reload.X+reload.W > panel.X+panel.W {
		t.Fatalf("Reload rect %+v is outside the panel %+v", reload, panel)
	}
	last, ok := tt.Find("screenshots")
	if !ok || last.W < 24 {
		t.Fatalf("last breadcrumb segment = %+v, want a visible screenshot label", last)
	}
	if last.X < panel.X || last.X+last.W > reload.X {
		t.Fatalf("last segment %+v overlaps the Reload control at %+v", last, reload)
	}
	if _, ok := tt.Find("docs"); ok {
		t.Fatalf("leading segment docs is still painted at 900px: %v", tt.Texts())
	}
	if _, ok := tt.Find("Hidden path segments"); !ok {
		t.Fatalf("collapsed breadcrumb has no ellipsis control: %v", tt.Texts())
	}

	// The ellipsis opens a menu that still reaches every hidden directory.
	if err := tt.Click("Hidden path segments"); err != nil {
		t.Fatalf("open hidden segments: %v", err)
	}
	tt.Frame()
	if err := tt.Click("docs"); err != nil {
		t.Fatalf("click hidden docs segment: %v", err)
	}
	tt.Frame()
	if got := h.app.filesDir; got != "docs" {
		t.Fatalf("hidden segment navigation = %q, want docs", got)
	}

	// At 1600 the whole path fits and no ellipsis is needed.
	h.app.filesDir = "docs/guides/screenshots"
	wide := ui.NewTester(h.app.View, 1600, 1000)
	wide.Frame()
	if _, ok := wide.Find("docs"); !ok {
		t.Fatalf("wide breadcrumb hides docs: %v", wide.Texts())
	}
	if _, ok := wide.Find("Hidden path segments"); ok {
		t.Fatalf("wide breadcrumb still collapses segments: %v", wide.Texts())
	}
}

// TestFilesBreadcrumbClampsLongPreviewName covers a preview with no directory
// segments: a long file name must ellipsize rather than push the pinned Reload
// control past the 220px panel.
func TestFilesBreadcrumbClampsLongPreviewName(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	h.app.rightOpen = true
	h.app.rightTab = filesTab
	const name = "a-very-long-preview-file-name-that-cannot-fit-the-panel.txt"
	rel := name
	if err := os.WriteFile(filepath.Join(h.dir, name), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.app.openFilePreview(rel)
	tt := ui.NewTester(h.app.View, 900, 600)
	tt.Frame()

	panel, _ := tt.Find("Right sidebar")
	reload, ok := tt.Find("Reload")
	if !ok {
		t.Fatalf("Reload missing in long-name preview: %v", tt.Texts())
	}
	if reload.W < 24 || reload.X+reload.W > panel.X+panel.W {
		t.Fatalf("Reload rect %+v is not pinned inside the panel %+v", reload, panel)
	}
	// The hidden breadcrumb control must not appear when there is nothing to
	// hide (a single long file name is clamped, not folded).
	if _, ok := tt.Find("Hidden path segments"); ok {
		t.Fatalf("single-name preview folded into an empty ellipsis menu: %v", tt.Texts())
	}
}
