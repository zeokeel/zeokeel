package shell

import (
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/store"
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

// TestSidebarNativeSelection is the native Sidebar's selection regression: the
// open session's row carries the brand tint, a plain row does not, and the
// highlighted row follows the screen (the Plugins destination is chosen while
// Plugins is open).
func TestSidebarNativeSelection(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	row, ok := tt.Find("live")
	if !ok {
		t.Fatalf("open session row missing: %q", tt.Texts())
	}
	// The native Sidebar paints the chosen item in the theme's
	// SurfacePressed (brand blue at 10%) while the sidebar has no focus, and
	// leaves the rest on the sidebar surface. Sample the item's own fill left
	// of its icon, where no glyph covers it.
	img := tt.Image()
	chosen := img.RGBAAt(int(row.X)-32, int(row.Y+row.H/2))
	if int(chosen.B) < int(chosen.R)+8 {
		t.Errorf("chosen session row fill = %v, want the brand blue tint", chosen)
	}
	wsRow, ok := tt.Find("test")
	if !ok {
		t.Fatalf("workspace row missing: %q", tt.Texts())
	}
	plain := img.RGBAAt(int(wsRow.X)-32, int(wsRow.Y+wsRow.H/2))
	if int(plain.B) >= int(plain.R)+8 {
		t.Errorf("unselected workspace row fill = %v, want the plain sidebar surface", plain)
	}

	// The highlight follows the screen: with Plugins open the destination row
	// is the chosen one and the session row is not.
	if err := tt.Click("Plugins"); err != nil {
		t.Fatalf("click Plugins: %v", err)
	}
	if h.app.Screen != Plugins {
		t.Fatalf("screen = %v, want Plugins", h.app.Screen)
	}
	row, _ = tt.Find("live")
	img = tt.Image()
	if c := img.RGBAAt(int(row.X)-32, int(row.Y+row.H/2)); int(c.B) >= int(c.R)+8 {
		t.Errorf("session row still chosen on Plugins: %v", c)
	}
	plugins, _ := tt.Find("Plugins")
	if c := img.RGBAAt(int(plugins.X)-24, int(plugins.Y+plugins.H/2)); int(c.B) < int(c.R)+8 {
		t.Errorf("Plugins destination row fill = %v, want the brand blue tint", c)
	}
}

// TestSidebarNativeDisclosure covers the workspace disclosure: the chevron
// button collapses and expands the open workspace without opening it again.
func TestSidebarNativeDisclosure(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	// A session whose title only shows in the sidebar: the open session's
	// title also appears in the screen header, so it cannot tell the two
	// states apart.
	if _, err := h.ctrl.CreateSession(ctx, "hidden session"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sessions, err := h.ctrl.SessionsForWorkspace(ctx, h.ctrl.CurrentWorkspaceID(), false, false)
	if err != nil {
		t.Fatalf("SessionsForWorkspace: %v", err)
	}
	for _, sess := range sessions {
		if sess.Title == "live" {
			if _, err := h.ctrl.OpenSession(ctx, sess.ID); err != nil {
				t.Fatalf("OpenSession: %v", err)
			}
		}
	}
	h.app.SetSnapshot(h.ctrl.Snapshot())
	h.app.Language = LangEN
	h.app.Screen = Session
	wsID := h.ctrl.CurrentWorkspaceID()
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("hidden session") {
		t.Fatalf("session row missing before collapse: %q", tt.Texts())
	}

	if err := tt.Click("Collapse workspace"); err != nil {
		t.Fatalf("click collapse chevron: %v", err)
	}
	tt.Frame()
	if !h.app.wsCollapsed[wsID] {
		t.Fatal("the chevron did not collapse the workspace")
	}
	if tt.HasText("hidden session") {
		t.Fatalf("collapsed workspace still shows its session rows: %q", tt.Texts())
	}
	if err := tt.Click("Expand workspace"); err != nil {
		t.Fatalf("click expand chevron: %v", err)
	}
	tt.Frame()
	if h.app.wsCollapsed[wsID] {
		t.Fatal("the chevron did not expand the workspace again")
	}
	if !tt.HasText("hidden session") {
		t.Fatalf("expanded workspace lost its session rows: %q", tt.Texts())
	}
}

// TestSidebarNativeKeyboardNavigation covers the keyboard behavior the native
// Sidebar brings for free: focused, the arrow keys move the choice through the
// items and the app follows, as a desktop source list does.
func TestSidebarNativeKeyboardNavigation(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	for _, title := range []string{"alpha", "beta"} {
		if _, err := h.ctrl.CreateSession(ctx, title); err != nil {
			t.Fatalf("CreateSession %q: %v", title, err)
		}
	}
	sessions, err := h.ctrl.SessionsForWorkspace(ctx, h.ctrl.CurrentWorkspaceID(), false, false)
	if err != nil || len(sessions) < 2 {
		t.Fatalf("SessionsForWorkspace = %+v, %v", sessions, err)
	}
	if _, err := h.ctrl.OpenSession(ctx, sessions[0].ID); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	h.app.SetSnapshot(h.ctrl.Snapshot())
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	// Tab to the Sidebar: its chosen row's text is then inside the focused
	// element. Arrow keys from there move the choice.
	for i := 0; i < 40 && !tt.Focused(sessions[0].Title); i++ {
		tt.Key(0, ui.KeyTab)
	}
	if !tt.Focused(sessions[0].Title) {
		t.Fatalf("Tab never reached the sidebar: %q", tt.Texts())
	}
	before := h.ctrl.CurrentSessionID()
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if got := h.ctrl.CurrentSessionID(); got == before {
		t.Fatalf("Down did not move the native Sidebar choice (still %q)", got)
	}
}

// TestSidebarNativeGroupingModes keeps the three View-options groupings
// working on the native tree: By workspace, Workspace Tree and In one list all
// render the open workspace's sessions.
func TestSidebarNativeGroupingModes(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	for _, mode := range []string{groupByWorkspace, groupByTree, groupByList} {
		t.Run(mode, func(t *testing.T) {
			h.app.groupBy = mode
			tt := ui.NewTester(h.app.View, 1600, 1000)
			tt.Frame()
			if !tt.HasText("live") {
				t.Fatalf("%s: session row missing: %q", mode, tt.Texts())
			}
			if mode == groupByList {
				// The flat grouping drops the workspace headers.
				if tt.HasText("Workspace actions for test") {
					t.Fatalf("%s: workspace rows leaked into the flat list: %q", mode, tt.Texts())
				}
				return
			}
			if _, ok := tt.Find("test"); !ok {
				t.Fatalf("%s: workspace row missing: %q", mode, tt.Texts())
			}
		})
	}
}

// TestRenderSidebarNativePreview renders the sidebar headlessly for brand
// review: light and dark at the 1600x1000 design size. The PNGs are written to
// docs/design/preview, so the design is reproducible straight from the test.
func TestRenderSidebarNativePreview(t *testing.T) {
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
			ctx := context.Background()
			first := h.ctrl.CurrentWorkspaceID()
			// A realistic tree: several sessions in the open workspace and two
			// more workspaces folded below it.
			for _, title := range []string{
				"原生 UI 迁移计划",
				"Sidebar 原生化",
				"Settings 独立窗口",
				"发布前回归",
			} {
				if _, err := h.ctrl.CreateSession(ctx, title); err != nil {
					t.Fatalf("CreateSession %q: %v", title, err)
				}
			}
			if _, err := h.ctrl.CreateWorkspace(ctx, "ZeoKeel", t.TempDir()); err != nil {
				t.Fatalf("CreateWorkspace: %v", err)
			}
			if _, err := h.ctrl.CreateWorkspace(ctx, "mygo", t.TempDir()); err != nil {
				t.Fatalf("CreateWorkspace: %v", err)
			}
			// Back to the first workspace with its newest session open, so the
			// tree shows the expanded workspace and the chosen row.
			if err := h.ctrl.SelectWorkspace(ctx, first); err != nil {
				t.Fatalf("SelectWorkspace: %v", err)
			}
			if sessions, err := h.ctrl.SessionsForWorkspace(ctx, first, false, false); err == nil && len(sessions) > 0 {
				if _, err := h.ctrl.OpenSession(ctx, sessions[len(sessions)-1].ID); err != nil {
					t.Fatalf("OpenSession: %v", err)
				}
			}
			h.app.SetSnapshot(h.ctrl.Snapshot())
			// The preview shows the product's default language and appearance.
			h.app.Language = LangZH
			h.app.Screen = Session
			if err := h.store.SetSetting(session.SettingTheme, tc.theme); err != nil {
				t.Fatalf("theme setting: %v", err)
			}
			img := ui.Render(h.app.View, 1600, 1000, 1)
			path := filepath.Join(dir, "sidebar-native-"+tc.name+".png")
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
