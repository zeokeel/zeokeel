package settings

// Close-to-tray setting helpers for the appearance page. The process host reads
// the same contracts.SettingCloseToTray key and normalizes it the same way:
// true/1/yes (any case, trimmed) enable hiding on close; everything else keeps
// the default (off, closing the window quits). A restart applies the change.

import "strings"

// closeToTraySettingEnabled normalizes a stored contracts.SettingCloseToTray
// value for the appearance page's toggle.
func closeToTraySettingEnabled(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}
