package shell

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

func TestHomeLiveAttachmentMenu(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	clickAddControl(t, tt)
	if !tt.HasText("Files") {
		t.Fatalf("attachment menu missing Files: %v", tt.Texts())
	}
	if err := tt.Click("Files"); err != nil {
		t.Fatalf("click Files: %v", err)
	}
	if h.app.attachOpen {
		t.Fatal("attachment menu stayed open after choosing an entry")
	}
	if tt.HasText("Add to message") {
		t.Fatalf("attachment menu is still rendered: %v", tt.Texts())
	}
}

func TestHomeLiveWorkspacePickerSwitches(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	second, err := h.ctrl.CreateWorkspace(ctx, "second-ws", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if err := tt.Click("Workspace, current: second-ws"); err != nil {
		t.Fatalf("click workspace chip: %v", err)
	}
	if !tt.HasText("second-ws") {
		t.Fatalf("workspace picker did not list second-ws: %v", tt.Texts())
	}
	if err := tt.Click("Switch to second-ws"); err != nil {
		t.Fatalf("click second workspace: %v", err)
	}
	if got := h.ctrl.CurrentWorkspaceID(); got != second.ID {
		t.Fatalf("current workspace = %q, want %q", got, second.ID)
	}
	if h.app.wsPickerOpen {
		t.Fatal("workspace picker stayed open after a switch")
	}
}

func TestHomeLiveWorkspaceChipWithoutWorkspaceAsksForOne(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	if err := h.ctrl.DeleteWorkspace(context.Background(), h.ctrl.CurrentWorkspaceID()); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if err := tt.Click("Choose workspace"); err != nil {
		t.Fatalf("click workspace chip: %v", err)
	}
	if !h.app.wsDialogOpen {
		t.Fatalf("chip without a workspace should open the add dialog: %v", tt.Texts())
	}
}

func TestHomeLivePermissionMenuPersists(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if err := tt.Click("Access mode, current: " + h.app.composerPermissionLabel(h.app.currentSettings().Permission)); err != nil {
		t.Fatalf("click permission chip: %v", err)
	}
	for _, want := range []string{"Read Only", "Workspace Write", "Full access"} {
		if !tt.HasText(want) {
			t.Fatalf("permission menu missing %q: %v", want, tt.Texts())
		}
	}
	if err := tt.Click("Read Only"); err != nil {
		t.Fatalf("click Read Only: %v", err)
	}
	cfg, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	// Home stages the choice for the next created session; it must not change
	// the General default or the last-opened session.
	if cfg.Permission != session.PermissionWorkspace {
		t.Fatalf("global permission = %q, want unchanged %q", cfg.Permission, session.PermissionWorkspace)
	}
	if h.app.homePermission != session.PermissionAsk {
		t.Fatalf("staged Home permission = %q, want ask", h.app.homePermission)
	}
	if h.app.Screen != Home {
		t.Fatalf("permission choice left Home for %v", h.app.Screen)
	}
	if h.app.liveMenus().permissionOpen {
		t.Fatal("permission menu stayed open after choosing a mode")
	}
	if got := h.ctrl.SessionPermissionOrDefault(h.ctrl.CurrentSessionID()); got == session.PermissionAsk {
		t.Fatalf("last session permission was changed to %q", got)
	}
	tt.Frame()
	if !tt.HasText("Access mode, current: " + h.app.composerPermissionLabel(session.PermissionAsk)) {
		t.Fatalf("Home chip did not show the staged mode: %v", tt.Texts())
	}
}

func TestSessionLivePermissionMenuPersists(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if err := tt.Click("Access mode, current: " + h.app.composerPermissionLabel(h.app.currentSettings().Permission)); err != nil {
		t.Fatalf("click session permission chip: %v", err)
	}
	if !tt.HasText("Read Only") {
		t.Fatalf("session permission menu missing Read Only: %v", tt.Texts())
	}
	if err := tt.Click("Full access"); err != nil {
		t.Fatalf("click Full access: %v", err)
	}
	cfg, err := h.ctrl.Settings().Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	// A Session choice writes only that session's override. The General
	// default (future sessions) stays Workspace Write.
	if cfg.Permission != session.PermissionWorkspace {
		t.Fatalf("global permission = %q, want unchanged %q", cfg.Permission, session.PermissionWorkspace)
	}
	if got := h.ctrl.SessionPermissionOrDefault(h.ctrl.CurrentSessionID()); got != session.PermissionFull {
		t.Fatalf("session permission = %q, want full", got)
	}
	if h.app.Screen != Session {
		t.Fatalf("session permission choice left the session for %v", h.app.Screen)
	}
}

func TestHomeLiveModelMenuSetsDefault(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	first, err := h.ctrl.Models().Create(store.Model{
		Name: "Model One", Model: "one", ReasoningEffort: "High",
	})
	if err != nil {
		t.Fatalf("Create first model: %v", err)
	}
	second, err := h.ctrl.Models().Create(store.Model{
		Name: "Model Two", Model: "two", ReasoningEffort: "Low",
	})
	if err != nil {
		t.Fatalf("Create second model: %v", err)
	}
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if !tt.HasText(first.Name) || !tt.HasText("High") {
		t.Fatalf("home model chip does not show the default and its effort: %v", tt.Texts())
	}
	if err := tt.Click(modelChipLabel(h, first)); err != nil {
		t.Fatalf("click model chip: %v", err)
	}
	if !tt.HasText(second.Name) || !tt.HasText("Low") {
		t.Fatalf("model menu missing the second model or its effort: %v", tt.Texts())
	}
	if err := tt.Click(second.Name); err != nil {
		t.Fatalf("click second model: %v", err)
	}
	// Home stages the model for the next created session; the Settings
	// default (and the last session) do not change.
	if got, err := h.ctrl.Models().DefaultID(); err != nil || got != first.ID {
		t.Fatalf("default model = %q, %v; want unchanged %q", got, err, first.ID)
	}
	if h.app.homeModel != second.ID {
		t.Fatalf("staged Home model = %q, want %q", h.app.homeModel, second.ID)
	}
	tt.Frame()
	if !tt.HasText(second.Name) {
		t.Fatalf("home model chip did not update: %v", tt.Texts())
	}
}

// modelChipLabel is the composer badge's accessible label for model, which
// includes the reasoning effort only when the configured model has one.
func modelChipLabel(h *liveApp, model *store.Model) string {
	label := "Select model, current " + model.Name
	if model.ReasoningEffort != "" {
		label += ", reasoning effort " + model.ReasoningEffort
	}
	return label
}

func TestSessionLiveModelMenuSetsDefault(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	first, err := h.ctrl.Models().Create(store.Model{Name: "Model One", Model: "one"})
	if err != nil {
		t.Fatalf("Create first model: %v", err)
	}
	second, err := h.ctrl.Models().Create(store.Model{Name: "Model Two", Model: "two"})
	if err != nil {
		t.Fatalf("Create second model: %v", err)
	}
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if !tt.HasText(first.Name) {
		t.Fatalf("session model badge does not show the default: %v", tt.Texts())
	}
	if err := tt.Click(modelChipLabel(h, first)); err != nil {
		t.Fatalf("click session model badge: %v", err)
	}
	if !tt.HasText(second.Name) {
		t.Fatalf("session model menu missing second model: %v", tt.Texts())
	}
	if err := tt.Click(second.Name); err != nil {
		t.Fatalf("click second model: %v", err)
	}
	// The session picker writes only session.<id>.model; Settings > Models
	// keeps the global default.
	if got, err := h.ctrl.Models().DefaultID(); err != nil || got != first.ID {
		t.Fatalf("default model = %q, %v; want unchanged %q", got, err, first.ID)
	}
	if got := h.ctrl.SessionModelID(h.ctrl.CurrentSessionID()); got != second.ID {
		t.Fatalf("session model = %q, want %q", got, second.ID)
	}
	tt.Frame()
	if !tt.HasText(second.Name) {
		t.Fatalf("session model badge did not update: %v", tt.Texts())
	}
}

func TestSessionLiveComposerEnterSends(t *testing.T) {
	app := m6LiveApp(t, &m6Provider{steps: []m6Step{{text: "answer"}}})
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()

	tt.Type("hello via enter")
	tt.Key(0, ui.KeyEnter)

	m6WaitIdle(t, app.Ctrl)
	app.Ctrl.Wait()
	app.SetSnapshot(app.Ctrl.Snapshot())
	if app.draft != "" {
		t.Fatalf("draft after Enter = %q, want empty", app.draft)
	}
	found := false
	for _, m := range app.Snapshot().Messages {
		if m.Role == state.RoleUser && m.Text == "hello via enter" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Enter did not submit the draft; messages: %+v", app.Snapshot().Messages)
	}
}

func TestSessionHeaderMoreMenuParity(t *testing.T) {
	app := m6LiveApp(t, &m6Provider{})
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()

	// DSH's session-header More actions popover is Download session log and
	// Feedback; session CRUD lives on the sidebar row's context menu.
	if err := tt.Click("More actions"); err != nil {
		t.Fatalf("click More actions: %v", err)
	}
	for _, want := range []string{"Download session log", "Feedback"} {
		if !tt.HasText(want) {
			t.Fatalf("More actions missing %q: %v", want, tt.Texts())
		}
	}
	for _, gone := range []string{"Rename", "Archive", "Delete", "Copy title"} {
		if tt.HasText(gone) {
			t.Fatalf("More actions still shows %q: %v", gone, tt.Texts())
		}
	}
	if err := tt.Click("Download session log"); err != nil {
		t.Fatalf("click Download session log: %v", err)
	}
	if !tt.HasText("Session log saved to") {
		t.Fatalf("download did not report a saved path: %v", tt.Texts())
	}
	if err := tt.Click("More actions"); err != nil {
		t.Fatalf("reopen More actions: %v", err)
	}
	if err := tt.Click("Feedback"); err != nil {
		t.Fatalf("click Feedback: %v", err)
	}
	if !tt.HasText("Feedback recorded") {
		t.Fatalf("feedback did not persist: %v", tt.Texts())
	}

	sess, err := app.Ctrl.CurrentSession()
	if err != nil {
		t.Fatalf("CurrentSession: %v", err)
	}
	// CRUD is still available from the selected sidebar row's menu.
	action := "Session actions for " + sess.Title
	if err := tt.Click(action); err != nil {
		t.Fatalf("open sidebar session menu: %v", err)
	}
	if err := tt.Click("Rename"); err != nil {
		t.Fatalf("click Rename: %v", err)
	}
	if !app.renameOpen {
		t.Fatalf("Rename did not open the dialog: %v", tt.Texts())
	}
	app.renameDraft = "renamed title"
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("click Save: %v", err)
	}
	sess, err = app.Ctrl.CurrentSession()
	if err != nil {
		t.Fatalf("CurrentSession: %v", err)
	}
	if sess.Title != "renamed title" {
		t.Fatalf("title = %q, want %q", sess.Title, "renamed title")
	}

	tt.Frame()
	action = "Session actions for renamed title"
	if err := tt.Click(action); err != nil {
		t.Fatalf("open sidebar menu for copy: %v", err)
	}
	if err := tt.Click("Copy title"); err != nil {
		t.Fatalf("click Copy title: %v", err)
	}
	if got := tt.Clipboard(); got != "renamed title" {
		t.Fatalf("clipboard = %q, want the session title", got)
	}

	if err := tt.Click(action); err != nil {
		t.Fatalf("open sidebar menu for archive: %v", err)
	}
	if err := tt.Click("Archive"); err != nil {
		t.Fatalf("click Archive: %v", err)
	}
	sessions, err := app.Ctrl.SessionsForWorkspace(context.Background(),
		app.Ctrl.CurrentWorkspaceID(), true, false)
	if err != nil {
		t.Fatalf("SessionsForWorkspace after archive: %v", err)
	}
	archived := false
	for _, row := range sessions {
		if row.ID == sess.ID {
			archived = row.Archived
		}
	}
	if !archived {
		t.Fatal("Archive did not archive the session")
	}

	app.archiveFilter = archiveAll
	tt.Frame()
	if err := tt.Click(action); err != nil {
		t.Fatalf("open sidebar menu after archive: %v", err)
	}
	if !tt.HasText("Unarchive") {
		t.Fatalf("archived session does not offer Unarchive: %v", tt.Texts())
	}
	if err := tt.Click("Unarchive"); err != nil {
		t.Fatalf("click Unarchive: %v", err)
	}
	sessions, err = app.Ctrl.SessionsForWorkspace(context.Background(),
		app.Ctrl.CurrentWorkspaceID(), true, false)
	if err != nil {
		t.Fatalf("SessionsForWorkspace after unarchive: %v", err)
	}
	for _, row := range sessions {
		if row.ID == sess.ID && row.Archived {
			t.Fatal("Unarchive left the session archived")
		}
	}
}
