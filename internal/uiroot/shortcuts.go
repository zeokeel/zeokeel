package uiroot

import (
	"log"
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// shortcutKey maps one canonical key name ("b", "k", ...) to a mygo key.
func shortcutKey(name string) (ui.Key, bool) {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "A":
		return ui.KeyA, true
	case "B":
		return ui.KeyB, true
	case "C":
		return ui.KeyC, true
	case "D":
		return ui.KeyD, true
	case "E":
		return ui.KeyE, true
	case "F":
		return ui.KeyF, true
	case "G":
		return ui.KeyG, true
	case "H":
		return ui.KeyH, true
	case "I":
		return ui.KeyI, true
	case "J":
		return ui.KeyJ, true
	case "K":
		return ui.KeyK, true
	case "L":
		return ui.KeyL, true
	case "M":
		return ui.KeyM, true
	case "N":
		return ui.KeyN, true
	case "O":
		return ui.KeyO, true
	case "P":
		return ui.KeyP, true
	case "Q":
		return ui.KeyQ, true
	case "R":
		return ui.KeyR, true
	case "S":
		return ui.KeyS, true
	case "T":
		return ui.KeyT, true
	case "U":
		return ui.KeyU, true
	case "V":
		return ui.KeyV, true
	case "W":
		return ui.KeyW, true
	case "X":
		return ui.KeyX, true
	case "Y":
		return ui.KeyY, true
	case "Z":
		return ui.KeyZ, true
	}
	return 0, false
}

// parseShortcutChord parses a stored chord ("cmd+k", "ctrl+b",
// "cmd+shift+k", "alt+b") into mygo modifiers and key. "cmd" is the platform
// command key (Command on macOS, Ctrl elsewhere); "ctrl" is the literal Ctrl.
func parseShortcutChord(raw string) (ui.Modifiers, ui.Key, bool) {
	parts := strings.Split(strings.TrimSpace(strings.ToLower(raw)), "+")
	if len(parts) < 2 {
		return 0, 0, false
	}
	key, ok := shortcutKey(parts[len(parts)-1])
	if !ok {
		return 0, 0, false
	}
	var mods ui.Modifiers
	for _, part := range parts[:len(parts)-1] {
		switch strings.TrimSpace(part) {
		case "cmd", "command", "cmd/ctrl":
			mods |= ui.Cmd
		case "ctrl", "control":
			mods |= ui.Ctrl
		case "shift":
			mods |= ui.Shift
		case "alt", "option":
			mods |= ui.Alt
		default:
			return 0, 0, false
		}
	}
	return mods, key, true
}

// newSessionShortcut implements Cmd/Ctrl+N: create a fresh session and show the
// conversation screen. A nil chat backend falls back to the registered
// zeokeel.session.new command (the session module owns the ChatService in the
// desktop host); errors are deliberately silent and panics are contained.
func (r *Root) newSessionShortcut() {
	runHandlerSafely("shortcut new-session", func() {
		r.mu.Lock()
		chat := r.chatService
		kernel := r.kernelContext
		r.mu.Unlock()
		if chat == nil {
			if fromKernel, ok := kernel.Get(contracts.KeyChat); ok {
				chat = fromKernel
			}
		}
		if chat != nil {
			if _, err := chat.NewSession(""); err != nil {
				return
			}
			_ = r.shortcutNavigator(kernel).Push("session")
			r.requestFrame()
			return
		}
		for _, command := range r.rt.Commands() {
			if command.ID != "zeokeel.session.new" {
				continue
			}
			if command.Enabled != nil && !command.Enabled() {
				return
			}
			if command.Run != nil {
				_ = command.Run()
			}
			r.requestFrame()
			return
		}
	})
}

// settingsShortcut implements Cmd/Ctrl+,: toggle the settings surface.
func (r *Root) settingsShortcut() {
	runHandlerSafely("shortcut settings", func() {
		r.mu.Lock()
		kernel := r.kernelContext
		r.mu.Unlock()
		r.shortcutNavigator(kernel).ToggleSettings()
		r.requestFrame()
	})
}

// shortcutNavigator prefers the kernel-provided KeyNavigator when a kernel
// context is bound (the host provides the same navigator as r.nav); it falls
// back to the root-owned navigator otherwise.
func (r *Root) shortcutNavigator(kernel cordis.Context) contracts.Navigator {
	if navigator, ok := kernel.Get(contracts.KeyNavigator); ok && navigator != nil {
		return navigator
	}
	return r.nav
}

// paletteShortcut resolves the command palette chord from the user's settings,
// falling back to the documented default. The settings surface re-binds the
// same chord in its own focus scope (fix round SH-07), so both callers must
// read it the same way.
func (r *Root) paletteShortcut() (ui.Modifiers, ui.Key) {
	mods, key := ui.Cmd, ui.KeyK
	if r.settingsService != nil {
		if parsedMods, parsedKey, ok := parseShortcutChord(r.settingsService.Get(contracts.SettingsUIShortcutCommandPalette,
			contracts.DefaultShortcutCommandPalette)); ok {
			mods, key = parsedMods, parsedKey
		}
	}
	return mods, key
}

