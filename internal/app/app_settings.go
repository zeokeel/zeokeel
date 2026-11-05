package app

import (
	"context"
	"log"
	"strings"

	"github.com/zeokeel/zeokeel/internal/shell"
)

// App-level settings.
//
// The shell owns the Settings UI and persists plain settings keys; the app owns
// the behavior those keys select. The helpers here read the live store so a
// setting change applies without a restart (the shell calls refreshWindows after
// every save).

// settingString reads one persisted setting through the live store, returning
// def when services are not ready, the key is missing, or the value is blank.
func (a *app) settingString(key, def string) string {
	if a == nil || a.services == nil {
		return def
	}
	raw, err := a.services.Store().GetSetting(key)
	if err != nil {
		return def
	}
	if value := strings.TrimSpace(raw); value != "" {
		return value
	}
	return def
}

// settingBool reads a boolean setting. Accepts the spellings the shell saves
// (true/false, 1/0, on/off, yes/no); anything else keeps def.
func (a *app) settingBool(key string, def bool) bool {
	raw := strings.ToLower(strings.TrimSpace(a.settingString(key, "")))
	switch raw {
	case "1", "true", "on", "yes", "enabled":
		return true
	case "0", "false", "off", "no", "disabled":
		return false
	default:
		return def
	}
}

// notificationsEnabled reads notification.enabled (default true).
func (a *app) notificationsEnabled() bool {
	return a.settingBool("notification.enabled", true)
}

// notificationsSuppressFocused reads notification.suppress_focused (default
// true: do not notify while the user is looking at the app).
func (a *app) notificationsSuppressFocused() bool {
	return a.settingBool("notification.suppress_focused", true)
}

// closeBehaviorQuits reports whether closing the main window should exit instead
// of hiding to the tray (ui.close_behavior: "hide" (default) or "quit").
func (a *app) closeBehaviorQuits() bool {
	return strings.EqualFold(a.settingString("ui.close_behavior", "hide"), "quit")
}

// trayNewSession handles the tray's New Session item: it goes through the shell
// like Cmd+N so the UI (screen switch, composer focus) stays consistent. A nil
// shell (services not ready yet) falls back to creating the session directly.
func (a *app) trayNewSession() {
	if a.shellApp != nil && a.shellApp.HandleMenuAction(shell.MenuActionNewSession) {
		return
	}
	if a.services == nil {
		return
	}
	if _, err := a.services.CreateSession(context.Background(), ""); err != nil {
		log.Printf("zeokeel: tray new session: %v", err)
	}
}

// traySettings handles the tray's Settings item: same path as Cmd+, so the
// separate settings window opens (or the in-app overlay falls back).
func (a *app) traySettings() {
	if a.shellApp == nil {
		return
	}
	if !a.shellApp.HandleMenuAction(shell.MenuActionSettings) {
		log.Printf("zeokeel: tray settings: no settings handler")
	}
}
