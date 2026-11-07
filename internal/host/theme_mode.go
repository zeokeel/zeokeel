package host

// The appearance three-state setting: light/dark force the app appearance,
// system follows the desktop. The host resolves the stored mode (with the
// legacy ui.theme.dark boolean as a fallback), applies it before the first
// frame, and in system mode follows later desktop appearance changes.

import (
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// systemTheme is the mygo appearance surface the host follows in system mode.
// *mygo.Theme implements it; tests inject a fake.
type systemTheme interface {
	IsDark() bool
	OnUpdated(func()) func()
}

// resolveThemeDark maps contracts.SettingThemeMode to the app darkness: light
// forces light, dark forces dark, and system/empty/unknown follow systemDark
// (an unknown value normalizes to system).
func resolveThemeDark(mode string, systemDark bool) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "light":
		return false
	case "dark":
		return true
	default:
		return systemDark
	}
}

// initialThemeDark resolves the startup appearance. A missing/empty mode falls
// back to the legacy ui.theme.dark boolean when that key exists; otherwise
// system/empty/unknown follow systemDark. followSystem reports whether the host
// should keep following desktop appearance changes.
func initialThemeDark(settings *Settings, systemDark bool) (dark bool, followSystem bool) {
	if settings == nil {
		return systemDark, true
	}
	mode := strings.TrimSpace(settings.Get(contracts.SettingThemeMode, ""))
	if mode == "" {
		if legacy, ok := settings.All()[settingDark]; ok {
			return strings.TrimSpace(legacy) == "true", false
		}
	}
	switch strings.ToLower(mode) {
	case "light":
		return false, false
	case "dark":
		return true, false
	default:
		return systemDark, true
	}
}

// applyThemeMode applies the stored mode to the host theme and, for
// system/empty/unknown modes, registers the desktop-appearance listener. The
// returned function unregisters the listener (nil when the mode is forced).
// Call it on the main thread once mygo is ready (App.WhenReady).
func applyThemeMode(theme contracts.ThemeService, settings *Settings, system systemTheme) func() {
	if theme == nil || system == nil {
		return nil
	}
	dark, followSystem := initialThemeDark(settings, system.IsDark())
	theme.SetDark(dark)
	if !followSystem {
		return nil
	}
	return system.OnUpdated(func() {
		theme.SetDark(system.IsDark())
	})
}
