package uiroot

import (
	"strings"

	"github.com/egoist/mygo/ui"

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

// bindChromeShortcuts registers the two configurable chrome shortcuts from the
// host settings (falling back to the documented defaults). It runs every
// frame, so a settings change takes effect on the next frame.
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
		r.requestFrame()
	})
}
