package shell

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/session"
)

// installShortcuts registers the window's keyboard shortcuts. It runs on
// every frame, as the engine collects handlers per frame.
//
//	Cmd/Ctrl+N  new session
//	Cmd/Ctrl+,  settings
//	Cmd/Ctrl+K  focus the composer in a session, else search sessions
//	Esc         close the topmost overlay or menu
func (a *App) installShortcuts(c *ui.Context) {
	c.OnShortcut(ui.Cmd, ui.KeyN, func() { a.newSession() })
	c.OnShortcut(ui.Cmd, ui.KeyComma, func() { a.openSettings() })
	c.OnShortcut(ui.Cmd, ui.KeyK, func() { a.focusComposerOrSearch(c) })
	c.OnShortcut(0, ui.KeyEscape, func() { a.closeTaskTopmost() })
}

// newSession implements the New Session button and Cmd/Ctrl+N. The static
// shell switches to its demo session; the live shell persists and opens a
// real one.
func (a *App) newSession() {
	a.moreOpen = false
	a.closeSelectors()

	if a.Ctrl == nil {
		a.Screen = Session
		return
	}
	// Settings is a modal: the buffer belongs to the screen behind it, so
	// the new session must not be told the old surface was "Settings".
	oldScreen := a.draftOwnerScreen()
	oldID := a.draftOwnerSessionID()
	id, err := a.Ctrl.CreateSession(context.Background(), a.T("session.new_session"))
	if err != nil {
		return
	}
	snap, err := a.Ctrl.OpenSession(context.Background(), id)
	if err != nil {
		return
	}
	a.SetSnapshot(snap)
	a.applyDefaultAgentPreset()
	a.Screen = Session
	// A new session starts with a blank composer; never carry another
	// session's text into it. The previous session keeps its own draft.
	a.moveDraftToSession(oldScreen, oldID, id)
}

// focusComposerOrSearch is Cmd/Ctrl+K: in a live session it focuses the
// composer; elsewhere it opens the session search palette. The search icon
// in the sidebar opens the same palette from any live screen.
func (a *App) focusComposerOrSearch(c *ui.Context) {
	if a.Screen == Session && a.Ctrl != nil {
		a.composerRef.Focus(c)
		return
	}
	a.openPalette()
}

// openPalette opens the session search overlay and gives its input the
// focus.
func (a *App) openPalette() {
	a.moreOpen = false
	a.closeSelectors()
	a.paletteQuery = ""
	a.paletteOpen = true
}

// openSession makes id current and shows the session screen.
func (a *App) openSession(id string) {
	if a.Ctrl == nil {
		a.Screen = Session
		return
	}
	oldScreen := a.draftOwnerScreen()
	oldID := a.draftOwnerSessionID()
	beforeWS := a.Ctrl.CurrentWorkspaceID()
	snap, err := a.Ctrl.OpenSession(context.Background(), id)
	if err != nil {
		return
	}
	a.SetSnapshot(snap)
	a.Screen = Session
	if ws := a.Ctrl.CurrentWorkspaceID(); ws != "" && ws != beforeWS {
		// Cross-workspace jump: the Files tab and workspace expansion follow
		// the newly selected workspace, like switching it from the sidebar.
		a.resetFiles()
		a.wsHoverID = ""
		a.setWorkspaceCollapsed(ws, false)
	}
	a.moveDraftToSession(oldScreen, oldID, id)
}

// focusComposer requests the keyboard focus for the live composer. The
// session renderer binds App.composerRef when it builds the input; until then
// the request waits for the binding.
func (a *App) focusComposer(c *ui.Context) {
	a.composerRef.Focus(c)
}

// closeOneOverlay closes one overlay per Esc, from the surface the user is
// most likely to want gone first, and reports whether anything was closed.
func (a *App) closeOneOverlay() bool {
	switch {
	case a.paletteOpen:
		a.paletteOpen = false
	case a.deleteOpen:
		a.deleteOpen = false
	case a.renameOpen:
		a.renameOpen = false
	case a.shortcutsOpen:
		a.shortcutsOpen = false
	case a.aboutOpen:
		a.aboutOpen = false
	case a.moreOpen:
		a.moreOpen = false
	case a.permissionOpen || a.languageOpen || a.fontSizeOpen || a.workOpen ||
		a.collapseOpen || a.sendOpen:
		a.closeSelectors()
	case a.attachOpen:
		a.attachOpen = false
	case a.Screen == Settings:
		a.closeSettings()
	default:
		return false
	}
	return true
}

