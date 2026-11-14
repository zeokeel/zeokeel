package host

// Window-close and quit policy. The tray owns the close-to-tray path (tray.go):
// with contracts.SettingCloseToTray on, closing the window hides it and the app
// keeps running. Otherwise closing the last window quits; when backend work is
// still in flight (a run, an automation run, a job, an active goal) the guard
// asks the user first instead of dropping it silently. Explicit quits (Cmd+Q,
// the tray's 退出, an update restart) ask the same question.
//
// The work probe comes from the UI root (Root.RunningWork, which reads the live
// services) and both the probe and the confirmation dialog are injectable, so
// the policy is unit tested without a desktop session.

import (
	"sync/atomic"

	"github.com/egoist/mygo"
)

// closeGuardDeps is the injected policy of closeGuard.
type closeGuardDeps struct {
	// runningWork reports whether backend work would be interrupted by a quit.
	// Nil means nothing ever runs.
	runningWork func() bool
	// confirm asks the user whether to quit anyway. It runs on the UI thread
	// inside the close/quit event and may block on a native dialog. Nil counts
	// as "cancel": work is never dropped without an answer.
	confirm func() bool
	// closeToTray mirrors contracts.SettingCloseToTray at startup: the tray's
	// own close listener hides the window, so the guard defers to it.
	closeToTray bool
	// keepAlive reports whether the host is still running; false while tearing
	// down, when the close/quit must proceed without a prompt.
	keepAlive func() bool
}

// closeGuard is the process host's close/quit policy.
type closeGuard struct {
	deps closeGuardDeps
	// allowQuit remembers the user's answer so the close-then-quit sequence
	// (window close -> last-window quit -> OnBeforeQuit) asks once, not twice.
	allowQuit atomic.Bool
}

// newCloseGuard builds the policy from its injected dependencies.
func newCloseGuard(deps closeGuardDeps) *closeGuard {
	return &closeGuard{deps: deps}
}

// working reports whether a quit would drop backend work the user has not been
// asked about yet.
func (g *closeGuard) working() bool {
	if g == nil || g.allowQuit.Load() {
		return false
	}
	return g.deps.runningWork != nil && g.deps.runningWork()
}

// alive reports whether the host is still running (not tearing down).
func (g *closeGuard) alive() bool {
	return g == nil || g.deps.keepAlive == nil || g.deps.keepAlive()
}

// confirmQuit asks the user; a missing dialog cancels (keeps the work).
func (g *closeGuard) confirmQuit() bool {
	if g == nil || g.deps.confirm == nil {
		return false
	}
	return g.deps.confirm()
}

// handleClose is the main window's OnClose listener. It prevents the close when
// backend work is running and the user cancels the confirmation; a confirmed
// close proceeds (the last-window quit then goes through allowQuitEvent without
// asking again). With close-to-tray on, the tray's listener hides the window
// and the guard stays quiet.
func (g *closeGuard) handleClose(event *mygo.CloseEvent) {
	if !g.alive() {
		return
	}
	if g.deps.closeToTray {
		return
	}
	if !g.working() {
		return
	}
	if g.confirmQuit() {
		g.allowQuit.Store(true)
		return
	}
	if event != nil {
		event.PreventDefault()
	}
}

// allowQuitEvent is the OnBeforeQuit gate: it reports whether the quit may
// proceed. A quit the user already confirmed at the window closes without a
// second prompt; an OS session end cannot ask and must not block the teardown.
func (g *closeGuard) allowQuitEvent(event *mygo.QuitEvent) bool {
	if g == nil {
		return true
	}
	if event != nil && event.SessionEnding {
		return true
	}
	if !g.alive() {
		return true
	}
	if !g.working() {
		return true
	}
	if g.confirmQuit() {
		g.allowQuit.Store(true)
		return true
	}
	if event != nil {
		event.PreventDefault()
	}
	return false
}

// quitConfirmCopy is the native confirmation dialog's copy: quitting (closing
// the window on a quit-on-close build, Cmd+Q, the tray's 退出) while work runs
// stops that work, so the safe answer is the default and cancel button.
func quitConfirmCopy(english bool) (title, message, detail, cancelLabel, quitLabel string) {
	if english {
		return "Work is still running",
			"Quitting ZeoKeel stops the tasks that are still running.",
			"Background sessions, automation runs and jobs are not finished yet.",
			"Cancel", "Quit Anyway"
	}
	return "仍有任务在运行",
		"退出 ZeoKeel 会停止正在运行的任务。",
		"后台会话、自动化任务或作业尚未完成。",
		"取消", "仍然退出"
}

// confirmQuitWithRunningWork shows the native running-work confirmation,
// parented to the window, and reports whether the user chose to quit anyway. A
// dialog failure keeps the app open (the safe default).
func confirmQuitWithRunningWork(window *mygo.Window, english bool) bool {
	title, message, detail, cancelLabel, quitLabel := quitConfirmCopy(english)
	result, err := mygo.Dialog.Message(mygo.MessageOptions{
		Parent:        window,
		Type:          mygo.MessageWarning,
		Title:         title,
		Message:       message,
		Detail:        detail,
		Buttons:       []string{cancelLabel, quitLabel},
		DefaultButton: 0,
		CancelButton:  0,
		// The quit action discards work: keep it out of Enter's reach and mark
		// it as the destructive choice.
		DestructiveButtons: []int{1},
	})
	if err != nil {
		return false
	}
	return result.Button == 1
}
