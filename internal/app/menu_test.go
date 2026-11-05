package app

import (
	"reflect"
	"runtime"
	"testing"

	"github.com/egoist/mygo"

	"github.com/zeokeel/zeokeel/internal/shell"
)

// newMenuTestApp builds the app/menu pair the way run() wires it: the shell
// owns the UI behaviour, the app registers the process-level handler, and no
// window, controller or desktop backend is needed.
func newMenuTestApp() (*app, *shell.App) {
	shellApp := &shell.App{}
	a := &app{shellApp: shellApp}
	shellApp.SetMenuAction(a.handleMenuAction)
	return a, shellApp
}

// menuItemByAction finds the command item with the given action ID.
func menuItemByAction(items []*mygo.MenuItem, action string) *mygo.MenuItem {
	for _, item := range items {
		if item.ID == action {
			return item
		}
	}
	return nil
}

// TestMenuBarStructure pins the platform menu layout: App/File/Edit/View/Window
// top-level roles plus the command items in their conventional menus.
func TestMenuBarStructure(t *testing.T) {
	a, _ := newMenuTestApp()
	items := a.buildMenu().Items()
	wantRoles := []mygo.MenuRole{
		mygo.RoleAppMenu, mygo.RoleFileMenu, mygo.RoleEditMenu,
		mygo.RoleViewMenu, mygo.RoleWindowMenu,
	}
	if len(items) != len(wantRoles) {
		t.Fatalf("top-level menu items = %d, want %d", len(items), len(wantRoles))
	}
	for i, role := range wantRoles {
		if items[i].Role != role {
			t.Fatalf("menu %d role = %q, want %q", i, items[i].Role, role)
		}
		if items[i].Label == "" {
			t.Errorf("menu %d has no label", i)
		}
	}

	// File: New Session and Search everywhere; Settings and Quit as well on the
	// platforms whose menu bar belongs to the window (macOS keeps those in the
	// application menu).
	file := items[1].Submenu
	if item := menuItemByAction(file, shell.MenuActionNewSession); item == nil {
		t.Fatalf("File menu has no %s item", shell.MenuActionNewSession)
	} else if item.Accelerator != "CmdOrCtrl+N" {
		t.Errorf("New Session accelerator = %q, want CmdOrCtrl+N", item.Accelerator)
	}
	if item := menuItemByAction(file, shell.MenuActionSearch); item == nil {
		t.Fatalf("File menu has no %s item", shell.MenuActionSearch)
	} else if item.Accelerator != "CmdOrCtrl+K" {
		t.Errorf("Search accelerator = %q, want CmdOrCtrl+K", item.Accelerator)
	}
	if runtime.GOOS != "darwin" {
		for _, action := range []string{shell.MenuActionSettings, shell.MenuActionQuit} {
			if menuItemByAction(file, action) == nil {
				t.Errorf("File menu has no %s item on %s", action, runtime.GOOS)
			}
		}
	}

	// View: the screens and the sidebar toggle.
	view := items[3].Submenu
	for _, action := range []string{
		shell.MenuActionToggleSidebar, shell.MenuActionPlugins, shell.MenuActionAutomation,
	} {
		if menuItemByAction(view, action) == nil {
			t.Errorf("View menu has no %s item", action)
		}
	}
	if toggle := menuItemByAction(view, shell.MenuActionToggleSidebar); toggle != nil &&
		toggle.Accelerator != "CmdOrCtrl+B" {
		t.Errorf("Toggle Sidebar accelerator = %q, want CmdOrCtrl+B", toggle.Accelerator)
	}

	// The application menu (macOS-only role) carries Settings and Quit with the
	// same dispatch path; mygo hides the whole menu on the other platforms.
	appItems := items[0].Submenu
	for _, action := range []string{shell.MenuActionSettings, shell.MenuActionQuit} {
		if menuItemByAction(appItems, action) == nil {
			t.Errorf("application menu has no %s item", action)
		}
	}
}

// TestMenuCommandAccelerators pins the accelerators to the chords the shell's
// window shortcuts install (internal/shell/shortcuts.go): the menu and the
// keyboard must agree.
func TestMenuCommandAccelerators(t *testing.T) {
	want := map[string]string{
		shell.MenuActionNewSession:    "CmdOrCtrl+N",
		shell.MenuActionSettings:      "CmdOrCtrl+,",
		shell.MenuActionSearch:        "CmdOrCtrl+K",
		shell.MenuActionToggleSidebar: "CmdOrCtrl+B",
		shell.MenuActionQuit:          "CmdOrCtrl+Q",
	}
	seen := make(map[string]bool, len(want))
	for _, cmd := range shell.MenuCommands() {
		accelerator, hasShortcut := want[cmd.Action]
		if !hasShortcut {
			if cmd.Accelerator != "" {
				t.Errorf("%s unexpectedly has accelerator %q", cmd.Action, cmd.Accelerator)
			}
			continue
		}
		seen[cmd.Action] = true
		if cmd.Accelerator != accelerator {
			t.Errorf("%s accelerator = %q, want %q", cmd.Action, cmd.Accelerator, accelerator)
		}
	}
	for action, accelerator := range want {
		if !seen[action] {
			t.Errorf("menu command %q with accelerator %q missing from the table", action, accelerator)
		}
	}
}

