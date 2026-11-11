package host

// Tests for the single-instance policy (A3 fixed behavior): the desktop process
// always keeps one instance and hands later launches to it. The
// ui.singleInstance row is no longer read — it was removed from Settings — so
// neither a stored opt-out from an older version nor any malformed value can
// disable the lock. RequestSingleInstanceLock/OnSecondInstance themselves need a
// GUI event loop, so the lock handshake is exercised manually (see runDesktop)
// and not here.

import (
	"testing"
)

// TestSingleInstanceEnabledAlwaysOn pins the fixed decision: every raw row value
// keeps single-instance mode.
func TestSingleInstanceEnabledAlwaysOn(t *testing.T) {
	for _, raw := range []string{
		"", "true", "TRUE", "1", "yes", "on", "bogus",
		"false", "FALSE", "0", "no", "off", "  false  ",
	} {
		if got := singleInstanceEnabled(raw); !got {
			t.Fatalf("singleInstanceEnabled(%q) = false, want true (fixed behavior)", raw)
		}
	}
}

// TestSingleInstanceSettingAlwaysOn covers the settings read path: the row is
// never consulted, so a stored opt-out (or a missing settings store) still keeps
// one instance.
func TestSingleInstanceSettingAlwaysOn(t *testing.T) {
	if !singleInstanceSetting(nil) {
		t.Fatal("single instance is disabled without a settings store")
	}
	settings, err := NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	if !singleInstanceSetting(settings) {
		t.Fatal("single instance is disabled by default")
	}
	if err := settings.Set("ui.singleInstance", "false"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !singleInstanceSetting(settings) {
		t.Fatal("a stored ui.singleInstance=false still disabled the lock")
	}
}
