package shell

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/session"
)

func TestHomeLiveSendCreatesSession(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	app := &App{Ctrl: h.ctrl, Language: LangEN, Screen: Home}
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus composer: %v", err)
	}
	tt.Type("hello from home")
	if err := tt.Click("Send message"); err != nil {
		t.Fatalf("click Send: %v", err)
	}
	if app.Screen != Session {
		t.Fatalf("screen=%v, want Session", app.Screen)
	}
	sess, err := h.ctrl.CurrentSession()
	if err != nil {
		t.Fatalf("CurrentSession: %v", err)
	}
	ws, ok, err := h.ctrl.CurrentWorkspace(context.Background())
	if err != nil || !ok {
		t.Fatalf("CurrentWorkspace: %v ok=%v", err, ok)
	}
	if sess.WorkspaceID != ws.ID {
		t.Fatalf("session workspace=%q want %q", sess.WorkspaceID, ws.ID)
	}
}

func TestHomeLiveNoWorkspaceAsksForOne(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	wsID := h.ctrl.CurrentWorkspaceID()
	if err := h.ctrl.DeleteWorkspace(ctx, wsID); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}
	app := &App{Ctrl: h.ctrl, Language: LangEN, Screen: Home}
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus composer: %v", err)
	}
	tt.Type("needs a workspace")
	if err := tt.Click("Send message"); err != nil {
		t.Fatalf("click Send: %v", err)
	}
	if !app.wsDialogOpen {
		t.Fatalf("send without workspace should open the add-workspace dialog")
	}
}

func TestSidebarWorkspaceCollapseHidesSessions(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	first, err := h.ctrl.CreateSession(ctx, "current one")
	if err != nil {
		t.Fatalf("CreateSession current: %v", err)
	}
	if _, err := h.ctrl.CreateSession(ctx, "visible session"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := h.ctrl.OpenSession(ctx, first); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	app := &App{Ctrl: h.ctrl, Language: LangEN, Screen: Session}
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("visible session") {
		t.Fatalf("session not visible before collapse: %v", tt.Texts())
	}
	wsID := h.ctrl.CurrentWorkspaceID()
	if app.wsCollapsed == nil {
		app.wsCollapsed = map[string]bool{}
	}
	app.wsCollapsed[wsID] = true
	tt.Frame()
	if tt.HasText("visible session") {
		t.Fatalf("session still visible after collapse: %v", tt.Texts())
	}
}

// TestHomeEmptySendDoesNotCreateSession guards the disabled Send state: an
// empty click or Enter must not create a session or navigate to one.
func TestHomeEmptySendDoesNotCreateSession(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	app := &App{Ctrl: h.ctrl, Language: LangEN, Screen: Home}
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()

	before, err := h.ctrl.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Send message"); err != nil {
		t.Fatalf("click empty Send message: %v", err)
	}
	if app.Screen != Home {
		t.Fatalf("empty Send left Home for %v", app.Screen)
	}
	// Enter in the empty composer takes the same early return.
	if err := tt.Click("Composer input"); err != nil {
		t.Fatalf("focus composer: %v", err)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	after, err := h.ctrl.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("empty submit created a session: before %d, after %d", len(before), len(after))
	}
	if app.Screen != Home {
		t.Fatalf("empty Enter left Home for %v", app.Screen)
	}
}

// TestHomeBrandHero pins the native Home's brand header: the ZeoKeel wordmark
// and an owned tagline are rendered, and the retired DSH hero copy and badge
// are gone (docs/design/native-ui.md §3.2).
func TestHomeBrandHero(t *testing.T) {
	h := newLiveAppAt(t, Home)
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	for _, want := range []string{"ZeoKeel", "What will you build today?"} {
		if !tt.HasText(want) {
			t.Fatalf("Home is missing the brand header %q: %q", want, tt.Texts())
		}
	}
	for _, gone := range []string{"Into the Unknown", "Preview"} {
		if tt.HasText(gone) {
			t.Fatalf("Home still renders the retired DSH copy %q: %q", gone, tt.Texts())
		}
	}

	// The content column is theme-derived and capped: the composer card fills
	// it on a 1600x1000 window instead of the retired 877/909 fixed widths.
	if r, ok := tt.Find("Home composer"); ok {
		if r.W > 720+1 {
			t.Fatalf("Home composer width = %v, want the 720 DIP brand column", r.W)
		}
		if r.W < 320 {
			t.Fatalf("Home composer width = %v, want a usable card on a wide window", r.W)
		}
	} else {
		t.Fatalf("Home composer is not labeled: %q", tt.Texts())
	}
}

// TestRenderHomeNativePreview renders the Home screen head for brand review:
// light and dark at the 1600x1000 design size. The PNGs are written to
// docs/design/preview so the design is reproducible straight from the test.
func TestRenderHomeNativePreview(t *testing.T) {
	dir := previewDir(t)
	for _, tc := range []struct {
		name  string
		theme string
	}{
		{"light", session.ThemeLight},
		{"dark", session.ThemeDark},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newLiveApp(t, &shellProvider{})
			// Chinese is the product default language; the preview shows the
			// shipped first-run look.
			h.app.Language = LangZH
			if err := h.store.SetSetting(session.SettingTheme, tc.theme); err != nil {
				t.Fatalf("theme setting: %v", err)
			}
			h.app.Screen = Home
			img := ui.Render(h.app.View, 1600, 1000, 1)
			path := filepath.Join(dir, "home-native-"+tc.name+".png")
			f, err := os.Create(path)
			if err != nil {
				t.Fatalf("create %s: %v", path, err)
			}
			if err := png.Encode(f, img); err != nil {
				f.Close()
				t.Fatalf("encode %s: %v", path, err)
			}
			if err := f.Close(); err != nil {
				t.Fatalf("close %s: %v", path, err)
			}
			if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
				t.Fatalf("preview %s is empty: %v", path, err)
			}
			t.Logf("wrote %s", path)
		})
	}
}
