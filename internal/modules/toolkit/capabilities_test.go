package toolkit

// Tests for the capability settings the toolkit resolves at activation: the
// shell tool's timeout (toolkit.shellTimeoutSeconds, 5–600s) and output cap
// (toolkit.shellMaxOutputKiB, 0 = the tool default or 1–4096 KiB) override the
// module Config's shellTimeoutMs/maxOutputBytes, and invalid rows keep the
// config values.

import (
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestShellTimeoutReadsCapabilitySetting covers the shell timeout resolution:
// a valid 5–600s row wins over Config.ShellTimeoutMs, while an absent, empty,
// malformed or out-of-range row keeps the config value (0 = the 30s default).
func TestShellTimeoutReadsCapabilitySetting(t *testing.T) {
	cases := []struct {
		name     string
		cfg      Config
		row      string
		settings bool
		want     time.Duration
	}{
		{name: "config 45s", cfg: Config{ShellTimeoutMs: 45_000}, want: 45 * time.Second},
		{name: "config default 30s", cfg: Config{}, want: 30 * time.Second},
		{name: "setting wins", cfg: Config{ShellTimeoutMs: 45_000},
			row: "120", settings: true, want: 120 * time.Second},
		{name: "lower bound", cfg: Config{ShellTimeoutMs: 45_000},
			row: "5", settings: true, want: 5 * time.Second},
		{name: "upper bound", cfg: Config{ShellTimeoutMs: 45_000},
			row: "600", settings: true, want: 600 * time.Second},
		{name: "below range keeps config", cfg: Config{ShellTimeoutMs: 45_000},
			row: "4", settings: true, want: 45 * time.Second},
		{name: "above range keeps config", cfg: Config{ShellTimeoutMs: 45_000},
			row: "601", settings: true, want: 45 * time.Second},
		{name: "malformed keeps config", cfg: Config{ShellTimeoutMs: 45_000},
			row: "soon", settings: true, want: 45 * time.Second},
		{name: "empty keeps config", cfg: Config{ShellTimeoutMs: 45_000},
			row: "", settings: true, want: 45 * time.Second},
		{name: "empty keeps default", cfg: Config{},
			row: "", settings: true, want: 30 * time.Second},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			settings := newMutableSettings(map[string]string{
				contracts.SettingToolkitShellTimeoutSeconds: test.row,
			})
			got := shellTimeout(test.cfg, settings, test.settings)
			if got != test.want {
				t.Fatalf("shellTimeout(%+v, row %q, settings %v) = %v, want %v",
					test.cfg, test.row, test.settings, got, test.want)
			}
		})
	}
}

// TestCapabilitiesShellOutputBytesReadSetting covers the shell output cap: an
// explicit 0 row selects the rig/tool default, a 1–4096 KiB row becomes bytes,
// and an absent/invalid row keeps Config.MaxOutputBytes untouched.
func TestCapabilitiesShellOutputBytesReadSetting(t *testing.T) {
	cases := []struct {
		name     string
		cfg      Config
		row      string
		settings bool
		want     int
	}{
		{name: "config kept without settings", cfg: Config{MaxOutputBytes: 8192}, want: 8192},
		{name: "config default", cfg: Config{}, want: 0},
		{name: "explicit default", cfg: Config{MaxOutputBytes: 8192},
			row: "0", settings: true, want: 0},
		{name: "kib overrides config", cfg: Config{MaxOutputBytes: 8192},
			row: "256", settings: true, want: 256 * 1024},
		{name: "lower bound", cfg: Config{MaxOutputBytes: 8192},
			row: "1", settings: true, want: 1024},
		{name: "upper bound", cfg: Config{MaxOutputBytes: 8192},
			row: "4096", settings: true, want: 4096 * 1024},
		{name: "above range keeps config", cfg: Config{MaxOutputBytes: 8192},
			row: "4097", settings: true, want: 8192},
		{name: "negative keeps config", cfg: Config{MaxOutputBytes: 8192},
			row: "-1", settings: true, want: 8192},
		{name: "malformed keeps config", cfg: Config{MaxOutputBytes: 8192},
			row: "lots", settings: true, want: 8192},
		{name: "empty keeps config", cfg: Config{MaxOutputBytes: 8192},
			row: "", settings: true, want: 8192},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			settings := newMutableSettings(map[string]string{
				contracts.SettingToolkitShellMaxOutputKiB: test.row,
			})
			got := shellMaxOutputBytes(test.cfg, settings, test.settings)
			if got != test.want {
				t.Fatalf("shellMaxOutputBytes(%+v, row %q, settings %v) = %d, want %d",
					test.cfg, test.row, test.settings, got, test.want)
			}
		})
	}
}
