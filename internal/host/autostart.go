package host

// Linux XDG autostart: the appearance page's "launch at login" switch writes
// contracts.SettingAutostart and this file keeps the per-user desktop entry in
// sync. The entry is best effort: a failure is logged and reported to the
// caller but never blocks startup or shutdown.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// autostartEntryName is the XDG autostart file name below
// <configHome>/autostart/.
const autostartEntryName = "zeokeel.desktop"

// autostartConfigHome resolves the XDG config home: $XDG_CONFIG_HOME when set,
// ~/.config otherwise.
func autostartConfigHome() (string, error) {
	if configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); configHome != "" {
		return configHome, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".config"), nil
}

// autostartEntryPath is the XDG autostart desktop entry path.
func autostartEntryPath(configHome string) string {
	return filepath.Join(configHome, "autostart", autostartEntryName)
}

// syncAutostart makes the per-user autostart entry match the stored setting:
// <configHome>/autostart/zeokeel.desktop is written when the setting is exactly
// "true" and removed otherwise. A failure is logged as a warning (never fatal)
// and returned for tests/diagnostics. The whole call is a no-op when settings is
// nil.
func syncAutostart(settings *Settings, logger *Logger) error {
	path, err := autostartEntryTarget()
	if err != nil {
		logger.Printf("%s: autostart: %v", appName, err)
		return err
	}
	enabled := settings != nil && settings.Get(contracts.SettingAutostart, "false") == "true"
	if !enabled {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			wrapped := fmt.Errorf("remove autostart entry %q: %w", path, err)
			logger.Printf("%s: autostart: %v", appName, wrapped)
			return wrapped
		}
		return nil
	}

	executable, err := os.Executable()
	if err != nil {
		wrapped := fmt.Errorf("locate executable: %w", err)
		logger.Printf("%s: autostart: %v", appName, wrapped)
		return wrapped
	}
	entry := autostartEntry(executable, settingsDataDir(settings))
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		wrapped := fmt.Errorf("create autostart dir %q: %w", dir, err)
		logger.Printf("%s: autostart: %v", appName, wrapped)
		return wrapped
	}
	if err := os.WriteFile(path, []byte(entry), 0o644); err != nil {
		wrapped := fmt.Errorf("write autostart entry %q: %w", path, err)
		logger.Printf("%s: autostart: %v", appName, wrapped)
		return wrapped
	}
	// WriteFile honours the umask; the entry's mode is part of its contract.
	if err := os.Chmod(path, 0o644); err != nil {
		wrapped := fmt.Errorf("chmod autostart entry %q: %w", path, err)
		logger.Printf("%s: autostart: %v", appName, wrapped)
		return wrapped
	}
	return nil
}

// autostartEntryTarget resolves the desktop entry path (tests use the same
// $XDG_CONFIG_HOME injection as the real process).
func autostartEntryTarget() (string, error) {
	configHome, err := autostartConfigHome()
	if err != nil {
		return "", err
	}
	return autostartEntryPath(configHome), nil
}

// settingsDataDir returns the settings' parent directory (the product data
// directory passed to the login launch as --data-dir).
func settingsDataDir(settings *Settings) string {
	if settings == nil {
		return ""
	}
	path := settings.Path()
	if path == "" {
		return ""
	}
	return filepath.Dir(path)
}

// autostartEntry renders the .desktop file: a single Exec line (the executable
// and the data directory are desktop-entry quoted, so spaces/specials in either
// path cannot break the entry), and the GNOME autostart marker.
func autostartEntry(executable, dataDir string) string {
	var builder strings.Builder
	builder.WriteString("[Desktop Entry]\n")
	builder.WriteString("Type=Application\n")
	builder.WriteString("Name=ZeoKeel\n")
	builder.WriteString("Exec=")
	builder.WriteString(quoteDesktopExec(executable))
	if dataDir != "" {
		builder.WriteString(" --data-dir ")
		builder.WriteString(quoteDesktopExec(dataDir))
	}
	builder.WriteString("\nX-GNOME-Autostart-enabled=true\n")
	builder.WriteString("Terminal=false\n")
	return builder.String()
}

// quoteDesktopExec quotes one Exec argument per the Desktop Entry spec: wrap in
// double quotes and escape backslash, double quote, backtick and dollar.
func quoteDesktopExec(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"`", "\\`",
		`$`, `\$`,
	)
	return `"` + replacer.Replace(value) + `"`
}
