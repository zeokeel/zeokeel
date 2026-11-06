//go:build darwin

package app

import (
	"os"
	"path/filepath"
)

// autostartPlistName is the LaunchAgent plist file name.
const autostartPlistName = "local.zeokeel.plist"

// setAutostart writes or removes a per-user LaunchAgent (macOS) that starts
// ZeoKeel at login. The plist is only written when enabling; disabling removes
// the file (a missing file is success) so launchd stops loading it next login.
func setAutostart(enabled bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, "Library", "LaunchAgents")
	path := filepath.Join(dir, autostartPlistName)
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	content := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>local.zeokeel</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + exe + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`
	return os.WriteFile(path, []byte(content), 0o600)
}
