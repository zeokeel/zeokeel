package uiroot

import (
	"log"
	"strings"
	"time"

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

// newSessionClickDebounce is how long a second click on a "new session" control
// is ignored. A double click is one intent, not two conversations, and the
// pointer path is the one that can fire twice in one gesture; the keyboard chord
// stays unguarded, because two deliberate presses are two requests (A4-I-04).
const newSessionClickDebounce = 600 * time.Millisecond

// newSessionFromClick is the pointer entry of the new-session action (the
// collapsed rail's button): it debounces a double click, then runs the same
// action as the chord.
func (r *Root) newSessionFromClick() {
	r.mu.Lock()
	last := r.newSessionClickedAt
	r.mu.Unlock()
	if !last.IsZero() && time.Since(last) < newSessionClickDebounce {
		return
	}
	r.mu.Lock()
	r.newSessionClickedAt = time.Now()
	r.mu.Unlock()
	r.newSessionShortcut()
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
	r.paletteCount = 0
	// The choice starts from the first match (and a close forgets it with the
	// rest of the transient state). Choose is the framework's "the app chose
	// this row": it also forgets the previous opening's chosen command, which
	// the List would otherwise follow by Key into the new rows and write back
	// as an old index, leaving a reopened palette with no visible chosen row
	// while Enter still ran one (fix round SH-02). Selected must be set first:
	// Choose does nothing without it. The rest of the list state is the
	// palette's transient overlay state and is reused, not zeroed.
	r.paletteList.Selected = &r.paletteSelected
	r.paletteList.Choose(0)
	r.requestFrame()
}

// closePalette dismisses the command palette and forgets its transient query,
// filter and choice, so the next opening starts clean. It is the one close path
// shared by the palette button, Escape and the root shortcut.
func (r *Root) closePalette() {
	if !r.paletteOpen {
		return
	}
	r.paletteOpen = false
	r.paletteQuery = ""
	r.paletteFilter = ""
	r.paletteSelected = 0
	r.paletteCount = 0
	r.requestFrame()
}

// sessionEscapeCommandID is the stable command the session module registers to
// adjudicate the conversation's Escape semantics (child view returns to the
// parent, an open FindBar closes, menus dismiss). The window-level fallback
// delegates to it when present so the same key keeps one meaning regardless of
// where the focus sits (2026-11 audit UX-03); the fallback's own deny/stop
// promise applies only when the module has not registered the command.
const sessionEscapeCommandID = "session.escape"

// escapeFallback is the window-level Escape. Screen-level handlers (the
// composer's stop key, the approval card's deny key, menus, dialogs and the
// inspector drawer) take Escape first when their scope holds the focus. When
// the focus sits outside the conversation — the sidebar footer after closing
// settings, say — those handlers never see the key, so the key that the
// approval card promises ("Esc 拒绝") would silently do nothing. This fallback
// keeps the promise: deny the oldest pending approval, else stop the running
// turn, but only on the conversation screen with no modal surface open (R4).
//
// The session module owns the conversation's full Escape semantics through the
// stable session.escape command (it knows the child view, the FindBar and the
// menu state the window fallback cannot see). When that command is registered
// and enabled the fallback defers to it instead of blind-denying the parent
// approval or stopping a hidden run (UX-03); only when the command is absent or
// disabled does the fallback keep the deny/stop promise itself.
func (r *Root) escapeFallback() {
	if r.nav == nil || r.nav.Current() != sessionScreenID {
		return
	}
	if r.paletteOpen || r.inspectorDrawerShown || r.nav.SettingsOpen() {
		return
	}
	if r.runCommandByID(sessionEscapeCommandID) {
		return
	}
	r.mu.Lock()
	kernel := r.kernelContext
	chat := r.chatService
	r.mu.Unlock()
	if approvals, ok := kernel.Get(contracts.KeyApprovals); ok {
		if pending := approvals.Pending(); len(pending) > 0 {
			if err := approvals.Resolve(pending[0].ID, contracts.ApprovalDeny); err == nil {
				r.requestFrame()
			}
			return
		}
	}
	if chat == nil {
		if fromKernel, ok := kernel.Get(contracts.KeyChat); ok {
			chat = fromKernel
		}
	}
	if chat == nil {
		return
	}
	id := chat.ActiveSession()
	if id == "" {
		return
	}
	if session, ok := chat.Session(id); ok && session.Running {
		if err := chat.Cancel(id); err == nil {
			r.requestFrame()
		}
	}
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

	// Right-sidebar page chords: the inspector module registers the commands
	// (they open the host panel themselves and are idempotent), so the host
	// only binds the chords the guide advertises.
	c.OnShortcut(ui.Cmd|ui.Alt, ui.KeyP, guardRepeat(func() { r.runCommandByID("rightpanel.files.open") }))

	// Built-in window shortcuts (not configurable): new session and settings.
	c.OnShortcut(ui.Cmd, ui.KeyN, r.newSessionShortcut)
	c.OnShortcut(ui.Cmd, ui.KeyComma, r.settingsShortcut)

}

// bindScreenEscape registers the window's Escape fallback: deny the oldest
// pending approval, else stop the running turn, else let the session module
// adjudicate through its own stable escape command. view calls it at the frame's
// root context, so the key reaches the screen's safe action from any chrome
// control, and an open dialog, menu or popover still consumes it first in its own
// scope (R4).
func (r *Root) bindScreenEscape(c *ui.Context) {
	// The shell's Escape fallback, declared at the frame's root context: mygo
	// routes the key down the ancestors of the focused element FIRST and only
	// then to this root-wide handler, so an open dialog, menu or popover still
	// takes the Escape that closes it (the P0 the interaction QA caught came from
	// binding the root ROW's subtree, which sits above nothing and below
	// everything). It is also the only form that registers outside a modal:
	// Context.OnWindowShortcut keeps a handler only inside a dialog, so an
	// explicit call here would leave the window with no fallback at all.
	c.OnShortcut(0, ui.KeyEscape, r.escapeFallback)
}

// runCommandByID runs one registered command by id, if it exists and is
// enabled, reporting whether it ran. The host uses it for chrome shortcuts
// whose action belongs to a module (the inspector's pane cycling) and for the
// Escape fallback's delegation to the session module's session.escape command
// (a false return means the command is absent or disabled, so the caller keeps
// its own fallback behavior).
func (r *Root) runCommandByID(id string) bool {
	for _, cmd := range r.rt.commandSpecs() {
		if cmd.ID != id {
			continue
		}
		if cmd.Enabled != nil && !cmd.Enabled() {
			return false
		}
		if err := runSafelyErr(cmd.Run); err != nil {
			log.Printf("uiroot: command %q: %v", id, err)
		}
		r.requestFrame()
		return true
	}
	return false
}
