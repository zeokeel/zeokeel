package app

import (
	"runtime"

	"github.com/egoist/mygo"

	"github.com/zeokeel/zeokeel/internal/shell"
)

// buildMenu returns the native menu-bar template. One template serves every
// platform: mygo hides the macOS-only roles (the application menu itself) on
// Windows and Linux, where menu bars belong to windows rather than the
// application.
//
//	macOS:         ZeoKeel  File  Edit  View  Window
//	Windows/Linux:          File  Edit  View  Window
func (a *app) buildMenu() *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		// macOS application menu (hidden elsewhere): Settings…/About/Hide/Quit.
		{Role: mygo.RoleAppMenu, Submenu: a.appMenuItems()},
		// File: the document-free application commands.
		{Role: mygo.RoleFileMenu, Submenu: a.fileMenuItems()},
		// Edit: the standard editing roles, so Cmd/Ctrl+C/V/A work in the
		// composer and every other native text input.
		{Role: mygo.RoleEditMenu},
		// View: the app's screens and the sidebar toggle.
		{Role: mygo.RoleViewMenu, Submenu: []*mygo.MenuItem{
			a.menuCommandItem(shell.MenuActionToggleSidebar),
			mygo.Separator(),
			a.menuCommandItem(shell.MenuActionPlugins),
			a.menuCommandItem(shell.MenuActionAutomation),
			mygo.Separator(),
			{Role: mygo.RoleToggleFullScreen},
		}},
		// Window: the platform's standard window actions (RoleWindowMenu picks
		// minimize/zoom/close and the macOS "Bring All to Front" entry).
		{Role: mygo.RoleWindowMenu},
	})
}

// appMenuItems is the macOS application menu. The parent role is hidden on
// Windows and Linux, where File carries Settings and Quit instead.
func (a *app) appMenuItems() []*mygo.MenuItem {
	return []*mygo.MenuItem{
		a.menuCommandItem(shell.MenuActionSettings),
		mygo.Separator(),
		{Role: mygo.RoleAbout},
		mygo.Separator(),
		{Role: mygo.RoleServices},
		mygo.Separator(),
		{Role: mygo.RoleHide},
		{Role: mygo.RoleHideOthers},
		{Role: mygo.RoleUnhide},
		mygo.Separator(),
		a.menuCommandItem(shell.MenuActionQuit),
	}
}

// fileMenuItems is the File menu. Settings and Quit sit in the application
// menu on macOS, where the platform convention puts them.
func (a *app) fileMenuItems() []*mygo.MenuItem {
	items := []*mygo.MenuItem{
		a.menuCommandItem(shell.MenuActionNewSession),
		a.menuCommandItem(shell.MenuActionSearch),
	}
	if runtime.GOOS != "darwin" {
		items = append(items,
			mygo.Separator(),
			a.menuCommandItem(shell.MenuActionSettings),
			mygo.Separator(),
			a.menuCommandItem(shell.MenuActionQuit),
		)
	}
	return items
}

// menuCommandItem builds one command item from the shell's command table, so
// the label and accelerator cannot drift from the shortcut they mirror.
func (a *app) menuCommandItem(action string) *mygo.MenuItem {
	cmd, ok := shell.MenuCommandByAction(action)
	label, accelerator := action, ""
	if ok {
		label = cmd.LabelEN
		accelerator = cmd.Accelerator
		if a.shellApp != nil {
			label = a.shellApp.MenuLabel(action)
		}
	}
	return &mygo.MenuItem{
		ID:          action,
		Label:       label,
		Accelerator: accelerator,
		Click:       func(*mygo.MenuItem, *mygo.Window) { a.dispatchMenuAction(action) },
	}
}

// installMenu puts the menu bar in place once the main window exists. macOS
// owns the menu bar at the application level; Windows and Linux attach menu
// bars to windows, so the same template becomes the main window's bar.
func (a *app) installMenu() {
	if a.win == nil {
		return
	}
	menu := a.buildMenu()
	if runtime.GOOS == "darwin" {
		mygo.App.SetMenu(menu)
		return
	}
	a.win.SetMenu(menu)
}

// dispatchMenuAction routes one native-menu command. The shell handles the UI
// commands and reports unknown ones; this layer handles process-level commands
// (Quit) and ignores anything else.
func (a *app) dispatchMenuAction(action string) {
	if a.shellApp != nil && a.shellApp.HandleMenuAction(action) {
		return
	}
	a.handleMenuAction(action)
}

// handleMenuAction is the handler registered with shell.App.SetMenuAction. It
// runs for every command the shell recognizes, after the shell ran its own UI
// behaviour; only process-level commands belong here.
func (a *app) handleMenuAction(action string) {
	switch action {
	case shell.MenuActionQuit:
		a.quitApplication()
	}
}

// quitApplication ends the process through one path shared by the tray, the
// menu bar and the Quit accelerator. a.quitting stops the close-to-hide
// handlers from re-showing the window while the app winds down.
func (a *app) quitApplication() {
	a.quitting = true
	if a.quitApp != nil {
		a.quitApp()
		return
	}
	mygo.App.Quit()
}
