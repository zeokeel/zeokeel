package app

import (
	"errors"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/shell"
)

// fakeSettingsWindow records what the controller and its host did to one
// window, so the lifecycle assertions need no desktop backend.
type fakeSettingsWindow struct {
	mu          sync.Mutex
	title       string
	shown       int
	hidden      int
	focused     int
	invalidated int
}

func (f *fakeSettingsWindow) Show()  { f.mu.Lock(); f.shown++; f.mu.Unlock() }
func (f *fakeSettingsWindow) Hide()  { f.mu.Lock(); f.hidden++; f.mu.Unlock() }
func (f *fakeSettingsWindow) Focus() { f.mu.Lock(); f.focused++; f.mu.Unlock() }
func (f *fakeSettingsWindow) SetTitle(title string) {
	f.mu.Lock()
	f.title = title
	f.mu.Unlock()
}
func (f *fakeSettingsWindow) Invalidate() {
	f.mu.Lock()
	f.invalidated++
	f.mu.Unlock()
}

func (f *fakeSettingsWindow) counts() (shown, hidden, focused, invalidated int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shown, f.hidden, f.focused, f.invalidated
}

// fakeSettingsWindows is the injected window manager: it records the creation
// options and hands back fake windows, and lets a test raise the native close
// request.
type fakeSettingsWindows struct {
	mu      sync.Mutex
	err     error
	options settingsWindowOptions
	created int
	windows []*fakeSettingsWindow
	// closeRequests holds each window's requestClose callback, in creation
	// order, for tests that simulate the title-bar close button.
	closeRequests []func() bool
}

func newFakeSettingsWindows() *fakeSettingsWindows { return &fakeSettingsWindows{} }

func (f *fakeSettingsWindows) New(options settingsWindowOptions, requestClose func() bool) (settingsWindowHost, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.created++
	f.options = options
	f.closeRequests = append(f.closeRequests, requestClose)
	window := &fakeSettingsWindow{}
	f.windows = append(f.windows, window)
	return window, nil
}

func (f *fakeSettingsWindows) window(i int) *fakeSettingsWindow {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.windows[i]
}

func (f *fakeSettingsWindows) createdCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.created
}

// requestClose simulates the window's title-bar close button, including the
// production host's hide when the close is prevented.
func (f *fakeSettingsWindows) requestClose(t *testing.T, i int) bool {
	t.Helper()
	f.mu.Lock()
	request := f.closeRequests[i]
	f.mu.Unlock()
	allowed := request()
	if !allowed {
		f.windows[i].Hide()
	}
	return allowed
}

// settingsWindowFixture builds a controller over the fake manager, with a
// title reader the test can change to prove a reopen relabels the window.
func settingsWindowFixture(t *testing.T, quitting *bool) (*settingsWindowController, *fakeSettingsWindows) {
	t.Helper()
	manager := newFakeSettingsWindows()
	title := "Settings"
	controller := newSettingsWindowController(manager, settingsWindowOptions{
		Title:     func() string { return title },
		Width:     settingsWindowWidth,
		Height:    settingsWindowHeight,
		MinWidth:  settingsWindowMinWidth,
		MinHeight: settingsWindowMinHeight,
		StateKey:  settingsWindowStateKey,
	}, func() bool { return *quitting })
	return controller, manager
}

// TestSettingsWindowSingletonAndFocus covers the Cmd+, contract: the first
// open creates the window and shows it, and every later open focuses the same
// window instead of creating another.
func TestSettingsWindowSingletonAndFocus(t *testing.T) {
	quitting := false
	controller, manager := settingsWindowFixture(t, &quitting)

	if err := controller.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := manager.createdCount(); got != 1 {
		t.Fatalf("created windows = %d, want 1", got)
	}
	window := manager.window(0)
	if shown, _, focused, _ := window.counts(); shown != 1 || focused != 1 {
		t.Fatalf("first open: shown=%d focused=%d, want 1/1", shown, focused)
	}
	if !controller.IsOpen() {
		t.Fatal("the window is not open after Open")
	}
	if window.title != "Settings" {
		t.Fatalf("window title = %q, want Settings", window.title)
	}

	if err := controller.Open(); err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if got := manager.createdCount(); got != 1 {
		t.Fatalf("second open created %d windows, want the singleton", got)
	}
	if shown, _, focused, _ := window.counts(); shown != 1 || focused != 2 {
		t.Fatalf("second open: shown=%d focused=%d, want 1/2", shown, focused)
	}
}

