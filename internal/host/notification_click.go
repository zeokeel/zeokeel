package host

// OS notification click routing: a click always brings the window to the front,
// and a notification tagged with a conversation (contracts.SessionNotifier) also
// opens that session. A click may arrive before the window and the assembly
// exist — a notification from an earlier run can launch the instance — so the
// target is kept until startup finishes (markReady) instead of being dropped.

import (
	"strings"
	"sync"

	"github.com/egoist/mygo"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// notificationClickDeps is the injected host around a notification click: the
// window, the conversation service, the UI root navigator and the notifier's
// session resolver. Every getter may be nil before startup finished.
type notificationClickDeps struct {
	window    func() *mygo.Window
	chat      func() contracts.ChatService
	navigator func() contracts.Navigator
	// session resolves a notification ID to the conversation it was tagged with.
	session func(notificationID string) (string, bool)
	logger  contracts.Logger
}

// notificationClicker routes App.OnNotificationClick. It is safe for use from
// the UI thread only (mygo fires the listener there), with the mutex covering
// the early-startup handoff with markReady.
type notificationClicker struct {
	deps notificationClickDeps

	mu      sync.Mutex
	ready   bool
	pending string
}

// newNotificationClicker builds the click router from its host dependencies.
func newNotificationClicker(deps notificationClickDeps) *notificationClicker {
	return &notificationClicker{deps: deps}
}

// handle is the App.OnNotificationClick listener: surface the window and, when
// the notification carries a session, open that conversation. A click that
// arrives before startup finished is kept and applied by markReady.
func (c *notificationClicker) handle(id string) {
	sessionID := c.sessionFor(id)
	c.bringFront()
	if sessionID == "" {
		return
	}
	c.mu.Lock()
	ready := c.ready
	if !ready {
		c.pending = sessionID
	}
	c.mu.Unlock()
	if !ready {
		return
	}
	c.openSession(sessionID)
}

// markReady runs once the assembly and the window exist: a click buffered during
// startup (the notification that launched the app) opens its conversation now.
func (c *notificationClicker) markReady() {
	c.mu.Lock()
	c.ready = true
	pending := c.pending
	c.pending = ""
	c.mu.Unlock()
	if pending != "" {
		c.openSession(pending)
	}
}

// sessionFor resolves the notification's tagged conversation ("" when none).
func (c *notificationClicker) sessionFor(id string) string {
	if c.deps.session == nil {
		return ""
	}
	sessionID, ok := c.deps.session(id)
	if !ok {
		return ""
	}
	return strings.TrimSpace(sessionID)
}

// bringFront shows and focuses the window; before the window exists it is a
// no-op (the startup show covers it).
func (c *notificationClicker) bringFront() {
	if c.deps.window == nil {
		return
	}
	notifyWindowFront(c.deps.window())
}

// openSession switches the active conversation and shows the session screen. A
// missing service, an unknown (deleted) session or a failing switch falls back
// to the window already being in front: a click never raises an error.
func (c *notificationClicker) openSession(sessionID string) {
	if c.deps.chat == nil {
		return
	}
	chat := c.deps.chat()
	if chat == nil {
		c.logf("notification click: no chat service; session %q not opened", sessionID)
		return
	}
	if _, ok := chat.Session(sessionID); !ok {
		c.logf("notification click: session %q no longer exists", sessionID)
		return
	}
	if err := chat.SetActiveSession(sessionID); err != nil {
		c.logf("notification click: switch to session %q: %v", sessionID, err)
		return
	}
	if c.deps.navigator == nil {
		return
	}
	navigator := c.deps.navigator()
	if navigator == nil {
		return
	}
	if err := navigator.Push("session"); err != nil {
		c.logf("notification click: show session screen: %v", err)
	}
}

// logf writes one click-routing note; a host without a logger stays silent.
func (c *notificationClicker) logf(format string, args ...any) {
	if c.deps.logger == nil {
		return
	}
	c.deps.logger.Printf(format, args...)
}
