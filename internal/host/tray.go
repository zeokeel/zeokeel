package host

// System tray integration: the desktop product adds a tray icon (brand mark)
// with a show/quit menu, and optionally hides the window instead of quitting
// when it is closed (contracts.SettingCloseToTray). The tray is best effort: a
// session without a tray host (bare Xvfb, for one) makes NewTray fail, and the
// host logs it and keeps running without a tray.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/egoist/mygo"

	"github.com/zeokeel/zeokeel/internal/brand"
	"github.com/zeokeel/zeokeel/internal/contracts"
)

// trayID is the tray icon's identity across launches (macOS menu-bar position).
const trayID = "zeokeel"

// trayHandler is the window/application side of the tray, injected by tests so
// the show/quit actions can be asserted without a desktop session. Its shape is
// part of the test-only linkname ABI in internal/regression, so it stays the
// show/quit pair; the extra app actions live in trayActions.
type trayHandler struct {
	show func()
	quit func()
}

// trayActions are the optional app actions the tray menu offers besides
// show/quit: start a fresh session and open settings. Both may be nil (the menu
// item is then omitted); the host wires them to ChatService and Navigator.
type trayActions struct {
	NewSession   func() error
	OpenSettings func()
}

// trayWindow is the slice of *mygo.Window the tray drives.
type trayWindow interface {
	Show()
	Restore()
	Focus()
	Hide()
	OnClose(func(*mygo.CloseEvent)) func()
}

// trayHandle is the slice of *mygo.Tray the host keeps; fakeable in tests.
type trayHandle interface {
	OnClick(func()) func()
	Destroy()
}

// trayFactory builds the native tray. Production uses newMygoTray; tests inject
// a fake to capture NewTray's options.
type trayFactory func(mygo.TrayOptions) (trayHandle, error)

// closeToTrayEnabled normalizes contracts.SettingCloseToTray: true/1/yes (any
// case, trimmed) enable hiding on close; empty, false, no, 0 or an unknown
// value keep the default (off, close quits).
func closeToTrayEnabled(settings *Settings) bool {
	if settings == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(settings.Get(contracts.SettingCloseToTray, "false"))) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

// setupTray creates the desktop tray and wires its actions. The window/menu
// show action restores and focuses the window; 退出 quits the application. When
// close-to-tray is enabled the window close is intercepted and hidden instead of
// quit. keepAlive lets the teardown path close the window normally (return false
// while shutting down). setupTray must run on the UI thread (App.WhenReady):
// NewTray requires the application to be ready.
// setupTray creates the desktop tray without app actions (kept as the stable
// entry point: internal/regression reaches it through a test-only linkname).
func setupTray(window *mygo.Window, settings *Settings, keepAlive func() bool) (trayHandle, error) {
	if window == nil {
		return nil, errors.New("system tray needs a window")
	}
	return setupTrayWithActions(window, settings, keepAlive, trayActions{}, nil)
}

// setupTrayWithActions is setupTray with the optional new-session/settings
// actions and the host logger for action-failure warnings.
func setupTrayWithActions(window trayWindow, settings *Settings, keepAlive func() bool,
	actions trayActions, logger contracts.Logger) (trayHandle, error) {
	return setupTrayWithFactory(window, settings, keepAlive, actions, newMygoTray, logger)
}

// setupTrayWithFactory is setupTrayWithActions with an injectable tray factory:
// tests use it to exercise the menu actions without a desktop session.
func setupTrayWithFactory(window trayWindow, settings *Settings, keepAlive func() bool,
	actions trayActions, factory trayFactory, logger contracts.Logger) (trayHandle, error) {
	if window == nil {
		return nil, errors.New("system tray needs a window")
	}
	handler := trayHandler{
		show: func() { showTrayWindow(window) },
		quit: func() { mygo.App.Quit() },
	}
	var warn func(format string, args ...any)
	if logger != nil {
		warn = logger.Printf
	}
	return createTrayWithActions(window, handler, actions, warn,
		closeToTrayEnabled(settings), keepAlive, factory)
}

