package shell

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/store"
)

// TestWorkspaceCRUDSwitchAndPersist drives the Add workspace dialog, the
// picker injection, switching, renaming, deleting and the ui.workspace
// persistence through the real shell and a temporary store.
func TestWorkspaceCRUDSwitchAndPersist(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := t.Context()
	tt := ui.NewTester(h.app.View, 1600, 1000)

	origID := h.ctrl.CurrentWorkspaceID()
	if origID == "" {
		t.Fatal("no workspace selected at start")
	}

	dir2 := t.TempDir()
	h.app.PickDirectory = func() (string, error) { return dir2, nil }

	// Add workspace: name typed into the focused field, root from Browse….
	if err := tt.Click("Add workspace"); err != nil {
		t.Fatalf("click Add workspace: %v", err)
	}
	if !tt.HasText("Workspace name") {
		t.Fatalf("add workspace dialog missing: %q", tt.Texts())
	}
	tt.Type("Second")
	if err := tt.Click("Browse…"); err != nil {
		t.Fatalf("click Browse: %v", err)
	}
	if h.app.wsRoot != dir2 {
		t.Fatalf("Browse root = %q, want %q", h.app.wsRoot, dir2)
	}
	if err := tt.Click("Create"); err != nil {
		t.Fatalf("click Create: %v", err)
	}

	workspaces, err := h.ctrl.ListWorkspaces(ctx)
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(workspaces) != 2 {
		t.Fatalf("workspaces = %+v, want 2", workspaces)
	}
	secondID := h.ctrl.CurrentWorkspaceID()
	if secondID == origID {
		t.Fatal("creating a workspace did not switch to it")
	}
	second, err := h.store.GetWorkspace(secondID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if second.Name != "Second" || second.Root != dir2 {
		t.Fatalf("created workspace = %+v, want Second at %q", second, dir2)
	}
	// The selection is persisted as ui.workspace.
	if got, err := h.store.GetSetting(session.SettingWorkspace); err != nil || got != secondID {
		t.Fatalf("ui.workspace = %q, %v; want %q", got, err, secondID)
	}

	// A session created now belongs to the new workspace.
	secondSess, err := h.ctrl.CreateSession(ctx, "Second session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Switch back to the original workspace by clicking its row.
	if err := tt.Click("test"); err != nil {
		t.Fatalf("click original workspace: %v", err)
	}
	if got := h.ctrl.CurrentWorkspaceID(); got != origID {
		t.Fatalf("current workspace = %q, want %q", got, origID)
	}
	if got, _ := h.store.GetSetting(session.SettingWorkspace); got != origID {
		t.Fatalf("ui.workspace after switch = %q, want %q", got, origID)
	}

	// Rename the second workspace through its context menu.
	if err := tt.RightClick("Second"); err != nil {
		t.Fatalf("right-click workspace: %v", err)
	}
	if !tt.HasText("Rename workspace") {
		t.Fatalf("workspace menu missing rename: %q", tt.Texts())
	}
	if err := tt.Click("Rename workspace"); err != nil {
		t.Fatalf("click rename workspace: %v", err)
	}
	tt.Type(" renamed")
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("click Save: %v", err)
	}
	renamed, err := h.store.GetWorkspace(secondID)
	if err != nil {
		t.Fatalf("GetWorkspace after rename: %v", err)
	}
	if renamed.Name != "Second renamed" {
		t.Fatalf("workspace name = %q, want Second renamed", renamed.Name)
	}

	// Delete it; its sessions cascade.
	if err := tt.RightClick("Second renamed"); err != nil {
		t.Fatalf("right-click renamed workspace: %v", err)
	}
	if err := tt.Click("Delete workspace"); err != nil {
		t.Fatalf("click delete workspace: %v", err)
	}
	if !tt.HasText("Delete workspace?") {
		t.Fatalf("workspace delete dialog missing: %q", tt.Texts())
	}
	if err := tt.Click("Delete"); err != nil {
		t.Fatalf("confirm delete workspace: %v", err)
	}
	if _, err := h.store.GetWorkspace(secondID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("workspace still exists after delete: %v", err)
	}
	if _, err := h.store.GetSession(secondSess); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("workspace sessions did not cascade: %v", err)
	}
	if got := h.ctrl.CurrentWorkspaceID(); got != origID {
		t.Fatalf("after delete current workspace = %q, want %q", got, origID)
	}
}

