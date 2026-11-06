package globalhotkey

// TDD scaffold round 1: public surface + behaviour contract. Functions return
// ErrNotImplemented until the platform backends land; the tests in
// globalhotkey_test.go are RED until then.

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Sentinel errors. Callers classify with errors.Is.
var (
	// ErrNotImplemented reports that a seam is part of the TDD scaffold.
	ErrNotImplemented = errors.New("globalhotkey: not implemented yet")
	// ErrInvalidHotkey reports an unparseable/unsafe shortcut spec.
	ErrInvalidHotkey = errors.New("globalhotkey: invalid hotkey")
	// ErrUnsupported reports that the platform has no global-hotkey backend.
	ErrUnsupported = errors.New("globalhotkey: global hotkeys are not supported on this platform")
	// ErrOccupied reports that another application already owns the shortcut.
	ErrOccupied = errors.New("globalhotkey: hotkey already registered by another application")
	// ErrAlreadyRegistered reports a duplicate registration in this process.
	ErrAlreadyRegistered = errors.New("globalhotkey: hotkey already registered")
	// ErrUnknownHandle reports an unregister for a handle that is not registered.
	ErrUnknownHandle = errors.New("globalhotkey: unknown hotkey handle")
	// ErrClosed reports a registration after Manager.Close.
	ErrClosed = errors.New("globalhotkey: manager is closed")
)

// Modifier is a bit set of held modifier keys.
type Modifier uint32

const (
	// ModCtrl is Ctrl (Windows/Linux) or Control.
	ModCtrl Modifier = 1 << iota
	// ModAlt is Alt (Windows/Linux) or Option (macOS).
	ModAlt
	// ModShift is Shift.
	ModShift
	// ModMeta is Win (Windows), Super (Linux) or Command (macOS).
	ModMeta
)

// Hotkey is one system-wide shortcut.
type Hotkey struct {
	Mods Modifier
	// Key is the canonical key name: "a".."z", "0".."9", "f1".."f24", or a
	// named key such as "space", "tab", "enter", "escape", "up", "delete".
	Key string
}

// Handle identifies one registered hotkey (manager-local id).
type Handle int

// String renders the canonical form, e.g. "Ctrl+Alt+Z".
func (h Hotkey) String() string {
	var parts []string
	if h.Mods&ModCtrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if h.Mods&ModAlt != 0 {
		parts = append(parts, "Alt")
	}
	if h.Mods&ModShift != 0 {
		parts = append(parts, "Shift")
	}
	if h.Mods&ModMeta != 0 {
		parts = append(parts, "Meta")
	}
	parts = append(parts, canonicalKeyDisplay(h.Key))
	return strings.Join(parts, "+")
}

// backend is the platform seam: it owns the OS-level registration and the
// callback dispatch. Manager is platform-independent and tested against fakes.
type backend interface {
	register(id int, h Hotkey, fn func()) error
	unregister(id int) error
	close() error
}

// Manager tracks registered hotkeys on top of one platform backend.
type Manager struct {
	mu      sync.Mutex
	b       backend
	next    int
	bySpec  map[string]Handle
	entries map[Handle]hotkeyEntry
	closed  bool
}

type hotkeyEntry struct {
	hotkey Hotkey
	// fn is kept for debugging/ownership; the backend owns dispatching.
	fn func()
}

// New returns the platform's global-hotkey manager.
func New() (*Manager, error) {
	b, err := platformBackend()
	if err != nil {
		return nil, err
	}
	return newManager(b), nil
}

// newManager wraps a backend (tests inject a fake).
func newManager(b backend) *Manager {
	return &Manager{
		b:       b,
		bySpec:  make(map[string]Handle),
		entries: make(map[Handle]hotkeyEntry),
	}
}

// Register registers h and returns its handle. The callback runs on the
// backend's dispatch goroutine; implementations must keep it short and must not
// call back into Manager synchronously.
func (m *Manager) Register(h Hotkey, fn func()) (Handle, error) {
	if fn == nil {
		return 0, fmt.Errorf("%w: nil callback", ErrInvalidHotkey)
	}
	if err := validateHotkey(h); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, ErrClosed
	}
	spec := h.String()
	if _, exists := m.bySpec[spec]; exists {
		return 0, fmt.Errorf("%w: %s", ErrAlreadyRegistered, spec)
	}
	m.next++
	id := m.next
	if err := m.b.register(id, h, fn); err != nil {
		return 0, err
	}
	handle := Handle(id)
	m.bySpec[spec] = handle
	m.entries[handle] = hotkeyEntry{hotkey: h, fn: fn}
	return handle, nil
}

// Unregister removes one hotkey. Unknown handles are ErrUnknownHandle.
func (m *Manager) Unregister(handle Handle) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	entry, ok := m.entries[handle]
	if !ok {
		return fmt.Errorf("%w: %d", ErrUnknownHandle, int(handle))
	}
	delete(m.entries, handle)
	delete(m.bySpec, entry.hotkey.String())
	if err := m.b.unregister(int(handle)); err != nil {
		return fmt.Errorf("globalhotkey: unregister %s: %w", entry.hotkey, err)
	}
	return nil
}