// togglePalette opens or closes the host command palette, as Cmd/Ctrl+K does.
// Opening it starts from a fresh query and a fresh choice every time.
func (r *Root) togglePalette() {
	r.paletteOpen = !r.paletteOpen
	r.paletteQuery = ""
	r.paletteFilter = ""
	r.paletteSelected = 0
	r.paletteCount = 0
	if r.paletteOpen {
		// The palette is a transient overlay: its list state must not outlive
		// one opening. A reused ListState follows the previous opening's chosen
		// item into the new list and writes that old index back into the
		// selection while the frame builds, which left a reopened palette with
		// no visible chosen row while Enter still ran one (fix round SH-02).
		// The reset happens here, at the frame boundary, never while the list
		// of the closing frame is still building.
		r.paletteList = ui.ListState{}
	}
	r.requestFrame()
}

// bindChromeShortcuts registers the two configurable chrome shortcuts from the
// host settings (falling back to the documented defaults) plus the built-in
// Cmd/Ctrl+N and Cmd/Ctrl+, actions. It runs every frame, so a settings change
// takes effect on the next frame; registrations are frame-scoped by mygo's
// context shortcut hook, so they never accumulate.
func (r *Root) bindChromeShortcuts(c *ui.Context) {
	// Toggles must act on a press, not on the auto-repeat of a key held down;
	// mygo v0.8 reports the repeat with Context.ShortcutRepeated
	// (docs/ui/input.md: "Shortcuts").
	guardRepeat := func(fn func()) func() {
		return func() {
			if c.ShortcutRepeated() {
				return
			}
			fn()
		}
	}
	toggleMods, toggleKey := ui.Cmd, ui.KeyB
	if r.settingsService != nil {
		if mods, key, ok := parseShortcutChord(r.settingsService.Get(contracts.SettingsUIShortcutToggleSidebar,
			contracts.DefaultShortcutToggleSidebar)); ok {
			toggleMods, toggleKey = mods, key
		}
	}
	c.OnShortcut(toggleMods, toggleKey, guardRepeat(r.ToggleSidebarCollapsed))

	paletteMods, paletteKey := r.paletteShortcut()
	c.OnShortcut(paletteMods, paletteKey, guardRepeat(r.togglePalette))

	// Built-in inspector shortcut: the right panel has a visible toggle, and a
	// keyboard path so the keyboard-first shell reaches it without a mouse. The
	// inspector only exists on the session screen (it reads that screen's read
	// model), so the toggle is a no-op elsewhere.
	c.OnShortcut(ui.Cmd|ui.Alt, ui.KeyI, guardRepeat(func() {
		if !r.inspectorAvailable() {
			return
		}
		// Closing the modal drawer must hand the keyboard back: the drawer
		// trapped it. A docked pane toggled by the chord leaves the focus
		// where the user had it (the composer, say).
		r.setRightPanelOpen(!r.rightOpen, r.inspectorDrawerShown)
	}))

	// Inspector pane cycling: the inspector module registers the commands, the
	// host binds the chord (H26: pane switching should not require opening the
	// pane menu with the pointer). While the inspector is closed the chord must
	// not silently mutate the hidden pane: it explains once and does nothing.
	c.OnShortcut(ui.Cmd|ui.Alt, ui.KeyLeft, guardRepeat(func() { r.runPaneCommand("rightpanel.pane.prev") }))
	c.OnShortcut(ui.Cmd|ui.Alt, ui.KeyRight, guardRepeat(func() { r.runPaneCommand("rightpanel.pane.next") }))

	// Built-in window shortcuts (not configurable): new session and settings.
	c.OnShortcut(ui.Cmd, ui.KeyN, r.newSessionShortcut)
	c.OnShortcut(ui.Cmd, ui.KeyComma, r.settingsShortcut)
}

// runPaneCommand runs one of the inspector's pane-cycle commands, but only
// while the inspector is open on the session screen: pane state is hidden with
// the inspector, and a chord that silently changes an invisible pane makes the
// next open show an unexpected pane. The first press while closed explains the
// no-op once per closed episode.
func (r *Root) runPaneCommand(id string) {
	if !r.inspectorAvailable() || !r.rightOpen {
		r.hintInspectorClosed()
		return
	}
	r.runCommandByID(id)
}

// hintInspectorClosed tells the user once why the pane chord did nothing. A
// host without a toast renderer (headless tests) just keeps the state.
func (r *Root) hintInspectorClosed() {
	if r.paneHintShown {
		return
	}
	r.paneHintShown = true
	if r.notifier != nil {
		r.notifier.Notify(contracts.NotifyInfo,
			r.tr("检查器已收起：先打开右侧栏再切换面板", "The inspector is closed: open it to switch panes"))
	}
}

// runCommandByID runs one registered command by id, if it exists and is
// enabled. The host uses it for chrome shortcuts whose action belongs to a
// module (the inspector's pane cycling).
func (r *Root) runCommandByID(id string) {
	for _, cmd := range r.rt.commandSpecs() {
		if cmd.ID != id {
			continue
		}
		if cmd.Enabled != nil && !cmd.Enabled() {
			return
		}
		if err := runSafelyErr(cmd.Run); err != nil {
			log.Printf("uiroot: command %q: %v", id, err)
		}
		r.requestFrame()
		return
	}
}