// newMygoTray is the production tray factory.
func newMygoTray(options mygo.TrayOptions) (trayHandle, error) {
	tray, err := mygo.NewTray(options)
	if err != nil {
		return nil, err
	}
	return tray, nil
}

// createTray builds the tray icon/menu and wires the listeners. The icon and
// the menu's 显示 ZeoKeel item both run handler.show; 退出 runs handler.quit.
// A close-to-tray close handler is registered only after the tray exists; with
// keepAlive returning false (shutdown) the close proceeds normally. A factory
// error returns a readable error and registers nothing.
func createTray(window trayWindow, handler trayHandler, closeToTray bool,
	keepAlive func() bool, factory trayFactory) (trayHandle, error) {
	return createTrayWithActions(window, handler, trayActions{}, nil, closeToTray, keepAlive, factory)
}

// createTrayWithActions is createTray with the optional new-session/settings
// menu actions; warn receives a failed new-session warning (may be nil).
func createTrayWithActions(window trayWindow, handler trayHandler, actions trayActions,
	warn func(format string, args ...any), closeToTray bool, keepAlive func() bool,
	factory trayFactory) (trayHandle, error) {
	if factory == nil {
		return nil, errors.New("system tray has no factory")
	}
	options := mygo.TrayOptions{
		ID:      trayID,
		Icon:    brand.MarkPNGBytes(),
		ToolTip: appName,
		Menu:    newTrayMenuWithActions(handler, actions, warn),
	}
	tray, err := factory(options)
	if err != nil {
		return nil, fmt.Errorf("create system tray: %w", err)
	}
	if tray == nil {
		return nil, errors.New("system tray factory returned no tray")
	}
	if handler.show != nil {
		tray.OnClick(handler.show)
	}
	if closeToTray && window != nil {
		window.OnClose(func(event *mygo.CloseEvent) {
			if keepAlive != nil && !keepAlive() {
				return
			}
			if event != nil {
				event.PreventDefault()
			}
			window.Hide()
		})
	}
	return tray, nil
}

// newTrayMenuWithActions builds the tray menu: show the main window, the
// optional new-session/settings actions, a separator, then quit. Items whose
// action is nil are omitted so a host without the action simply does not show
// it. warn receives a failed new-session warning (may be nil).
func newTrayMenuWithActions(handler trayHandler, actions trayActions, warn func(format string, args ...any)) *mygo.Menu {
	items := []*mygo.MenuItem{
		{
			ID:    "zeokeel.show",
			Label: "显示 ZeoKeel",
			Click: func(*mygo.MenuItem, *mygo.Window) {
				if handler.show != nil {
					handler.show()
				}
			},
		},
	}
	if actions.NewSession != nil {
		items = append(items, &mygo.MenuItem{
			ID:    "zeokeel.new-session",
			Label: "新建会话",
			Click: func(*mygo.MenuItem, *mygo.Window) {
				if err := actions.NewSession(); err != nil {
					if warn != nil {
						warn("%s: tray new session: %v", appName, err)
					}
					return
				}
				if handler.show != nil {
					handler.show()
				}
			},
		})
	}
	if actions.OpenSettings != nil {
		items = append(items, &mygo.MenuItem{
			ID:    "zeokeel.settings",
			Label: "设置",
			Click: func(*mygo.MenuItem, *mygo.Window) {
				actions.OpenSettings()
				if handler.show != nil {
					handler.show()
				}
			},
		})
	}
	items = append(items, mygo.Separator(), &mygo.MenuItem{
		ID:    "zeokeel.quit",
		Label: "退出",
		Click: func(*mygo.MenuItem, *mygo.Window) {
			if handler.quit != nil {
				handler.quit()
			}
		},
	})
	return mygo.NewMenu(items)
}

// showTrayWindow restores and focuses the main window (tray activation).
func showTrayWindow(window trayWindow) {
	if window == nil {
		return
	}
	window.Show()
	window.Restore()
	window.Focus()
}
