package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestSidebarCollapseRailEveryScreen checks DSH's Collapse sidebar control:
// the live sidebar becomes a 64px icon rail with an always-available reopen
// control, Cmd/Ctrl+B toggles it, and every screen can get the full
// sidebar back. Settings keeps the shortcut because its modal covers the
// rail.
func TestSidebarCollapseRailEveryScreen(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	app := h.app
	app.Screen = Home
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()

	if !tt.HasText("Workspaces") {
		t.Fatalf("expanded sidebar missing its Workspaces heading: %q", tt.Texts())
	}

	// The brand-row control collapses the sidebar to the icon rail.
	if err := tt.Click("Collapse sidebar"); err != nil {
		t.Fatalf("click Collapse sidebar: %v", err)
	}
	if !app.sidebarCollapsed {
		t.Fatal("Collapse sidebar did not set sidebarCollapsed")
	}
	if tt.HasText("Workspaces") {
		t.Fatalf("collapsed rail still shows the expanded heading: %q", tt.Texts())
	}
	if _, ok := tt.Find("Expand sidebar"); !ok {
		t.Fatalf("collapsed rail has no Expand sidebar control: %q", tt.Texts())
	}

	// The rail's workspace icon opens that workspace's session flyout
	// (UX-08): the tree is hidden in the rail, so the flyout is the way to
	// reach a session or start one there.
	if _, ok := tt.Find("test"); !ok {
		t.Fatalf("collapsed rail has no workspace icon: %q", tt.Texts())
	}
	if err := tt.Click("test"); err != nil {
		t.Fatalf("click rail workspace: %v", err)
	}
	for _, want := range []string{"New session here", "Open workspace"} {
		if !tt.HasText(want) {
			t.Fatalf("rail workspace flyout missing %q: %q", want, tt.Texts())
		}
	}
	tt.Key(0, ui.KeyEscape)
	if _, ok := tt.Find("New session here"); ok {
		t.Fatalf("Escape did not close the rail workspace flyout: %q", tt.Texts())
	}
	if err := tt.Click("More"); err != nil {
		t.Fatalf("click rail More: %v", err)
	}
	if !app.moreOpen {
		t.Fatal("rail More did not open the account menu")
	}
	if !tt.HasText("Settings") {
		t.Fatalf("rail More menu missing Settings: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	if app.moreOpen {
		t.Fatal("Escape did not close the rail More menu")
	}

	// The rail keeps the primary navigation clickable.
	if err := tt.Click("Plugins"); err != nil {
		t.Fatalf("click rail Plugins: %v", err)
	}
	if app.Screen != Plugins {
		t.Fatalf("rail Plugins screen = %v, want Plugins", app.Screen)
	}
	if err := tt.Click("Expand sidebar"); err != nil {
		t.Fatalf("click Expand sidebar: %v", err)
	}
	if app.sidebarCollapsed {
		t.Fatal("Expand sidebar left sidebarCollapsed set")
	}
	if !tt.HasText("Workspaces") {
		t.Fatalf("expanded sidebar did not come back: %q", tt.Texts())
	}

	// Cmd/Ctrl+B toggles both ways.
	tt.Key(ui.Cmd, ui.KeyB)
	if !app.sidebarCollapsed {
		t.Fatal("Cmd/Ctrl+B did not collapse the sidebar")
	}
	tt.Key(ui.Cmd, ui.KeyB)
	if app.sidebarCollapsed {
		t.Fatal("Cmd/Ctrl+B did not expand the sidebar")
	}

	// Every screen keeps the rail and its reopen control.
	for _, screen := range []Screen{Home, Session, Plugins, Automation} {
		app.Screen = screen
		tt.Frame()
		tt.Key(ui.Cmd, ui.KeyB)
		if !app.sidebarCollapsed {
			t.Fatalf("screen %v: Cmd/Ctrl+B did not collapse", screen)
		}
		if _, ok := tt.Find("Expand sidebar"); !ok {
			t.Fatalf("screen %v: collapsed rail lost Expand sidebar", screen)
		}
		tt.Key(ui.Cmd, ui.KeyB)
		if app.sidebarCollapsed {
			t.Fatalf("screen %v: Cmd/Ctrl+B did not expand", screen)
		}
	}

	// Settings paints a modal over the sidebar, so its way back is the
	// keyboard shortcut rather than a rail click.
	app.Screen = Settings
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyB)
	if !app.sidebarCollapsed {
		t.Fatal("Settings: Cmd/Ctrl+B did not collapse")
	}
	tt.Key(ui.Cmd, ui.KeyB)
	if app.sidebarCollapsed {
		t.Fatal("Settings: Cmd/Ctrl+B did not expand")
	}
}

