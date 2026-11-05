//go:build linux

package globalhotkey

// Linux X11 backend: pure Go, no cgo (the C2/C1 hard constraints). It loads
// libX11.so.6 at runtime through purego's dlopen wrapper and binds only the
// small Xlib surface a global hotkey needs:
//
//	XOpenDisplay / XDefaultRootWindow / XSelectInput
//	XStringToKeysym / XKeysymToKeycode
//	XGrabKey / XUngrabKey / XSync
//	XPending / XNextEvent (dedicated event goroutine)
//	XCloseDisplay / XInitThreads / XSetErrorHandler
//
// Registration grabs four modifier variants per hotkey (none, NumLock,
// CapsLock, both) because X deals modifiers as state bits; dispatch masks the
// lock bits out, so a hotkey keeps working with NumLock/CapsLock on. Wayland
// sessions expose no X display (or an XWayland without global grabs depending
// on the compositor); those degrade to ErrUnsupported and the shell falls back
// to in-app shortcuts — the same documented behavior as before for headless
// environments.
//
// X11 calls are not safe across threads without XInitThreads, which the loader
// calls once. The backend still serializes access by design: registration and
// teardown happen on the caller goroutine, event pumping happens on one
// dedicated goroutine, and close() joins the pump before ungrabbing/closing.

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	x11KeyPress = 2

	// X modifier masks (see X.h).
	x11ShiftMask   = 1 << 0
	x11LockMask    = 1 << 1 // CapsLock
	x11ControlMask = 1 << 2
	x11Mod1Mask    = 1 << 3 // Alt
	x11Mod2Mask    = 1 << 4 // NumLock
	x11Mod3Mask    = 1 << 5
	x11Mod4Mask    = 1 << 6 // Super/Meta

	x11GrabModeAsync = 1 // GrabModeAsync
	x11KeyPressMask  = 1 << 0

	// Common X protocol error codes observed through the error handler.
	x11BadAccess = 10 // a client already holds the grab

	// Offsets inside the 192-byte XEvent union for a 64-bit XKeyEvent:
	// type(0) serial(8) send_event(16) display(24) window(32) root(40)
	// subwindow(48) time(56) x(64) y(68) x_root(72) y_root(76)
	// state(80) keycode(84) same_screen(88).
	x11KeyEventStateOffset   = 80
	x11KeyEventKeycodeOffset = 84
	x11EventPumpInterval     = 10 * time.Millisecond
)

// x11EventBuf is the opaque 192-byte XEvent union (24 uint64 words on 64-bit).
type x11EventBuf [24]uint64

// x11API is the bound Xlib function table; tests inject a fake.
type x11API struct {
	OpenDisplay       func(name *byte) uintptr
	CloseDisplay      func(dpy uintptr) int32
	DefaultRootWindow func(dpy uintptr) uintptr
	SelectInput       func(dpy, win uintptr, mask int64) int32
	Pending           func(dpy uintptr) int32
	NextEvent         func(dpy uintptr, ev *x11EventBuf) int32
	GrabKey           func(dpy uintptr, keycode, mods uint32, win uintptr, ownerEvents, pointerMode, keybdMode int32)
	UngrabKey         func(dpy uintptr, keycode, mods uint32, win uintptr)
	Sync              func(dpy uintptr, discard int32) int32
	StringToKeysym    func(name *byte) uintptr
	KeysymToKeycode   func(dpy uintptr, keysym uintptr) uint32
	InitThreads       func() int32
	SetErrorHandler   func(handler uintptr) uintptr
}

// x11LastError is the most recent X protocol error reported by Xlib's error
// handler. XGrabKey reports conflicts asynchronously (BadAccess after XSync),
// not through a return value, so registration flushes with XSync and then
// inspects this slot.
var x11LastError atomic.Int32

// x11ErrorCallback must outlive the process (Xlib keeps the pointer).
var x11ErrorCallback = purego.NewCallback(func(_ uintptr, event unsafe.Pointer) int32 {
	if event != nil {
		// XErrorEvent: type(0) display(8) resourceid(16) serial(24)
		// error_code(32) request_code(33) minor_code(34).
		x11ReportXError(*(*uint8)(unsafe.Add(event, 32)))
	}
	return 0
})