// TestWorkspaceRestorePersistedSelection reopens the shell and checks the
// saved ui.workspace wins over the first workspace.
func TestWorkspaceRestorePersistedSelection(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := t.Context()
	first := h.ctrl.CurrentWorkspaceID()
	second, err := h.ctrl.CreateWorkspace(ctx, "Persisted", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	// Put the controller back on the first workspace but leave the persisted
	// preference pointing at the second, which is what a restart sees.
	if err := h.ctrl.SelectWorkspace(ctx, first); err != nil {
		t.Fatalf("SelectWorkspace first: %v", err)
	}
	if err := h.store.SetSetting(session.SettingWorkspace, second.ID); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	// A fresh App over the same controller skips its first-frame restore so
	// the persisted value is the only selection it has seen.
	app := &App{Screen: Home, Ctrl: h.ctrl}
	app.restoreWorkspace()
	if got := h.ctrl.CurrentWorkspaceID(); got != second.ID {
		t.Fatalf("restored workspace = %q, want %q (first %q)", got, second.ID, first)
	}
}

// contentSearchSeed creates sessions with searchable message content and an
// archived session, and returns their ids.
type contentSearchSeed struct {
	liveID, alphaID, betaID string
	workspaceID             string
}

func seedSearchSessions(t *testing.T, h *liveApp) contentSearchSeed {
	t.Helper()
	ctx := context.Background()
	ws := h.ctrl.CurrentWorkspaceID()
	liveID := h.ctrl.CurrentSessionID()

	alphaID, err := h.ctrl.CreateSession(ctx, "Alpha notes")
	if err != nil {
		t.Fatalf("CreateSession alpha: %v", err)
	}
	betaID, err := h.ctrl.CreateSession(ctx, "Beta plan")
	if err != nil {
		t.Fatalf("CreateSession beta: %v", err)
	}
	if _, err := h.store.AppendMessage(store.Message{
		SessionID: alphaID, Role: "user", Content: "the phoenix project kickoff",
	}); err != nil {
		t.Fatalf("AppendMessage alpha: %v", err)
	}
	if _, err := h.store.AppendMessage(store.Message{
		SessionID: betaID, Role: "assistant", Content: "budget spreadsheet attached",
	}); err != nil {
		t.Fatalf("AppendMessage beta: %v", err)
	}
	return contentSearchSeed{liveID: liveID, alphaID: alphaID, betaID: betaID, workspaceID: ws}
}

// sessionArchived reads a session's archive flag from the archived-aware
// single-row accessor.
func sessionArchived(t *testing.T, h *liveApp, id string) bool {
	t.Helper()
	sess, err := h.store.GetSessionWithArchive(id)
	if err != nil {
		t.Fatalf("GetSessionWithArchive: %v", err)
	}
	return sess.Archived
}

// TestSessionSearchMatchesContentAndOpens checks LIKE matching over message
// content through DSH's inline sidebar search, plus the separate Cmd/Ctrl+K
// palette with its keyboard navigation.
func TestSessionSearchMatchesContentAndOpens(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	seed := seedSearchSessions(t, h)
	tt := ui.NewTester(h.app.View, 1600, 1000)

	// The sidebar's Search sessions expands an inline field: no dialog.
	h.app.Screen = Home
	tt.Frame()
	if err := tt.Click("Search sessions"); err != nil {
		t.Fatalf("click sidebar search: %v", err)
	}
	if h.app.paletteOpen {
		t.Fatal("sidebar search opened the modal palette")
	}
	if !h.app.inlineSearch {
		t.Fatal("sidebar search did not expand the inline field")
	}
	if !tt.HasText("Search session names") {
		t.Fatalf("inline search field missing: %q", tt.Texts())
	}
	if _, ok := tt.Find("Sort: Last updated"); ok {
		t.Fatal("the search dialog is open, want the inline field only")
	}

	tt.Type("phoenix")
	results := h.app.searchResultsFor(h.app.inlineQuery)
	if len(results) != 1 || results[0].ID != seed.alphaID {
		t.Fatalf("content search results = %+v, want only Alpha", results)
	}
	if !tt.HasText("Alpha notes") {
		t.Fatalf("inline results do not show Alpha: %q", tt.Texts())
	}
	if err := tt.Click("Open Alpha notes"); err != nil {
		t.Fatalf("open inline result: %v", err)
	}
	if h.app.Screen != Session || h.ctrl.CurrentSessionID() != seed.alphaID {
		t.Fatalf("inline result opened screen %v session %q, want Alpha", h.app.Screen, h.ctrl.CurrentSessionID())
	}

	// Cmd/Ctrl+K keeps the separate palette, searchable by title too.
	h.app.Screen = Home
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	if !h.app.paletteOpen {
		t.Fatal("Cmd/Ctrl+K did not open the palette")
	}
	tt.Type("budget")
	results = h.app.searchResults()
	if len(results) != 1 || results[0].ID != seed.betaID {
		t.Fatalf("content search results = %+v, want only Beta", results)
	}
	tt.Key(0, ui.KeyEscape)

	// Keyboard navigation moves the highlight before Enter opens it.
	h.app.Screen = Home
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	all := h.app.searchResults()
	if len(all) < 2 {
		t.Fatalf("search returned %d sessions, want at least 2", len(all))
	}
	want := all[1].ID
	tt.Key(0, ui.KeyDown)
	if h.app.searchIndex != 1 {
		t.Fatalf("search index = %d, want 1", h.app.searchIndex)
	}
	tt.Key(0, ui.KeyEnter)
	if got := h.ctrl.CurrentSessionID(); got != want {
		t.Fatalf("opened session = %q, want %q", got, want)
	}
}

// TestSessionSearchSortAndArchive checks the Manual sort option, the archive
// filter, hiding archived sessions from the sidebar, and archive/unarchive.
func TestSessionSearchSortAndArchive(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	seed := seedSearchSessions(t, h)
	ctx := t.Context()
	tt := ui.NewTester(h.app.View, 1600, 1000)

	// Archive Beta and check the default lists hide it.
	if err := h.ctrl.SetSessionArchived(ctx, seed.betaID, true); err != nil {
		t.Fatalf("SetSessionArchived: %v", err)
	}
	if got, err := h.ctrl.SearchSessions(ctx, "budget", false, false); err != nil || len(got) != 0 {
		t.Fatalf("hidden archive search = %+v, %v; want none", got, err)
	}
	if got, err := h.ctrl.SearchSessions(ctx, "budget", true, false); err != nil || len(got) != 1 || !got[0].Archived {
		t.Fatalf("include archive search = %+v, %v; want one archived", got, err)
	}

	// The sidebar hides the archived session until Show archived is on.
	// Open a live session first so the header title is not Beta plan.
	if _, err := h.ctrl.OpenSession(ctx, seed.alphaID); err != nil {
		t.Fatalf("OpenSession alpha: %v", err)
	}
	h.app.Screen = Session
	tt.Frame()
	if tt.HasText("Beta plan") {
		t.Fatalf("sidebar shows archived session: %q", tt.Texts())
	}
	h.app.archiveFilter = archiveAll
	tt.Frame()
	if !tt.HasText("Beta plan") {
		t.Fatalf("All conversations did not reveal the session: %q", tt.Texts())
	}
	h.app.archiveFilter = archiveHide
	tt.Frame()

	// Manual sort keeps creation order; Last updated puts the newest first.
	h.app.Screen = Home
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	updated := h.app.searchResults()
	if len(updated) == 0 || updated[0].ID == seed.liveID {
		t.Fatalf("last-updated order = %+v, want a newer session first", updated)
	}
	if err := tt.Click("Sort: Last updated"); err != nil {
		t.Fatalf("click sort: %v", err)
	}
	if !h.app.manualSort {
		t.Fatal("Sort: Last updated did not switch to Manual")
	}
	manual := h.app.searchResults()
	if len(manual) == 0 || manual[0].ID != seed.liveID {
		t.Fatalf("manual order = %+v, want the first-created session first", manual)
	}

	// Show archived from inside the modal, then unarchive the result.
	h.app.manualSort = false
	h.app.archiveFilter = archiveHide
	h.app.paletteQuery = "budget"
	tt.Frame()
	// Search for Beta's content while archived: no result.
	if got := h.app.searchResults(); len(got) != 0 {
		t.Fatalf("archived search result = %+v, want none", got)
	}
	if err := tt.Click("Hide archived"); err != nil {
		t.Fatalf("click the archive filter chip: %v", err)
	}
	if got := h.app.searchResults(); len(got) != 1 || got[0].ID != seed.betaID {
		t.Fatalf("after Show archived results = %+v, want Beta", got)
	}
	if err := tt.Click("Unarchive"); err != nil {
		t.Fatalf("click Unarchive: %v", err)
	}
	if sessionArchived(t, h, seed.betaID) {
		t.Fatal("Unarchive left the session archived")
	}
	tt.Key(0, ui.KeyEscape)
	if h.app.paletteOpen {
		t.Fatal("Esc did not close the session search")
	}

	// The row context menu archives and unarchives from the sidebar.
	if err := tt.RightClick("Alpha notes"); err != nil {
		t.Fatalf("right-click Alpha: %v", err)
	}
	if err := tt.Click("Archive"); err != nil {
		t.Fatalf("click Archive: %v", err)
	}
	if !sessionArchived(t, h, seed.alphaID) {
		t.Fatal("Archive left the session unarchived")
	}
	h.app.archiveFilter = archiveAll
	tt.Frame()
	if err := tt.RightClick("Alpha notes · archived"); err != nil {
		t.Fatalf("right-click archived Alpha: %v", err)
	}
	if !tt.HasText("Unarchive") {
		t.Fatalf("archived row menu missing Unarchive: %q", tt.Texts())
	}
	if err := tt.Click("Unarchive"); err != nil {
		t.Fatalf("click Unarchive from row: %v", err)
	}
	if sessionArchived(t, h, seed.alphaID) {
		t.Fatal("row Unarchive left the session archived")
	}
}

// TestSearchStoreLikeEscaping checks that % and _ in a query match
// literally instead of acting as wildcards.
func TestSearchStoreLikeEscaping(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := t.Context()
	id, err := h.ctrl.CreateSession(ctx, "Percent % literal")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := h.store.AppendMessage(store.Message{
		SessionID: id, Role: "user", Content: "underscore _ here",
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if got, err := h.ctrl.SearchSessions(ctx, "%", false, false); err != nil || len(got) != 1 || got[0].ID != id {
		t.Fatalf("literal %% search = %+v, %v; want the %% session", got, err)
	}
	if got, err := h.ctrl.SearchSessions(ctx, "_", false, false); err != nil || len(got) != 1 || got[0].ID != id {
		t.Fatalf("literal _ search = %+v, %v; want the underscore session", got, err)
	}
	// A wildcard-shaped query does not match everything.
	h.ctrl.CreateSession(ctx, "plain title")
	if got, _ := h.ctrl.SearchSessions(ctx, "%%", false, false); len(got) != 0 {
		t.Fatalf("double %% matched %d sessions, want 0", len(got))
	}
}

// TestSearchIncludesAllMessages makes sure the search looks past the first
// message of a session.
func TestSearchIncludesAllMessages(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := t.Context()
	id, err := h.ctrl.CreateSession(ctx, "Many messages")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for _, text := range []string{"first message", "second needle deep inside"} {
		if _, err := h.store.AppendMessage(store.Message{
			SessionID: id, Role: "user", Content: text,
		}); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}
	got, err := h.ctrl.SearchSessions(ctx, "needle", false, false)
	if err != nil || len(got) != 1 || got[0].ID != id {
		t.Fatalf("deep content search = %+v, %v", got, err)
	}
	if !strings.Contains(strings.ToLower(got[0].Title), "many") {
		t.Fatalf("search returned %+v, want Many messages", got)
	}
}

// TestLiveSelectedWorkspaceKeepsSidebarFill is the live counterpart of
// TestSelectedWorkspaceKeepsSidebarFill: the selected workspace row must not
// paint the hover tint and its folder glyph must use the accent color.
func TestLiveSelectedWorkspaceKeepsSidebarFill(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if c := tt.Image().RGBAAt(150, 262); c.R != 0xF9 || c.G != 0xFA || c.B != 0xFB {
		t.Errorf("selected live workspace row fill = %v, want sidebar #F9FAFB", c)
	}
	accent := false
	for y := 248; y < 278 && !accent; y++ {
		for x := 16; x < 40; x++ {
			c := tt.Image().RGBAAt(x, y)
			if c.R < 120 && c.G < 170 && c.B > 180 {
				accent = true
				break
			}
		}
	}
	if !accent {
		t.Error("selected live workspace folder is not drawn in the accent color")
	}
}
