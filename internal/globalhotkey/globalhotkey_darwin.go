//go:build darwin

package globalhotkey

// macOS backend: Carbon.framework RegisterEventHotKey through purego dlopen —
// pure Go, no cgo (the CGO_ENABLED=0 hard constraint), no new module dependency.
// Carbon's hot-key API is deprecated since macOS 10.8 but still shipped and is
// the standard way to register a system-wide shortcut from a non-AppKit-specific
// process; the modern alternative (CGEventTap) needs Accessibility permission and
// a cgo/helper-process design, which is out of scope.
//
// Threading: RegisterEventHotKey / InstallEventHandler / RemoveEventHandler /
// UnregisterEventHotKey must run on the application's main thread. The app wiring
// calls syncGlobalHotkey from AppKit's startup path (internal/app/app.go) and
// later from the settings UI, which both run on the main goroutine; New() and
// Register() return an error when there is no application event target (headless
// CI / non-GUI process) instead of crashing.
//
// Runtime validation: this backend is compiled and cross-built for darwin
// amd64/arm64 in the Linux dev environment but cannot be executed here; the
// dlopen paths, the EventHotKeyID ABI packing and the main-thread requirement are
// documented risks (see doc.go).

import (
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
)

// carbonAPI is the bound Carbon/HIToolbox function table.
type carbonAPI struct {
	GetApplicationEventTarget func() uintptr
	// InstallEventHandler(EventTargetRef, EventHandlerUPP, ItemCount,
	//                      const EventTypeSpec *, void *userData,
	//                      EventHandlerRef *outRef)
	InstallEventHandler func(target, handler, numTypes uintptr, types *carbonEventTypeSpec, userData, outRef uintptr) int32
	RemoveEventHandler  func(handlerRef uintptr) int32
	// RegisterEventHotKey(UInt32 keyCode, UInt32 modifiers, EventHotKeyID id,
	//                      EventTargetRef target, OptionBits options,
	//                      EventHotKeyRef *outRef)
	RegisterEventHotKey   func(keyCode, modifiers uint32, hotKeyID uint64, target, options, outRef uintptr) int32
	UnregisterEventHotKey func(hotkeyRef uintptr) int32
	// NewEventHandlerUPP is optional on 64-bit: UPPs are plain function
	// pointers there, so the callback pointer can be passed directly when this
	// compatibility symbol is absent.
	NewEventHandlerUPP func(proc uintptr) uintptr
}

// carbonLoad loads HIToolbox once; tests may replace it on darwin.
var carbonLoad = loadCarbonLib

var (
	carbonLoadOnce sync.Once
	carbonLoaded   *carbonAPI
	carbonLoadErr  error
)

// carbonFrameworkPaths are tried in order. Hot keys live in HIToolbox; the
// Carbon umbrella is a fallback for unusual layouts.
var carbonFrameworkPaths = []string{
	"/System/Library/Frameworks/Carbon.framework/Frameworks/HIToolbox.framework/HIToolbox",
	"/System/Library/Frameworks/Carbon.framework/Carbon",
}

// loadCarbonLib dlopens HIToolbox/Carbon and binds the hot-key surface.
func loadCarbonLib() (*carbonAPI, error) {
	carbonLoadOnce.Do(func() {
		var lastErr error
		for _, path := range carbonFrameworkPaths {
			api, err := bindCarbonLib(path)
			if err != nil {
				lastErr = err
				continue
			}
			carbonLoaded = api
			return
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("no Carbon/HIToolbox framework found")
		}
		carbonLoadErr = lastErr
	})
	return carbonLoaded, carbonLoadErr
}

// bindCarbonLib opens one framework path and binds the required symbols.
func bindCarbonLib(path string) (*carbonAPI, error) {
	handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("dlopen %s: %w", path, err)
	}
	api := &carbonAPI{}
	bindings := []struct {
		fptr any
		name string
	}{
		{&api.GetApplicationEventTarget, "GetApplicationEventTarget"},
		{&api.InstallEventHandler, "InstallEventHandler"},
		{&api.RemoveEventHandler, "RemoveEventHandler"},
		{&api.RegisterEventHotKey, "RegisterEventHotKey"},
		{&api.UnregisterEventHotKey, "UnregisterEventHotKey"},
	}
	for _, b := range bindings {
		if _, err := purego.Dlsym(handle, b.name); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", path, b.name, err)
		}
		purego.RegisterLibFunc(b.fptr, handle, b.name)
	}
	if _, err := purego.Dlsym(handle, "NewEventHandlerUPP"); err == nil {
		purego.RegisterLibFunc(&api.NewEventHandlerUPP, handle, "NewEventHandlerUPP")
	}
	return api, nil
}

// carbonCallbacks maps the per-handler userData key to the user callback. The
// Carbon event handler is a C callback, so it cannot receive a Go pointer; the
// key packs the backend identity and the manager-local handle.
var (
	carbonCallbacks  sync.Map // map[uint64]func()
	carbonBackendSeq atomic.Uint64
)

// carbonEventHandler is the Carbon EventHandlerProcPtr installed for every hot
// key; it dispatches to the user callback on its own goroutine so the AppKit
// main thread never runs user work.
var carbonEventHandler = purego.NewCallback(func(_ uintptr, _ uintptr, userData uintptr) int32 {
	if fn, ok := carbonCallbacks.Load(uint64(userData)); ok {
		go fn.(func())()
	}
	return 0
})