// x11ReportXError records one X error code (shared with tests).
func x11ReportXError(code uint8) { x11LastError.Store(int32(code)) }

// x11Load loads libX11 and binds the needed functions. Tests replace it.
var x11Load = loadX11Lib

// loadX11Lib dlopens libX11.so.6 (the standard Linux soname) and binds the
// minimal function table. It never unloads the library: the process owns it for
// its lifetime and unloading while a display is open would be worse.
func loadX11Lib() (*x11API, error) {
	handle, err := purego.Dlopen("libX11.so.6", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("dlopen libX11.so.6: %w", err)
	}
	api := &x11API{}
	bindings := []struct {
		fptr any
		name string
	}{
		{&api.OpenDisplay, "XOpenDisplay"},
		{&api.CloseDisplay, "XCloseDisplay"},
		{&api.DefaultRootWindow, "XDefaultRootWindow"},
		{&api.SelectInput, "XSelectInput"},
		{&api.Pending, "XPending"},
		{&api.NextEvent, "XNextEvent"},
		{&api.GrabKey, "XGrabKey"},
		{&api.UngrabKey, "XUngrabKey"},
		{&api.Sync, "XSync"},
		{&api.StringToKeysym, "XStringToKeysym"},
		{&api.KeysymToKeycode, "XKeysymToKeycode"},
		{&api.InitThreads, "XInitThreads"},
		{&api.SetErrorHandler, "XSetErrorHandler"},
	}
	for _, b := range bindings {
		if _, err := purego.Dlsym(handle, b.name); err != nil {
			return nil, fmt.Errorf("libX11: %s: %w", b.name, err)
		}
		purego.RegisterLibFunc(b.fptr, handle, b.name)
	}
	// Xlib needs this before any multi-goroutine use (registration thread +
	// event pump). It is safe to call more than once.
	api.InitThreads()
	api.SetErrorHandler(x11ErrorCallback)
	return api, nil
}

// x11Registration is one live grab set: the keycode plus the user modifier
// mask; the grab variants (lock mask combinations) are derived from mods.
type x11Registration struct {
	keycode uint32
	mods    uint32
	fn      func()
}

// x11Backend implements backend on top of one X display connection.
type x11Backend struct {
	api  *x11API
	dpy  uintptr
	root uintptr

	mu     sync.Mutex
	regs   map[int]*x11Registration
	quit   chan struct{}
	done   chan struct{}
	closed atomic.Bool
}

// newX11Backend opens the X display from $DISPLAY and starts the event pump.
// A headless session (no DISPLAY) or a session without usable X11 degrades to
// ErrUnsupported instead of failing startup.
func newX11Backend() (*x11Backend, error) {
	display := strings.TrimSpace(os.Getenv("DISPLAY"))
	if display == "" {
		return nil, fmt.Errorf("%w: no DISPLAY (headless session)", ErrUnsupported)
	}
	api, err := x11Load()
	if err != nil {
		return nil, fmt.Errorf("%w: X11 client library unavailable: %v", ErrUnsupported, err)
	}
	dpy := api.OpenDisplay(nil)
	if dpy == 0 {
		return nil, fmt.Errorf("%w: cannot open X display %q", ErrUnsupported, display)
	}
	root := api.DefaultRootWindow(dpy)
	if root == 0 {
		api.CloseDisplay(dpy)
		return nil, fmt.Errorf("%w: X display %q has no root window", ErrUnsupported, display)
	}
	// KeyPress delivery for grabbed keys does not strictly require the mask,
	// but selecting it is the documented XGrabKey setup and keeps the event
	// stream explicit.
	api.SelectInput(dpy, root, x11KeyPressMask)
	b := &x11Backend{
		api:  api,
		dpy:  dpy,
		root: root,
		regs: make(map[int]*x11Registration),
		quit: make(chan struct{}),
		done: make(chan struct{}),
	}
	go b.loop()
	return b, nil
}

