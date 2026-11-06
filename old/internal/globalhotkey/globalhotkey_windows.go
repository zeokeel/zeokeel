//go:build windows

package globalhotkey

// Windows backend: RegisterHotKey on a dedicated OS thread with a message loop.
//
// RegisterHotKey(NULL hwnd) binds the shortcut to the *calling thread's* message
// queue, so all registration and the WM_HOTKEY pump must run on one goroutine
// that is locked to its OS thread. Manager calls into this backend
// synchronously; commands are handed to the loop and acknowledged, so a failed
// RegisterHotKey (e.g. ERROR_HOTKEY_ALREADY_REGISTERED = 1409) surfaces as
// ErrOccupied to the caller.
//
// The code is pure Go (stdlib syscall, no cgo) and is cross-compiled on every
// release build; it cannot be executed in the Linux dev environment, so the
// Windows-specific paths are covered by cross-compilation only.

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

var (
	modUser32   = syscall.NewLazyDLL("user32.dll")
	modKernel32 = syscall.NewLazyDLL("kernel32.dll")

	procRegisterHotKey     = modUser32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = modUser32.NewProc("UnregisterHotKey")
	procGetMessageW        = modUser32.NewProc("GetMessageW")
	procPostThreadMessageW = modUser32.NewProc("PostThreadMessageW")
	procGetCurrentThreadID = modKernel32.NewProc("GetCurrentThreadId")
)

const (
	wmHotkey                   = 0x0312 // WM_HOTKEY
	wmQuit                     = 0x0012 // WM_QUIT
	wmWake                     = 0x8001 // WM_APP + 1: nudge the blocked GetMessage loop
	modNoRepeat                = 0x4000
	errHotkeyAlreadyRegistered = 1409
)

// msg mirrors the Windows MSG structure (x64 layout).
type msg struct {
	hwnd     uintptr
	message  uint32
	_        uint32
	wParam   uintptr
	lParam   uintptr
	time     uint32
	ptX      int32
	ptY      int32
	lPrivate uint32
	_        uint32
}

type winOp int

const (
	opRegister winOp = iota
	opUnregister
	opQuit
)

type winCmd struct {
	op     winOp
	id     int
	hotkey Hotkey
	fn     func()
	reply  chan error
}

// windowsBackend owns one message-loop goroutine.
type windowsBackend struct {
	cmds     chan winCmd
	done     chan struct{}
	ready    chan struct{}
	threadID uint32

	mu      sync.Mutex
	hotkeys map[int]func()

	closed atomic.Bool
}

// platformBackend returns the Windows backend.
func platformBackend() (backend, error) {
	b := &windowsBackend{
		cmds:    make(chan winCmd),
		done:    make(chan struct{}),
		ready:   make(chan struct{}),
		hotkeys: make(map[int]func()),
	}
	go b.loop()
	<-b.ready
	return b, nil
}

// loop is the locked OS thread that owns the message queue.
func (b *windowsBackend) loop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(b.done)

	tid, _, _ := procGetCurrentThreadID.Call()
	b.threadID = uint32(tid)
	close(b.ready)

	for {
		// Drain pending commands first (non-blocking) so register/unregister
		// never wait behind an idle message pump.
		drained := false
		for {
			select {
			case cmd := <-b.cmds:
				drained = true
				if b.handle(cmd) {
					return
				}
			default:
				goto wait
			}
		}
	wait:
		if drained {
			continue
		}
		var m msg
		ret, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) == 0 { // WM_QUIT
			return
		}
		if int32(ret) == -1 { // error
			if err != nil && !errors.Is(err, syscall.Errno(0)) {
				return
			}
			return
		}
		switch m.message {
		case wmHotkey:
			id := int(m.wParam)
			b.mu.Lock()
			fn := b.hotkeys[id]
			b.mu.Unlock()
			if fn != nil {
				go fn() // never block the message pump
			}
		case wmWake:
			// Nothing to do: the outer loop drains commands next.
		}
	}
}

// handle applies one command; it reports true when the loop must exit.
func (b *windowsBackend) handle(cmd winCmd) (quit bool) {
	switch cmd.op {
	case opRegister:
		cmd.reply <- b.registerHotKey(cmd.id, cmd.hotkey, cmd.fn)
	case opUnregister:
		cmd.reply <- b.unregisterHotKey(cmd.id)
	case opQuit:
		return true
	}
	return false
}

