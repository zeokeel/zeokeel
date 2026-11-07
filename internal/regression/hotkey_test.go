package regression

// Adversarial audit: the global hotkey (host half). The host package unit-tests
// the wiring; this file adds the regression-layer boundary/negative checks:
// accelerator normalization (duplicate modifiers, multiple keys, unknown keys,
// modifier-only, empty/alias/case/trim), registration failure with zero side
// effects, the callback's window calls and the shutdown unregister hook.
//
// hotkey.go's helpers are unexported; this test-only file reaches them through
// //go:linkname declarations with structurally identical types (the same
// technique used for the tray and the pending-update helpers). No production
// code is changed and the real mygo.GlobalShortcut is never touched.

import (
	"strings"
	"testing"
	_ "unsafe"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/host"
)

// hotkeyWindowAlias mirrors host.hotkeyWindow.
type hotkeyWindowAlias interface {
	Show()
	Restore()
	Focus()
}

// hotkeyRegistrarAlias mirrors host.hotkeyRegistrar.
type hotkeyRegistrarAlias interface {
	Register(accelerator string, fn func()) (off func(), err error)
}

// hostNormalizeHotkey is host.normalizeHotkey reached by linkname.
//
//go:linkname hostNormalizeHotkey github.com/zeokeel/zeokeel/internal/host.normalizeHotkey
func hostNormalizeHotkey(raw string) (string, error)

// hostSetupGlobalHotkey is host.setupGlobalHotkey reached by linkname.
//
//go:linkname hostSetupGlobalHotkey github.com/zeokeel/zeokeel/internal/host.setupGlobalHotkey
func hostSetupGlobalHotkey(window hotkeyWindowAlias, settings *host.Settings, registrar hotkeyRegistrarAlias) (func(), error)

// hotkeyFakeWindow records the window calls made by the hotkey callback.
type hotkeyFakeWindow struct {
	shown, restored, focused int
}

func (w *hotkeyFakeWindow) Show()    { w.shown++ }
func (w *hotkeyFakeWindow) Restore() { w.restored++ }
func (w *hotkeyFakeWindow) Focus()   { w.focused++ }

// hotkeyFakeRegistrar records registrations and the unregister calls.
type hotkeyFakeRegistrar struct {
	accelerator string
	fn          func()
	registerErr error
	registered  bool
	offHits     int
}

func (r *hotkeyFakeRegistrar) Register(accelerator string, fn func()) (func(), error) {
	r.accelerator = accelerator
	r.fn = fn
	if r.registerErr != nil {
		return nil, r.registerErr
	}
	r.registered = true
	return func() { r.offHits++ }, nil
}

// hotkeyTestSettings builds host settings with one globalHotkey row.
func hotkeyTestSettings(t *testing.T, value string) *host.Settings {
	t.Helper()
	settings, err := host.NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	if value != "" {
		if err := settings.Set(contracts.SettingGlobalHotkey, value); err != nil {
			t.Fatalf("Set(globalHotkey=%q): %v", value, err)
		}
	}
	return settings
}

// TestHotkeyNormalizationTable covers the accelerator grammar: aliases, case and
// whitespace folding, canonical modifier order, the F1-F12/letter/digit keys,
// and every rejected shape (duplicate modifiers, multiple keys, unknown
// tokens, modifier-only, empty tokens, overlong input).
func TestHotkeyNormalizationTable(t *testing.T) {
	accept := map[string]string{
		"":                               "",
		"   ":                            "",
		"CmdOrCtrl+Shift+Space":          "CmdOrCtrl+Shift+Space",
		" ctrl+shift+space ":             "Ctrl+Shift+Space",
		"commandorcontrol+option+return": "CmdOrCtrl+Alt+Enter",
		"shift+ctrl+k":                   "Ctrl+Shift+K",
		"Super+Space":                    "Super+Space",
		"meta+space":                     "Super+Space",
		"win+space":                      "Super+Space",
		"command+space":                  "Cmd+Space",
		"control+space":                  "Ctrl+Space",
		"alt+space":                      "Alt+Space",
		"CmdOrCtrl+f12":                  "CmdOrCtrl+F12",
		"Ctrl+0":                         "Ctrl+0",
		"Ctrl+Esc":                       "Ctrl+Escape",
		"Ctrl+Return":                    "Ctrl+Enter",
		"Ctrl+Del":                       "Ctrl+Delete",
		"Ctrl+Ins":                       "Ctrl+Insert",
		"Shift+PageUp":                   "Shift+PageUp",
		"Alt+Down":                       "Alt+Down",
		"  CMDORCTRL + A  ":              "CmdOrCtrl+A",
	}
	for raw, want := range accept {
		got, err := hostNormalizeHotkey(raw)
		if err != nil {
			t.Fatalf("normalizeHotkey(%q) error: %v", raw, err)
		}
		if got != want {
			t.Fatalf("normalizeHotkey(%q) = %q, want %q", raw, got, want)
		}
	}

	reject := []string{
		"Space",                           // no modifier
		"CmdOrCtrl",                       // modifier only
		"Ctrl+Shift",                      // two modifiers, no key
		"Ctrl+Shift+A+B",                  // multiple keys
		"Hyper+Space",                     // unknown modifier
		"Ctrl+Ctrl+Space",                 // duplicate modifier
		"Ctrl+control+Space",              // duplicate modifier via alias
		"Ctrl+F13",                        // F13 unsupported
		"Ctrl+Banana",                     // unknown key
		"Ctrl++Space",                     // empty modifier token
		"+Space",                          // empty leading token
		"Ctrl+",                           // missing key
		"not-a-hotkey",                    // garbage
		"Ctrl+😀",                          // unsupported single rune
		"Ctrl+" + strings.Repeat("A", 64), // overlong key
		strings.Repeat("Ctrl+", 20) + "A", // overlong accelerator
	}
	for _, raw := range reject {
		if got, err := hostNormalizeHotkey(raw); err == nil {
			t.Fatalf("normalizeHotkey(%q) = %q, want an error", raw, got)
		}
	}
}

