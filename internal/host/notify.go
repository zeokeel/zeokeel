package host

// Desktop notification integration: the UI root's notifier mirrors
// contracts.SystemNotifier notifications (background run completions/failures,
// approval waits) to the OS once mygo is ready. This file is the mygo half: the
// platform sink the root installs and the click-to-front handler. It is best
// effort — a platform without notification support keeps the in-app toast only,
// and the settings page shows the switch disabled.

import (
	"context"
	"time"

	"github.com/egoist/mygo"
)

// osNotificationTimeout bounds one OS notification delivery. mygo's
// ShowContext may wait for the platform, and the first notification on macOS
// waits for the user's authorization answer; a user who ignores the prompt
// must not leave the delivery goroutine parked forever.
const osNotificationTimeout = 5 * time.Second

// mygoOSNotifier returns the OS notification sink backed by mygo's notification
// API. The UI root calls it from its own goroutine, so a delivery that waits for
// the platform never blocks a notification caller.
func mygoOSNotifier() func(id, title, body string) error {
	return func(id, title, body string) error {
		ctx, cancel := context.WithTimeout(context.Background(), osNotificationTimeout)
		defer cancel()
		return mygo.NewNotification(mygo.NotificationOptions{
			ID:    id,
			Title: title,
			Body:  body,
		}).ShowContext(ctx)
	}
}

// notifyWindowFront brings the main window to the front when the user clicks
// an OS notification. Notifications carry the app-level message only, so any
// click (also one on a notification of an earlier run, whose click launched
// this instance) simply surfaces the window.
func notifyWindowFront(window *mygo.Window) {
	if window == nil || window.IsDestroyed() {
		return
	}
	window.Show()
	window.Restore()
	window.Focus()
}
