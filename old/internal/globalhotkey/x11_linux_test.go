//go:build linux

package globalhotkey

// TDD tests for the Linux X11 backend (task-44). The backend seam is an
// injected x11API: these tests run without an X server and pin the grab
// variants (numlock/capslock), key mapping, event dispatch, ungrab and close
// semantics. The real dlopen implementation is covered by cross-compilation
// and the headless ErrUnsupported test.

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"
)

// --- fake Xlib ------------------------------------------------------------

type x11GrabCall struct {
	keycode     uint32
	mods        uint32
	window      uintptr
	ownerEvents int32
	pointerMode int32
	keybdMode   int32
}

type fakeX11 struct {
	mu             sync.Mutex
	grabs          []x11GrabCall
	ungrabs        []x11GrabCall
	queue          []x11EventBuf
	closedDisplays int
	selects        int
	syncHook       func()
	keysyms        map[string]uintptr
	keycodes       map[uintptr]uint32
}

func newFakeX11() (*fakeX11, *x11API) {
	f := &fakeX11{
		keysyms:  map[string]uintptr{"z": 0x7a, "F12": 0xffc9, "space": 0x20},
		keycodes: map[uintptr]uint32{0x7a: 38, 0xffc9: 96, 0x20: 65},
	}
	api := &x11API{
		OpenDisplay: func(*byte) uintptr { return 1 },
		CloseDisplay: func(uintptr) int32 {
			f.mu.Lock()
			f.closedDisplays++
			f.mu.Unlock()
			return 0
		},
		DefaultRootWindow: func(uintptr) uintptr { return 2 },
		SelectInput: func(uintptr, uintptr, int64) int32 {
			f.mu.Lock()
			f.selects++
			f.mu.Unlock()
			return 0
		},
		Pending: func(uintptr) int32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.queue) == 0 {
				return 0
			}
			return 1
		},
		NextEvent: func(_ uintptr, ev *x11EventBuf) int32 {
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.queue) == 0 {
				return 0
			}
			*ev = f.queue[0]
			f.queue = f.queue[1:]
			return 1
		},
		GrabKey: func(_ uintptr, keycode, mods uint32, window uintptr, ownerEvents, pointerMode, keybdMode int32) {
			f.mu.Lock()
			f.grabs = append(f.grabs, x11GrabCall{keycode, mods, window, ownerEvents, pointerMode, keybdMode})
			f.mu.Unlock()
		},
		UngrabKey: func(_ uintptr, keycode, mods uint32, window uintptr) {
			f.mu.Lock()
			f.ungrabs = append(f.ungrabs, x11GrabCall{keycode: keycode, mods: mods, window: window})
			f.mu.Unlock()
		},
		Sync: func(uintptr, int32) int32 {
			if f.syncHook != nil {
				f.syncHook()
			}
			return 0
		},
		StringToKeysym: func(name *byte) uintptr {
			return f.keysyms[cString(name)]
		},
		KeysymToKeycode: func(_ uintptr, keysym uintptr) uint32 { return f.keycodes[keysym] },
		InitThreads:     func() int32 { return 1 },
		SetErrorHandler: func(uintptr) uintptr { return 0 },
	}
	return f, api
}

func cString(p *byte) string {
	if p == nil {
		return ""
	}
	var n int
	for *(*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(p)) + uintptr(n))) != 0 {
		n++
	}
	return string(unsafe.Slice(p, n))
}

func (f *fakeX11) pushKeyEvent(typ int32, state, keycode uint32) {
	var buf x11EventBuf
	*(*int32)(unsafe.Pointer(&buf[0])) = typ
	*(*uint32)(unsafe.Pointer(uintptr(unsafe.Pointer(&buf[0])) + x11KeyEventStateOffset)) = state
	*(*uint32)(unsafe.Pointer(uintptr(unsafe.Pointer(&buf[0])) + x11KeyEventKeycodeOffset)) = keycode
	f.mu.Lock()
	f.queue = append(f.queue, buf)
	f.mu.Unlock()
}

func (f *fakeX11) snapshotGrabs() []x11GrabCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]x11GrabCall(nil), f.grabs...)
}

func (f *fakeX11) snapshotUngrabs() []x11GrabCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]x11GrabCall(nil), f.ungrabs...)
}

func (f *fakeX11) closeCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closedDisplays
}

func withFakeX11(t *testing.T) (*fakeX11, *x11API) {
	t.Helper()
	f, api := newFakeX11()
	prev := x11Load
	x11Load = func() (*x11API, error) { return api, nil }
	t.Cleanup(func() { x11Load = prev })
	t.Setenv("DISPLAY", ":99")
	return f, api
}

