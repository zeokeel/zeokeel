package globalhotkey

// Carbon (macOS) pure mapping helpers. They live outside the darwin build tag so
// the keycode/modifier/status semantics are unit-tested on every host; the
// Carbon calls themselves are in globalhotkey_darwin.go.

import "fmt"

// Carbon constants (HIToolbox / CarbonEvents.h and Events.h). The API is
// deprecated since macOS 10.8 but still shipped and the standard way for a
// non-AppKit-accessible global hotkey.
const (
	// carbonKeyEventClass is the FourCharCode 'keyb'.
	carbonKeyEventClass = 0x6b657962
	// carbonHotKeyPressed is kEventHotKeyPressed (Events.h).
	carbonHotKeyPressed = 1

	// carbonHotKeyExistsErr is eventHotKeyExistsErr: the hotkey is already
	// registered by another process.
	carbonHotKeyExistsErr = -9878

	// carbonHotKeySignature is the FourCharCode 'ZKee' stored in the
	// EventHotKeyID signature; the id sits in the low 32 bits.
	carbonHotKeySignature = 0x5A4B6565

	// Carbon modifier masks (Events.h).
	carbonCommandKey = 1 << 8  // cmdKey
	carbonShiftKey   = 1 << 9  // shiftKey
	carbonOptionKey  = 1 << 11 // optionKey
	carbonControlKey = 1 << 12 // controlKey
)

// carbonEventTypeSpec mirrors EventTypeSpec { UInt32 eventClass; UInt32 eventKind; }.
type carbonEventTypeSpec struct {
	class uint32
	kind  uint32
}

// carbonHotKeyEventSpec is the one event type the backend listens for.
func carbonHotKeyEventSpec() carbonEventTypeSpec {
	return carbonEventTypeSpec{class: carbonKeyEventClass, kind: carbonHotKeyPressed}
}

// carbonHotKeyID packs the EventHotKeyID { signature, id } into the single
// 8-byte register value the C ABI passes for the struct.
func carbonHotKeyID(id int) uint64 {
	return uint64(carbonHotKeySignature)<<32 | uint64(uint32(id))
}

// carbonVirtualKey maps a canonical Hotkey.Key to a macOS virtual keycode
// (kVK_* from HIToolbox Events.h). F21-F24 have no Carbon kVK_ constants.
func carbonVirtualKey(key string) (uint32, bool) {
	if len(key) == 1 {
		switch key[0] {
		case 'a':
			return 0x00, true
		case 's':
			return 0x01, true
		case 'd':
			return 0x02, true
		case 'f':
			return 0x03, true
		case 'h':
			return 0x04, true
		case 'g':
			return 0x05, true
		case 'z':
			return 0x06, true
		case 'x':
			return 0x07, true
		case 'c':
			return 0x08, true
		case 'v':
			return 0x09, true
		case 'b':
			return 0x0B, true
		case 'q':
			return 0x0C, true
		case 'w':
			return 0x0D, true
		case 'e':
			return 0x0E, true
		case 'r':
			return 0x0F, true
		case 'y':
			return 0x10, true
		case 't':
			return 0x11, true
		case '1':
			return 0x12, true
		case '2':
			return 0x13, true
		case '3':
			return 0x14, true
		case '4':
			return 0x15, true
		case '6':
			return 0x16, true
		case '5':
			return 0x17, true
		case '9':
			return 0x19, true
		case '7':
			return 0x1A, true
		case '8':
			return 0x1C, true
		case '0':
			return 0x1D, true
		case 'o':
			return 0x1F, true
		case 'u':
			return 0x20, true
		case 'i':
			return 0x22, true
		case 'p':
			return 0x23, true
		case 'l':
			return 0x25, true
		case 'j':
			return 0x26, true
		case 'k':
			return 0x28, true
		case 'n':
			return 0x2D, true
		case 'm':
			return 0x2E, true
		}
		return 0, false
	}
	named := map[string]uint32{
		"space": 0x31, "tab": 0x30, "enter": 0x24, "escape": 0x35, "backspace": 0x33,
		"left": 0x7B, "right": 0x7C, "down": 0x7D, "up": 0x7E,
		"home": 0x73, "end": 0x77, "pageup": 0x74, "pagedown": 0x79, "delete": 0x75,
	}
	if code, ok := named[key]; ok {
		return code, true
	}
	if len(key) >= 2 && key[0] == 'f' {
		n := 0
		for _, r := range key[1:] {
			if r < '0' || r > '9' {
				return 0, false
			}
			n = n*10 + int(r-'0')
		}
		fkeys := [...]uint32{
			0x7A, 0x78, 0x63, 0x76, 0x60, 0x61, 0x62, 0x64, 0x65, 0x6D,
			0x67, 0x6F, 0x69, 0x6B, 0x71, 0x6A, 0x40, 0x4F, 0x50, 0x5A,
		}
		if n >= 1 && n <= len(fkeys) {
			return fkeys[n-1], true
		}
	}
	return 0, false
}

// carbonModifierMask maps the package modifier set to Carbon modifier masks.
func carbonModifierMask(mods Modifier) (uint32, bool) {
	if mods == 0 || mods&^(ModCtrl|ModAlt|ModShift|ModMeta) != 0 {
		return 0, false
	}
	var mask uint32
	if mods&ModCtrl != 0 {
		mask |= carbonControlKey
	}
	if mods&ModAlt != 0 {
		mask |= carbonOptionKey
	}
	if mods&ModShift != 0 {
		mask |= carbonShiftKey
	}
	if mods&ModMeta != 0 {
		mask |= carbonCommandKey
	}
	return mask, true
}

// carbonStatusError maps an OSStatus to the package's error vocabulary.
func carbonStatusError(status int32) error {
	switch status {
	case 0:
		return nil
	case carbonHotKeyExistsErr:
		return fmt.Errorf("%w: macOS Carbon reported eventHotKeyExistsErr", ErrOccupied)
	default:
		return fmt.Errorf("globalhotkey: Carbon OSStatus %d", status)
	}
}