// TestMenuDispatchRunsShellCommand proves a menu click reaches the shell: the
// Settings command opens the live overlay through the shell's dispatch.
func TestMenuDispatchRunsShellCommand(t *testing.T) {
	a, shellApp := newMenuTestApp()
	item := a.buildMenu().ItemByID(shell.MenuActionSettings)
	if item == nil {
		t.Fatalf("menu item %q missing", shell.MenuActionSettings)
	}
	if item.Click == nil {
		t.Fatalf("menu item %q has no click handler", shell.MenuActionSettings)
	}
	item.Click(item, nil)
	if shellApp.Screen != shell.Settings {
		t.Fatalf("Settings click left screen = %v, want Settings", shellApp.Screen)
	}
}

// TestMenuDispatchQuit checks the process-level command: the menu's Quit runs
// the quit seam and marks the app as quitting.
func TestMenuDispatchQuit(t *testing.T) {
	a, shellApp := newMenuTestApp()
	quits := 0
	a.quitApp = func() { quits++ }
	item := a.buildMenu().ItemByID(shell.MenuActionQuit)
	if item == nil {
		t.Fatalf("menu item %q missing", shell.MenuActionQuit)
	}
	item.Click(item, nil)
	if quits != 1 {
		t.Fatalf("Quit click ran the quit seam %d times, want 1", quits)
	}
	if !a.quitting {
		t.Fatal("Quit click did not mark the app as quitting")
	}
	// The shell's dispatch reaches the same handler, which matters for the
	// native Quit accelerator.
	if !shellApp.HandleMenuAction(shell.MenuActionQuit) {
		t.Fatal("shell did not recognize the Quit action")
	}
	if quits != 2 {
		t.Fatalf("Quit dispatch ran the quit seam %d times, want 2", quits)
	}
}

// TestMenuActionHookSeesEveryCommand checks the decoupling seam: every
// recognized menu command reaches the registered handler, together with the
// shell behaviour.
func TestMenuActionHookSeesEveryCommand(t *testing.T) {
	shellApp := &shell.App{}
	var got []string
	shellApp.SetMenuAction(func(action string) { got = append(got, action) })
	a := &app{shellApp: shellApp}

	a.dispatchMenuAction(shell.MenuActionNewSession)
	a.dispatchMenuAction(shell.MenuActionPlugins)
	if shellApp.Screen != shell.Plugins {
		t.Fatalf("Plugins command left screen = %v, want Plugins", shellApp.Screen)
	}
	want := []string{shell.MenuActionNewSession, shell.MenuActionPlugins}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("menu action hook calls = %v, want %v", got, want)
	}
}

// TestMenuLabelsFollowShellLanguage checks the command labels come from the
// shell's language resolution, so the menu bar matches the UI at startup.
func TestMenuLabelsFollowShellLanguage(t *testing.T) {
	a, shellApp := newMenuTestApp()
	shellApp.Language = shell.LangEN
	if got := a.menuCommandItem(shell.MenuActionNewSession).Label; got != "New Session" {
		t.Fatalf("English label = %q, want New Session", got)
	}
	shellApp.Language = shell.LangZH
	item := a.buildMenu().ItemByID(shell.MenuActionNewSession)
	if item == nil {
		t.Fatal("Chinese menu is missing the New Session item")
	}
	if item.Label != "新建会话" {
		t.Fatalf("Chinese label = %q, want 新建会话", item.Label)
	}
}

// TestMenuUnknownActionDoesNotPanic covers forward compatibility: an action a
// newer app build sends must be reported unknown and ignored, never crash.
func TestMenuUnknownActionDoesNotPanic(t *testing.T) {
	a, shellApp := newMenuTestApp()
	for _, action := range []string{"not_a_command", ""} {
		if shellApp.HandleMenuAction(action) {
			t.Errorf("HandleMenuAction(%q) reported a known action", action)
		}
		a.dispatchMenuAction(action)
	}
}

// TestInstallMenuWithoutWindowIsSafe covers the degenerate case: installMenu
// runs before a window exists (a headless test or a failed window creation) and
// must be a no-op instead of panicking.
func TestInstallMenuWithoutWindowIsSafe(t *testing.T) {
	a, _ := newMenuTestApp()
	a.installMenu()
}
