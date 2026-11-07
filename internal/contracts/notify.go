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