// carbonRegistration is one live Carbon registration.
type carbonRegistration struct {
	hotkeyRef  uintptr
	handlerRef uintptr
	key        uint64
	fn         func()
}

// carbonBackend implements backend with Carbon event hot keys.
type carbonBackend struct {
	api    *carbonAPI
	target uintptr
	seq    uint64

	mu     sync.Mutex
	regs   map[int]*carbonRegistration
	closed atomic.Bool
}

// platformBackend returns the macOS Carbon backend.
func platformBackend() (backend, error) {
	return newCarbonBackend()
}

// newCarbonBackend binds Carbon and resolves the application event target. A
// headless/non-GUI process (no AppKit event target) degrades to ErrUnsupported so
// startup keeps working with in-app shortcuts.
func newCarbonBackend() (backend, error) {
	api, err := carbonLoad()
	if err != nil {
		return nil, fmt.Errorf("%w: Carbon/HIToolbox unavailable: %v", ErrUnsupported, err)
	}
	target := api.GetApplicationEventTarget()
	if target == 0 {
		return nil, fmt.Errorf("%w: no application event target (global hotkeys need the GUI event loop)", ErrUnsupported)
	}
	return &carbonBackend{
		api:    api,
		target: target,
		seq:    carbonBackendSeq.Add(1),
		regs:   make(map[int]*carbonRegistration),
	}, nil
}

// register installs the event handler and registers the hot key.
func (b *carbonBackend) register(id int, h Hotkey, fn func()) error {
	if b.closed.Load() {
		return ErrClosed
	}
	if fn == nil {
		return fmt.Errorf("%w: nil callback", ErrInvalidHotkey)
	}
	keycode, ok := carbonVirtualKey(h.Key)
	if !ok {
		return fmt.Errorf("%w: key %q has no macOS virtual keycode", ErrInvalidHotkey, h.Key)
	}
	mods, ok := carbonModifierMask(h.Mods)
	if !ok {
		return fmt.Errorf("%w: invalid modifier set %d", ErrInvalidHotkey, h.Mods)
	}
	b.mu.Lock()
	if _, exists := b.regs[id]; exists {
		b.mu.Unlock()
		return fmt.Errorf("%w: handle %d", ErrAlreadyRegistered, id)
	}
	b.mu.Unlock()

	key := b.seq<<32 | uint64(uint32(id))
	spec := carbonHotKeyEventSpec()
	handler := carbonEventHandler
	if b.api.NewEventHandlerUPP != nil {
		handler = b.api.NewEventHandlerUPP(carbonEventHandler)
	}
	var handlerRef uintptr
	if status := b.api.InstallEventHandler(b.target, handler, 1, &spec, uintptr(key), uintptr(unsafe.Pointer(&handlerRef))); status != 0 {
		return fmt.Errorf("globalhotkey: InstallEventHandler: %w", carbonStatusError(status))
	}
	var hotkeyRef uintptr
	if status := b.api.RegisterEventHotKey(keycode, mods, carbonHotKeyID(id), b.target, 0, uintptr(unsafe.Pointer(&hotkeyRef))); status != 0 {
		_ = b.api.RemoveEventHandler(handlerRef)
		return carbonStatusError(status)
	}

	reg := &carbonRegistration{hotkeyRef: hotkeyRef, handlerRef: handlerRef, key: key, fn: fn}
	b.mu.Lock()
	if b.closed.Load() {
		b.mu.Unlock()
		_ = b.api.UnregisterEventHotKey(hotkeyRef)
		_ = b.api.RemoveEventHandler(handlerRef)
		return ErrClosed
	}
	b.regs[id] = reg
	b.mu.Unlock()
	carbonCallbacks.Store(key, fn)
	return nil
}

// unregister removes one hot key and its event handler.
func (b *carbonBackend) unregister(id int) error {
	b.mu.Lock()
	reg := b.regs[id]
	delete(b.regs, id)
	b.mu.Unlock()
	if reg == nil {
		return fmt.Errorf("%w: %d", ErrUnknownHandle, id)
	}
	carbonCallbacks.Delete(reg.key)
	var firstErr error
	if status := b.api.UnregisterEventHotKey(reg.hotkeyRef); status != 0 {
		firstErr = fmt.Errorf("globalhotkey: UnregisterEventHotKey: %w", carbonStatusError(status))
	}
	if status := b.api.RemoveEventHandler(reg.handlerRef); status != 0 && firstErr == nil {
		firstErr = fmt.Errorf("globalhotkey: RemoveEventHandler: %w", carbonStatusError(status))
	}
	return firstErr
}

// close unregisters every hot key and its handler. Idempotent.
func (b *carbonBackend) close() error {
	if b.closed.Swap(true) {
		return nil
	}
	b.mu.Lock()
	regs := b.regs
	b.regs = make(map[int]*carbonRegistration)
	b.mu.Unlock()
	for _, reg := range regs {
		carbonCallbacks.Delete(reg.key)
		_ = b.api.UnregisterEventHotKey(reg.hotkeyRef)
		_ = b.api.RemoveEventHandler(reg.handlerRef)
	}
	return nil
}
