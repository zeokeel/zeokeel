package contracts

// Host chrome shortcut settings. Values are canonical chord strings such as
// "cmd+b", "ctrl+b", "alt+b", "cmd+shift+b"; the host parses them on every
// frame, so a change applies immediately.
const (
	// SettingsUIShortcutToggleSidebar is the chord collapsing/expanding the
	// left sidebar (default cmd+b).
	SettingsUIShortcutToggleSidebar = "ui.shortcut.toggleSidebar"
	// SettingsUIShortcutCommandPalette is the chord opening the command
	// palette (default cmd+k).
	SettingsUIShortcutCommandPalette = "ui.shortcut.commandPalette"
)

// DefaultShortcutToggleSidebar / DefaultShortcutCommandPalette are the
// documented defaults.
const (
	DefaultShortcutToggleSidebar  = "cmd+b"
	DefaultShortcutCommandPalette = "cmd+k"
)
