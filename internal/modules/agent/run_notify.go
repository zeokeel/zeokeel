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
// run. It stays quiet for the active session (the user is already watching it)
// and for a user Stop (Cancel is an explicit action, not news).
func (s *service) notifyBackgroundRunFinished(sessionID string, runErr error, runCtx context.Context) {
	if s == nil || s.notifier == nil {
		return
	}
	if runCtx != nil && runCtx.Err() != nil {
		return
	}
	s.mu.Lock()
	active := s.active
	title := ""
	if state, ok := s.sessions[sessionID]; ok {
		title = strings.TrimSpace(state.title)
	}
	s.mu.Unlock()
	if active == sessionID {
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
