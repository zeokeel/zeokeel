package host

// Tests for the single-instance policy: the pure setting-to-bool decision table.
// RequestSingleInstanceLock/OnSecondInstance themselves need a GUI event loop, so
// the lock handshake is exercised manually (see runDesktop) and not here.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestSingleInstanceEnabled(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "default empty", raw: "", want: true},
		{name: "true", raw: "true", want: true},
		{name: "upper true", raw: "TRUE", want: true},
		{name: "one", raw: "1", want: true},
		{name: "yes", raw: "yes", want: true},
		{name: "on", raw: "on", want: true},
		{name: "false", raw: "false", want: false},
		{name: "upper false", raw: "FALSE", want: false},
		{name: "zero", raw: "0", want: false},
		{name: "no", raw: "no", want: false},
		{name: "off", raw: "off", want: false},
		{name: "padded false", raw: "  false  ", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := singleInstanceEnabled(test.raw); got != test.want {
				t.Fatalf("singleInstanceEnabled(%q) = %v, want %v", test.raw, got, test.want)
			}
		})
	}
}

// TestSingleInstanceSettingDefault pins the setting key and its default: the
// process runs single-instance unless the user explicitly opted out.
func TestSingleInstanceSettingDefault(t *testing.T) {
	settings, err := NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	if !singleInstanceEnabled(settings.Get(contracts.SettingSingleInstance, "true")) {
		t.Fatal("single instance is disabled by default")
	}
	if err := settings.Set(contracts.SettingSingleInstance, "false"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if singleInstanceEnabled(settings.Get(contracts.SettingSingleInstance, "true")) {
		t.Fatal("single instance stayed enabled after the setting was turned off")
	}
}
