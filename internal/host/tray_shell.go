package host

// The tray shell of the gateway product (S1 of the native-UI retirement).
//
// The desktop product owned a window: the tray's "show" raised it, the close
// button could hide it (contracts.SettingCloseToTray), and quit ran the mygo
// event loop. The browser product has no window, so the same tray keeps its
// icon/menu but "show" now opens the client's URL, and the close/quit policy
// belongs to the browser tab.
//
// Two guarantees make this safe on a server:
//   - the gateway starts first (runServe launches it in a goroutine), so a tray
//     that cannot start never costs the service;
//   - mygo.App.Run failing ("cannot open display") is a warning, not an error.
//     `--no-tray`, and a Linux session without DISPLAY/WAYLAND_DISPLAY, skip the
//     mygo event loop entirely (no attempt, no warning).

import (
	"context"
	"errors"
	"fmt"
	"os"
	goruntime "runtime"

	"github.com/egoist/mygo"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/gateway"
	"github.com/zeokeel/zeokeel/internal/lib/openurl"
)

// BrowserWindow is the tray's view of "the app window": in the browser product
// it is the client tab, addressed by the URL the gateway published.
//
// It implements the window surface tray.go already speaks (show/restore/focus/
// hide/close), so the tray code keeps its single code path: the desktop product
// passed a real *mygo.Window, the gateway product passes this.
type BrowserWindow struct {
	// Open opens the browser client. Nil means the tray still shows its menu but
	// the show item does nothing (used by tests).
	Open func() error
}

// Show opens the client (the tray icon click and the menu's show item).
func (w *BrowserWindow) Show() { w.open() }

// Restore opens the client: a browser tab is never minimized.
func (w *BrowserWindow) Restore() { w.open() }

// Focus opens the client.
func (w *BrowserWindow) Focus() { w.open() }

// Hide is a no-op: a browser tab cannot be hidden by the host.
func (w *BrowserWindow) Hide() {}

// OnClose registers nothing and returns a no-op unsubscribe: the browser tab's
// close button is not the host's to observe. The close-to-tray setting keeps a
// row in the settings store (a contract type) but has no effect here.
func (w *BrowserWindow) OnClose(func(*mygo.CloseEvent)) func() { return func() {} }

// open runs the injected opener, ignoring a missing one.
func (w *BrowserWindow) open() {
	if w.Open == nil {
		return
	}
	_ = w.Open()
}

// openClientURL opens the running gateway's browser client.
func openClientURL(dataDir string) error {
	info, err := gateway.ReadInfo(dataDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", gateway.InfoFileName, err)
	}
	return openurl.Open(info.URL())
}

// trayEnabled reports whether this process should run the tray shell at all.
//
// The flag wins over everything (a server admin asked for no tray). On Linux a
// session without a display cannot host a tray: the check is silent on purpose —
// the process is a server, not a broken desktop app.
func trayEnabled(noTray bool) bool {
	if noTray {
		return false
	}
	if goruntime.GOOS == "linux" {
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return false
		}
	}
	return true
}

// startTrayShell runs the tray on the CALLER's goroutine, which must be the
// process main goroutine (mygo requires it). It blocks until the app quits or
// the event loop fails to start.
//
// The gw pointer supplies the live run/approval counters; settings keeps the
// language and close-to-tray rows (the latter now only affects the menu label
// the desktop used to change).
func startTrayShell(ctx context.Context, dataDir string, settings *Settings,
	status func() (running, approvals int), english func() bool, quit func(), logger contracts.Logger) error {
	if logger == nil {
		logger = discardLogger{}
	}
	if quit == nil {
		quit = func() { mygo.App.Quit() }
	}
	window := &BrowserWindow{Open: func() error { return openClientURL(dataDir) }}
	actions := trayActions{
		English: english,
		Status:  status,
	}
	// The tray needs a ready application, and only the main thread may create it.
	mygo.App.WhenReady(func() {
		if _, err := setupTrayWithActions(window, nil, func() bool {
			return ctx.Err() == nil
		}, actions, logger); err != nil {
			logger.Printf("%s: tray unavailable: %v", appName, err)
		}
		// The global hotkey is a tray feature: pressing it is "open the client".
		// Best effort, exactly like the tray itself.
		if _, err := setupGlobalHotkey(window, settings, mygoHotkeyRegistrar{}); err != nil {
			logger.Printf("%s: global hotkey unavailable: %v", appName, err)
		}
	})
	// Quit requests (tray menu, SIGINT handled by the caller) end the loop.
	go func() {
		<-ctx.Done()
		mygo.App.Quit()
	}()
	if err := mygo.App.Run(); err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return nil
		}
		return err
	}
	return nil
}

// discardLogger is the no-op fallback for startTrayShell.
type discardLogger struct{}

// Printf implements contracts.Logger.
func (discardLogger) Printf(string, ...any) {}
