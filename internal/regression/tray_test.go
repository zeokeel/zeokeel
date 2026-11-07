package regression

// Adversarial audit: the desktop system tray (host half) and the close-to-tray
// policy. The host package unit-tests the wiring with fake seams; this file adds
// the regression-layer check (same product invariant, reached from the black-box
// test package) for: setting normalization, the tray options/icon/menu actions,
// factory failure fallback with zero side effects, close interception and the
// shutdown guard.
//
// tray.go's helpers are unexported; this test-only file reaches them through
// //go:linkname declarations with structurally identical parameter/result types
// (the same technique used for host.applyPendingUpdate). No production code is
// changed and the test never creates a native tray: the factory is a fake.

import (
	"bytes"
	"errors"
	"image/png"
	"strings"
	"testing"
	_ "unsafe"

	"github.com/egoist/mygo"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/host"
)

// trayWindowAlias mirrors host.trayWindow (the slice of *mygo.Window the tray
// drives). The method set and order must match host's interface.
type trayWindowAlias interface {
	Show()
	Restore()
	Focus()
	Hide()
	OnClose(func(*mygo.CloseEvent)) func()
}

// trayHandlerAlias mirrors host.trayHandler (show/quit closures by value).
type trayHandlerAlias struct {
	show func()
	quit func()
}

// trayHandleAlias mirrors host.trayHandle.
type trayHandleAlias interface {
	OnClick(func()) func()
	Destroy()
}

// hostCreateTray is host.createTray reached through a test-only linkname.
//
//go:linkname hostCreateTray github.com/zeokeel/zeokeel/internal/host.createTray
func hostCreateTray(window trayWindowAlias, handler trayHandlerAlias, closeToTray bool,
	keepAlive func() bool, factory func(mygo.TrayOptions) (trayHandleAlias, error)) (trayHandleAlias, error)

// hostCloseToTrayEnabled is host.closeToTrayEnabled reached by linkname.
//
//go:linkname hostCloseToTrayEnabled github.com/zeokeel/zeokeel/internal/host.closeToTrayEnabled
func hostCloseToTrayEnabled(settings *host.Settings) bool

// hostSetupTray is host.setupTray reached by linkname; only the nil-window guard
// is exercised (a real window would require a running mygo application).
//
//go:linkname hostSetupTray github.com/zeokeel/zeokeel/internal/host.setupTray
func hostSetupTray(window *mygo.Window, settings *host.Settings, keepAlive func() bool) (trayHandleAlias, error)

// trayFakeWindow records the window calls made through the tray.
type trayFakeWindow struct {
	shown, restored, focused, hidden int
	closeFn                          func(*mygo.CloseEvent)
	unregistered                     int
}

func (w *trayFakeWindow) Show()    { w.shown++ }
func (w *trayFakeWindow) Restore() { w.restored++ }
func (w *trayFakeWindow) Focus()   { w.focused++ }
func (w *trayFakeWindow) Hide()    { w.hidden++ }
func (w *trayFakeWindow) OnClose(fn func(*mygo.CloseEvent)) func() {
	w.closeFn = fn
	return func() { w.unregistered++ }
}

// trayFakeHandle records the tray listeners.
type trayFakeHandle struct {
	onClick   func()
	destroyed bool
}

func (t *trayFakeHandle) OnClick(fn func()) func() { t.onClick = fn; return func() {} }
func (t *trayFakeHandle) Destroy()                 { t.destroyed = true }

// newTraySettings builds host settings with one close-to-tray value.
func newTraySettings(t *testing.T, value string) *host.Settings {
	t.Helper()
	settings, err := host.NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	if value != "" {
		if err := settings.Set(contracts.SettingCloseToTray, value); err != nil {
			t.Fatalf("Set(closeToTray=%q): %v", value, err)
		}
	}
	return settings
}

// trayCounters counts handler invocations (shared with the closures).
type trayCounters struct {
	shows, quits int
}

