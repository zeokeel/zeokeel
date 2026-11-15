package uiroot

// The foreground-session read model: which conversation is actually on screen,
// so a background-run completion notification is silent only for the
// conversation the user is watching. The backend's active session cannot answer
// that on its own — the user can leave the conversation screen (insights,
// settings, another overlay) while the session stays active.
//
// The desktop host provides this as contracts.KeyForegroundSession before any
// module loads; the agent reads it optionally. The service reads the live
// navigation and chat state on every call (never a copied snapshot), because
// the notification path runs off the UI thread when a run settles.

import (
	"strings"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// foregroundSession implements contracts.ForegroundSession for the desktop
// host. A zero-value root (nil) has nothing in the foreground.
type foregroundSession struct {
	root *Root
}

// ForegroundSession returns the conversation on screen in the foreground: the
// chat backend's active session, but only while the conversation screen is the
// one on top. On any other screen (insights, settings, a palette result) the
// user is not watching a conversation, so ok is false — even when the backend's
// active session has not changed yet.
func (f foregroundSession) ForegroundSession() (string, bool) {
	root := f.root
	if root == nil || root.nav == nil || root.nav.Current() != sessionScreenID {
		return "", false
	}
	// The chat backend comes from the embedder (tests) or the kernel (the
	// desktop host provides this service before the agent provides KeyChat).
	root.mu.Lock()
	chat := root.chatService
	kernel := root.kernelContext
	root.mu.Unlock()
	if chat == nil && kernel != (cordis.Context{}) {
		if fromKernel, ok := kernel.Get(contracts.KeyChat); ok {
			chat = fromKernel
		}
	}
	if chat == nil {
		return "", false
	}
	sessionID := strings.TrimSpace(chat.ActiveSession())
	if sessionID == "" {
		return "", false
	}
	return sessionID, true
}

// ForegroundSession returns the host's foreground-session service for the
// process assembly: the host provides it as contracts.KeyForegroundSession
// before any module loads. A nil root has none.
func (r *Root) ForegroundSession() contracts.ForegroundSession {
	if r == nil {
		return nil
	}
	return foregroundSession{root: r}
}
