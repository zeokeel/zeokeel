package shell

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
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