// createFakeTray creates the tray with a recording factory.
func createFakeTray(t *testing.T, window trayWindowAlias, closeToTray bool, keepAlive func() bool,
) (*trayFakeHandle, *mygo.TrayOptions, *trayCounters, error) {
	t.Helper()
	handle := &trayFakeHandle{}
	counters := &trayCounters{}
	var options mygo.TrayOptions
	created, err := hostCreateTray(window, trayHandlerAlias{
		show: func() { counters.shows++ },
		quit: func() { counters.quits++ },
	}, closeToTray, keepAlive, func(o mygo.TrayOptions) (trayHandleAlias, error) {
		options = o
		return handle, nil
	})
	if err != nil {
		return nil, nil, counters, err
	}
	createdHandle, ok := created.(*trayFakeHandle)
	if !ok || createdHandle != handle {
		t.Fatalf("createTray returned %T, want the fake handle", created)
	}
	return handle, &options, counters, nil
}

// TestTrayCloseToTraySettingNormalization covers the setting normalization: only
// true/1/yes (any case, trimmed) enable close-to-tray; nil settings and every
// other value keep the default off.
func TestTrayCloseToTraySettingNormalization(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"true", true}, {"TRUE", true}, {" True ", true},
		{"1", true}, {"yes", true}, {"YES", true},
		{"", false}, {"false", false}, {"0", false}, {"no", false},
		{"banana", false}, {"2", false}, {"truee", false}, {"   ", false},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			settings := newTraySettings(t, test.value)
			if got := hostCloseToTrayEnabled(settings); got != test.want {
				t.Fatalf("closeToTrayEnabled(%q) = %v, want %v", test.value, got, test.want)
			}
		})
	}
	if hostCloseToTrayEnabled(nil) {
		t.Fatal("closeToTrayEnabled(nil) = true, want false")
	}
	// A stored "false" explicitly keeps the default off.
	settings := newTraySettings(t, "false")
	if hostCloseToTrayEnabled(settings) {
		t.Fatal("close-to-tray enabled by an explicit false setting")
	}
}

// TestTrayOptionsMenuAndActions covers the successful creation: the brand icon,
// id/tooltip, the show/quit menu and the click handler all reach the handlers.
func TestTrayOptionsMenuAndActions(t *testing.T) {
	window := &trayFakeWindow{}
	handle, options, counters, err := createFakeTray(t, window, false, nil)
	if err != nil {
		t.Fatalf("createTray: %v", err)
	}
	if options.ID != "zeokeel" || options.ToolTip != "ZeoKeel" {
		t.Fatalf("tray options id/tooltip = %q/%q", options.ID, options.ToolTip)
	}
	if len(options.Icon) == 0 {
		t.Fatal("tray options carry no icon")
	}
	if _, err := png.Decode(bytes.NewReader(options.Icon)); err != nil {
		t.Fatalf("tray icon is not a PNG: %v", err)
	}
	if options.Menu == nil {
		t.Fatal("tray options carry no menu")
	}
	if handle.onClick == nil {
		t.Fatal("tray click handler was not wired")
	}
	// Tray activation shows the window (click + the 显示 menu item).
	handle.onClick()
	if counters.shows != 1 {
		t.Fatalf("tray click shows = %d, want 1", counters.shows)
	}
	items := options.Menu.Items()
	shown := 0
	for _, item := range items {
		switch item.ID {
		case "zeokeel.show":
			shown++
			if item.Label != "显示 ZeoKeel" {
				t.Fatalf("show item label = %q", item.Label)
			}
			if item.Click != nil {
				item.Click(item, nil)
			}
		case "zeokeel.quit":
			if item.Label != "退出" {
				t.Fatalf("quit item label = %q", item.Label)
			}
			if item.Click != nil {
				item.Click(item, nil)
			}
		}
	}
	if shown != 1 {
		t.Fatalf("tray menu show items = %d, want 1", shown)
	}
	if counters.shows != 2 || counters.quits != 1 {
		t.Fatalf("after menu actions shows/quits = %d/%d, want 2/1", counters.shows, counters.quits)
	}
}