// --- pure mapping tests ---------------------------------------------------

func TestX11ModMask(t *testing.T) {
	cases := []struct {
		mods Modifier
		want uint32
		ok   bool
	}{
		{ModCtrl, x11ControlMask, true},
		{ModAlt, x11Mod1Mask, true},
		{ModShift, x11ShiftMask, true},
		{ModMeta, x11Mod4Mask, true},
		{ModCtrl | ModAlt | ModShift | ModMeta, x11ControlMask | x11Mod1Mask | x11ShiftMask | x11Mod4Mask, true},
		{0, 0, false},
		{Modifier(1 << 10), 0, false},
	}
	for _, tc := range cases {
		got, ok := x11ModMask(tc.mods)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("x11ModMask(%b) = (%d,%v), want (%d,%v)", tc.mods, got, ok, tc.want, tc.ok)
		}
	}
}

func TestX11GrabVariantsCoverLockMasks(t *testing.T) {
	variants := x11GrabVariants(x11ControlMask | x11Mod1Mask)
	if len(variants) != 4 {
		t.Fatalf("variants = %d, want 4 (none/numlock/capslock/both): %v", len(variants), variants)
	}
	want := []uint32{
		x11ControlMask | x11Mod1Mask,
		x11ControlMask | x11Mod1Mask | x11Mod2Mask,
		x11ControlMask | x11Mod1Mask | x11LockMask,
		x11ControlMask | x11Mod1Mask | x11Mod2Mask | x11LockMask,
	}
	for i := range want {
		if variants[i] != want[i] {
			t.Fatalf("variant[%d] = %d, want %d", i, variants[i], want[i])
		}
	}
}

func TestX11KeySymName(t *testing.T) {
	cases := map[string]string{
		"a": "a", "z": "z", "0": "0", "9": "9",
		"f1": "F1", "f12": "F12", "f24": "F24",
		"space": "space", "tab": "Tab", "enter": "Return", "escape": "Escape",
		"backspace": "BackSpace", "up": "Up", "down": "Down", "left": "Left", "right": "Right",
		"home": "Home", "end": "End", "pageup": "Prior", "pagedown": "Next",
		"insert": "Insert", "delete": "Delete",
	}
	for key, want := range cases {
		got, ok := x11KeySymName(key)
		if !ok || got != want {
			t.Fatalf("x11KeySymName(%q) = (%q,%v), want (%q,true)", key, got, ok, want)
		}
	}
	if _, ok := x11KeySymName("nope"); ok {
		t.Fatal("x11KeySymName accepted an unknown key")
	}
}

func TestX11EventMatchesIgnoresLockModifiers(t *testing.T) {
	reg := &x11Registration{keycode: 38, mods: x11ControlMask | x11Mod1Mask}
	cases := []struct {
		keycode, state uint32
		want           bool
	}{
		{38, x11ControlMask | x11Mod1Mask, true},
		{38, x11ControlMask | x11Mod1Mask | x11Mod2Mask, true},
		{38, x11ControlMask | x11Mod1Mask | x11LockMask, true},
		{38, x11ControlMask | x11Mod1Mask | x11Mod2Mask | x11LockMask, true},
		{38, x11ControlMask | x11Mod1Mask | x11ShiftMask, false}, // extra modifier
		{38, x11ControlMask, false},                              // missing modifier
		{39, x11ControlMask | x11Mod1Mask, false},                // other key
	}
	for _, tc := range cases {
		if got := x11EventMatches(reg, tc.keycode, tc.state); got != tc.want {
			t.Fatalf("x11EventMatches(key=%d,state=%d) = %v, want %v", tc.keycode, tc.state, got, tc.want)
		}
	}
}

// --- backend lifecycle with the fake -------------------------------------

