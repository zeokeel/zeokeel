package contracts

import cordis "github.com/zeokeel/zeokeel-kernel"

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
	Text string
	// ActionLabel names the optional action button ("Undo"); Action runs on the
	// UI thread when it is pressed. Both empty/zero for a plain notification.
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
