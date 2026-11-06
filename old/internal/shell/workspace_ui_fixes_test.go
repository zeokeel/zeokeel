package shell

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestHomeSidebarEntry pins PD-11: a session always has a visible way back to
// the Home composer, through the sidebar Home destination the brand row and the
// collapsed rail mirror.
func TestHomeSidebarEntry(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("Home"); err != nil {
		t.Fatalf("click Home destination: %v", err)
	}
	tt.Frame()
	if h.app.Screen != Home {
		t.Fatalf("Home destination opened %v, want Home", h.app.Screen)
	}
	if !tt.HasText("What will you build today?") && !tt.HasText("今天想构建什么？") {
		t.Fatalf("Home screen not rendered after navigation: %q", tt.Texts())
	}
	// The session stays open behind the Home screen, so choosing it returns to
	// the same conversation.
	if id := h.ctrl.CurrentSessionID(); id == "" {
		t.Fatal("going Home closed the current session")
	}
}

// TestSidebarWorkspacesEmptyState pins UX-09: with no workspaces the tree area
// explains the state and offers the add action instead of staying blank.
func TestSidebarWorkspacesEmptyState(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	workspaces, err := h.ctrl.ListWorkspaces(context.Background())
	if err != nil || len(workspaces) == 0 {
		t.Fatalf("fixture workspaces = %+v, %v", workspaces, err)
	}
	if err := h.ctrl.DeleteWorkspace(context.Background(), workspaces[0].ID); err != nil {
		t.Fatalf("delete fixture workspace: %v", err)
	}
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("No workspaces yet") {
		t.Fatalf("empty workspace state missing: %q", tt.Texts())
	}
	if err := tt.Click("Add workspace"); err != nil {
		t.Fatalf("click Add workspace: %v", err)
	}
	tt.Frame()
	if !h.app.wsDialogOpen {
		t.Fatal("Add workspace action did not open the dialog")
	}
}

// TestWorkspaceDialogVisibleLabels pins UX-05: the Add workspace dialog shows
// a visible label for each of its two inputs.
func TestWorkspaceDialogVisibleLabels(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Home
	h.app.beginAddWorkspace()
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	for _, want := range []string{"Workspace name", "Root path"} {
		if !tt.HasText(want) {
			t.Fatalf("dialog label %q missing: %q", want, tt.Texts())
		}
		if _, ok := tt.Find(want); !ok {
			t.Fatalf("dialog label %q is not a visible element", want)
		}
	}
}

// TestWorkspaceRootValidation pins PD-17: a typed root is checked before it is
// persisted, a missing one is only created on request, and a file path is
// rejected.
func TestWorkspaceRootValidation(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Home
	root := filepath.Join(t.TempDir(), "new-project")

	// Missing root without the create choice: refused with an actionable line.
	h.app.beginAddWorkspace()
	h.app.wsName = "project"
	h.app.wsRoot = root
	h.app.wsCreateRoot = false
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("This folder does not exist yet.") {
		t.Fatalf("missing-root hint missing: %q", tt.Texts())
	}
	if !tt.HasText("Create this folder") {
		t.Fatalf("create-folder choice missing: %q", tt.Texts())
	}
	if err := tt.Click("Create"); err != nil {
		t.Fatalf("click Create (missing root): %v", err)
	}
	tt.Frame()
	if h.app.wsErr == "" || !tt.HasText("That folder does not exist") {
		t.Fatalf("missing root was not refused: err=%q texts=%q", h.app.wsErr, tt.Texts())
	}
	if _, err := os.Stat(root); err == nil {
		t.Fatal("refused create still made the directory")
	}
	if before, _ := h.ctrl.ListWorkspaces(context.Background()); len(before) != 1 {
		t.Fatalf("refused create changed the workspaces: %+v", before)
	}

	// With the create choice the directory is made and the workspace saved.
	h.app.wsCreateRoot = true
	if err := tt.Click("Create"); err != nil {
		t.Fatalf("click Create (create root): %v", err)
	}
	tt.Frame()
	if !h.app.wsDialogOpen {
		// A successful create closes the dialog; a still-open one reports why.
		if h.app.wsErr != "" {
			t.Fatalf("create with create-root failed: %s", h.app.wsErr)
		}
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Fatalf("create choice did not create the directory: %v", err)
	}
	if got, _ := h.ctrl.ListWorkspaces(context.Background()); len(got) != 2 {
		t.Fatalf("workspaces after create = %d, want 2", len(got))
	}

	// A relative root is rejected: it would resolve against the app's own
	// working directory.
	h.app.beginAddWorkspace()
	h.app.wsName = "relative"
	h.app.wsRoot = "smoke-ws"
	tt.Frame()
	if err := tt.Click("Create"); err != nil {
		t.Fatalf("click Create (relative root): %v", err)
	}
	tt.Frame()
	if h.app.wsErr == "" || !tt.HasText("Use an absolute path") {
		t.Fatalf("relative root was not refused: err=%q texts=%q", h.app.wsErr, tt.Texts())
	}

	// Opening the dialog again resets the create-folder choice so a typo can
	// never inherit an earlier "create" decision.
	h.app.wsCreateRoot = true
	h.app.beginAddWorkspace()
	if h.app.wsCreateRoot {
		t.Fatal("beginAddWorkspace kept a stale create-root choice")
	}

	// A regular file as the root is rejected.
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	h.app.beginAddWorkspace()
	h.app.wsName = "bad"
	h.app.wsRoot = file
	h.app.wsCreateRoot = false
	tt.Frame()
	if err := tt.Click("Create"); err != nil {
		t.Fatalf("click Create (file root): %v", err)
	}
	tt.Frame()
	if h.app.wsErr == "" || !tt.HasText("The root path is not a folder.") {
		t.Fatalf("file root was not refused: err=%q texts=%q", h.app.wsErr, tt.Texts())
	}
}