// register grabs the hotkey (all lock-modifier variants) and starts dispatching
// its callback.
func (b *x11Backend) register(id int, h Hotkey, fn func()) error {
	if b.closed.Load() {
		return ErrClosed
	}
	if fn == nil {
		return fmt.Errorf("%w: nil callback", ErrInvalidHotkey)
	}
	baseMods, ok := x11ModMask(h.Mods)
	if !ok {
		return fmt.Errorf("%w: invalid modifier set %d", ErrInvalidHotkey, h.Mods)
	}
	keycode, _, err := b.keycodeFor(h.Key)
	if err != nil {
		return err
	}
	b.mu.Lock()
	if _, exists := b.regs[id]; exists {
		b.mu.Unlock()
		return fmt.Errorf("%w: handle %d", ErrAlreadyRegistered, id)
	}
	b.mu.Unlock()

	variants := x11GrabVariants(baseMods)
	x11LastError.Store(0)
	for _, mods := range variants {
		b.api.GrabKey(b.dpy, keycode, mods, b.root, 1, x11GrabModeAsync, x11GrabModeAsync)
	}
	b.api.Sync(b.dpy, 0)
	if code := x11LastError.Load(); code != 0 {
		// A failed grab can be partial (some variants succeeded); undo them all
		// so a retry after the conflicting client exits has a clean slate.
		for _, mods := range variants {
			b.api.UngrabKey(b.dpy, keycode, mods, b.root)
		}
		b.api.Sync(b.dpy, 0)
		if code == x11BadAccess {
			return fmt.Errorf("%w: %s", ErrOccupied, h)
		}
		return fmt.Errorf("globalhotkey: X11 grab %s failed with X error %d", h, code)
	}

	reg := &x11Registration{keycode: keycode, mods: baseMods, fn: fn}
	b.mu.Lock()
	if b.closed.Load() {
		b.mu.Unlock()
		for _, mods := range variants {
			b.api.UngrabKey(b.dpy, keycode, mods, b.root)
		}
		return ErrClosed
	}
	b.regs[id] = reg
	b.mu.Unlock()
	return nil
}

// unregister releases the hotkey's grabs. Unknown ids surface as
// ErrUnknownHandle (Manager should not call this, but the backend is defensive).
func (b *x11Backend) unregister(id int) error {
	b.mu.Lock()
	reg := b.regs[id]
	delete(b.regs, id)
	b.mu.Unlock()
	if reg == nil {
		return fmt.Errorf("%w: %d", ErrUnknownHandle, id)
	}
	for _, mods := range x11GrabVariants(reg.mods) {
		b.api.UngrabKey(b.dpy, reg.keycode, mods, b.root)
	}
	b.api.Sync(b.dpy, 0)
	return nil
}

// close stops the pump, releases every grab and closes the display. Idempotent.
func (b *x11Backend) close() error {
	if b.closed.Swap(true) {
		return nil
	}
	close(b.quit)
	<-b.done

	b.mu.Lock()
	regs := b.regs
	b.regs = make(map[int]*x11Registration)
	b.mu.Unlock()
	for _, reg := range regs {
		for _, mods := range x11GrabVariants(reg.mods) {
			b.api.UngrabKey(b.dpy, reg.keycode, mods, b.root)
		}
	}
	b.api.Sync(b.dpy, 0)
	b.api.CloseDisplay(b.dpy)
	return nil
}

// loop is the dedicated X event pump goroutine.
func (b *x11Backend) loop() {
	defer close(b.done)
	for {
		select {
		case <-b.quit:
			return
		default:
		}
		if b.api.Pending(b.dpy) == 0 {
			select {
			case <-b.quit:
				return
			case <-time.After(x11EventPumpInterval):
				continue
			}
		}
		var ev x11EventBuf
		if b.api.NextEvent(b.dpy, &ev) == 0 {
			continue
		}
		typ, state, keycode := x11DecodeKeyEvent(&ev)
		if typ != x11KeyPress {
			continue
		}
		b.dispatch(keycode, state)
	}
}

// dispatch runs every matching callback on its own goroutine so a slow callback
// never stalls the pump or the other hotkeys.
func (b *x11Backend) dispatch(keycode, state uint32) {
	b.mu.Lock()
	var fns []func()
	for _, reg := range b.regs {
		if x11EventMatches(reg, keycode, state) {
			fns = append(fns, reg.fn)
		}
	}
	b.mu.Unlock()
	for _, fn := range fns {
		go fn()
	}
}

