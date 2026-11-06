package app

import (
	"testing"

	"github.com/egoist/mygo"
)

// display is a test helper for a primary display with a work area.
func display(x, y, width, height int) mygo.Display {
	return mygo.Display{
		Primary:  true,
		Bounds:   mygo.Rectangle{X: x, Y: y, Width: width, Height: height},
		WorkArea: mygo.Rectangle{X: x, Y: y, Width: width, Height: height},
	}
}

// TestMainWindowOptionsClampToWorkArea covers the AR-18/PD-15 default window:
// the preferred 1600x1000 must not open larger than a laptop work area.
func TestMainWindowOptionsClampToWorkArea(t *testing.T) {
	opts := mainWindowOptionsFor([]mygo.Display{display(0, 0, 1366, 728)})
	if opts.Width != 1366 || opts.Height != 728 {
		t.Fatalf("window options = %dx%d, want the 1366x728 work area", opts.Width, opts.Height)
	}
	if opts.MinWidth != mainWindowMinWidth || opts.MinHeight != mainWindowMinHeight {
		t.Fatalf("minimum size = %dx%d, want 900x600", opts.MinWidth, opts.MinHeight)
	}
	if opts.StateKey != windowStateKey {
		t.Fatalf("state key = %q, want %q", opts.StateKey, windowStateKey)
	}
}

// TestMainWindowOptionsSmallerThanMinimum keeps the minimum size usable when the
// work area itself is smaller than the old fixed 900x600 minimum.
func TestMainWindowOptionsSmallerThanMinimum(t *testing.T) {
	opts := mainWindowOptionsFor([]mygo.Display{display(0, 0, 800, 480)})
	if opts.Width != 800 || opts.Height != 480 {
		t.Fatalf("window options = %dx%d, want 800x480", opts.Width, opts.Height)
	}
	if opts.MinWidth != 800 || opts.MinHeight != 480 {
		t.Fatalf("minimum size = %dx%d, want it clamped to the work area", opts.MinWidth, opts.MinHeight)
	}
}

// TestMainWindowOptionsNoDisplays keeps the documented defaults when the backend
// reports no displays (for example a test environment).
func TestMainWindowOptionsNoDisplays(t *testing.T) {
	opts := mainWindowOptionsFor(nil)
	if opts.Width != mainWindowWidth || opts.Height != mainWindowHeight {
		t.Fatalf("window options = %dx%d, want the 1600x1000 defaults", opts.Width, opts.Height)
	}
	if opts.MinWidth != mainWindowMinWidth || opts.MinHeight != mainWindowMinHeight {
		t.Fatalf("minimum size = %dx%d, want 900x600", opts.MinWidth, opts.MinHeight)
	}
}

// TestClampWindowStateOffScreen moves a saved window fully back on screen.
func TestClampWindowStateOffScreen(t *testing.T) {
	states := map[string]mygo.WindowState{
		windowStateKey: {X: 5000, Y: 5000, Width: 1600, Height: 1000},
	}
	clamped := clampWindowStateMap(states, []mygo.Display{display(0, 0, 1920, 1040)})[windowStateKey]
	if clamped.X != 320 || clamped.Y != 40 || clamped.Width != 1600 || clamped.Height != 1000 {
		t.Fatalf("clamped state = %+v, want x=320 y=40 1600x1000 inside 1920x1040", clamped)
	}
}

// TestClampWindowStateShrinksOversized restores an over-large saved window to a
// size that fits the current monitor.
func TestClampWindowStateShrinksOversized(t *testing.T) {
	states := map[string]mygo.WindowState{
		windowStateKey: {X: 0, Y: 0, Width: 2560, Height: 1440},
	}
	clamped := clampWindowStateMap(states, []mygo.Display{display(0, 0, 1366, 728)})[windowStateKey]
	if clamped.Width != 1366 || clamped.Height != 728 {
		t.Fatalf("clamped size = %dx%d, want 1366x728", clamped.Width, clamped.Height)
	}
	if clamped.X != 0 || clamped.Y != 0 {
		t.Fatalf("clamped position = (%d,%d), want (0,0)", clamped.X, clamped.Y)
	}
}

// TestClampWindowStateKeepsSecondaryDisplay leaves a window on a connected
// secondary display (negative coordinates) untouched.
func TestClampWindowStateKeepsSecondaryDisplay(t *testing.T) {
	displays := []mygo.Display{
		display(0, 0, 1920, 1040),
		{Primary: false, WorkArea: mygo.Rectangle{X: -1280, Y: 0, Width: 1280, Height: 1024}},
	}
	states := map[string]mygo.WindowState{
		windowStateKey: {X: -1200, Y: 100, Width: 800, Height: 600},
	}
	clamped := clampWindowStateMap(states, displays)[windowStateKey]
	if clamped != states[windowStateKey] {
		t.Fatalf("clamped state = %+v, want the secondary-display state unchanged", clamped)
	}
}

// TestClampWindowStateRemovedMonitor brings a window saved on an unplugged
// monitor back to the primary display.
func TestClampWindowStateRemovedMonitor(t *testing.T) {
	states := map[string]mygo.WindowState{
		windowStateKey: {X: -1200, Y: 100, Width: 800, Height: 600},
	}
	clamped := clampWindowStateMap(states, []mygo.Display{display(0, 0, 1920, 1040)})[windowStateKey]
	if clamped.X != 0 || clamped.Y != 100 {
		t.Fatalf("clamped state = %+v, want x=0 y=100 on the primary display", clamped)
	}
}

// TestClampWindowStatePreservesFlags keeps maximized/full-screen state: the
// backend places a maximized window itself.
func TestClampWindowStatePreservesFlags(t *testing.T) {
	state := mygo.WindowState{X: 10, Y: 20, Width: 1600, Height: 1000, Maximized: true, FullScreen: false}
	clamped := clampWindowState(state, []mygo.Display{display(0, 0, 1366, 728)})
	if !clamped.Maximized || clamped.FullScreen {
		t.Fatalf("clamped flags = maximized:%v fullscreen:%v, want preserved", clamped.Maximized, clamped.FullScreen)
	}
	if clamped.Width != 1366 || clamped.Height != 728 {
		t.Fatalf("clamped size = %dx%d, want 1366x728", clamped.Width, clamped.Height)
	}
}

// TestSettingsWindowSizeClamped covers the settings window: the 900x840
// preferred height must not be taller than the work area (UX-06 + AR-18).
func TestSettingsWindowSizeClamped(t *testing.T) {
	width, height, minWidth, minHeight := clampSizeToWorkArea(
		settingsWindowWidth, settingsWindowHeight, settingsWindowMinWidth, settingsWindowMinHeight,
		mygo.Rectangle{Width: 1366, Height: 728})
	if width != 900 || height != 728 {
		t.Fatalf("settings size = %dx%d, want 900x728", width, height)
	}
	if minWidth != settingsWindowMinWidth || minHeight != settingsWindowMinHeight {
		t.Fatalf("settings minimum = %dx%d, want 640x480", minWidth, minHeight)
	}
}

// TestClampSizeNeverIncreases proves the helpers only shrink: a work area larger
// than the preferred size keeps the preferred size.
func TestClampSizeNeverIncreases(t *testing.T) {
	width, height, minWidth, minHeight := clampSizeToWorkArea(800, 600, 400, 300, mygo.Rectangle{Width: 3840, Height: 2160})
	if width != 800 || height != 600 || minWidth != 400 || minHeight != 300 {
		t.Fatalf("clamp grew the size: %dx%d min %dx%d", width, height, minWidth, minHeight)
	}
}
