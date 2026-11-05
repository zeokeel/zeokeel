// The independent settings window: a non-modal, native mygo window that hosts
// the same settings UI the main window's modal overlay used to show.
//
// The window is created lazily on the first open, focused on later opens, and
// hidden (not destroyed) when the user closes it, so reopening it keeps the
// tab and the scroll position. Only quitting the application closes it for
// real: its close handler asks the controller, exactly as the main window's
// close-to-hide handler does.
//
// The window plumbing sits behind two small interfaces (settingsWindowManager
// and settingsWindowHost) so the lifecycle, the singleton and the close
// semantics are unit tested without a desktop backend: mygo windows cannot be
// created in a headless test, and the controller must not need one to be
// tested.
package app

import (
	"errors"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// settingsWindowStateKey is the window-state key of the independent settings
// window. The app's window-state store keeps its bounds and maximized flag
// between launches, like the main window's, so the window reopens where the
// user left it.
const settingsWindowStateKey = "settings"

// errSettingsWindowUnavailable reports that no desktop backend could create
// the settings window (the application is not ready, as in tests or a headless
// run). The caller falls back to the in-app modal overlay.
var errSettingsWindowUnavailable = errors.New("app: settings window unavailable")

// settingsWindowOptions is what the settings window shows and how it is
// created. Content is the shell's own settings view, so the independent window
// and the in-app modal host one implementation.
type settingsWindowOptions struct {
	// Title returns the window title in the current language. It is read on
	// every open, so switching the language relabels the window.
	Title func() string
	// Width and Height are the window's initial size in DIPs; MinWidth and
	// MinHeight keep the two-column settings layout usable.
	Width, Height       int
	MinWidth, MinHeight int
	// StateKey persists the window's bounds in the app's window-state store.
	StateKey string
	// Content draws the settings UI.
	Content func(*ui.Context)
}

// settingsWindowHost is the minimal window the controller drives. The
// production host wraps a mygo.Window; tests use a fake, so no desktop backend
// is needed to test the lifecycle.
type settingsWindowHost interface {
	Show()
	Hide()
	Focus()
	SetTitle(string)
	Invalidate()
}

// settingsWindowManager creates the settings window. The production
// implementation wraps mygo.NewWindow; tests inject a fake.
type settingsWindowManager interface {
	// New creates the window and registers the close request: requestClose
	// reports whether the close may proceed (the app is quitting). When it
	// returns false the host keeps the window, hiding it instead, as the main
	// window's close-to-hide handler does.
	New(options settingsWindowOptions, requestClose func() bool) (settingsWindowHost, error)
}

// settingsWindowController owns the application's single settings window: it
// creates it on the first Open, focuses it on the next ones, and hides it on a
// close request so the UI state survives.
type settingsWindowController struct {
	mu      sync.Mutex
	manager settingsWindowManager
	options settingsWindowOptions
	// quitting reports whether the application is winding down: only then may
	// the window close for real.
	quitting func() bool
	window   settingsWindowHost
	// hidden is true while the window exists but is not on screen.
	hidden bool
}

// newSettingsWindowController returns a controller that creates its window
// through manager on the first Open. quitting may be nil (never quitting).
func newSettingsWindowController(manager settingsWindowManager, options settingsWindowOptions, quitting func() bool) *settingsWindowController {
	return &settingsWindowController{manager: manager, options: options, quitting: quitting}
}

// Open shows the settings window, or focuses it when it is already on screen.
// The second open never creates a second window: Settings is a singleton.
func (c *settingsWindowController) Open() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.window == nil {
		if c.manager == nil {
			return errSettingsWindowUnavailable
		}
		window, err := c.manager.New(c.options, c.closeRequested)
		if err != nil {
			return err
		}
		c.window = window
		c.hidden = true
	}
	if c.options.Title != nil {
		c.window.SetTitle(c.options.Title())
	}
	if c.hidden {
		c.window.Show()
		c.hidden = false
	}
	c.window.Focus()
	return nil
}

// Hide closes the settings window the way its Close controls and its title-bar
// close button do: it hides and the app keeps running, as the main window does.
func (c *settingsWindowController) Hide() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.window == nil {
		return
	}
	c.window.Hide()
	c.hidden = true
}

// Invalidate repaints the window when it exists, so a setting changed in the
// main window is visible in the settings window at once.
func (c *settingsWindowController) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.window != nil {
		c.window.Invalidate()
	}
}

// IsOpen reports whether the window exists and is visible.
func (c *settingsWindowController) IsOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.window != nil && !c.hidden
}

// closeRequested is the native close request the host asks before closing. It
// returns whether the close may proceed: only while the application quits.
// Otherwise the window hides and reopening it keeps its state.
func (c *settingsWindowController) closeRequested() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.quitting != nil && c.quitting() {
		return true
	}
	c.hidden = true
	return false
}

