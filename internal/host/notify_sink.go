package host

// Wiring the OS notification sink (AUDIT-2026-11-24 A-7).
//
// The OS sink used to live in the desktop tray shell. It is now the platform's
// own command-line notifier (internal/host/osnotify.go): the browser client is
// the window, but "tell me when it is done" must still work with the tab closed.
// This file is the link between the gateway's toast stream and that sink: the
// notifications the caller marked System are delivered, unless the user turned
// the setting off.

import (
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// installSystemNotifier registers the OS notification sink on the gateway's
// notifier. It is best effort: an OS without notification support simply keeps
// the in-app toasts.
//
// The seam is contracts.Notifier's own SetHandler: the gateway already exposes
// it, so the host needs no extra capability surface.
func installSystemNotifier(notifier contracts.Notifier, settings *Settings, logger contracts.Logger) {
	installSystemNotifierWith(notifier, settings, logger, systemNotifier())
}

// installSystemNotifierWith is installSystemNotifier with an injectable sink
// (the tests use it; production passes the platform command).
func installSystemNotifierWith(notifier contracts.Notifier, settings *Settings, logger contracts.Logger,
	sink func(id, title, body string) error) {
	if notifier == nil || sink == nil {
		return
	}
	notifier.SetHandler(func(notification contracts.Notification) {
		if !notification.System {
			// Ordinary toasts ("Copied") stay in the app.
			return
		}
		if settings != nil && !contracts.SystemNotificationsEnabled(settings.Get(contracts.SettingSystemNotifications, "")) {
			return
		}
		title := appName
		if text := strings.TrimSpace(notification.Text); text != "" {
			title = text
		}
		// Delivery may wait for the platform (macOS asks for authorization on the
		// first notification), so it never runs on the caller's goroutine.
		go func() {
			if err := sink(notification.ID, appName, title); err != nil && logger != nil {
				logger.Printf("%s: os notification: %v", appName, err)
			}
		}()
	})
}
