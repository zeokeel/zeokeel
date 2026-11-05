package globalhotkey

// TDD contract tests for the global-hotkey seam. Round 1: RED (functions return
// ErrNotImplemented). Round 2: GREEN once Manager/ParseHotkey and the Windows
// backend land; Linux/macOS keep the documented ErrUnsupported path.
//
// Run: go test -count=1 ./internal/globalhotkey

import (
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// fakeBackend records register/unregister/close calls and can inject OS-level
// failures (e.g. ErrOccupied). It also lets tests fire callbacks.
type fakeBackend struct {
	mu           sync.Mutex
	registered   map[int]Hotkey
	callbacks    map[int]func()
	unregistered []int
	closeCalls   int
	registerErr  error
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{registered: map[int]Hotkey{}, callbacks: map[int]func(){}}
}

func (f *fakeBackend) register(id int, h Hotkey, fn func()) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.registerErr != nil {
		return f.registerErr
	}
	f.registered[id] = h
	f.callbacks[id] = fn
	return nil
}

func (f *fakeBackend) unregister(id int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unregistered = append(f.unregistered, id)
	delete(f.registered, id)
	delete(f.callbacks, id)
	return nil
}

func (f *fakeBackend) close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	return nil
}

// trigger fires the callback registered under id (simulates a WM_HOTKEY).
func (f *fakeBackend) trigger(id int) {
	f.mu.Lock()
	fn := f.callbacks[id]
	f.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (f *fakeBackend) registeredCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.registered)
}

// TestParseHotkey covers the parser/canonicalisation contract.
func TestParseHotkey(t *testing.T) {
	valid := []struct {
		spec string
		want Hotkey
	}{
		{"Ctrl+Alt+Z", Hotkey{Mods: ModCtrl | ModAlt, Key: "z"}},
		{"ctrl+alt+z", Hotkey{Mods: ModCtrl | ModAlt, Key: "z"}},
		{"Shift+F12", Hotkey{Mods: ModShift, Key: "f12"}},
		{"Meta+Space", Hotkey{Mods: ModMeta, Key: "space"}},
		{"Ctrl+Shift+PageDown", Hotkey{Mods: ModCtrl | ModShift, Key: "pagedown"}},
		{"Alt+1", Hotkey{Mods: ModAlt, Key: "1"}},
		{"Ctrl+Enter", Hotkey{Mods: ModCtrl, Key: "enter"}},
	}
	for _, tc := range valid {
		got, err := ParseHotkey(tc.spec)
		if err != nil {
			t.Fatalf("ParseHotkey(%q): %v", tc.spec, err)
		}
		if got != tc.want {
			t.Fatalf("ParseHotkey(%q) = %+v, want %+v", tc.spec, got, tc.want)
		}
	}

	invalid := []string{
		"",
		"Z",        // no modifier: would steal a typing key
		"Ctrl+",    // no key
		"+Z",       // no modifier before key
		"Ctrl+Alt", // no key
		"Ctrl+Alt+Unknown",
		"Ctrl+Z+Q",              // only one key allowed
		"Ctrl++Z",               // empty segment
		"Ctrl+Ctrl+Z",           // duplicate modifier
		"Alt+F25",               // out of range
		"Shift+Ctrl+Alt+Meta+Z", // still valid: check the valid count below
	}
	for _, spec := range invalid {
		got, err := ParseHotkey(spec)
		if spec == "Shift+Ctrl+Alt+Meta+Z" {
			// Four modifiers are allowed; this entry documents that intent.
			if err != nil {
				t.Fatalf("ParseHotkey(%q): %v", spec, err)
			}
			if got.Mods != ModCtrl|ModAlt|ModShift|ModMeta || got.Key != "z" {
				t.Fatalf("ParseHotkey(%q) = %+v", spec, got)
			}
			continue
		}
		if !errors.Is(err, ErrInvalidHotkey) {
			t.Fatalf("ParseHotkey(%q) err = %v, want ErrInvalidHotkey", spec, err)
		}
	}
}

// TestHotkeyString covers canonical display and modifier ordering.
func TestHotkeyString(t *testing.T) {
	cases := []struct{ spec, want string }{
		{"ctrl+alt+z", "Ctrl+Alt+Z"},
		{"shift+meta+f12", "Shift+Meta+F12"},
		{"Meta+Ctrl+Space", "Ctrl+Meta+Space"},
		{"Alt+PageDown", "Alt+PageDown"},
	}
	for _, tc := range cases {
		h, err := ParseHotkey(tc.spec)
		if err != nil {
			t.Fatalf("ParseHotkey(%q): %v", tc.spec, err)
		}
		if got := h.String(); got != tc.want {
			t.Fatalf("(%q).String() = %q, want %q", tc.spec, got, tc.want)
		}
	}
}