// keycodeFor maps a canonical key name to an X keycode (and the keysym used).
func (b *x11Backend) keycodeFor(key string) (uint32, uintptr, error) {
	name, ok := x11KeySymName(key)
	if !ok {
		return 0, 0, fmt.Errorf("%w: key %q has no X11 keysym", ErrInvalidHotkey, key)
	}
	cname := make([]byte, len(name)+1)
	copy(cname, name)
	keysym := b.api.StringToKeysym(&cname[0])
	if keysym == 0 {
		return 0, 0, fmt.Errorf("%w: XStringToKeysym(%q) returned 0", ErrInvalidHotkey, name)
	}
	keycode := b.api.KeysymToKeycode(b.dpy, keysym)
	if keycode == 0 {
		return 0, 0, fmt.Errorf("%w: XKeysymToKeycode(%q) returned 0", ErrInvalidHotkey, name)
	}
	return keycode, keysym, nil
}

// x11DecodeKeyEvent reads type/state/keycode out of a 64-bit XKeyEvent.
func x11DecodeKeyEvent(ev *x11EventBuf) (typ int32, state, keycode uint32) {
	base := unsafe.Pointer(&ev[0])
	typ = *(*int32)(base)
	state = *(*uint32)(unsafe.Pointer(uintptr(base) + x11KeyEventStateOffset))
	keycode = *(*uint32)(unsafe.Pointer(uintptr(base) + x11KeyEventKeycodeOffset))
	return typ, state, keycode
}

// x11ModMask maps the package's modifier set to X modifier masks.
func x11ModMask(mods Modifier) (uint32, bool) {
	if mods == 0 || mods&^(ModCtrl|ModAlt|ModShift|ModMeta) != 0 {
		return 0, false
	}
	var mask uint32
	if mods&ModCtrl != 0 {
		mask |= x11ControlMask
	}
	if mods&ModAlt != 0 {
		mask |= x11Mod1Mask
	}
	if mods&ModShift != 0 {
		mask |= x11ShiftMask
	}
	if mods&ModMeta != 0 {
		mask |= x11Mod4Mask
	}
	return mask, true
}

// x11GrabVariants returns the four lock-modifier combinations a grab must cover:
// no lock, NumLock, CapsLock, both. X treats them as ordinary state bits, so
// one XGrabKey per variant is required.
func x11GrabVariants(base uint32) []uint32 {
	return []uint32{
		base,
		base | x11Mod2Mask,
		base | x11LockMask,
		base | x11Mod2Mask | x11LockMask,
	}
}

// x11KeySymName maps a canonical Hotkey.Key to its X keysym name (as accepted by
// XStringToKeysym).
func x11KeySymName(key string) (string, bool) {
	if len(key) == 1 {
		if key[0] >= 'a' && key[0] <= 'z' || key[0] >= '0' && key[0] <= '9' {
			return key, true
		}
		return "", false
	}
	named := map[string]string{
		"space": "space", "tab": "Tab", "enter": "Return", "escape": "Escape",
		"backspace": "BackSpace", "up": "Up", "down": "Down", "left": "Left", "right": "Right",
		"home": "Home", "end": "End", "pageup": "Prior", "pagedown": "Next",
		"insert": "Insert", "delete": "Delete",
	}
	if name, ok := named[key]; ok {
		return name, true
	}
	if key[0] == 'f' {
		n := 0
		for _, r := range key[1:] {
			if r < '0' || r > '9' {
				return "", false
			}
			n = n*10 + int(r-'0')
		}
		if n >= 1 && n <= 24 {
			return "F" + strings.TrimPrefix(key, "f"), true
		}
	}
	return "", false
}

// x11EventMatches reports whether one KeyPress event belongs to reg: same
// keycode and the same modifier set after masking the lock bits (so
// NumLock/CapsLock never change whether a hotkey fires).
func x11EventMatches(reg *x11Registration, keycode, state uint32) bool {
	if reg == nil || keycode != reg.keycode {
		return false
	}
	if state&x11Mod3Mask != 0 { // Mod3 is not part of the hotkey vocabulary
		return false
	}
	masked := state &^ (x11Mod2Mask | x11LockMask)
	return masked == reg.mods
}
