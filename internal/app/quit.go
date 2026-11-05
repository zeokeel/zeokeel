package app

import "github.com/egoist/mygo"

// The window's native menu bar (File/Edit/View/Window) was retired on
// 2026-11-05: the user does not want it (ADR 0006). This package no longer
// builds a mygo.Menu template, never installs one on the window, and removes
// the application menu explicitly in app.run. The tray menu (installTray) plus
// the shell's keyboard shortcuts and in-app entry points are the command
// entry points; the shell owns the UI commands (internal/shell/shortcuts.go).
//
// This file keeps the one process-level command that is not a window-menu
// concern: Quit.

// quitApplication ends the process through one path shared by the tray's Quit
// item. a.quitting stops the close-to-hide handlers from re-showing the window
// while the app winds down; the other quit paths (the OS, the in-app More
// menu's Quit) are covered by the mygo.App.OnBeforeQuit listener in app.run.
func (a *app) quitApplication() {
	a.quitting = true
	if a.quitApp != nil {
		a.quitApp()
		return
	}
	mygo.App.Quit()
}
