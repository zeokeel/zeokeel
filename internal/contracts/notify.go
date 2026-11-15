package contracts

import (
	"strings"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Notification levels.
const (
	NotifyInfo    = "info"
	NotifySuccess = "success"
	NotifyWarning = "warning"
	NotifyError   = "error"
)

// Notification is one transient host toast.
type Notification struct {
	// ID identifies the notification for dismissal.
	ID string
	// Level is one of the Notify* constants.
	Level string
	// Text is the toast copy.
	// SessionID optionally names the conversation a click should open. The
	// desktop host records it when the notifier supports SessionNotifier, and
	// the in-app toast then offers an "open conversation" action with the same
	// behavior as the OS notification click.
	SessionID string
	Text      string
	// ActionLabel names the optional action button ("Undo"); Action runs on the
	// UI thread when it is pressed. Both empty/zero for a plain notification.
	// A session-tagged notification gets both filled by the desktop notifier
	// when the caller supplies no action of its own.
	ActionLabel string
	Action      func()
}

// Notifier lets modules raise transient toasts (the old shell's overlay
// notifications). The desktop host provides it; a UI module renders it.
type Notifier interface {
	// SetHandler installs the toast handler (nil clears it).
	SetHandler(fn func(Notification))
	// Notify posts one toast; the handler may be nil.
	Notify(level, text string) Notification
	// Dismiss removes one toast (idempotent).
	Dismiss(id string)
}

// KeyNotifier is provided by the desktop host.
var KeyNotifier = cordis.NewServiceKey[Notifier]("zeokeel/notifier")

// ActionNotifier is the optional half of a Notifier that shows one action
// button on a toast ("Undo" after a reversible deletion). A backend that does
// not implement it still gets the plain notification.
type ActionNotifier interface {
	NotifyAction(level, text, actionLabel string, action func()) Notification
}

// SystemNotifier is the optional half of a Notifier that can also show a
// notification through the operating system's notification center, and report
// whether the platform supports it. The desktop host implements it (mygo
// notifications); a backend that only implements Notifier still works.
//
// Only notifications important enough to reach the user outside the app (a
// background run finishing or failing, an approval waiting) go through
// NotifySystem: ordinary toasts such as "Copied" stay in the app, so the OS
// notification center is not spammed.
type SystemNotifier interface {
	// NotifySystem posts one notification that the desktop host also mirrors to
	// the OS notification center (best effort, respecting the system
	// notification setting). The in-app toast still shows.
	NotifySystem(level, text string) Notification
	// SystemNotificationsSupported reports whether the host installed a working
	// OS notification sink. The settings page disables its switch when false.
	SystemNotificationsSupported() bool
}

// SessionNotifier is the optional half of a Notifier that keeps the
// notification's originating session, so a click can open that conversation
// instead of only bringing the window to the front. The desktop host also puts
// the same "open conversation" action on the in-app toast, so the toast is a
// working entry point while the window is in front.
type SessionNotifier interface {
	// NotifySystemFor posts one OS-mirrored notification tagged with sessionID.
	// An empty sessionID behaves like NotifySystem.
	NotifySystemFor(level, text, sessionID string) Notification
	// SystemNotificationsSupported reports whether an OS sink is installed.
	SystemNotificationsSupported() bool
}

// SystemNotificationsEnabled normalizes the SettingSystemNotifications value:
// "false", "0" and "no" (case-insensitive, trimmed) disable OS notifications;
// anything else — including an empty, malformed or missing value — keeps them
// enabled, the product default.
func SystemNotificationsEnabled(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "false", "0", "no":
		return false
	default:
		return true
	}
}

// NotifyRunFinished posts the notification one background run raises when it
// finishes: success copy for a completed run, error copy for a failed one. The
// agent and automation backend modules call it when a run ends while the user
// may be looking elsewhere; the desktop host also mirrors it to an OS
// notification, and a click brings the app to the front. A nil notifier (a
// product without the desktop bridge) makes it a no-op.
func NotifyRunFinished(notifier Notifier, text string, failed bool) Notification {
	return NotifyRunFinishedForSession(notifier, text, failed, "")
}

// NotifyRunFinishedForSession is NotifyRunFinished with the originating session:
// a host with SessionNotifier records it so a notification click can open the
// conversation that finished.
func NotifyRunFinishedForSession(notifier Notifier, text string, failed bool, sessionID string) Notification {
	if notifier == nil {
		return Notification{}
	}
	level := NotifySuccess
	if failed {
		level = NotifyError
	}
	return notifyOutsideAppForSession(notifier, level, text, sessionID)
}

// NotifyApprovalWaiting posts the notification a run raises when it parks on an
// approval request: the run cannot continue until the user decides, so the
// level is warning. A nil notifier is a no-op.
func NotifyApprovalWaiting(notifier Notifier, text string) Notification {
	if notifier == nil {
		return Notification{}
	}
	return notifyOutsideApp(notifier, NotifyWarning, text)
}

// notifyOutsideAppForSession is notifyOutsideApp plus the optional session tag.
func notifyOutsideAppForSession(notifier Notifier, level, text, sessionID string) Notification {
	if sessionID != "" {
		if session, ok := notifier.(SessionNotifier); ok {
			return session.NotifySystemFor(level, text, sessionID)
		}
	}
	return notifyOutsideApp(notifier, level, text)
}

// notifyOutsideApp uses the optional OS half of the notifier when the host has
// one, and degrades to the plain in-app toast otherwise.
func notifyOutsideApp(notifier Notifier, level, text string) Notification {
	if system, ok := notifier.(SystemNotifier); ok {
		return system.NotifySystem(level, text)
	}
	return notifier.Notify(level, text)
}