// shortcutRow is one line of the keyboard shortcuts editor. id names the
// persisted override (keybinding.<id>); keys is the effective binding.
type shortcutRow struct {
	id     string
	action string
	keys   string
}

// defaultShortcutRows is the single source of truth for the editor, so the
// list cannot drift from the bindings installTaskShortcuts registers. Keys
// this build does not implement (fork a session, open-in-app, window
// refresh/close) are deliberately absent rather than listed as if they worked.
func (a *App) defaultShortcutRows() []shortcutRow {
	return []shortcutRow{
		{"new_session", a.T("menu.new_session"), "Cmd/Ctrl+N"},
		{"add_workspace", a.T("workspace.add_workspace"), "Cmd/Ctrl+O"},
		{"settings", a.T("menu.settings"), "Cmd/Ctrl+,"},
		{"search", a.T("menu.focus_composer_or_search_outside_a_session"), "Cmd/Ctrl+K"},
		{"shortcuts", a.T("menu.keyboard_shortcuts"), "Cmd/Ctrl+/"},
		{"send_alternate", a.tr("Send message with the other busy behavior", "使用另一种忙碌行为发送消息"), "Cmd/Ctrl+Enter"},
		{"rename_session", a.T("menu.rename_session"), "Cmd/Ctrl+Alt+G"},
		{"archive_session", a.tr("Archive session", "归档会话"), "Cmd/Ctrl+Shift+A"},
		{"toggle_sidebar", a.tr("Toggle left sidebar", "切换左侧边栏"), "Cmd/Ctrl+B"},
		{"toggle_right_sidebar", a.tr("Toggle right sidebar", "切换右侧边栏"), "Cmd/Ctrl+Alt+B"},
		{"workspace_files", a.tr("Workspace files", "工作区文件"), "Cmd/Ctrl+P"},
		{"new_terminal", a.tr("New terminal", "新建终端"), "Ctrl+`"},
		{"escape", a.T("menu.close_overlay_or_menu"), "Esc"},
	}
}

// shortcutRows returns the rows with persisted overrides applied.
func (a *App) shortcutRows() []shortcutRow {
	rows := a.defaultShortcutRows()
	overrides := a.shortcutOverrides()
	for i := range rows {
		if v := strings.TrimSpace(overrides[rows[i].id]); v != "" {
			rows[i].keys = v
		}
	}
	return rows
}

// keybindingKey is the settings key for one shortcut override.
func keybindingKey(id string) string { return "keybinding." + id }

// shortcutOverrides lazily loads the persisted keybinding overrides.
func (a *App) shortcutOverrides() map[string]string {
	if a.shortcutsOverridesLoaded {
		return a.shortcutKeymap
	}
	a.shortcutsOverridesLoaded = true
	a.shortcutKeymap = make(map[string]string)
	if a.Ctrl == nil {
		return a.shortcutKeymap
	}
	for _, row := range a.defaultShortcutRows() {
		if raw, err := a.Ctrl.Store().GetSetting(keybindingKey(row.id)); err == nil {
			if strings.TrimSpace(raw) != "" {
				a.shortcutKeymap[row.id] = strings.TrimSpace(raw)
			}
		}
	}
	return a.shortcutKeymap
}

// setShortcutOverride persists one binding after validating its syntax.
func (a *App) setShortcutOverride(id, keys string) bool {
	return a.saveShortcutOverride(id, keys) == nil
}

