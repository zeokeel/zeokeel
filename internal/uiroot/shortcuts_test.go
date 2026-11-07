package uiroot

// Shortcut chord parsing for the configurable chrome shortcuts.

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestParseShortcutChord(t *testing.T) {
	tests := []struct {
		raw  string
		mods ui.Modifiers
		key  ui.Key
		ok   bool
	}{

		{"cmd+b", ui.Cmd, ui.KeyB, true},
		{"cmd+k", ui.Cmd, ui.KeyK, true},
		{"ctrl+b", ui.Ctrl, ui.KeyB, true},
		{"alt+k", ui.Alt, ui.KeyK, true},
		{"cmd+shift+b", ui.Cmd | ui.Shift, ui.KeyB, true},
		{"shift+ctrl+k", ui.Shift | ui.Ctrl, ui.KeyK, true},
		{"", 0, 0, false},
		{"b", 0, 0, false},
		{"cmd+", 0, 0, false},
		{"cmd+1", 0, 0, false},
		{"hyper+b", 0, 0, false},
	}
	for _, test := range tests {
		mods, key, ok := parseShortcutChord(test.raw)
		if ok != test.ok {
			t.Fatalf("parseShortcutChord(%q) ok = %v, want %v", test.raw, ok, test.ok)
			continue
		}
		if !ok {
			continue
		}
		if mods != test.mods || key != test.key {
			t.Fatalf("parseShortcutChord(%q) = %v/%v, want %v/%v", test.raw, mods, key, test.mods, test.key)
		}
	}
}