// TestRightSidebarHeaderControls drives DSH's New tab, Split, Fullscreen
// and Collapse right sidebar controls and checks each has real behavior.
func TestRightSidebarHeaderControls(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	writeTestFile(t, h.dir, "note.txt", "hello")
	writeTestFile(t, h.dir, "sub/inner.txt", "inner")
	tt := ui.NewTester(h.app.View, 1600, 1000)

	if err := tt.Click("Open right sidebar"); err != nil {
		t.Fatalf("open right sidebar: %v", err)
	}
	if !h.app.rightOpen {
		t.Fatal("right sidebar did not open")
	}
	normal, ok := tt.Find("Right sidebar")
	if !ok {
		t.Fatal("right sidebar panel has no label")
	}
	if normal.W < rightSidebarW-1 || normal.W > rightSidebarW+1 {
		t.Fatalf("normal right sidebar width = %v, want %v (DSH 720)", normal.W, rightSidebarW)
	}

	// New tab: navigate into a directory, then reset the Files tab.
	if err := tt.Click("Terminal"); err != nil {
		t.Fatalf("click Terminal: %v", err)
	}
	if err := tt.Click("New tab"); err != nil {
		t.Fatalf("click New tab: %v", err)
	}
	if err := tt.Click("Workspace files"); err != nil {
		t.Fatalf("click Workspace files in the New tab menu: %v", err)
	}
	if h.app.rightTab != filesTab {
		t.Fatalf("New tab left tab = %v, want Files", h.app.rightTab)
	}
	if err := tt.Click("sub/"); err != nil {
		t.Fatalf("click sub/: %v", err)
	}
	if h.app.filesDir != "sub" {
		t.Fatalf("filesDir = %q, want sub", h.app.filesDir)
	}
	if err := tt.Click("New tab"); err != nil {
		t.Fatalf("click New tab from sub: %v", err)
	}
	if err := tt.Click("Workspace files"); err != nil {
		t.Fatalf("click Workspace files: %v", err)
	}
	if h.app.filesDir != "" || h.app.filePath != "" {
		t.Fatalf("New tab left files state dir=%q path=%q", h.app.filesDir, h.app.filePath)
	}
	if !tt.HasText("Opened a new Files tab") {
		t.Fatalf("New tab toast missing: %q", tt.Texts())
	}

	// Split: the panel takes half the window; toggling restores it.
	if err := tt.Click("Split"); err != nil {
		t.Fatalf("click Split: %v", err)
	}
	if !h.app.rightSplit {
		t.Fatal("Split did not set rightSplit")
	}
	split, ok := tt.Find("Right sidebar")
	if !ok {
		t.Fatal("split right sidebar lost its label")
	}
	// Split gives each pane half of the content column (660 at 1600x1000),
	// which can be narrower than the 720px docked panel.
	if split.W != normal.W && (split.W < 600 || split.W > 700) {
		t.Fatalf("split width = %v, want about half the content column", split.W)
	}
	if err := tt.Click("Split"); err != nil {
		t.Fatalf("click Split again: %v", err)
	}
	if h.app.rightSplit {
		t.Fatal("second Split did not clear rightSplit")
	}

	// Fullscreen: the panel fills the content column and the screen behind
	// it is not built; the control becomes Exit fullscreen.
	if err := tt.Click("Fullscreen"); err != nil {
		t.Fatalf("click Fullscreen: %v", err)
	}
	if !h.app.rightFullscreen {
		t.Fatal("Fullscreen did not set rightFullscreen")
	}
	full, ok := tt.Find("Right sidebar")
	if !ok {
		t.Fatal("fullscreen right sidebar lost its label")
	}
	if full.W < 1000 {
		t.Fatalf("fullscreen width = %v, want the content column", full.W)
	}
	if _, ok := tt.Find("Exit fullscreen"); !ok {
		t.Fatalf("fullscreen control did not become Exit fullscreen: %q", tt.Texts())
	}
	if tt.HasText("Chat") {
		t.Fatalf("fullscreen still built the session header behind it: %q", tt.Texts())
	}
	if err := tt.Click("Exit fullscreen"); err != nil {
		t.Fatalf("click Exit fullscreen: %v", err)
	}
	if h.app.rightFullscreen {
		t.Fatal("Exit fullscreen left rightFullscreen set")
	}
	if got, ok := tt.Find("Right sidebar"); !ok || got.W > normal.W+1 {
		t.Fatalf("after Exit fullscreen width = %v (ok=%v), want %v", got.W, ok, normal.W)
	}

	// Collapse: a.rightOpen goes false and the panel disappears.
	if err := tt.Click("Collapse right sidebar"); err != nil {
		t.Fatalf("click Collapse right sidebar: %v", err)
	}
	if h.app.rightOpen {
		t.Fatal("Collapse right sidebar left rightOpen set")
	}
	if _, ok := tt.Find("Right sidebar"); ok {
		t.Fatal("collapsed right sidebar is still rendered")
	}
}