// TestTrayFactoryFailuresHaveNoSideEffects covers the best-effort fallback: a
// missing/erroring/nil-returning factory yields a readable error and registers
// no click or close listener (even when close-to-tray is enabled).
func TestTrayFactoryFailuresHaveNoSideEffects(t *testing.T) {
	window := &trayFakeWindow{}
	handler := trayHandlerAlias{show: func() {}, quit: func() {}}

	if _, err := hostCreateTray(window, handler, false, nil, nil); err == nil ||
		!strings.Contains(err.Error(), "no factory") {
		t.Fatalf("nil factory error = %v, want a readable no-factory error", err)
	}
	factoryErr := errors.New("no tray host under xvfb")
	if _, err := hostCreateTray(window, handler, true, nil,
		func(mygo.TrayOptions) (trayHandleAlias, error) { return nil, factoryErr }); err == nil ||
		!strings.Contains(err.Error(), "create system tray") || !errors.Is(err, factoryErr) {
		t.Fatalf("factory error = %v, want a wrapped create error", err)
	}
	if _, err := hostCreateTray(window, handler, true, nil,
		func(mygo.TrayOptions) (trayHandleAlias, error) { return nil, nil }); err == nil ||
		!strings.Contains(err.Error(), "no tray") {
		t.Fatalf("nil tray error = %v, want a readable no-tray error", err)
	}
	if window.closeFn != nil {
		t.Fatal("a failed tray creation registered a close handler")
	}
	if window.hidden != 0 {
		t.Fatalf("a failed tray creation hid the window %d times", window.hidden)
	}
	// The setupTray entry point rejects a nil window before any tray work.
	if _, err := hostSetupTray(nil, newTraySettings(t, "true"), nil); err == nil ||
		!strings.Contains(err.Error(), "window") {
		t.Fatalf("setupTray(nil window) error = %v, want a window requirement", err)
	}
}

// TestTrayCloseInterceptionAndShutdownGuard covers close-to-tray: with the tray
// alive a close hides the window and is prevented; shutting down (keepAlive
// false) lets the close proceed; with the setting off no close handler is
// registered at all.
func TestTrayCloseInterceptionAndShutdownGuard(t *testing.T) {
	// Close-to-tray on: the close is prevented and the window hidden.
	alive := true
	window := &trayFakeWindow{}
	if _, _, _, err := createFakeTray(t, window, true, func() bool { return alive }); err != nil {
		t.Fatalf("createTray(closeToTray): %v", err)
	}
	if window.closeFn == nil {
		t.Fatal("close-to-tray registered no close handler")
	}
	event := &mygo.CloseEvent{}
	window.closeFn(event)
	if !event.DefaultPrevented() {
		t.Fatal("close was not prevented while the tray keeps the app alive")
	}
	if window.hidden != 1 {
		t.Fatalf("window hidden %d times on close, want 1", window.hidden)
	}

	// Shutdown guard: during teardown the close proceeds normally.
	alive = false
	shutdownEvent := &mygo.CloseEvent{}
	window.closeFn(shutdownEvent)
	if shutdownEvent.DefaultPrevented() {
		t.Fatal("close was prevented during shutdown")
	}
	if window.hidden != 1 {
		t.Fatalf("window hidden %d times during shutdown, want unchanged (1)", window.hidden)
	}

	// Nil keepAlive means "not shutting down": the close is still intercepted.
	noGuard := &trayFakeWindow{}
	if _, _, _, err := createFakeTray(t, noGuard, true, nil); err != nil {
		t.Fatalf("createTray(no guard): %v", err)
	}
	noGuardEvent := &mygo.CloseEvent{}
	noGuard.closeFn(noGuardEvent)
	if !noGuardEvent.DefaultPrevented() || noGuard.hidden != 1 {
		t.Fatalf("nil keepAlive did not intercept the close (prevented=%v hidden=%d)",
			noGuardEvent.DefaultPrevented(), noGuard.hidden)
	}

	// Setting off: closing quits; no handler is registered.
	off := &trayFakeWindow{}
	if _, _, _, err := createFakeTray(t, off, false, func() bool { return true }); err != nil {
		t.Fatalf("createTray(closeToTray off): %v", err)
	}
	if off.closeFn != nil {
		t.Fatal("close-to-tray off registered a close handler")
	}
}
