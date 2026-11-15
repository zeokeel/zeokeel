package contracts

import (
	cordis "github.com/zeokeel/zeokeel-kernel"
)

// ForegroundSession reports the conversation the user is actually watching in
// the foreground of the desktop product: the session on screen right now, not
// merely the backend's active session.
//
// The distinction matters for background-run notifications: the agent silences
// a completed run for the conversation in front of the user, and a notification
// for a run the user cannot see is news. The backend's own active-session state
// cannot tell those apart — a session stays "active" while the user is on
// another screen (insights, settings, another overlay) or away from the window.
//
// It is optional: the desktop host provides it (internal/uiroot), and a backend
// running without it (headless products, tests) keeps its previous
// active-session rule.
type ForegroundSession interface {
	// ForegroundSession returns the conversation on screen in the foreground,
	// with ok=false when no conversation is in front of the user (another
	// screen is showing, no window is visible, or no session is open).
	ForegroundSession() (sessionID string, ok bool)
}

// KeyForegroundSession is provided by the desktop UI host (internal/uiroot) and
// consumed by the agent backend when a background run finishes. It is optional:
// a product without a desktop foreground read model provides nothing.
var KeyForegroundSession = cordis.NewServiceKey[ForegroundSession]("zeokeel/ui/foreground-session")
