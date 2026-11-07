package host

// Single-instance policy: on desktop launches the process keeps one instance and
// hands later launches to it (mygo's RequestSingleInstanceLock/OnSecondInstance).
// The lock handshake needs the GUI event loop, so only the setting-to-bool
// decision is unit-tested; runDesktop carries the lock itself.

import (
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// singleInstanceEnabled reports whether the stored setting keeps one instance.
// The default is on; only recognizable off values disable it, so an unknown or
// future value never silently breaks the single-instance guarantee.
func singleInstanceEnabled(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "false", "0", "no", "off":
		return false
	default:
		return true
	}
}

// singleInstanceSetting reads the setting key with its documented default.
func singleInstanceSetting(settings *Settings) bool {
	if settings == nil {
		return true
	}
	return singleInstanceEnabled(settings.Get(contracts.SettingSingleInstance, "true"))
}
