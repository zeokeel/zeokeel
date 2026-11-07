package host

// Tests for the global-hotkey wiring: accelerator normalization and
// registration through an injectable registrar/window (the real mygo
// GlobalShortcut needs a desktop session; the xvfb smoke covers it).

import (
	"errors"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeHotkeyWindow records the window calls made by the hotkey.
type fakeHotkeyWindow struct {
	shown, restored, focused int
}

func (f *fakeHotkeyWindow) Show()    { f.shown++ }
func (f *fakeHotkeyWindow) Restore() { f.restored++ }
func (f *fakeHotkeyWindow) Focus()   { f.focused++ }

// fakeRegistrar records registrations and returns an unregister callback.
type fakeRegistrar struct {
	registrations []hotkeyRegistration
	registerErr   error
	offCalls      int
}

type hotkeyRegistration struct {
	accelerator string
	fn          func()
}

func (f *fakeRegistrar) Register(accelerator string, fn func()) (func(), error) {
	f.registrations = append(f.registrations, hotkeyRegistration{accelerator: accelerator, fn: fn})
	if f.registerErr != nil {
		return nil, f.registerErr
	}
	return func() { f.offCalls++ }, nil
}

func hotkeySettings(t *testing.T, value string) *Settings {
	t.Helper()
	settings, err := NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	if value != "" {
		if err := settings.Set(contracts.SettingGlobalHotkey, value); err != nil {
			t.Fatalf("Set(globalHotkey): %v", err)
		}
	}
	return settings
}

func TestNormalizeHotkey(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "empty disables", raw: "", want: ""},
		{name: "spaces only disables", raw: "   ", want: ""},
		{name: "canonical combo", raw: "CmdOrCtrl+Shift+Space", want: "CmdOrCtrl+Shift+Space"},
		{name: "lower case normalizes", raw: " ctrl+shift+space ", want: "Ctrl+Shift+Space"},
		{name: "aliases normalize", raw: "commandorcontrol+option+return", want: "CmdOrCtrl+Alt+Enter"},
		{name: "order normalizes", raw: "shift+ctrl+k", want: "Ctrl+Shift+K"},
		{name: "super", raw: "Super+Space", want: "Super+Space"},
		{name: "function key", raw: "CmdOrCtrl+f12", want: "CmdOrCtrl+F12"},
		{name: "digit", raw: "Ctrl+0", want: "Ctrl+0"},
		{name: "escape alias", raw: "Ctrl+Esc", want: "Ctrl+Escape"},
		{name: "missing modifier", raw: "Space", wantErr: true},
		{name: "only modifier", raw: "CmdOrCtrl", wantErr: true},
		{name: "multiple keys", raw: "Ctrl+Shift+A+B", wantErr: true},
		{name: "unknown modifier", raw: "Hyper+Space", wantErr: true},
		{name: "unknown key", raw: "Ctrl+Banana", wantErr: true},
		{name: "f13 unsupported", raw: "Ctrl+F13", wantErr: true},
		{name: "empty token", raw: "Ctrl++Space", wantErr: true},
		{name: "garbage", raw: "not-a-hotkey", wantErr: true},
		{name: "duplicate modifier", raw: "Ctrl+Ctrl+Space", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeHotkey(test.raw)
			if test.wantErr {
				if err == nil {
					t.Fatalf("normalizeHotkey(%q) = %q, nil; want an error", test.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeHotkey(%q): %v", test.raw, err)
			}
			if got != test.want {
				t.Fatalf("normalizeHotkey(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestSetupGlobalHotkeyRegistersAndShowsWindow(t *testing.T) {
	window := &fakeHotkeyWindow{}
	registrar := &fakeRegistrar{}
	off, err := setupGlobalHotkey(window, hotkeySettings(t, " CmdOrCtrl+Shift+Space "), registrar)
	if err != nil {
		t.Fatalf("setupGlobalHotkey: %v", err)
	}
	if off == nil {
		t.Fatal("setupGlobalHotkey returned no unregister function")
	}
	if len(registrar.registrations) != 1 {
		t.Fatalf("registrations = %d, want 1", len(registrar.registrations))
	}
	registration := registrar.registrations[0]
	if registration.accelerator != "CmdOrCtrl+Shift+Space" {
		t.Fatalf("registered accelerator = %q", registration.accelerator)
	}
	registration.fn()
	if window.shown != 1 || window.restored != 1 || window.focused != 1 {
		t.Fatalf("window calls = show %d/restore %d/focus %d, want 1/1/1",
			window.shown, window.restored, window.focused)
	}
	off()
	if registrar.offCalls != 1 {
		t.Fatalf("unregister calls = %d, want 1", registrar.offCalls)
	}
}

func TestSetupGlobalHotkeyDisabledOrInvalid(t *testing.T) {
	t.Run("empty setting skips", func(t *testing.T) {
		registrar := &fakeRegistrar{}
		off, err := setupGlobalHotkey(&fakeHotkeyWindow{}, hotkeySettings(t, ""), registrar)
		if err != nil {
			t.Fatalf("setupGlobalHotkey(empty) error = %v, want nil", err)
		}
		if off != nil {
			t.Fatal("setupGlobalHotkey(empty) returned an unregister function, want nil")
		}
		if len(registrar.registrations) != 0 {
			t.Fatalf("registrations = %d, want 0", len(registrar.registrations))
		}
	})
	t.Run("invalid setting errors", func(t *testing.T) {
		registrar := &fakeRegistrar{}
		off, err := setupGlobalHotkey(&fakeHotkeyWindow{}, hotkeySettings(t, "nonsense"), registrar)
		if err == nil {
			t.Fatal("setupGlobalHotkey(invalid) = nil error")
		}
		if off != nil {
			t.Fatal("setupGlobalHotkey(invalid) returned an unregister function, want nil")
		}
		if len(registrar.registrations) != 0 {
			t.Fatalf("registrations = %d, want 0", len(registrar.registrations))
		}
	})
	t.Run("nil settings skips", func(t *testing.T) {
		registrar := &fakeRegistrar{}
		off, err := setupGlobalHotkey(&fakeHotkeyWindow{}, nil, registrar)
		if err != nil {
			t.Fatalf("setupGlobalHotkey(nil settings) error = %v, want nil", err)
		}
		if off != nil {
			t.Fatal("setupGlobalHotkey(nil settings) returned an unregister function, want nil")
		}
	})
}

func TestSetupGlobalHotkeyRegistrationFailureIsSafe(t *testing.T) {
	window := &fakeHotkeyWindow{}
	registrar := &fakeRegistrar{registerErr: errors.New("shortcut is taken")}
	off, err := setupGlobalHotkey(window, hotkeySettings(t, "Ctrl+Shift+K"), registrar)
	if err == nil {
		t.Fatal("setupGlobalHotkey(registration failure) = nil error")
	}
	if off != nil {
		t.Fatal("setupGlobalHotkey(registration failure) returned an unregister function, want nil")
	}
	if window.shown != 0 || window.focused != 0 {
		t.Fatal("the hotkey callback ran even though registration failed")
	}
}

func TestSetupGlobalHotkeyNeedsWindow(t *testing.T) {
	registrar := &fakeRegistrar{}
	if _, err := setupGlobalHotkey(nil, hotkeySettings(t, "Ctrl+Shift+K"), registrar); err == nil {
		t.Fatal("setupGlobalHotkey(nil window) = nil error")
	}
	if len(registrar.registrations) != 0 {
		t.Fatal("registered although there was no window")
	}
}
