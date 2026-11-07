package host

// Tests for the Linux XDG autostart entry: file contents/quoting, idempotent
// enable, disable, the XDG_CONFIG_HOME override and the failure path. The host
// never blocks startup on these errors (runDesktop/persistPreferences log them).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// autostartSettings builds settings whose data directory is dataDir and stores
// the autostart value ("" leaves the key unset).
func autostartSettings(t *testing.T, dataDir, value string) *Settings {
	t.Helper()
	settings, err := NewSettings(dataDir)
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	if value != "" {
		if err := settings.Set(contracts.SettingAutostart, value); err != nil {
			t.Fatalf("Set(%s): %v", contracts.SettingAutostart, err)
		}
	}
	return settings
}

func TestAutostartConfigHomeHonoursXDG(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	got, err := autostartConfigHome()
	if err != nil {
		t.Fatalf("autostartConfigHome: %v", err)
	}
	if got != configHome {
		t.Fatalf("config home = %q, want $XDG_CONFIG_HOME %q", got, configHome)
	}
}

func TestAutostartConfigHomeFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", home)
	got, err := autostartConfigHome()
	if err != nil {
		t.Fatalf("autostartConfigHome: %v", err)
	}
	if got != filepath.Join(home, ".config") {
		t.Fatalf("config home = %q, want %q", got, filepath.Join(home, ".config"))
	}
}

func TestSyncAutostartEnableWritesEntry(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	dataDir := filepath.Join(t.TempDir(), "data dir with spaces")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	settings := autostartSettings(t, dataDir, "true")

	if err := syncAutostart(settings, nil); err != nil {
		t.Fatalf("syncAutostart(enable): %v", err)
	}
	path := filepath.Join(configHome, "autostart", "zeokeel.desktop")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read autostart entry: %v", err)
	}
	content := string(body)
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	for _, want := range []string{
		"[Desktop Entry]",
		"Type=Application",
		"Name=ZeoKeel",
		"Exec=\"",
		executable,
		"--data-dir \"",
		dataDir,
		"X-GNOME-Autostart-enabled=true",
		"Terminal=false",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("autostart entry = %q, want it to contain %q", content, want)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat entry: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("entry mode = %o, want 0644", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat autostart dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o755 {
		t.Fatalf("autostart dir mode = %o, want 0755", dirInfo.Mode().Perm())
	}
}

func TestSyncAutostartEnableIsIdempotent(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	dataDir := t.TempDir()
	settings := autostartSettings(t, dataDir, "true")
	if err := syncAutostart(settings, nil); err != nil {
		t.Fatalf("first syncAutostart: %v", err)
	}
	path := filepath.Join(configHome, "autostart", "zeokeel.desktop")
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatalf("tamper entry: %v", err)
	}
	if err := syncAutostart(settings, nil); err != nil {
		t.Fatalf("second syncAutostart: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read entry: %v", err)
	}
	if !strings.Contains(string(body), "[Desktop Entry]") {
		t.Fatalf("second sync did not overwrite the stale entry: %q", body)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir autostart: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("autostart dir = %d entries, want only zeokeel.desktop", len(entries))
	}
}

func TestSyncAutostartDisableRemovesEntry(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	dataDir := t.TempDir()
	settings := autostartSettings(t, dataDir, "true")
	if err := syncAutostart(settings, nil); err != nil {
		t.Fatalf("syncAutostart(enable): %v", err)
	}
	path := filepath.Join(configHome, "autostart", "zeokeel.desktop")
	if err := settings.Set(contracts.SettingAutostart, "false"); err != nil {
		t.Fatalf("Set(disable): %v", err)
	}
	if err := syncAutostart(settings, nil); err != nil {
		t.Fatalf("syncAutostart(disable): %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("autostart entry still exists after disable: %v", err)
	}
}

func TestSyncAutostartDisableMissingIsOK(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	settings := autostartSettings(t, t.TempDir(), "false")
	if err := syncAutostart(settings, nil); err != nil {
		t.Fatalf("syncAutostart(disable, no entry) = %v, want nil", err)
	}
}

func TestSyncAutostartRejectsUntrueValue(t *testing.T) {
	// The setting only enables autostart when it is exactly "true"; any other
	// stored value means the entry must not exist.
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	path := filepath.Join(configHome, "autostart", "zeokeel.desktop")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir autostart dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed entry: %v", err)
	}
	settings := autostartSettings(t, t.TempDir(), "1")
	if err := syncAutostart(settings, nil); err != nil {
		t.Fatalf("syncAutostart(1): %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("entry stayed for a non-true value: %v", err)
	}
}

func TestSyncAutostartWriteFailureReturnsError(t *testing.T) {
	// XDG_CONFIG_HOME points at a regular file, so creating autostart/ below it
	// must fail; the host logs the error instead of blocking startup.
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", blocked)
	settings := autostartSettings(t, t.TempDir(), "true")
	if err := syncAutostart(settings, nil); err == nil {
		t.Fatal("syncAutostart with an unwritable config home returned nil, want an error")
	}
}