func TestX11BackendGrabsVariantsDispatchesAndCloses(t *testing.T) {
	f, _ := withFakeX11(t)
	b, err := newX11Backend()
	if err != nil {
		t.Fatalf("newX11Backend: %v", err)
	}
	fired := make(chan struct{}, 1)
	if err := b.register(1, Hotkey{Mods: ModCtrl | ModAlt, Key: "z"}, func() { fired <- struct{}{} }); err != nil {
		t.Fatalf("register: %v", err)
	}
	grabs := f.snapshotGrabs()
	if len(grabs) != 4 {
		t.Fatalf("XGrabKey calls = %d, want 4 variants", len(grabs))
	}
	for i, g := range grabs {
		if g.keycode != 38 || g.window != 2 || g.ownerEvents != 1 || g.pointerMode != 1 || g.keybdMode != 1 {
			t.Fatalf("grab[%d] = %+v, want keycode 38 window 2 async modes", i, g)
		}
	}
	// NumLock held: still matches (grab registered the Mod2 variant).
	f.pushKeyEvent(x11KeyPress, x11ControlMask|x11Mod1Mask|x11Mod2Mask, 38)
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("callback did not fire for a matching KeyPress")
	}
	// Extra Shift does not match.
	f.pushKeyEvent(x11KeyPress, x11ControlMask|x11Mod1Mask|x11ShiftMask, 38)
	select {
	case <-fired:
		t.Fatal("callback fired for a non-matching modifier state")
	case <-time.After(150 * time.Millisecond):
	}
	if err := b.unregister(1); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if got := len(f.snapshotUngrabs()); got != 4 {
		t.Fatalf("XUngrabKey calls = %d, want 4 variants", got)
	}
	if err := b.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := f.closeCalls(); got != 1 {
		t.Fatalf("XCloseDisplay calls = %d, want 1", got)
	}
	if err := b.close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if got := f.closeCalls(); got != 1 {
		t.Fatalf("XCloseDisplay after second close = %d, want 1", got)
	}
}

func TestX11BackendOccupiedMapsToErrOccupied(t *testing.T) {
	f, api := withFakeX11(t)
	api.Sync = func(uintptr, int32) int32 {
		x11ReportXError(x11BadAccess)
		return 0
	}
	b, err := newX11Backend()
	if err != nil {
		t.Fatalf("newX11Backend: %v", err)
	}
	defer b.close()
	if err := b.register(1, Hotkey{Mods: ModCtrl, Key: "z"}, func() {}); !errors.Is(err, ErrOccupied) {
		t.Fatalf("register err = %v, want ErrOccupied", err)
	}
	if got := len(f.snapshotUngrabs()); got != 4 {
		t.Fatalf("cleanup ungrabs = %d, want 4", got)
	}
}

func TestX11BackendUnknownKeyRejected(t *testing.T) {
	withFakeX11(t)
	b, err := newX11Backend()
	if err != nil {
		t.Fatalf("newX11Backend: %v", err)
	}
	defer b.close()
	if err := b.register(1, Hotkey{Mods: 0, Key: "z"}, func() {}); !errors.Is(err, ErrInvalidHotkey) {
		t.Fatalf("register with empty mods err = %v, want ErrInvalidHotkey", err)
	}
	if err := b.register(1, Hotkey{Mods: ModCtrl, Key: "nope"}, func() {}); !errors.Is(err, ErrInvalidHotkey) {
		t.Fatalf("register unknown key err = %v, want ErrInvalidHotkey", err)
	}
}

func TestX11PlatformBackendHeadless(t *testing.T) {
	t.Setenv("DISPLAY", "")
	if _, err := platformBackend(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("platformBackend headless err = %v, want ErrUnsupported", err)
	}
}

// TestX11RealDisplaySmoke exercises the real dlopen/Xlib path when an X server
// is available (CI sets ZEOKEEL_X11_TEST=1 under Xvfb). It opens a display,
// grabs Ctrl+Alt+Z (four lock-modifier variants), then releases and closes.
func TestX11RealDisplaySmoke(t *testing.T) {
	if os.Getenv("ZEOKEEL_X11_TEST") != "1" {
		t.Skip("set ZEOKEEL_X11_TEST=1 with a real X display to run the dlopen smoke test")
	}
	prev := x11Load
	x11Load = loadX11Lib
	t.Cleanup(func() { x11Load = prev })
	if strings.TrimSpace(os.Getenv("DISPLAY")) == "" {
		t.Skip("no DISPLAY")
	}
	b, err := newX11Backend()
	if err != nil {
		t.Fatalf("newX11Backend: %v", err)
	}
	defer func() {
		if err := b.close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}()
	if err := b.register(1, Hotkey{Mods: ModCtrl | ModAlt, Key: "z"}, func() {}); err != nil {
		t.Fatalf("register: %v", err)
	}
	// A second X client cannot grab the same shortcut: Xlib reports BadAccess
	// asynchronously after XSync, which must map to ErrOccupied.
	b2, err := newX11Backend()
	if err != nil {
		t.Fatalf("second display: %v", err)
	}
	if err := b2.register(1, Hotkey{Mods: ModCtrl | ModAlt, Key: "z"}, func() {}); !errors.Is(err, ErrOccupied) {
		t.Fatalf("second client register err = %v, want ErrOccupied", err)
	}
	if err := b2.close(); err != nil {
		t.Fatalf("close second display: %v", err)
	}
	if err := b.unregister(1); err != nil {
		t.Fatalf("unregister: %v", err)
	}
}
