package agent

// Background-run completion notification.
//
// When a run finishes while the user is looking at another conversation, the
// desktop product raises one toast (mirrored to the OS notification center by
// the host) so a background reply or failure is not missed. A click can open the
// originating session (contracts.SessionNotifier). The notifier is optional: a
// headless product has none and this is a no-op.

import (
	"context"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// notifyBackgroundRunFinished posts the completion notification for one settled
// run. It stays quiet for the conversation the user is watching — the desktop
// host's foreground session when it provides one, the backend's active session
// otherwise (headless products, tests) — and for a user Stop (Cancel is an
// explicit action, not news).
func (s *service) notifyBackgroundRunFinished(sessionID string, runErr error, runCtx context.Context) {
	if s == nil || s.notifier == nil {
		return
	}
	if runCtx != nil && runCtx.Err() != nil {
		return
	}
	s.mu.Lock()
	active := s.active
	foreground := s.foreground
	title := ""
	if state, ok := s.sessions[sessionID]; ok {
		title = strings.TrimSpace(state.title)
	}
	s.mu.Unlock()
	// The active session is the fallback read model; it cannot tell that the
	// user moved to another screen, so a host-provided foreground session takes
	// over the decision: quiet only for the conversation actually on screen, and
	// news for everything else (including the active session when another screen
	// is showing). The call runs after s.mu is released: the foreground service
	// may read the backend's active session.
	quiet := active == sessionID
	if foreground != nil {
		frontID, front := foreground.ForegroundSession()
		quiet = front && frontID == sessionID
	}
	if quiet {
		return
	}
	failed := runErr != nil
	label := "已完成"
	if failed {
		label = "运行失败"
	}
	text := ""
	if title != "" {
		text = "会话「" + title + "」" + label
	} else if failed {
		text = "后台会话运行失败"
	} else {
		text = "后台会话已完成"
	}
	contracts.NotifyRunFinishedForSession(s.notifier, text, failed, sessionID)
}