// TestHotkeyRegistrationLifecycle covers the successful path: the normalized
// accelerator is registered, the callback shows/restores/focuses the window and
// the returned shutdown hook unregisters the shortcut.
func TestHotkeyRegistrationLifecycle(t *testing.T) {
	window := &hotkeyFakeWindow{}
	registrar := &hotkeyFakeRegistrar{}
	off, err := hostSetupGlobalHotkey(window, hotkeyTestSettings(t, " ctrl+shift+space "), registrar)
	if err != nil {
		t.Fatalf("setupGlobalHotkey: %v", err)
	}
	if off == nil {
		t.Fatal("setupGlobalHotkey returned no unregister hook")
	}
	if registrar.accelerator != "Ctrl+Shift+Space" {
		t.Fatalf("registered accelerator = %q, want Ctrl+Shift+Space", registrar.accelerator)
	}
	if !registrar.registered || registrar.fn == nil {
		t.Fatal("the registrar was not called with a callback")
	}
	registrar.fn()
	if window.shown != 1 || window.restored != 1 || window.focused != 1 {
		t.Fatalf("hotkey callback window calls = show %d/restore %d/focus %d, want 1/1/1",
			window.shown, window.restored, window.focused)
	}
	off()
	if registrar.offHits != 1 {
		t.Fatalf("shutdown unregister calls = %d, want 1", registrar.offHits)
	}

	// Empty setting (or nil settings) disables the hotkey: no registration, no off.
	disabledRegistrar := &hotkeyFakeRegistrar{}
	off, err = hostSetupGlobalHotkey(&hotkeyFakeWindow{}, hotkeyTestSettings(t, ""), disabledRegistrar)
	if err != nil || off != nil || disabledRegistrar.registered {
		t.Fatalf("empty setting: off=%v err=%v registered=%v, want nil/nil/false",
			off != nil, err, disabledRegistrar.registered)
	}
	off, err = hostSetupGlobalHotkey(&hotkeyFakeWindow{}, nil, disabledRegistrar)
	if err != nil || off != nil || disabledRegistrar.registered {
		t.Fatalf("nil settings: off=%v err=%v registered=%v, want nil/nil/false",
			off != nil, err, disabledRegistrar.registered)
	}
}

// TestHotkeyFailuresHaveNoSideEffects covers invalid settings and registration
// failures: a readable error, no off hook, no window calls and no registration.
func TestHotkeyFailuresHaveNoSideEffects(t *testing.T) {
	// Invalid stored accelerator: rejected before the registrar is called.
	invalidRegistrar := &hotkeyFakeRegistrar{}
	if _, err := hostSetupGlobalHotkey(&hotkeyFakeWindow{}, hotkeyTestSettings(t, "Ctrl+Banana"), invalidRegistrar); err == nil ||
		!strings.Contains(err.Error(), "global hotkey") {
		t.Fatalf("invalid hotkey error = %v, want a wrapped validation error", err)
	}
	if invalidRegistrar.registered || invalidRegistrar.fn != nil {
		t.Fatal("an invalid hotkey still reached the registrar")
	}

	// Registration failure (shortcut owned by the desktop): readable wrapped
	// error, no off hook, zero window calls.
	window := &hotkeyFakeWindow{}
	failingRegistrar := &hotkeyFakeRegistrar{registerErr: errHotkeyOwned}
	off, err := hostSetupGlobalHotkey(window, hotkeyTestSettings(t, "Ctrl+Shift+Space"), failingRegistrar)
	if err == nil || !strings.Contains(err.Error(), "register global hotkey") || off != nil {
		t.Fatalf("registration failure = (off set: %v, err %v), want a wrapped register error and nil off", off != nil, err)
	}
	if window.shown+window.restored+window.focused != 0 {
		t.Fatalf("registration failure touched the window: %+v", window)
	}

	// Missing collaborators are rejected before doing any work.
	if _, err := hostSetupGlobalHotkey(nil, hotkeyTestSettings(t, "Ctrl+Space"), &hotkeyFakeRegistrar{}); err == nil ||
		!strings.Contains(err.Error(), "window") {
		t.Fatalf("nil window error = %v", err)
	}
	if _, err := hostSetupGlobalHotkey(&hotkeyFakeWindow{}, hotkeyTestSettings(t, "Ctrl+Space"), nil); err == nil ||
		!strings.Contains(err.Error(), "registrar") {
		t.Fatalf("nil registrar error = %v", err)
	}
}

// errHotkeyOwned is the scripted "shortcut already taken" registration error.
var errHotkeyOwned = &hotkeyTestError{"shortcut already registered by the desktop"}

// hotkeyTestError is a minimal error value for the registrar failure probe.
type hotkeyTestError struct{ text string }

func (e *hotkeyTestError) Error() string { return e.text }
