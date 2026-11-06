package app

import "github.com/egoist/mygo"

// Window geometry.
//
// ZeoKeel used to open its main window at a fixed 1600x1000 (min 900x600) and
// restore saved bounds unchanged. On a 1366x768 laptop that opens a window wider
// and taller than the screen: the title bar is reachable but the bottom/right of
// the UI is cut off (AR-18/PD-15). The same applies to the settings window.
//
// These helpers clamp a requested window size and a restored window state to the
// current display work area (which excludes the taskbar/dock/menu bar). They are
// pure functions over mygo.Display so they can be unit-tested headless; the
// production callers read the displays from mygo.Screen once the app is ready.

// mainWindowSize, mainWindowMinSize and settingsWindowSize are the preferred
// sizes before clamping to the display work area.
const (
	mainWindowWidth     = 1600
	mainWindowHeight    = 1000
	mainWindowMinWidth  = 900
	mainWindowMinHeight = 600
)

// primaryWorkArea returns the primary display's work area, or ok=false when the
// backend reports no displays (headless tests, exotic sessions).
func primaryWorkArea(displays []mygo.Display) (mygo.Rectangle, bool) {
	for _, display := range displays {
		if display.Primary {
			return display.WorkArea, validWorkArea(display.WorkArea)
		}
	}
	if len(displays) > 0 {
		return displays[0].WorkArea, validWorkArea(displays[0].WorkArea)
	}
	return mygo.Rectangle{}, false
}

// validWorkArea reports whether r describes a real screen area.
func validWorkArea(r mygo.Rectangle) bool {
	return r.Width > 0 && r.Height > 0
}

// clampSizeToWorkArea shrinks width/height to fit the work area and keeps the
// minimum size usable on small screens: a fixed 900x600 minimum would itself be
// larger than a 800x480 display. Values are never increased.
func clampSizeToWorkArea(width, height, minWidth, minHeight int, workArea mygo.Rectangle) (w, h, minW, minH int) {
	w, h, minW, minH = width, height, minWidth, minHeight
	if !validWorkArea(workArea) {
		return
	}
	if w > workArea.Width {
		w = workArea.Width
	}
	if h > workArea.Height {
		h = workArea.Height
	}
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	if minW > w {
		minW = w
	}
	if minH > h {
		minH = h
	}
	return
}

// nearestWorkArea returns the work area of the display that overlaps bounds the
// most, falling back to the primary work area when bounds is off every display
// (for example a monitor that has since been unplugged).
func nearestWorkArea(bounds mygo.Rectangle, displays []mygo.Display) (mygo.Rectangle, bool) {
	best := mygo.Rectangle{}
	bestOverlap := 0
	for _, display := range displays {
		if !validWorkArea(display.WorkArea) {
			continue
		}
		overlap := overlapArea(bounds, display.WorkArea)
		if overlap > bestOverlap {
			best, bestOverlap = display.WorkArea, overlap
		}
	}
	if bestOverlap > 0 {
		return best, true
	}
	return primaryWorkArea(displays)
}

// overlapArea returns the area of the intersection of a and b, or 0.
func overlapArea(a, b mygo.Rectangle) int {
	left := maxInt(a.X, b.X)
	top := maxInt(a.Y, b.Y)
	right := minInt(a.X+a.Width, b.X+b.Width)
	bottom := minInt(a.Y+a.Height, b.Y+b.Height)
	if right <= left || bottom <= top {
		return 0
	}
	return (right - left) * (bottom - top)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// clampWindowState fits a restored window state inside the nearest display work
// area: the size shrinks to fit and the position moves so the whole window is
// visible. Maximized/full-screen flags are preserved; a maximized window is
// already placed by the backend.
func clampWindowState(state mygo.WindowState, displays []mygo.Display) mygo.WindowState {
	if state.Width <= 0 || state.Height <= 0 || len(displays) == 0 {
		return state
	}
	in := mygo.Rectangle{X: state.X, Y: state.Y, Width: state.Width, Height: state.Height}
	workArea, ok := nearestWorkArea(in, displays)
	if !ok {
		return state
	}
	if in.X < workArea.X {
		in.X = workArea.X
	}
	if in.Y < workArea.Y {
		in.Y = workArea.Y
	}
	if in.Width > workArea.Width {
		in.Width = workArea.Width
	}
	if in.Height > workArea.Height {
		in.Height = workArea.Height
	}
	if in.X+in.Width > workArea.X+workArea.Width {
		in.X = workArea.X + workArea.Width - in.Width
	}
	if in.Y+in.Height > workArea.Y+workArea.Height {
		in.Y = workArea.Y + workArea.Height - in.Height
	}
	state.X, state.Y, state.Width, state.Height = in.X, in.Y, in.Width, in.Height
	return state
}

// mainWindowOptionsFor returns the main window's creation options with the
// preferred size clamped to the primary display's work area. Content is filled
// in by the caller (it needs the shell view).
func mainWindowOptionsFor(displays []mygo.Display) mygo.WindowOptions {
	width, height := mainWindowWidth, mainWindowHeight
	minWidth, minHeight := mainWindowMinWidth, mainWindowMinHeight
	if workArea, ok := primaryWorkArea(displays); ok {
		width, height, minWidth, minHeight = clampSizeToWorkArea(width, height, minWidth, minHeight, workArea)
	}
	return mygo.WindowOptions{
		Title:     "ZeoKeel",
		Width:     width,
		Height:    height,
		MinWidth:  minWidth,
		MinHeight: minHeight,
		StateKey:  windowStateKey,
	}
}

// currentDisplays returns the connected displays from the desktop backend. It is
// only called once the app is ready (window creation and window-state load), when
// the mygo Screen API has a backend.
func currentDisplays() []mygo.Display {
	return mygo.Screen.Displays()
}

// clampWindowStateMap clamps every persisted window state to the current display
// work areas, so a window saved on a larger monitor (or with stale bounds) is
// restored fully visible instead of off-screen.
func clampWindowStateMap(states map[string]mygo.WindowState, displays []mygo.Display) map[string]mygo.WindowState {
	if len(states) == 0 || len(displays) == 0 {
		return states
	}
	out := make(map[string]mygo.WindowState, len(states))
	for key, state := range states {
		out[key] = clampWindowState(state, displays)
	}
	return out
}