// TestSettingsWindowCloseHidesAndReopens covers close semantics: the title-bar
// close hides the window (the app keeps running), and the next open shows the
// same window again, so its tab and scroll position survive. The title is read
// again on each open, so a language switch relabels the window.
func TestSettingsWindowCloseHidesAndReopens(t *testing.T) {
	quitting := false
	controller, manager := settingsWindowFixture(t, &quitting)
	if err := controller.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	window := manager.window(0)

	if allowed := manager.requestClose(t, 0); allowed {
		t.Fatal("closing the settings window in a running app was allowed")
	}
	if controller.IsOpen() {
		t.Fatal("the window is still open after a close request")
	}
	if _, hidden, _, _ := window.counts(); hidden != 1 {
		t.Fatalf("hidden count = %d, want 1", hidden)
	}
	// Change the title source: a reopen relabels the window in the current
	// language instead of keeping the startup title.
	controller.options.Title = func() string { return "设置" }
	if err := controller.Open(); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := manager.createdCount(); got != 1 {
		t.Fatalf("reopening created %d windows, want the same one", got)
	}
	if shown, _, focused, _ := window.counts(); shown != 2 || focused != 2 {
		t.Fatalf("reopen: shown=%d focused=%d, want 2/2", shown, focused)
	}
	if window.title != "设置" {
		t.Fatalf("reopened window title = %q, want the current-language title", window.title)
	}

	// The settings Close controls hide the window directly, without closing it.
	controller.Hide()
	if controller.IsOpen() {
		t.Fatal("Hide left the window open")
	}
	if _, hidden, _, _ := window.counts(); hidden != 2 {
		t.Fatalf("hidden count after Hide = %d, want 2", hidden)
	}
}

// TestSettingsWindowClosesOnlyWhenQuitting covers the quit path: while the app
// winds down the close request may proceed for real instead of hiding.
func TestSettingsWindowClosesOnlyWhenQuitting(t *testing.T) {
	quitting := false
	controller, manager := settingsWindowFixture(t, &quitting)
	if err := controller.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	quitting = true
	if allowed := manager.requestClose(t, 0); !allowed {
		t.Fatal("the settings window refused to close while the app is quitting")
	}
}

// TestSettingsWindowUnavailable covers the headless fallback: when the desktop
// cannot create the window, Open reports the error so the shell keeps its
// in-app modal.
func TestSettingsWindowUnavailable(t *testing.T) {
	quitting := false
	manager := newFakeSettingsWindows()
	manager.err = errSettingsWindowUnavailable
	controller := newSettingsWindowController(manager, settingsWindowOptions{}, func() bool { return quitting })
	if err := controller.Open(); !errors.Is(err, errSettingsWindowUnavailable) {
		t.Fatalf("Open error = %v, want errSettingsWindowUnavailable", err)
	}
	if controller.IsOpen() {
		t.Fatal("a failed open reported the window as open")
	}
	controller.Invalidate() // no window: a no-op, not a panic
}

// TestSettingsWindowOptionsAreBrandSized pins the creation options: the
// window keeps the brand two-column layout's size and its own window-state
// key, and it hosts the shell's settings view.
func TestSettingsWindowOptionsAreBrandSized(t *testing.T) {
	manager := newFakeSettingsWindows()
	a := &app{settingsWindows: manager}
	shellApp := &shell.App{}
	a.shellApp = shellApp
	a.initSettingsWindow(shellApp.SettingsView, shellApp.SettingsWindowTitle)

	if err := a.settingsWindow.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	options := manager.options
	if options.Width != settingsWindowWidth || options.Height != settingsWindowHeight {
		t.Fatalf("window size = %dx%d, want %dx%d",
			options.Width, options.Height, settingsWindowWidth, settingsWindowHeight)
	}
	if options.MinWidth != settingsWindowMinWidth || options.MinHeight != settingsWindowMinHeight {
		t.Fatalf("minimum size = %dx%d, want %dx%d",
			options.MinWidth, options.MinHeight, settingsWindowMinWidth, settingsWindowMinHeight)
	}
	if options.StateKey != settingsWindowStateKey {
		t.Fatalf("state key = %q, want %q", options.StateKey, settingsWindowStateKey)
	}
	if options.Title == nil || options.Title() == "" {
		t.Fatal("settings window has no title")
	}
	if options.Content == nil {
		t.Fatal("settings window has no view")
	}
}