// TestOpeningSettingsClosesComposerPopovers pins UX-04: a popover the composer
// left open must not outlive the surface that owns it when Settings opens.
func TestOpeningSettingsClosesComposerPopovers(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	h.app.liveMenus().permissionOpen = true
	h.app.liveMenus().modelOpen = true
	h.app.attachOpen = true
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	st := h.app.liveMenus()
	if st.permissionOpen || st.modelOpen || h.app.attachOpen {
		t.Fatalf("settings left composer popovers open: permission=%v model=%v attach=%v",
			st.permissionOpen, st.modelOpen, h.app.attachOpen)
	}
	if h.app.Screen != Settings {
		t.Fatalf("settings did not open: %v", h.app.Screen)
	}

	// Going Home closes them too.
	h.app.closeSettings()
	h.app.Screen = Session
	tt.Frame()
	h.app.liveMenus().permissionOpen = true
	tt.Frame()
	if err := tt.Click("Home"); err != nil {
		t.Fatalf("click Home: %v", err)
	}
	tt.Frame()
	if h.app.liveMenus().permissionOpen {
		t.Fatal("Home navigation left the composer menu open")
	}
}

// TestRailWorkspaceFlyoutReachesSessions pins UX-08: with the sidebar
// collapsed, a workspace icon opens a flyout that lists its sessions and the
// workspace actions, so a session stays reachable from the icon rail.
func TestRailWorkspaceFlyoutReachesSessions(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session
	h.app.sidebarCollapsed = true
	workspaceID := h.ctrl.CurrentWorkspaceID()
	open := h.app.railWorkspacePopover(workspaceID)
	*open = true
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("live") {
		t.Fatalf("rail flyout lists no sessions: %q", tt.Texts())
	}
	if !tt.HasText("New session here") || !tt.HasText("Open workspace") {
		t.Fatalf("rail flyout lists no workspace actions: %q", tt.Texts())
	}
	if err := tt.Click("live"); err != nil {
		t.Fatalf("click session in rail flyout: %v", err)
	}
	tt.Frame()
	if h.app.Screen != Session {
		t.Fatalf("rail flyout session opened %v, want Session", h.app.Screen)
	}
	if *open {
		t.Fatal("rail flyout stayed open after choosing a session")
	}
}

// TestPluginsPageMatchesBuiltinDefs pins PD-08: the Plugins page and Settings >
// Built-in plugins list the same six plugins from builtinPluginDefs, and the
// experimental rows say they are not implemented yet.
func TestPluginsPageMatchesBuiltinDefs(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Plugins
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	for _, def := range builtinPluginDefs {
		label := h.app.pluginRowName(def.titleEN)
		if !tt.HasText(label) {
			t.Fatalf("Plugins page missing built-in %q: %q", label, tt.Texts())
		}
	}
	if !tt.HasText("Not implemented") {
		t.Fatalf("experimental rows are not marked: %q", tt.Texts())
	}
	// The row toggle for a page row is bound to the same settings key the
	// settings page writes: toggling the Web search switch persists
	// plugin.web_search. The name itself opens the detail editor, so the
	// click targets the switch by its label.
	if err := tt.Click("Enable Web search"); err != nil {
		t.Fatalf("click Web search row switch: %v", err)
	}
	tt.Frame()
	if got, err := h.ctrl.PluginEnabled("web_search"); err != nil || got {
		t.Fatalf("PluginEnabled(web_search) = %v, %v; want false", got, err)
	}
}