// saveShortcutOverride is setShortcutOverride with the reason it failed, so
// the inline editor can tell an invalid chord from a store write failure
// instead of silently doing nothing.
func (a *App) saveShortcutOverride(id, keys string) error {
	keys = strings.TrimSpace(keys)
	if _, _, ok := parseShortcut(keys); !ok {
		if keys == "" {
			return fmt.Errorf("enter a shortcut first")
		}
		return fmt.Errorf("%q is not a valid shortcut; use a key such as Cmd+Shift+K", keys)
	}
	if a.Ctrl == nil || a.Ctrl.Store() == nil {
		return fmt.Errorf("the settings store is unavailable")
	}
	if err := a.Ctrl.Store().SetSetting(keybindingKey(id), keys); err != nil {
		return fmt.Errorf("could not save %q: %v", keys, err)
	}
	// The map is loaded lazily by shortcutOverrides; calling this method
	// before any load used to panic with "assignment to entry in nil map".
	if a.shortcutKeymap == nil {
		a.shortcutKeymap = make(map[string]string)
	}
	a.shortcutsOverridesLoaded = true
	a.shortcutKeymap[id] = keys
	return nil
}

// restoreShortcutDefaults clears every persisted binding override.
func (a *App) restoreShortcutDefaults() {
	if a.Ctrl == nil {
		return
	}
	overrides := a.shortcutOverrides()
	for _, row := range a.defaultShortcutRows() {
		_ = a.Ctrl.Store().SetSetting(keybindingKey(row.id), "")
		delete(overrides, row.id)
	}
}

// hasShortcutOverrides reports whether Restore all defaults has work to do.
func (a *App) hasShortcutOverrides() bool {
	return len(a.shortcutOverrides()) > 0
}

// shortcutOverride returns the persisted binding for id, or "".
func (a *App) shortcutOverride(id string) string {
	return strings.TrimSpace(a.shortcutOverrides()[id])
}

// bindShortcut registers fn for id's default binding, replacing it with a
// persisted override when one parses. This is what makes an edited shortcut
// real instead of a label change.
func (a *App) bindShortcut(c *ui.Context, id string, mods ui.Modifiers, key ui.Key, fn func()) {
	if raw := a.shortcutOverride(id); raw != "" {
		if m, k, ok := parseShortcut(raw); ok {
			mods, key = m, k
		}
	}
	c.OnShortcut(mods, key, fn)
}

// parseShortcut parses "Cmd+Shift+K", "Ctrl+Alt+B", "Esc", "Ctrl+`" or
// "Cmd+," into the modifiers and key OnShortcut takes. Cmd and Ctrl are
// treated as the platform command key, matching the labels.
func parseShortcut(s string) (ui.Modifiers, ui.Key, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, ui.KeyUnknown, false
	}
	parts := strings.Split(s, "+")
	var mods ui.Modifiers
	keyPart := strings.TrimSpace(parts[len(parts)-1])
	for _, raw := range parts[:len(parts)-1] {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "cmd", "command", "ctrl", "control", "cmd/ctrl":
			mods |= ui.Cmd
		case "alt", "option", "opt":
			mods |= ui.Alt
		case "shift":
			mods |= ui.Shift
		default:
			return 0, ui.KeyUnknown, false
		}
	}
	if keyPart == "" {
		return 0, ui.KeyUnknown, false
	}
	lower := strings.ToLower(keyPart)
	switch lower {
	case "esc", "escape":
		return mods, ui.KeyEscape, true
	case "enter", "return":
		return mods, ui.KeyEnter, true
	case "tab":
		return mods, ui.KeyTab, true
	case "space":
		return mods, ui.KeySpace, true
	case "backquote", "`":
		return mods, ui.KeyBackquote, true
	case "slash", "/":
		return mods, ui.KeySlash, true
	case "comma", ",":
		return mods, ui.KeyComma, true
	case "period", ".":
		return mods, ui.KeyPeriod, true
	case "minus", "-":
		return mods, ui.KeyMinus, true
	}
	if len(lower) == 1 {
		switch {
		case lower[0] >= 'a' && lower[0] <= 'z':
			return mods, ui.KeyA + ui.Key(lower[0]-'a'), true
		case lower[0] >= '0' && lower[0] <= '9':
			return mods, ui.Key0 + ui.Key(lower[0]-'0'), true
		}
	}
	return 0, ui.KeyUnknown, false
}

// compile-time assertion that the session settings type stays the source of
// truth for the shell's settings interface.
var _ settingsAPI = (*session.SettingsStore)(nil)
