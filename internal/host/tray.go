package host

// System tray integration: the desktop product adds a tray icon (brand mark)
// with a show/quit menu, and optionally hides the window instead of quitting
// when it is closed (contracts.SettingCloseToTray). The tray is best effort: a
// session without a tray host (bare Xvfb, for one) makes NewTray fail, and the
// host logs it and keeps running without a tray.

import (
	"errors"
	"fmt"
	"strconv"
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
//
// English and Status are the live chrome: the product language for the menu
// labels and tooltip, and the run/approval counters the tray mirrors (P2-9).
// Both may be nil: a bare test host keeps the product's Chinese labels and no
// activity line, so the tray still works without the desktop services.
type trayActions struct {
	NewSession   func() error
	OpenSettings func()
	English      func() bool
	Status       func() (running, approvals int)
}

// trayEnglish reports whether the tray copy is English (the product language).
func trayEnglish(actions trayActions) bool {
	return actions.English != nil && actions.English()
}

// trayActivity reads the live run/approval counters; a host without the status
// provider reports no activity.
func trayActivity(actions trayActions) (running, approvals int) {
	if actions.Status == nil {
		return 0, 0
	}
	return actions.Status()
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
	tray, _, err := setupTrayWithActivity(window, settings, keepAlive, actions, newMygoTray, logger)
	return tray, err
}

// setupTrayWithActivity is setupTrayWithActions returning the refresh function
// the host calls when the run/approval state or the product language changes.
// The tray's tooltip and menu then follow the live state (P2-9); a created tray
// whose handle cannot update keeps the chrome it was created with, so a bare
// test handle or a platform without hover text still works.
func setupTrayWithActivity(window trayWindow, settings *Settings, keepAlive func() bool,
	actions trayActions, factory trayFactory, logger contracts.Logger) (trayHandle, func(), error) {
	if window == nil {
		return nil, nil, errors.New("system tray needs a window")
	}
	handler := trayHandler{
		show: func() { showTrayWindow(window) },
		quit: func() { mygo.App.Quit() },
	}
	var warn func(format string, args ...any)
	if logger != nil {
		warn = logger.Printf
	}
	tray, err := createTrayWithActions(window, handler, actions, warn,
		closeToTrayEnabled(settings), keepAlive, factory)
	if err != nil {
		return nil, nil, err
	}
	view := trayStatusView{handler: handler, actions: actions, warn: warn}
	return tray, func() { refreshTrayActivity(tray, view) }, nil
}

// setupTrayWithFactory is setupTrayWithActivity with an injectable tray factory:
// tests use it to exercise the menu actions without a desktop session.
func setupTrayWithFactory(window trayWindow, settings *Settings, keepAlive func() bool,
	actions trayActions, factory trayFactory, logger contracts.Logger) (trayHandle, error) {
	tray, _, err := setupTrayWithActivity(window, settings, keepAlive, actions, factory, logger)
	return tray, err
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
	running, approvals := trayActivity(actions)
	options := mygo.TrayOptions{
		ID:      trayID,
		Icon:    brand.MarkPNGBytes(),
		ToolTip: activityStatusText(appName, running, approvals, trayEnglish(actions)),
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

// activityStatusText is the app name plus the live activity the window title and
// the tray tooltip show: "ZeoKeel · 运行中 2", "ZeoKeel · 等待审批", both in that
// order, and the app name alone when nothing runs and no approval waits. english
// localizes the whole line to the product language.
func activityStatusText(appName string, running, approvals int, english bool) string {
	parts := []string{appName}
	if running > 0 {
		if english {
			parts = append(parts, "Running "+strconv.Itoa(running))
		} else {
			parts = append(parts, "运行中 "+strconv.Itoa(running))
		}
	}
	if approvals > 0 {
		if english {
			parts = append(parts, "Waiting for approval")
		} else {
			parts = append(parts, "等待审批")
		}
	}
	return strings.Join(parts, " · ")
}

// trayActivityLine is the tray menu's activity line: the counters without the
// app name (the menu already belongs to the app), or "" when idle.
func trayActivityLine(running, approvals int, english bool) string {
	line := activityStatusText("", running, approvals, english)
	return strings.TrimPrefix(line, " · ")
}

// trayMenuLabels returns the tray menu's copy in the product language.
func trayMenuLabels(english bool) (show, newSession, settings, quit string) {
	if english {
		return "Show ZeoKeel", "New session", "Settings", "Quit"
	}
	return "显示 ZeoKeel", "新建会话", "设置", "退出"
}

// trayStatusUpdater is the optional part of a tray handle that can change its
// tooltip and menu after creation. mygo's tray implements it; a bare handle (a
// test fake, a platform without hover text) need not, and then the tray keeps
// the chrome it was created with.
type trayStatusUpdater interface {
	SetToolTip(tip string)
	SetMenu(menu *mygo.Menu)
}

// trayStatusView is what the host needs to re-apply the tray's live chrome: the
// handlers/actions the menu runs, the action-failure warning, and the app name
// of the tooltip.
type trayStatusView struct {
	handler trayHandler
	actions trayActions
	warn    func(format string, args ...any)
}

// refreshTrayActivity re-applies the localized activity tooltip and menu to a
// tray that supports updates. The host calls it when a run or an approval
// changes and when the product language switches.
func refreshTrayActivity(tray trayHandle, view trayStatusView) {
	updater, ok := tray.(trayStatusUpdater)
	if !ok {
		return
	}
	running, approvals := trayActivity(view.actions)
	updater.SetToolTip(activityStatusText(appName, running, approvals, trayEnglish(view.actions)))
	updater.SetMenu(newTrayMenuWithActions(view.handler, view.actions, view.warn))
}

// newTrayMenuWithActions builds the tray menu: show the main window, the
// optional new-session/settings actions, a separator, then quit. Items whose
// action is nil are omitted so a host without the action simply does not show
// it. warn receives a failed new-session warning (may be nil).
func newTrayMenuWithActions(handler trayHandler, actions trayActions, warn func(format string, args ...any)) *mygo.Menu {
	english := trayEnglish(actions)
	showLabel, newSessionLabel, settingsLabel, quitLabel := trayMenuLabels(english)
	items := []*mygo.MenuItem{
		{
			ID:    "zeokeel.show",
			Label: showLabel,
			Click: func(*mygo.MenuItem, *mygo.Window) {
				if handler.show != nil {
					handler.show()
				}
			},
		},
	}
	// The live activity is information, not an action: a disabled line under
	// the show item, present only while something runs or an approval waits.
	if running, approvals := trayActivity(actions); running > 0 || approvals > 0 {
		items = append(items, &mygo.MenuItem{
			ID:       "zeokeel.status",
			Label:    trayActivityLine(running, approvals, english),
			Disabled: true,
		})
	}
	if actions.NewSession != nil {
		items = append(items, &mygo.MenuItem{
			ID:    "zeokeel.new-session",
			Label: newSessionLabel,
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
			Label: settingsLabel,
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
		Label: quitLabel,
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