// wake nudges the message loop.
func (b *windowsBackend) wake() {
	procPostThreadMessageW.Call(uintptr(b.threadID), wmWake, 0, 0)
}

// register registers the hotkey on the loop thread.
func (b *windowsBackend) register(id int, h Hotkey, fn func()) error {
	if b.closed.Load() {
		return ErrClosed
	}
	reply := make(chan error, 1)
	b.cmds <- winCmd{op: opRegister, id: id, hotkey: h, fn: fn, reply: reply}
	b.wake()
	if err := <-reply; err != nil {
		return err
	}
	b.mu.Lock()
	b.hotkeys[id] = fn
	b.mu.Unlock()
	return nil
}

func (b *windowsBackend) unregister(id int) error {
	if b.closed.Load() {
		return ErrClosed
	}
	reply := make(chan error, 1)
	b.cmds <- winCmd{op: opUnregister, id: id, reply: reply}
	b.wake()
	if err := <-reply; err != nil {
		return err
	}
	b.mu.Lock()
	delete(b.hotkeys, id)
	b.mu.Unlock()
	return nil
}

// close stops the loop and releases the hotkeys.
func (b *windowsBackend) close() error {
	if b.closed.Swap(true) {
		return nil
	}
	reply := make(chan error, 1)
	b.cmds <- winCmd{op: opQuit, reply: reply}
	b.wake()
	<-reply
	<-b.done
	return nil
}

// registerHotKey is called on the loop thread only.
func (b *windowsBackend) registerHotKey(id int, h Hotkey, fn func()) error {
	mods := uint32(0)
	if h.Mods&ModCtrl != 0 {
		mods |= 0x0002 // MOD_CONTROL
	}
	if h.Mods&ModAlt != 0 {
		mods |= 0x0001 // MOD_ALT
	}
	if h.Mods&ModShift != 0 {
		mods |= 0x0004 // MOD_SHIFT
	}
	if h.Mods&ModMeta != 0 {
		mods |= 0x0008 // MOD_WIN
	}
	vk, ok := virtualKey(h.Key)
	if !ok {
		return fmt.Errorf("%w: no virtual-key mapping for %q", ErrInvalidHotkey, h.Key)
	}
	ret, _, callErr := procRegisterHotKey.Call(0, uintptr(id), uintptr(mods|modNoRepeat), uintptr(vk))
	if ret == 0 {
		if errno, ok := callErr.(syscall.Errno); ok && errno == errHotkeyAlreadyRegistered {
			return fmt.Errorf("%w: %s", ErrOccupied, h)
		}
		return fmt.Errorf("globalhotkey: RegisterHotKey(%s): %v", h, callErr)
	}
	b.mu.Lock()
	b.hotkeys[id] = fn
	b.mu.Unlock()
	return nil
}

// unregisterHotKey is called on the loop thread only.
func (b *windowsBackend) unregisterHotKey(id int) error {
	ret, _, callErr := procUnregisterHotKey.Call(0, uintptr(id))
	if ret == 0 {
		return fmt.Errorf("globalhotkey: UnregisterHotKey: %v", callErr)
	}
	return nil
}

// virtualKey maps a canonical key to a Windows virtual-key code.
func virtualKey(key string) (uint32, bool) {
	if len(key) == 1 {
		switch {
		case key[0] >= 'a' && key[0] <= 'z':
			return uint32(key[0] - 'a' + 'A'), true
		case key[0] >= '0' && key[0] <= '9':
			return uint32(key[0]), true
		}
		return 0, false
	}
	if key[0] == 'f' {
		n := 0
		for _, r := range key[1:] {
			n = n*10 + int(r-'0')
		}
		if n >= 1 && n <= 24 {
			return uint32(0x70 + n - 1), true // VK_F1..VK_F24
		}
		return 0, false
	}
	named := map[string]uint32{
		"space": 0x20, "tab": 0x09, "enter": 0x0D, "escape": 0x1B, "backspace": 0x08,
		"left": 0x25, "up": 0x26, "right": 0x27, "down": 0x28,
		"home": 0x24, "end": 0x23, "pageup": 0x21, "pagedown": 0x22,
		"insert": 0x2D, "delete": 0x2E,
	}
	vk, ok := named[key]
	return vk, ok
}