// Close unregisters every hotkey and releases the backend. Idempotent.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	handles := make([]Handle, 0, len(m.entries))
	for handle := range m.entries {
		handles = append(handles, handle)
	}
	m.entries = make(map[Handle]hotkeyEntry)
	m.bySpec = make(map[string]Handle)
	m.mu.Unlock()

	for _, handle := range handles {
		_ = m.b.unregister(int(handle)) // best effort; the backend is closing
	}
	return m.b.close()
}

// ParseHotkey parses a spec such as "Ctrl+Alt+Z" or "shift+meta+f12". At least
// one modifier is required; the key must be a single letter/digit, F1-F24 or a
// named key (space, tab, enter, escape, backspace, arrows, home/end, pageup/
// pagedown, insert, delete).
func ParseHotkey(spec string) (Hotkey, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return Hotkey{}, fmt.Errorf("%w: empty hotkey", ErrInvalidHotkey)
	}
	parts := strings.Split(trimmed, "+")
	if len(parts) < 2 {
		return Hotkey{}, fmt.Errorf("%w: %q needs at least one modifier and a key", ErrInvalidHotkey, spec)
	}
	var mods Modifier
	seen := map[Modifier]bool{}
	for _, raw := range parts[:len(parts)-1] {
		mod, ok := parseModifier(raw)
		if !ok {
			return Hotkey{}, fmt.Errorf("%w: unknown modifier %q", ErrInvalidHotkey, raw)
		}
		if seen[mod] {
			return Hotkey{}, fmt.Errorf("%w: duplicate modifier %q", ErrInvalidHotkey, raw)
		}
		seen[mod] = true
		mods |= mod
	}
	key, ok := canonicalKey(parts[len(parts)-1])
	if !ok {
		return Hotkey{}, fmt.Errorf("%w: unknown key %q", ErrInvalidHotkey, parts[len(parts)-1])
	}
	return Hotkey{Mods: mods, Key: key}, nil
}

// parseModifier maps a spec token to a Modifier bit.
func parseModifier(raw string) (Modifier, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "ctrl", "control":
		return ModCtrl, true
	case "alt", "option", "opt":
		return ModAlt, true
	case "shift":
		return ModShift, true
	case "meta", "win", "super", "cmd", "command":
		return ModMeta, true
	default:
		return 0, false
	}
}

// canonicalKey normalizes a key token to the Hotkey.Key vocabulary.
func canonicalKey(raw string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	if key == "" {
		return "", false
	}
	if len(key) == 1 {
		if key[0] >= 'a' && key[0] <= 'z' || key[0] >= '0' && key[0] <= '9' {
			return key, true
		}
		return "", false
	}
	switch key {
	case "space", "tab", "enter", "escape", "backspace",
		"up", "down", "left", "right",
		"home", "end", "pageup", "pagedown", "insert", "delete":
		return key, true
	}
	if strings.HasPrefix(key, "f") {
		n := 0
		for _, r := range key[1:] {
			if r < '0' || r > '9' {
				return "", false
			}
			n = n*10 + int(r-'0')
		}
		if n >= 1 && n <= 24 {
			return key, true
		}
	}
	return "", false
}

// validateHotkey rejects Hotkey values built by hand instead of ParseHotkey.
func validateHotkey(h Hotkey) error {
	if h.Mods == 0 || h.Mods&^(ModCtrl|ModAlt|ModShift|ModMeta) != 0 {
		return fmt.Errorf("%w: invalid modifier set %d", ErrInvalidHotkey, h.Mods)
	}
	key, ok := canonicalKey(h.Key)
	if !ok {
		return fmt.Errorf("%w: unknown key %q", ErrInvalidHotkey, h.Key)
	}
	if key != h.Key {
		return fmt.Errorf("%w: key %q is not canonical", ErrInvalidHotkey, h.Key)
	}
	return nil
}

// canonicalKeyDisplay renders a key for Hotkey.String.
func canonicalKeyDisplay(key string) string {
	switch len(key) {
	case 0:
		return "?"
	case 1:
		return strings.ToUpper(key)
	}
	switch key {
	case "f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9", "f10",
		"f11", "f12", "f13", "f14", "f15", "f16", "f17", "f18", "f19", "f20",
		"f21", "f22", "f23", "f24":
		return strings.ToUpper(key)
	case "space":
		return "Space"
	case "tab":
		return "Tab"
	case "enter":
		return "Enter"
	case "escape":
		return "Escape"
	case "backspace":
		return "Backspace"
	case "up":
		return "Up"
	case "down":
		return "Down"
	case "left":
		return "Left"
	case "right":
		return "Right"
	case "home":
		return "Home"
	case "end":
		return "End"
	case "pageup":
		return "PageUp"
	case "pagedown":
		return "PageDown"
	case "insert":
		return "Insert"
	case "delete":
		return "Delete"
	default:
		return key
	}
}
