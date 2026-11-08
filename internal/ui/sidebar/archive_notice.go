package sidebar

// The archive action's success feedback: archiving is reversible, so it is
// reported with a toast whose action restores the conversation. The toasts UI
// module renders the action button; a host whose notifier lacks the optional
// action half still gets the plain success toast.

import (
	"github.com/zeokeel/zeokeel/internal/contracts"
)

// actionNotifier is the optional notifier half that shows one action button on
// a toast (contracts.ActionNotifier). It is declared here so the sidebar keeps
// working when the host only provides the plain notifier.
type actionNotifier interface {
	NotifyAction(level, text, actionLabel string, action func()) contracts.Notification
}

// raiseSessionArchivedToast reports one archived conversation. When the host
// supports action toasts, the toast offers Undo, which unarchives the session
// again; otherwise it is a plain success toast.
func raiseSessionArchivedToast(state *paneState, chat contracts.ChatService, runtime contracts.UIRuntime, sessionID, title string) {
	if state == nil || state.notifier == nil {
		return
	}
	text := state.T("已归档会话：", "Session archived: ") + title
	undoLabel := state.T("撤销", "Undo")
	undo := func() {
		if archiver, ok := chat.(contracts.SessionArchiver); ok {
			_ = archiver.SetSessionArchived(sessionID, false)
		}
		runtime.Invalidate()
	}
	if notifier, ok := state.notifier.(actionNotifier); ok {
		notifier.NotifyAction(contracts.NotifySuccess, text, undoLabel, undo)
		return
	}
	state.notifier.Notify(contracts.NotifySuccess, text)
}