// mygoSettingsWindows is the production window manager: one mygo window per
// settings window, created hidden and shown on the first open.
type mygoSettingsWindows struct{}

// newMygoSettingsWindows returns the production manager.
func newMygoSettingsWindows() settingsWindowManager { return mygoSettingsWindows{} }

// New creates the mygo window. It returns errSettingsWindowUnavailable when the
// application is not ready (a headless run or a test), which makes Settings
// fall back to the in-app modal overlay instead of failing.
func (mygoSettingsWindows) New(options settingsWindowOptions, requestClose func() bool) (settingsWindowHost, error) {
	if !mygo.App.IsReady() {
		return nil, errSettingsWindowUnavailable
	}
	title := ""
	if options.Title != nil {
		title = options.Title()
	}
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:     title,
		Width:     options.Width,
		Height:    options.Height,
		MinWidth:  options.MinWidth,
		MinHeight: options.MinHeight,
		StateKey:  options.StateKey,
		Hidden:    true,
		Content:   ui.View(options.Content),
	})
	if win == nil || win.IsDestroyed() {
		return nil, errSettingsWindowUnavailable
	}
	win.OnClose(func(e *mygo.CloseEvent) {
		if requestClose() {
			return
		}
		// The app keeps running: hide the window and reopen it with its tab
		// and scroll position intact.
		e.PreventDefault()
		win.Hide()
	})
	return &mygoSettingsWindow{win: win}, nil
}

// mygoSettingsWindow is the production host around one mygo.Window.
type mygoSettingsWindow struct{ win *mygo.Window }

func (w *mygoSettingsWindow) Show()                 { w.win.Show() }
func (w *mygoSettingsWindow) Hide()                 { w.win.Hide() }
func (w *mygoSettingsWindow) Focus()                { w.win.Focus() }
func (w *mygoSettingsWindow) SetTitle(title string) { w.win.SetTitle(title) }
func (w *mygoSettingsWindow) Invalidate()           { w.win.Invalidate() }

// Settings window metrics. The window hosts the two-column settings layout, so
// it opens at a size that keeps the brand nav and a comfortable page column
// without the modal's 32 DIP card margin, and can shrink to 640x480. The height
// leaves room for the full first screen (title, tab rail and the last settings
// row) instead of clipping the bottom row (UX-06).
const (
	settingsWindowWidth     = 900
	settingsWindowHeight    = 840
	settingsWindowMinWidth  = 640
	settingsWindowMinHeight = 480
)

// initSettingsWindow wires the app's single settings window with the shell's
// settings view. It creates nothing yet: the window is created on the first
// open, so a user who never opens Settings pays no window cost.
func (a *app) initSettingsWindow(content func(*ui.Context), title func() string) {
	manager := a.settingsWindows
	if manager == nil {
		manager = newMygoSettingsWindows()
	}
	// Clamp the preferred size to the current display work area: the 900x840
	// settings window must still fit a 1366x768 laptop screen (AR-18/PD-15).
	// Tests and headless callers have no desktop backend, so the display query
	// only runs once the app is ready (it must never block a unit test).
	width, height, minWidth, minHeight := settingsWindowWidth, settingsWindowHeight, settingsWindowMinWidth, settingsWindowMinHeight
	if mygo.App.IsReady() {
		if workArea, ok := primaryWorkArea(currentDisplays()); ok {
			width, height, minWidth, minHeight = clampSizeToWorkArea(width, height, minWidth, minHeight, workArea)
		}
	}
	a.settingsWindow = newSettingsWindowController(manager, settingsWindowOptions{
		Title:     title,
		Width:     width,
		Height:    height,
		MinWidth:  minWidth,
		MinHeight: minHeight,
		StateKey:  settingsWindowStateKey,
		Content:   content,
	}, func() bool { return a.quitting })
}

// showSettingsWindow opens the settings window or focuses it when it is
// already open. It reports whether the window is on screen; false lets the
// shell fall back to the in-app modal when the desktop cannot create it.
func (a *app) showSettingsWindow() bool {
	if a.settingsWindow == nil {
		return false
	}
	return a.settingsWindow.Open() == nil
}

// hideSettingsWindow is the settings UI's Close: the window hides, the app
// keeps running, and reopening it shows the same tab and scroll position.
func (a *app) hideSettingsWindow() {
	if a.settingsWindow != nil {
		a.settingsWindow.Hide()
	}
}

// refreshWindows repaints both windows after a setting is saved in either one:
// a theme, language or conversation-size change made in the settings window
// applies to the main window at once, and the settings window repaints when
// the main window changes a setting.
func (a *app) refreshWindows() {
	if a.win != nil {
		a.win.Invalidate()
	}
	if a.settingsWindow != nil {
		a.settingsWindow.Invalidate()
	}
}