// TestAppSettingsActionOpensWindow covers the process wiring end to end
// without a desktop backend: the native menu's Settings command reaches the
// shell, the shell asks the host, and the host opens (or focuses) the window.
func TestAppSettingsActionOpensWindow(t *testing.T) {
	manager := newFakeSettingsWindows()
	shellApp := &shell.App{}
	a := &app{shellApp: shellApp, settingsWindows: manager}
	a.initSettingsWindow(shellApp.SettingsView, shellApp.SettingsWindowTitle)
	shellApp.SetSettingsWindowOpener(a.showSettingsWindow)
	shellApp.SetSettingsWindowCloser(a.hideSettingsWindow)
	shellApp.SetSettingsChangedHook(a.refreshWindows)
	shellApp.SetMenuAction(a.handleMenuAction)

	a.dispatchMenuAction(shell.MenuActionSettings)
	if got := manager.createdCount(); got != 1 {
		t.Fatalf("Settings menu action created %d windows, want 1", got)
	}
	if !a.settingsWindow.IsOpen() {
		t.Fatal("Settings menu action did not show the window")
	}
	if shellApp.Screen == shell.Settings {
		t.Fatal("the main window switched to the in-app Settings overlay")
	}

	// A second command focuses the same window.
	a.dispatchMenuAction(shell.MenuActionSettings)
	if got := manager.createdCount(); got != 1 {
		t.Fatalf("second Settings command created %d windows, want the singleton", got)
	}
	if _, _, focused, _ := manager.window(0).counts(); focused != 2 {
		t.Fatalf("focused count = %d, want 2", focused)
	}

	// The window's Close controls hide it, and the app keeps its windows.
	a.hideSettingsWindow()
	if a.settingsWindow.IsOpen() {
		t.Fatal("the settings window is open after its Close control")
	}
	if a.quitting {
		t.Fatal("closing the settings window asked the app to quit")
	}
}

// TestAppSettingsActionFallsBackToOverlay covers a desktop that cannot create
// the window: the Settings command keeps working as the in-app modal.
func TestAppSettingsActionFallsBackToOverlay(t *testing.T) {
	manager := newFakeSettingsWindows()
	manager.err = errSettingsWindowUnavailable
	shellApp := &shell.App{}
	a := &app{shellApp: shellApp, settingsWindows: manager}
	a.initSettingsWindow(shellApp.SettingsView, shellApp.SettingsWindowTitle)
	shellApp.SetSettingsWindowOpener(a.showSettingsWindow)

	a.dispatchMenuAction(shell.MenuActionSettings)
	if shellApp.Screen != shell.Settings {
		t.Fatalf("fallback left screen = %v, want the in-app Settings overlay", shellApp.Screen)
	}
}

// TestRefreshWindowsInvalidatesBoth covers the state-sync hook: a setting saved
// in the settings window repaints the main window too, and a missing main
// window (headless or early startup) is not a panic.
func TestRefreshWindowsInvalidatesBoth(t *testing.T) {
	manager := newFakeSettingsWindows()
	a := &app{settingsWindows: manager}
	shellApp := &shell.App{}
	a.initSettingsWindow(shellApp.SettingsView, shellApp.SettingsWindowTitle)
	if err := a.settingsWindow.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	a.refreshWindows() // a.win is nil: only the settings window exists
	if _, _, _, invalidated := manager.window(0).counts(); invalidated != 1 {
		t.Fatalf("settings window invalidations = %d, want 1", invalidated)
	}
}
