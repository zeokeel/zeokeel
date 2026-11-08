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

// bindChromeShortcuts registers the two configurable chrome shortcuts from the
// host settings (falling back to the documented defaults) plus the built-in
// Cmd/Ctrl+N and Cmd/Ctrl+, actions. It runs every frame, so a settings change
// takes effect on the next frame; registrations are frame-scoped by mygo's
// context shortcut hook, so they never accumulate.
func (r *Root) bindChromeShortcuts(c *ui.Context) {
	toggleMods, toggleKey := ui.Cmd, ui.KeyB
	if r.settingsService != nil {
		if mods, key, ok := parseShortcutChord(r.settingsService.Get(contracts.SettingsUIShortcutToggleSidebar,
			contracts.DefaultShortcutToggleSidebar)); ok {
			toggleMods, toggleKey = mods, key
		}
	}
	c.OnShortcut(toggleMods, toggleKey, r.ToggleSidebarCollapsed)

	paletteMods, paletteKey := ui.Cmd, ui.KeyK
	if r.settingsService != nil {
		if mods, key, ok := parseShortcutChord(r.settingsService.Get(contracts.SettingsUIShortcutCommandPalette,
			contracts.DefaultShortcutCommandPalette)); ok {
			paletteMods, paletteKey = mods, key
		}
	}
	c.OnShortcut(paletteMods, paletteKey, func() {
		r.paletteOpen = !r.paletteOpen
		r.paletteQuery = ""
		r.paletteFilter = ""
		r.paletteSelected = 0
		r.requestFrame()
	})

	// Built-in inspector shortcut: the right panel has a visible toggle, and a
	// keyboard path so the keyboard-first shell reaches it without a mouse.
	c.OnShortcut(ui.Cmd|ui.Alt, ui.KeyI, func() {
		if !r.hasAnySlot(contracts.SlotSessionPanelRight) {
			return
		}
		r.rightOpen = !r.rightOpen
		r.requestFrame()
	})

	// Inspector pane cycling: the inspector module registers the commands, the
	// host binds the chord (H26: pane switching should not require opening the
	// pane menu with the pointer).
	c.OnShortcut(ui.Cmd|ui.Alt, ui.KeyLeft, func() { r.runCommandByID("rightpanel.pane.prev") })
	c.OnShortcut(ui.Cmd|ui.Alt, ui.KeyRight, func() { r.runCommandByID("rightpanel.pane.next") })

	// Built-in window shortcuts (not configurable): new session and settings.
	c.OnShortcut(ui.Cmd, ui.KeyN, r.newSessionShortcut)
	c.OnShortcut(ui.Cmd, ui.KeyComma, r.settingsShortcut)
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
