//go:build windows

package app

import (
	"errors"
	"os"

	"golang.org/x/sys/windows/registry"
)

// autostartRunKey is the per-user Run registry key Windows starts at login.
const autostartRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// autostartValueName is the value name ZeoKeel owns inside the Run key.
const autostartValueName = "ZeoKeel"

// setAutostart writes or removes the per-user Run registry value (Windows).
// HKCU needs no elevation; a missing value on disable is success.
func setAutostart(enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, autostartRunKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer key.Close() //nolint:errcheck // best effort
	if !enabled {
		if err := key.DeleteValue(autostartValueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return key.SetStringValue(autostartValueName, `"`+exe+`"`)
}
