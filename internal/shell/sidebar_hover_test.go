package shell

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestWorkspaceHoverCard checks the pointer hover card: name, root path
// abbreviated against the home directory, created time, copy-to-clipboard
// with a toast, and that the row's click and context menu still work.
func TestWorkspaceHoverCard(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, "proj")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	h := newLiveApp(t, &shellProvider{})
	origin := h.ctrl.CurrentWorkspaceID()
	ws, err := h.ctrl.CreateWorkspace(context.Background(), "hovered", root)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	// CreateWorkspace selects the new workspace; go back to the first one
	// so the row under test is not the selected row.
	if h.ctrl.CurrentWorkspaceID() != ws.ID {
		t.Fatal("test setup: new workspace should be current")
	}
	if err := h.ctrl.SelectWorkspace(context.Background(), origin); err != nil {
		t.Fatalf("SelectWorkspace: %v", err)
	}

	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if h.app.wsHoverID != "" {
		t.Fatalf("hover card open before hovering: %q", h.app.wsHoverID)
	}

	row, ok := tt.Find("hovered")
	if !ok {
		t.Fatalf("workspace row missing: %q", tt.Texts())
	}
	tt.Move(row.X+row.W/2, row.Y+row.H/2)
	tt.Frame()
	if h.app.wsHoverID != ws.ID {
		t.Fatalf("hover id = %q, want %q", h.app.wsHoverID, ws.ID)
	}

	wantPath := "~" + string(filepath.Separator) + "proj"
	if !tt.HasText(wantPath) {
		t.Fatalf("hover card path missing %q: %q", wantPath, tt.Texts())
	}
	wantCreated := "Created " + formatCreatedAt(ws.CreatedAt)
	if !tt.HasText(wantCreated) {
		t.Fatalf("hover card creation time missing %q: %q", wantCreated, tt.Texts())
	}
	if _, ok := tt.Find("Copy path"); !ok {
		t.Fatalf("hover card copy action missing: %q", tt.Texts())
	}

	// Copy the root path; the card closes after copying.
	if err := tt.Click("Copy path"); err != nil {
		t.Fatalf("click Copy path: %v", err)
	}
	if got := tt.Clipboard(); got != root {
		t.Fatalf("clipboard = %q, want %q", got, root)
	}
	if !tt.HasText("Copied workspace path") {
		t.Fatalf("copy toast missing: %q", tt.Texts())
	}
	if h.app.wsHoverID != "" {
		t.Fatalf("hover card stayed open after copy: %q", h.app.wsHoverID)
	}

	// Hovering another workspace switches the card.
	originRow, ok := tt.Find("test")
	if !ok {
		t.Fatalf("origin workspace row missing: %q", tt.Texts())
	}
	tt.Move(originRow.X+originRow.W/2, originRow.Y+originRow.H/2)
	tt.Frame()
	if h.app.wsHoverID != origin {
		t.Fatalf("hover id after moving = %q, want %q", h.app.wsHoverID, origin)
	}

	// Leaving both the row and the card dismisses the card after a short
	// grace period.
	tt.Move(900, 800)
	tt.Frame()
	if h.app.wsHoverID != origin {
		t.Fatalf("hover id left the row too early: %q", h.app.wsHoverID)
	}
	tt.Advance(hoverCloseDelay + 40*time.Millisecond)
	tt.Frame()
	if h.app.wsHoverID != "" {
		t.Fatalf("hover card did not close after leaving: %q", h.app.wsHoverID)
	}
	if tt.HasText("Copy path") {
		t.Fatalf("dismissed hover card is still rendered: %q", tt.Texts())
	}

	// A row click still selects the workspace, and the context menu still
	// opens from the row.
	if err := tt.Click("hovered"); err != nil {
		t.Fatalf("click hovered row: %v", err)
	}
	if got := h.ctrl.CurrentWorkspaceID(); got != ws.ID {
		t.Fatalf("row click workspace = %q, want %q", got, ws.ID)
	}
	if err := tt.Click("test"); err != nil {
		t.Fatalf("click test row: %v", err)
	}
	if got := h.ctrl.CurrentWorkspaceID(); got != origin {
		t.Fatalf("row click back workspace = %q, want %q", got, origin)
	}

	if err := tt.RightClick("hovered"); err != nil {
		t.Fatalf("right-click hovered row: %v", err)
	}
	if !tt.HasText("Rename workspace") {
		t.Fatalf("workspace context menu missing after hover: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)

	// The row's actions stay usable while its hover card shows: the "+"
	// creates a session in that workspace, and the ellipsis opens its menu.
	tt.Move(row.X+row.W/2, row.Y+row.H/2)
	tt.Frame()
	if _, ok := tt.Find("New session in hovered"); !ok {
		t.Fatalf("hovered row lost its new-session action: %q", tt.Texts())
	}
	if err := tt.Click("New session in hovered"); err != nil {
		t.Fatalf("click New session in hovered: %v", err)
	}
	if h.app.Screen != Session {
		t.Fatalf("new session action screen = %v, want Session", h.app.Screen)
	}
	sess, err := h.ctrl.CurrentSession()
	if err != nil {
		t.Fatalf("CurrentSession: %v", err)
	}
	if sess.WorkspaceID != ws.ID {
		t.Fatalf("new session workspace = %q, want %q", sess.WorkspaceID, ws.ID)
	}
}

// TestWorkspaceDragReorderPersists drops Alpha before the first workspace,
// checks ui.workspace_order, the rendered order, a fresh App reading it,
// and the menu's Move up/Move down fallback.
func TestWorkspaceDragReorderPersists(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	first := h.ctrl.CurrentWorkspaceID()
	alpha, err := h.ctrl.CreateWorkspace(ctx, "Alpha", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace Alpha: %v", err)
	}
	beta, err := h.ctrl.CreateWorkspace(ctx, "Beta", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace Beta: %v", err)
	}
	// Creating a workspace selects it; put the controller back on the first
	// workspace so a row click during the drag would be visible.
	if err := h.ctrl.SelectWorkspace(ctx, first); err != nil {
		t.Fatalf("SelectWorkspace: %v", err)
	}
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	src, ok := tt.Find("Alpha")
	if !ok {
		t.Fatalf("Alpha row missing: %q", tt.Texts())
	}
	dst, ok := tt.Find("test")
	if !ok {
		t.Fatalf("test row missing: %q", tt.Texts())
	}
	// Drop on the top half of the first row: Alpha goes before it.
	x, y := dst.X+dst.W/2, dst.Y+2
	tt.Press(src.X+src.W/2, src.Y+src.H/2)
	tt.Move(x, y)
	tt.Release(x, y)

	want := alpha.ID + "," + first + "," + beta.ID
	raw, err := h.store.GetSetting(settingWorkspaceOrder)
	if err != nil {
		t.Fatalf("GetSetting(%s): %v", settingWorkspaceOrder, err)
	}
	if raw != want {
		t.Fatalf("%s = %q, want %q", settingWorkspaceOrder, raw, want)
	}
	// A drag is not a click: it must not switch the workspace.
	if got := h.ctrl.CurrentWorkspaceID(); got != first {
		t.Fatalf("drag changed workspace to %q, want %q", got, first)
	}

	// Rendered order and a fresh App's order both come from the setting.
	orderIDs := func(app *App) []string {
		list, err := app.workspaceList()
		if err != nil {
			t.Fatalf("workspaceList: %v", err)
		}
		ids := make([]string, 0, len(list))
		for _, ws := range list {
			ids = append(ids, ws.ID)
		}
		return ids
	}
	got := orderIDs(h.app)
	wantIDs := []string{alpha.ID, first, beta.ID}
	if strings.Join(got, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("rendered workspace order = %v, want %v", got, wantIDs)
	}
	fresh := &App{Ctrl: h.ctrl, Screen: Home}
	if got := orderIDs(fresh); strings.Join(got, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("fresh App workspace order = %v, want %v", got, wantIDs)
	}
	alphaRect, _ := tt.Find("Alpha")
	firstRect, _ := tt.Find("test")
	if alphaRect.Y >= firstRect.Y {
		t.Fatalf("Alpha y=%v is not above test y=%v after the drop", alphaRect.Y, firstRect.Y)
	}

	// The workspace menu's Move down is the accessible fallback: Alpha goes
	// back below the first workspace.
	if err := tt.RightClick("Alpha"); err != nil {
		t.Fatalf("right-click Alpha: %v", err)
	}
	if !tt.HasText("Move down") {
		t.Fatalf("workspace menu missing Move down: %q", tt.Texts())
	}
	if err := tt.Click("Move down"); err != nil {
		t.Fatalf("click Move down: %v", err)
	}
	want = first + "," + alpha.ID + "," + beta.ID
	raw, err = h.store.GetSetting(settingWorkspaceOrder)
	if err != nil {
		t.Fatalf("GetSetting after move: %v", err)
	}
	if raw != want {
		t.Fatalf("after Move down %s = %q, want %q", settingWorkspaceOrder, raw, want)
	}

	// Move up is the reverse control and returns Alpha to the front.
	if err := tt.RightClick("Alpha"); err != nil {
		t.Fatalf("right-click Alpha after Move down: %v", err)
	}
	if !tt.HasText("Move up") {
		t.Fatalf("workspace menu missing Move up: %q", tt.Texts())
	}
	if err := tt.Click("Move up"); err != nil {
		t.Fatalf("click Move up: %v", err)
	}
	want = alpha.ID + "," + first + "," + beta.ID
	raw, err = h.store.GetSetting(settingWorkspaceOrder)
	if err != nil {
		t.Fatalf("GetSetting after Move up: %v", err)
	}
	if raw != want {
		t.Fatalf("after Move up %s = %q, want %q", settingWorkspaceOrder, raw, want)
	}
}

// TestSessionPinSortAndCopyID pins an old session from its row menu, checks
// that it persists as session.<id>.pinned, that pinned sessions lead the
// sidebar order, and that the menu copies the session id.
func TestSessionPinSortAndCopyID(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	liveID := h.ctrl.CurrentSessionID()
	alphaID, err := h.ctrl.CreateSession(ctx, "alpha")
	if err != nil {
		t.Fatalf("CreateSession alpha: %v", err)
	}
	betaID, err := h.ctrl.CreateSession(ctx, "beta")
	if err != nil {
		t.Fatalf("CreateSession beta: %v", err)
	}
	if _, err := h.ctrl.OpenSession(ctx, liveID); err != nil {
		t.Fatalf("OpenSession live: %v", err)
	}
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	sessions, err := h.ctrl.SessionsForWorkspace(ctx, h.ctrl.CurrentWorkspaceID(), false, false)
	if err != nil {
		t.Fatalf("SessionsForWorkspace: %v", err)
	}

	// Pin the oldest session; it moves to the front of the group.
	if err := tt.RightClick("live"); err != nil {
		t.Fatalf("right-click live session: %v", err)
	}
	if !tt.HasText("Pin") {
		t.Fatalf("session menu missing Pin: %q", tt.Texts())
	}
	if err := tt.Click("Pin"); err != nil {
		t.Fatalf("click Pin: %v", err)
	}
	raw, err := h.store.GetSetting(sessionPinnedKey(liveID))
	if err != nil {
		t.Fatalf("GetSetting pin: %v", err)
	}
	if raw != "true" {
		t.Fatalf("%s = %q, want true", sessionPinnedKey(liveID), raw)
	}
	pinned := h.app.pinnedFirst(sessions)
	if len(pinned) == 0 || pinned[0].ID != liveID {
		t.Fatalf("pinnedFirst = %+v, want live first", pinned)
	}
	fresh := &App{Ctrl: h.ctrl}
	if !fresh.sessionPinned(liveID) {
		t.Fatal("a fresh App did not read the persisted pin")
	}
	liveRect, _ := tt.Find("live")
	betaRect, _ := tt.Find("beta")
	alphaRect, _ := tt.Find("alpha")
	if liveRect.Y >= betaRect.Y || liveRect.Y >= alphaRect.Y {
		t.Fatalf("pinned live y=%v is not above beta y=%v and alpha y=%v",
			liveRect.Y, betaRect.Y, alphaRect.Y)
	}

	// The menu now offers Unpin, which clears the setting.
	if err := tt.RightClick("live"); err != nil {
		t.Fatalf("right-click pinned live: %v", err)
	}
	if !tt.HasText("Unpin") {
		t.Fatalf("pinned session menu missing Unpin: %q", tt.Texts())
	}
	if err := tt.Click("Unpin"); err != nil {
		t.Fatalf("click Unpin: %v", err)
	}
	if raw, err := h.store.GetSetting(sessionPinnedKey(liveID)); err != nil || raw != "false" {
		t.Fatalf("%s = %q, %v; want false", sessionPinnedKey(liveID), raw, err)
	}
	if h.app.sessionPinned(liveID) {
		t.Fatal("Unpin left the in-memory pin set")
	}

	// Copy session ID writes the complete id, not the title.
	if err := tt.RightClick("alpha"); err != nil {
		t.Fatalf("right-click alpha: %v", err)
	}
	if !tt.HasText("Copy session ID") {
		t.Fatalf("session menu missing Copy session ID: %q", tt.Texts())
	}
	if err := tt.Click("Copy session ID"); err != nil {
		t.Fatalf("click Copy session ID: %v", err)
	}
	if got := tt.Clipboard(); got != alphaID {
		t.Fatalf("clipboard = %q, want session id %q", got, alphaID)
	}
	if !tt.HasText("Copied session ID") {
		t.Fatalf("copy id toast missing: %q", tt.Texts())
	}
	if betaID == liveID || alphaID == betaID {
		t.Fatalf("test setup produced duplicate session ids: %q %q %q", liveID, alphaID, betaID)
	}
	// The sessions remain store sessions with the pinned key absent for
	// sessions never pinned.
	if _, err := h.store.GetSetting(sessionPinnedKey(betaID)); err == nil {
		t.Fatalf("beta has an unexpected pin setting")
	}
}