// TestManagerRegisterUnregister covers the happy path and callback dispatch.
func TestManagerRegisterUnregister(t *testing.T) {
	fake := newFakeBackend()
	m := newManager(fake)

	var fired int
	h, err := ParseHotkey("Ctrl+Alt+Z")
	if err != nil {
		t.Fatalf("ParseHotkey: %v", err)
	}
	handle, err := m.Register(h, func() { fired++ })
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got := fake.registeredCount(); got != 1 {
		t.Fatalf("backend registrations = %d, want 1", got)
	}
	fake.trigger(int(handle))
	if fired != 1 {
		t.Fatalf("callback fired %d times, want 1", fired)
	}
	if err := m.Unregister(handle); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if got := len(fake.unregistered); got != 1 {
		t.Fatalf("backend unregistrations = %d, want 1", got)
	}
	if got := fake.registeredCount(); got != 0 {
		t.Fatalf("backend registrations after unregister = %d, want 0", got)
	}
}

// TestManagerRejectsDuplicate: the same shortcut cannot be registered twice.
func TestManagerRejectsDuplicate(t *testing.T) {
	fake := newFakeBackend()
	m := newManager(fake)
	h, _ := ParseHotkey("Ctrl+Alt+Z")
	if _, err := m.Register(h, func() {}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if _, err := m.Register(h, func() {}); !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("duplicate Register err = %v, want ErrAlreadyRegistered", err)
	}
	if got := fake.registeredCount(); got != 1 {
		t.Fatalf("backend registrations = %d, want 1", got)
	}
}

// TestManagerSurfacesOSOccupied: an OS-level conflict (shortcut owned by another
// app) maps to ErrOccupied and never leaves a half-registered entry.
func TestManagerSurfacesOSOccupied(t *testing.T) {
	fake := newFakeBackend()
	fake.registerErr = ErrOccupied
	m := newManager(fake)
	h, _ := ParseHotkey("Ctrl+Alt+Z")
	if _, err := m.Register(h, func() {}); !errors.Is(err, ErrOccupied) {
		t.Fatalf("Register err = %v, want ErrOccupied", err)
	}
	fake.registerErr = nil
	// The failed registration must not block a retry.
	if _, err := m.Register(h, func() {}); err != nil {
		t.Fatalf("retry Register: %v", err)
	}
}

// TestManagerRejectsNilCallback: a registration without a callback is a bug.
func TestManagerRejectsNilCallback(t *testing.T) {
	m := newManager(newFakeBackend())
	h, _ := ParseHotkey("Ctrl+Alt+Z")
	if _, err := m.Register(h, nil); !errors.Is(err, ErrInvalidHotkey) {
		t.Fatalf("Register(nil) err = %v, want ErrInvalidHotkey", err)
	}
}

// TestManagerUnregisterUnknown covers unknown handles.
func TestManagerUnregisterUnknown(t *testing.T) {
	m := newManager(newFakeBackend())
	if err := m.Unregister(Handle(42)); !errors.Is(err, ErrUnknownHandle) {
		t.Fatalf("Unregister(42) err = %v, want ErrUnknownHandle", err)
	}
}

// TestManagerClose: idempotent close, unregisters everything, rejects new
// registrations afterwards.
func TestManagerClose(t *testing.T) {
	fake := newFakeBackend()
	m := newManager(fake)
	h1, _ := ParseHotkey("Ctrl+Alt+Z")
	h2, _ := ParseHotkey("Shift+F12")
	if _, err := m.Register(h1, func() {}); err != nil {
		t.Fatalf("Register h1: %v", err)
	}
	if _, err := m.Register(h2, func() {}); err != nil {
		t.Fatalf("Register h2: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := fake.closeCalls; got != 1 {
		t.Fatalf("backend close calls = %d, want 1", got)
	}
	if got := fake.registeredCount(); got != 0 {
		t.Fatalf("backend registrations after Close = %d, want 0", got)
	}
	if _, err := m.Register(h1, func() {}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Register after Close err = %v, want ErrClosed", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if got := fake.closeCalls; got != 1 {
		t.Fatalf("backend close calls after second Close = %d, want 1", got)
	}
}

// TestNewPlatformSupport pins the documented platform matrix: Windows has a
// backend; Linux/macOS return ErrUnsupported without touching the OS.
func TestNewPlatformSupport(t *testing.T) {
	m, err := New()
	switch runtime.GOOS {
	case "windows":
		if err != nil {
			t.Fatalf("New() on windows: %v", err)
		}
		if m == nil {
			t.Fatal("New() on windows = nil manager")
		}
		if err := m.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	case "linux", "darwin":
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("New() on %s err = %v, want ErrUnsupported", runtime.GOOS, err)
		}
		if m != nil {
			t.Fatalf("New() on %s returned a manager with ErrUnsupported", runtime.GOOS)
		}
	default:
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("New() on %s err = %v, want ErrUnsupported", runtime.GOOS, err)
		}
	}
}

// TestInvalidHotkeyDisplay guards the fallback rendering used by logs.
func TestInvalidHotkeyDisplay(t *testing.T) {
	if got := (Hotkey{Mods: ModCtrl, Key: "z"}).String(); got != "Ctrl+Z" {
		t.Fatalf("String() = %q", got)
	}
	if got := (Hotkey{Key: "space"}).String(); !strings.Contains(got, "Space") {
		t.Fatalf("String() = %q", got)
	}
}
