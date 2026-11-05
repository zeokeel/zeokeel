package shell

import (
	"context"
	"testing"

	"github.com/zeokeel/zeokeel/internal/state"
)

// TestViewedSessionIDPrefersSnapshot pins the "session on screen" precedence:
// a background submit can move the controller's current session, so the
// snapshot the view draws wins; the controller is the fallback for frames that
// have no snapshot yet.
func TestViewedSessionIDPrefersSnapshot(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	controllerCurrent := h.ctrl.CurrentSessionID()
	if controllerCurrent == "" {
		t.Fatal("fixture has no current session")
	}

	h.app.SetSnapshot(&state.Snapshot{SessionID: "shown-session"})
	if got := h.app.viewedSessionID(); got != "shown-session" {
		t.Fatalf("viewedSessionID() = %q, want the snapshot's session", got)
	}

	h.app.SetSnapshot(&state.Snapshot{})
	if got := h.app.viewedSessionID(); got != controllerCurrent {
		t.Fatalf("viewedSessionID() = %q, want the controller's current session %q", got, controllerCurrent)
	}
}

// TestFilesRootFollowsViewedSession proves the Files tree is rooted at the
// shown session's workspace, not at whichever workspace the controller has
// selected: a background submit in another workspace may move the selection
// while the screen still shows the first session.
func TestFilesRootFollowsViewedSession(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()

	wsA, ok, err := h.ctrl.CurrentWorkspace(ctx)
	if err != nil || !ok {
		t.Fatalf("CurrentWorkspace: %v, ok=%v", err, ok)
	}
	sessA := h.ctrl.CurrentSessionID()
	if sessA == "" {
		t.Fatal("fixture has no current session")
	}

	rootB := t.TempDir()
	wsB, err := h.ctrl.CreateWorkspace(ctx, "other", rootB)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sessB, err := h.ctrl.CreateSessionInWorkspace(ctx, wsB.ID, "in other")
	if err != nil {
		t.Fatalf("CreateSessionInWorkspace: %v", err)
	}
	snapB, err := h.ctrl.OpenSession(ctx, sessB)
	if err != nil {
		t.Fatalf("OpenSession(%q): %v", sessB, err)
	}
	// Make the controller's current session differ from the one on screen,
	// the state a background/queued submit leaves behind.
	if _, err := h.ctrl.OpenSession(ctx, sessA); err != nil {
		t.Fatalf("OpenSession(%q): %v", sessA, err)
	}
	h.app.SetSnapshot(snapB)

	if got := h.app.viewedSessionID(); got != sessB {
		t.Fatalf("viewedSessionID() = %q, want %q", got, sessB)
	}
	if got := h.app.filesRoot(); got != rootB {
		t.Fatalf("filesRoot() = %q, want the shown session's workspace %q", got, rootB)
	}

	// Selecting the other workspace (what the sidebar or another submit does)
	// must not move the tree away from the session the screen shows.
	if err := h.ctrl.SelectWorkspace(ctx, wsA.ID); err != nil {
		t.Fatalf("SelectWorkspace(%q): %v", wsA.ID, err)
	}
	if got := h.app.filesRoot(); got != rootB {
		t.Fatalf("after selecting workspace A, filesRoot() = %q, want %q", got, rootB)
	}

	// Home has no session to follow: the selected workspace is the answer.
	h.app.Screen = Home
	h.app.SetSnapshot(nil)
	if got := h.app.filesRoot(); got != wsA.Root {
		t.Fatalf("Home filesRoot() = %q, want the selected workspace %q", got, wsA.Root)
	}
}

// TestFilesStateResetsOnSessionSwitch proves a session change clears the
// directory and preview a different session opened: the previewed relative
// path may not exist under the next session's workspace, so it must not leak.
func TestFilesStateResetsOnSessionSwitch(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()

	h.app.SetSnapshot(h.ctrl.Snapshot())
	// The first frame only records the session: it must not discard a
	// navigation an earlier frame set up (the filesDir tests rely on it).
	h.app.filesDir = "sub"
	h.app.filePath = "sub/note.txt"
	h.app.fileText = "stale"
	h.app.fileErr = "stale error"
	h.app.ensureFilesSession()
	if h.app.filesDir != "sub" || h.app.filePath != "sub/note.txt" {
		t.Fatalf("first ensureFilesSession reset navigation: dir=%q path=%q",
			h.app.filesDir, h.app.filePath)
	}

	// The same session keeps its navigation.
	h.app.ensureFilesSession()
	if h.app.filesDir != "sub" || h.app.filePath != "sub/note.txt" {
		t.Fatalf("same-session ensureFilesSession reset navigation: dir=%q path=%q",
			h.app.filesDir, h.app.filePath)
	}

	sessB, err := h.ctrl.CreateSession(ctx, "second")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	snapB, err := h.ctrl.OpenSession(ctx, sessB)
	if err != nil {
		t.Fatalf("OpenSession(%q): %v", sessB, err)
	}
	h.app.SetSnapshot(snapB)
	h.app.ensureFilesSession()

	if h.app.filesDir != "" || h.app.filePath != "" || h.app.fileText != "" || h.app.fileErr != "" {
		t.Fatalf("session switch kept stale Files state: dir=%q path=%q text=%q err=%q",
			h.app.filesDir, h.app.filePath, h.app.fileText, h.app.fileErr)
	}
	if got := h.app.filesRoot(); got != h.app.workspaceRoot() {
		t.Fatalf("filesRoot() = %q, want the shared workspace root %q", got, h.app.workspaceRoot())
	}
}

// TestFilesRootDataDirFallback covers a workspace without a root: the app
// builds its agent for the data directory, so the tree must show that root
// instead of reporting "no workspace root".
func TestFilesRootDataDirFallback(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ZEOKEEL_DATA_DIR", dataDir)

	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	ws, err := h.ctrl.CreateWorkspace(ctx, "no root", "")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess, err := h.ctrl.CreateSessionInWorkspace(ctx, ws.ID, "rootless")
	if err != nil {
		t.Fatalf("CreateSessionInWorkspace: %v", err)
	}
	snap, err := h.ctrl.OpenSession(ctx, sess)
	if err != nil {
		t.Fatalf("OpenSession(%q): %v", sess, err)
	}
	h.app.SetSnapshot(snap)

	if got := h.app.filesRoot(); got != dataDir {
		t.Fatalf("filesRoot() = %q, want the data directory %q", got, dataDir)
	}
}
