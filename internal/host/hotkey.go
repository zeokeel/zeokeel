package host

// Global hotkey: the appearance page stores an Electron-style accelerator in
// contracts.SettingGlobalHotkey; at desktop startup the host registers it as a
// system-wide shortcut that shows/restores and focuses the main window. The
// registration is best effort: an invalid value or a shortcut the desktop
// already owns is logged and skipped, never fatal.

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// maxHotkeyRunes bounds a stored accelerator (defensive).
const maxHotkeyRunes = 64

// hotkeyModifierOrder is the canonical modifier order in a normalized string.
var hotkeyModifierOrder = []string{"CmdOrCtrl", "Cmd", "Ctrl", "Alt", "Shift", "Super"}

// hotkeyRegistrar registers a global accelerator and returns its unregister
// function. The production implementation wraps mygo.GlobalShortcut; tests
// inject a fake.
type hotkeyRegistrar interface {
	Register(accelerator string, fn func()) (off func(), err error)
}

// mygoHotkeyRegistrar is the production registrar: mygo's Register has no
// unregister handle, so the returned off function calls Unregister(acc).
// (The process exit would release it anyway; the explicit unregister keeps a
// module teardown symmetric.)
type mygoHotkeyRegistrar struct{}

var _ hotkeyRegistrar = mygoHotkeyRegistrar{}

func (mygoHotkeyRegistrar) Register(accelerator string, fn func()) (func(), error) {
	if err := mygo.GlobalShortcut.Register(accelerator, fn); err != nil {
		return nil, err
	}
	return func() { mygo.GlobalShortcut.Unregister(accelerator) }, nil
}

// hotkeyWindow is the slice of *mygo.Window the hotkey drives; fakeable.
type hotkeyWindow interface {
	Show()
	Restore()
	Focus()
}

// setupGlobalHotkey validates the stored accelerator and registers it. An empty
// setting (or nil settings) disables the hotkey and returns no unregister func.
// Invalid values and registration failures return a readable error and register
// nothing; the caller logs the warning and continues startup.
func setupGlobalHotkey(window hotkeyWindow, settings *Settings, registrar hotkeyRegistrar) (func(), error) {
	if window == nil {
		return nil, errors.New("global hotkey needs a window")
	}
	if registrar == nil {
		return nil, errors.New("global hotkey has no registrar")
	}
	raw := ""
	if settings != nil {
		raw = settings.Get(contracts.SettingGlobalHotkey, "")
	}
	accelerator, err := normalizeHotkey(raw)
	if err != nil {
		return nil, fmt.Errorf("global hotkey: %w", err)
	}
	if accelerator == "" {
		return nil, nil
	}
	off, err := registrar.Register(accelerator, func() {
		window.Show()
		window.Restore()
		window.Focus()
	})
	if err != nil {
		return nil, fmt.Errorf("register global hotkey %q: %w", accelerator, err)
	}
	return off, nil
}

// normalizeHotkey trims and canonicalizes a stored accelerator: at least one
// modifier (CmdOrCtrl/Cmd/Ctrl/Alt/Shift/Super) plus one key (A-Z, 0-9, F1-F12
// or a named key such as Space/Enter/Escape). An empty value means disabled.
func normalizeHotkey(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if len([]rune(trimmed)) > maxHotkeyRunes {
		return "", fmt.Errorf("hotkey %q is too long (max %d characters)", trimmed, maxHotkeyRunes)
	}
	parts := strings.Split(trimmed, "+")
	if len(parts) < 2 {
		return "", fmt.Errorf("hotkey %q needs at least one modifier and one key", trimmed)
	}
	modifiers := make([]string, 0, len(parts)-1)
	seen := make(map[string]bool, len(parts)-1)
	for _, part := range parts[:len(parts)-1] {
		modifier, ok := canonicalHotkeyModifier(part)
		if !ok {
			return "", fmt.Errorf("hotkey %q has an unknown modifier %q", trimmed, strings.TrimSpace(part))
		}
		if seen[modifier] {
			return "", fmt.Errorf("hotkey %q repeats modifier %q", trimmed, modifier)
		}
		seen[modifier] = true
		modifiers = append(modifiers, modifier)
	}
	key, err := canonicalHotkeyKey(parts[len(parts)-1])
	if err != nil {
		return "", fmt.Errorf("hotkey %q: %w", trimmed, err)
	}
	sort.SliceStable(modifiers, func(left, right int) bool {
		return hotkeyModifierRank(modifiers[left]) < hotkeyModifierRank(modifiers[right])
	})
	return strings.Join(append(modifiers, key), "+"), nil
}

// hotkeyModifierRank is the canonical modifier order in the normalized string.
func hotkeyModifierRank(modifier string) int {
	for index, candidate := range hotkeyModifierOrder {
		if modifier == candidate {
			return index
		}
	}
	return len(hotkeyModifierOrder)
}

// canonicalHotkeyModifier maps one modifier token to its canonical spelling.
func canonicalHotkeyModifier(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "cmdorctrl", "commandorcontrol":
		return "CmdOrCtrl", true
	case "cmd", "command":
		return "Cmd", true
	case "ctrl", "control":
		return "Ctrl", true
	case "alt", "option":
		return "Alt", true
	case "shift":
		return "Shift", true
	case "super", "meta", "win":
		return "Super", true
	default:
		return "", false
	}
}

// canonicalHotkeyKey maps one key token to its canonical spelling.
func canonicalHotkeyKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", errors.New("missing key")
	}
	runes := []rune(key)
	if len(runes) == 1 {
		switch {
		case runes[0] >= 'a' && runes[0] <= 'z', runes[0] >= 'A' && runes[0] <= 'Z':
			return strings.ToUpper(string(runes[0])), nil
		case runes[0] >= '0' && runes[0] <= '9':
			return string(runes[0]), nil
		default:
			return "", fmt.Errorf("unsupported key %q (use A-Z, 0-9, F1-F12 or a named key)", key)
		}
	}
	lower := strings.ToLower(key)
	if named, ok := namedHotkeyKeys[lower]; ok {
		return named, nil
	}
	if len(lower) >= 2 && lower[0] == 'f' {
		if number, err := strconv.Atoi(lower[1:]); err == nil && number >= 1 && number <= 12 && strconv.Itoa(number) == lower[1:] {
			return fmt.Sprintf("F%d", number), nil
		}
	}
	return "", fmt.Errorf("unsupported key %q (use A-Z, 0-9, F1-F12 or a named key)", key)
}

// namedHotkeyKeys are the supported named keys.
var namedHotkeyKeys = map[string]string{
	"space":     "Space",
	"enter":     "Enter",
	"return":    "Enter",
	"escape":    "Escape",
	"esc":       "Escape",
	"tab":       "Tab",
	"backspace": "Backspace",
	"delete":    "Delete",
	"del":       "Delete",
	"insert":    "Insert",
	"ins":       "Insert",
	"up":        "Up",
	"down":      "Down",
	"left":      "Left",
	"right":     "Right",
	"home":      "Home",
	"end":       "End",
	"pageup":    "PageUp",
	"pagedown":  "PageDown",
}
