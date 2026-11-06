//go:build linux

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetAutostartLinux covers the XDG autostart entry: enabling writes a
// desktop file that starts the current executable, disabling removes it, and a
// repeated disable stays quiet.
func TestSetAutostartLinux(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := setAutostart(true); err != nil {
		t.Fatalf("setAutostart(true): %v", err)
	}
	path := filepath.Join(dir, "autostart", autostartEntryName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read autostart entry: %v", err)
	}
	for _, want := range []string{"[Desktop Entry]", "Type=Application", "Name=ZeoKeel", "Exec=", "Terminal=false"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("autostart entry missing %q:\n%s", want, data)
		}
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("autostart entry mode = %v, %v; want 0600", info, err)
	}

	if err := setAutostart(false); err != nil {
		t.Fatalf("setAutostart(false): %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("autostart entry survived disable: %v", err)
	}
	if err := setAutostart(false); err != nil {
		t.Fatalf("idempotent disable: %v", err)
	}
}

// TestSyncAutostartFromSetting covers the app wiring: the persisted
// app.autostart value drives the XDG entry.
func TestSyncAutostartFromSetting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	a, st := newSettingsTestApp(t)
	path := filepath.Join(dir, "autostart", autostartEntryName)

	a.syncAutostartFromSetting() // default false: nothing written
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("autostart file created without the setting: %v", err)
	}
	if err := st.SetSetting("app.autostart", "true"); err != nil {
		t.Fatal(err)
	}
	a.syncAutostartFromSetting()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("autostart file missing after enabling: %v", err)
	}
	if err := st.SetSetting("app.autostart", "false"); err != nil {
		t.Fatal(err)
	}
	a.syncAutostartFromSetting()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("autostart file survived disabling: %v", err)
	}
}
