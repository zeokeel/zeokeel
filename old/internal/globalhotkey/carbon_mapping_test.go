package globalhotkey

// TDD tests for the Carbon (macOS) backend's pure mapping layer. They run on
// every host so the macOS keycode/modifier/status semantics are pinned even
// though RegisterEventHotKey itself can only execute on macOS.

import (
	"errors"
	"testing"
)

func TestCarbonVirtualKey(t *testing.T) {
	cases := map[string]uint32{
		"a": 0x00, "s": 0x01, "d": 0x02, "f": 0x03, "h": 0x04, "g": 0x05, "z": 0x06,
		"x": 0x07, "c": 0x08, "v": 0x09, "b": 0x0B, "q": 0x0C, "w": 0x0D, "e": 0x0E,
		"r": 0x0F, "y": 0x10, "t": 0x11, "1": 0x12, "2": 0x13, "3": 0x14, "4": 0x15,
		"6": 0x16, "5": 0x17, "9": 0x19, "7": 0x1A, "8": 0x1C, "0": 0x1D,
		"o": 0x1F, "u": 0x20, "i": 0x22, "p": 0x23, "l": 0x25, "j": 0x26,
		"k": 0x28, "n": 0x2D, "m": 0x2E,
		"space": 0x31, "tab": 0x30, "enter": 0x24, "escape": 0x35, "backspace": 0x33,
		"left": 0x7B, "right": 0x7C, "down": 0x7D, "up": 0x7E,
		"home": 0x73, "end": 0x77, "pageup": 0x74, "pagedown": 0x79, "delete": 0x75,
		"f1": 0x7A, "f2": 0x78, "f3": 0x63, "f4": 0x76, "f5": 0x60, "f6": 0x61,
		"f7": 0x62, "f8": 0x64, "f9": 0x65, "f10": 0x6D, "f11": 0x67, "f12": 0x6F,
		"f13": 0x69, "f14": 0x6B, "f15": 0x71, "f16": 0x6A, "f17": 0x40, "f18": 0x4F,
		"f19": 0x50, "f20": 0x5A,
	}
	for key, want := range cases {
		got, ok := carbonVirtualKey(key)
		if !ok || got != want {
			t.Fatalf("carbonVirtualKey(%q) = (0x%X,%v), want (0x%X,true)", key, got, ok, want)
		}
	}
	for _, key := range []string{"f21", "f24", "nope", "", "A"} {
		if _, ok := carbonVirtualKey(key); ok {
			t.Fatalf("carbonVirtualKey(%q) unexpectedly succeeded", key)
		}
	}
}

func TestCarbonModifierMask(t *testing.T) {
	cases := []struct {
		mods Modifier
		want uint32
		ok   bool
	}{
		{ModCtrl, carbonControlKey, true},
		{ModAlt, carbonOptionKey, true},
		{ModShift, carbonShiftKey, true},
		{ModMeta, carbonCommandKey, true},
		{ModCtrl | ModAlt | ModShift | ModMeta, carbonControlKey | carbonOptionKey | carbonShiftKey | carbonCommandKey, true},
		{0, 0, false},
		{Modifier(1 << 10), 0, false},
	}
	for _, tc := range cases {
		got, ok := carbonModifierMask(tc.mods)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("carbonModifierMask(%b) = (0x%X,%v), want (0x%X,%v)", tc.mods, got, ok, tc.want, tc.ok)
		}
	}
}

func TestCarbonStatusError(t *testing.T) {
	if err := carbonStatusError(0); err != nil {
		t.Fatalf("carbonStatusError(0) = %v, want nil", err)
	}
	if err := carbonStatusError(carbonHotKeyExistsErr); !errors.Is(err, ErrOccupied) {
		t.Fatalf("carbonStatusError(hotKeyExistsErr) = %v, want ErrOccupied", err)
	}
	if err := carbonStatusError(-50); err == nil || errors.Is(err, ErrOccupied) {
		t.Fatalf("carbonStatusError(-50) = %v, want a non-occupied wrapped error", err)
	}
}

func TestCarbonEventSpec(t *testing.T) {
	spec := carbonHotKeyEventSpec()
	if spec.class != carbonKeyEventClass || spec.kind != carbonHotKeyPressed {
		t.Fatalf("spec = {%#x,%#x}, want {%#x,%#x}", spec.class, spec.kind, carbonKeyEventClass, carbonHotKeyPressed)
	}
	// FourCharCode 'keyb'.
	if carbonKeyEventClass != 0x6b657962 {
		t.Fatalf("carbonKeyEventClass = %#x, want 'keyb'", carbonKeyEventClass)
	}
	if carbonHotKeyPressed != 1 {
		t.Fatalf("carbonHotKeyPressed = %d, want 1", carbonHotKeyPressed)
	}
}

func TestCarbonHotKeyIDPacking(t *testing.T) {
	if got := carbonHotKeyID(7); got&0xffffffff != 7 {
		t.Fatalf("carbonHotKeyID(7) = %#x, want id in the low 32 bits", got)
	}
	if carbonHotKeyID(7)>>32 != carbonHotKeySignature {
		t.Fatalf("carbonHotKeyID signature = %#x, want %#x", carbonHotKeyID(7)>>32, carbonHotKeySignature)
	}
}
