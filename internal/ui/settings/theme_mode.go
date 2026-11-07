package settings

// The appearance page's three-state theme mode: light, dark or follow the
// desktop (system). The selection writes the new ui.theme.mode key and keeps the
// legacy ui.theme.dark boolean in step for older builds; the process host applies
// the mode and follows desktop changes in system mode.

import (
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Theme mode indexes in the segmented control.
const (
	themeModeLight = iota
	themeModeDark
	themeModeSystem
)

// themeModeNames are the stored values of the three modes.
var themeModeNames = []string{"light", "dark", "system"}

// appearanceThemeMode normalizes the stored mode (with the legacy dark boolean
// fallback) to the segmented index. Empty and unknown values default to light;
// the legacy ui.theme.dark=true selects dark.
func appearanceThemeMode(raw, legacyDark string) int {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "light":
		return themeModeLight
	case "dark":
		return themeModeDark
	case "system":
		return themeModeSystem
	case "":
		if strings.TrimSpace(legacyDark) == "true" {
			return themeModeDark
		}
		return themeModeLight
	default:
		return themeModeLight
	}
}

// applyThemeModeSelection applies one segmented choice: light/dark force the
// host theme and preview immediately; system stores the mode plus the legacy
// false placeholder (the process host follows the desktop when it starts).
func applyThemeModeSelection(state *appearanceState, store contracts.SettingsService,
	theme contracts.ThemeService, locale contracts.LocaleService) {
	mode := themeModeName(state.themeMode)
	switch state.themeMode {
	case themeModeDark:
		theme.SetDark(true)
		_ = store.Set(contracts.SettingsUIThemeDark, "true")
	case themeModeLight:
		theme.SetDark(false)
		_ = store.Set(contracts.SettingsUIThemeDark, "false")
	case themeModeSystem:
		// The host follows the desktop at startup; keep the legacy placeholder
		// false so older builds start light.
		_ = store.Set(contracts.SettingsUIThemeDark, "false")
	default:
		state.themeMode = themeModeLight
		mode = themeModeName(state.themeMode)
		theme.SetDark(false)
		_ = store.Set(contracts.SettingsUIThemeDark, "false")
	}
	_ = store.Set(contracts.SettingThemeMode, mode)
	state.status = localized(locale, "主题模式已切换", "Theme mode switched")
	state.statusErr = false
}

// themeModeName returns the stored value for a segmented index.
func themeModeName(index int) string {
	if index < 0 || index >= len(themeModeNames) {
		return themeModeNames[themeModeLight]
	}
	return themeModeNames[index]
}
