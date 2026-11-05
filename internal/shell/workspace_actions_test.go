package shell

import (
	"context"
	"testing"
)

// TestWorkspaceNewSessionAction proves the workspace row's "+" action creates
// a session inside that workspace and opens it, which is the DSH behavior.
func TestWorkspaceNewSessionAction(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	second, err := h.ctrl.CreateWorkspace(ctx, "second", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	app := &App{Ctrl: h.ctrl, Screen: Home}
	app.newSessionInWorkspace(*second)
	if app.Screen != Session {
		t.Fatalf("screen=%v, want Session", app.Screen)
	}
	sess, err := h.ctrl.CurrentSession()
	if err != nil {
		t.Fatalf("CurrentSession: %v", err)
	}
	if sess.WorkspaceID != second.ID {
		t.Fatalf("session workspace=%q, want %q", sess.WorkspaceID, second.ID)
	}
	// The session is listed under that workspace.
	sessions, err := h.ctrl.SessionsForWorkspace(ctx, second.ID, false, false)
	if err != nil {
		t.Fatalf("SessionsForWorkspace: %v", err)
	}
	found := false
	for _, s := range sessions {
		if s.ID == sess.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("new session %q not under workspace %q", sess.ID, second.ID)
	}
}
