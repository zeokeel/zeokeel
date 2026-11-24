package host

// The "launch at login" switch (AUDIT-2026-11-24 A-7): the settings row was
// written by the settings screen and read by nothing, so the XDG entry was never
// created or removed. syncAutostart now runs at startup and whenever the row
// changes (Settings.WatchAutostart).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestSettingsChangeSyncsAutostartEntry drives the switch through the settings
// store, the way the settings screen does.
func TestSettingsChangeSyncsAutostartEntry(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	settings, err := NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	logger := NewLogger(t.TempDir())
	t.Cleanup(func() { _ = logger.Close() })
	settings.WatchAutostart(func() {
		if err := syncAutostart(settings, logger); err != nil {
			t.Errorf("syncAutostart after Set: %v", err)
		}
	})

	entry := filepath.Join(configHome, "autostart", autostartEntryName)

	if err := settings.Set(contracts.SettingAutostart, "true"); err != nil {
		t.Fatalf("Set(true): %v", err)
	}
	data, err := os.ReadFile(entry)
	if err != nil {
		t.Fatalf("autostart entry was not written: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("autostart entry is empty")
	}

	if err := settings.Set(contracts.SettingAutostart, "false"); err != nil {
		t.Fatalf("Set(false): %v", err)
	}
	if _, statErr := os.Stat(entry); !os.IsNotExist(statErr) {
		t.Fatalf("autostart entry still exists after turning the switch off: %v", statErr)
	}

	// Setting an unrelated row must not touch the entry.
	if err := settings.Set("ui.language", "en"); err != nil {
		t.Fatalf("Set(unrelated): %v", err)
	}
	if _, statErr := os.Stat(entry); !os.IsNotExist(statErr) {
		t.Fatalf("an unrelated setting recreated the autostart entry: %v", statErr)
	}
}
