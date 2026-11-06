//go:build linux

package app

import (
	"os"
	"path/filepath"
)

// autostartEntryName is the XDG autostart desktop file name.
const autostartEntryName = "zeokeel.desktop"

// setAutostart writes or removes the XDG autostart entry for the current
// executable (Linux). The entry follows the freedesktop autostart spec and is
// only written when enabling; disabling removes it (a missing file is success).
func setAutostart(enabled bool) error {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir = filepath.Join(home, ".config")
	}
	dir = filepath.Join(dir, "autostart")
	path := filepath.Join(dir, autostartEntryName)
	if !enabled {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	content := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=ZeoKeel\n" +
		"Comment=ZeoKeel desktop AI agent\n" +
		"Exec=\"" + exe + "\"\n" +
		"Terminal=false\n" +
		"X-GNOME-Autostart-enabled=true\n"
	return os.WriteFile(path, []byte(content), 0o600)
}
