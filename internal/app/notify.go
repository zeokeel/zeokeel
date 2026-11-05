package app

import (
	"context"
	"log"
	"strings"
	"sync"

	"github.com/egoist/mygo"

	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/shell"
	"github.com/zeokeel/zeokeel/internal/state"
)

// notifier is the desktop-notification seam. Show reports whether the
// notification was handed to the OS: false means notifications are unavailable
// (or the user denied them), and the caller keeps running without them. The
// production implementation wraps mygo's Notification API; tests inject a fake,
// so the notification logic needs no display, notification service or
// permission prompt.
type notifier interface {
	Show(title, body string, onClick func()) bool
}

// notificationGroup is the mygo notification stack group: the app's task
// notifications gather together in Notification Center (macOS) instead of
// mixing with the app's other notifications.
const notificationGroup = "zeokeel.tasks"

// noticeKind is one kind of desktop notification ZeoKeel raises.
type noticeKind int

const (
	// noticeTurnComplete is a turn that ended idle after producing output.
	noticeTurnComplete noticeKind = iota
	// noticeTurnFailed is a turn that ended in PhaseFailed.
	noticeTurnFailed
	// noticeApproval is a tool call or a question waiting for the user.
	noticeApproval
)

// String names the kind in logs.
func (k noticeKind) String() string {
	switch k {
	case noticeTurnComplete:
		return "turn_complete"
	case noticeTurnFailed:
		return "turn_failed"
	case noticeApproval:
		return "approval"
	}
	return "unknown"
}

// observedPhase is what the dispatcher remembers of a session between
// snapshots: enough to detect one phase transition, no message content.
type observedPhase struct {
	phase state.Phase
}

// notificationDispatcher turns session-snapshot phase transitions into desktop
// notifications. It is deliberately independent of both mygo (through notifier)
// and the shell: Observe takes immutable snapshots, and every dependency that
// touches the desktop (active window, language, click action) is an injectable
// function so tests cover the whole matrix headlessly.
//
// It reports at most one notification per transition:
//
//	running → idle                      turn complete (when the turn produced output)
//	running → failed (or waiting→failed) turn failed
//	any → waiting_approval/waiting_user  approval or question pending
//
// The first snapshot of a session only records a baseline: startup, a restored
// session or reopening the app must not replay old turns as notifications.
type notificationDispatcher struct {
	notifier notifier
	// active reports whether the user is looking at the app; notifications are
	// suppressed while it is true. nil means "assume the user is looking", the
	// safe default when the desktop state is unknown.
	active func() bool
	// language returns the UI language ("zh"/"en") for the copy. nil means
	// Chinese, the product default.
	language func() string
	// focusSession is the notification click action: bring the window forward
	// and, when the session is known, show that session.
	focusSession func(sessionID string)

	mu sync.Mutex
	// seen is the last phase observed per session id. Terminal phases are
	// dropped after handling, so the map stays bounded and the next turn starts
	// from a fresh baseline.
	seen map[string]observedPhase
}

// newNotificationDispatcher builds the dispatcher the app wires to the service
// update stream.
func newNotificationDispatcher(n notifier, active func() bool, language func() string, focusSession func(sessionID string)) *notificationDispatcher {
	return &notificationDispatcher{
		notifier:     n,
		active:       active,
		language:     language,
		focusSession: focusSession,
		seen:         make(map[string]observedPhase),
	}
}

// Observe folds one snapshot into the phase state machine and raises a
// notification for a qualifying transition. It is cheap on the common path (a
// token update) and safe to call from any goroutine, which the service update
// stream is not guaranteed to be.
func (d *notificationDispatcher) Observe(snap *state.Snapshot) {
	if d == nil || d.notifier == nil || snap == nil {
		return
	}
	key := snap.SessionID
	if key == "" {
		key = "default"
	}
	d.mu.Lock()
	if d.seen == nil {
		d.seen = make(map[string]observedPhase)
	}
	previous, seen := d.seen[key]
	d.seen[key] = observedPhase{phase: snap.Phase}
	d.mu.Unlock()

	kind, ok := notificationFor(seen, previous.phase, snap)
	if ok {
		// A notification is only useful while the user is not already looking
		// at the app. Skipping here (instead of queueing) also guarantees that
		// walking away after the turn finished cannot raise a late duplicate.
		if !d.isActive() {
			title, body := notificationText(d.currentLanguage(), kind)
			d.notifier.Show(title, body, d.clickAction(snap.SessionID))
		}
	}
	// A terminal phase has no further transition to report; forgetting it keeps
	// the per-session map small and avoids a stale baseline if the same session
	// is restored later.
	switch snap.Phase {
	case state.PhaseIdle, state.PhaseFailed:
		d.mu.Lock()
		if current, ok := d.seen[key]; ok && current.phase == snap.Phase {
			delete(d.seen, key)
		}
		d.mu.Unlock()
	}
}

// notificationFor maps one phase transition to the notification it raises.
func notificationFor(seen bool, previous state.Phase, snap *state.Snapshot) (noticeKind, bool) {
	if !seen {
		// Startup, restore or the first snapshot of a newly opened session:
		// record the baseline and stay quiet.
		return 0, false
	}
	if previous == snap.Phase {
		return 0, false
	}
	switch snap.Phase {
	case state.PhaseWaitingApproval, state.PhaseWaitingUser:
		return noticeApproval, true
	case state.PhaseFailed:
		if previous == state.PhaseRunning || previous == state.PhaseWaitingApproval ||
			previous == state.PhaseWaitingUser {
			return noticeTurnFailed, true
		}
	case state.PhaseIdle:
		if previous == state.PhaseRunning && turnHasOutput(snap) {
			return noticeTurnComplete, true
		}
	}
	return 0, false
}

// turnHasOutput reports whether the turn that just ended produced something the
// user would want to read. A turn stopped before the model streamed anything
// must not raise a "task complete" notification; the user's own prompt does not
// count as output.
func turnHasOutput(snap *state.Snapshot) bool {
	for i := len(snap.Messages) - 1; i >= 0; i-- {
		msg := snap.Messages[i]
		if msg.Role != state.RoleAssistant && msg.Role != state.RoleTool {
			continue
		}
		if strings.TrimSpace(msg.Text) != "" || strings.TrimSpace(msg.Reasoning) != "" {
			return true
		}
	}
	return len(snap.Tools) > 0
}

// isActive reports whether the user is looking at the app. Without a window or
// a probe, it assumes they are: silently dropping a notification is better than
// interrupting someone who is already reading the result.
func (d *notificationDispatcher) isActive() bool {
	return d.active == nil || d.active()
}

// currentLanguage returns the UI language the copy follows.
func (d *notificationDispatcher) currentLanguage() string {
	if d.language == nil {
		return session.LanguageChinese
	}
	return d.language()
}

// clickAction builds the per-notification focus callback. It captures the
// session id, never message content.
func (d *notificationDispatcher) clickAction(sessionID string) func() {
	focus := d.focusSession
	if focus == nil {
		return nil
	}
	return func() { focus(sessionID) }
}

// notificationText is the localized title/body pair for one notification. The
// copy names the event only: prompts, tool arguments, tool output, session
// titles and error text never leave the app, because notifications are shown
// on a lock screen and stay in the system's notification center.
func notificationText(lang string, kind noticeKind) (title, body string) {
	english := lang == session.LanguageEnglish
	switch kind {
	case noticeTurnComplete:
		if english {
			return "Task complete", "The agent finished its turn. Open ZeoKeel to review the result."
		}
		return "任务完成", "Agent 已完成本轮任务。打开 ZeoKeel 查看结果。"
	case noticeTurnFailed:
		if english {
			return "Task failed", "The agent stopped with an error. Open ZeoKeel for details."
		}
		return "任务失败", "Agent 在本轮执行中遇到错误。打开 ZeoKeel 查看详情。"
	case noticeApproval:
		if english {
			return "Approval needed", "A tool call is waiting for your approval. Open ZeoKeel to respond."
		}
		return "需要确认", "有工具调用正在等待你的批准。打开 ZeoKeel 处理。"
	}
	return "", ""
}

// systemNotifier is the production notifier. supported and show are seams so
// tests can cover the unsupported fallback and the option mapping without a
// display or a notification service.
type systemNotifier struct {
	// supported reports whether the OS can show notifications. nil uses mygo.
	supported func() bool
	// show delivers one notification. nil uses mygo.NewNotification.
	show func(title, body string, onClick func()) bool
}

// newSystemNotifier returns the production notifier: mygo's notification API
// with the real support probe and delivery.
func newSystemNotifier() systemNotifier { return systemNotifier{} }

// Show implements notifier. When notifications are unavailable it logs once per
// notification and reports false; the app keeps working without them (that is
// the degradation path on a headless Linux box or a macOS binary that was not
// packaged with an app bundle).
func (n systemNotifier) Show(title, body string, onClick func()) bool {
	supported := n.supported
	if supported == nil {
		supported = mygo.NotificationsSupported
	}
	if !supported() {
		log.Printf("zeokeel: desktop notifications unavailable; not showing %q", title)
		return false
	}
	show := n.show
	if show == nil {
		show = showMygoNotification
	}
	return show(title, body, onClick)
}

// showMygoNotification creates and shows one mygo notification. Show may block
// on the macOS permission prompt and waits for delivery, so it runs off the
// service update goroutine; the click handler is registered before showing.
func showMygoNotification(title, body string, onClick func()) bool {
	n := mygo.NewNotification(mygo.NotificationOptions{
		Title: title,
		Body:  body,
		Group: notificationGroup,
	})
	if onClick != nil {
		n.OnClick(onClick)
	}
	go func() {
		if err := n.Show(); err != nil {
			log.Printf("zeokeel: notification %q: %v", title, err)
		}
	}()
	return true
}

// windowActive reports whether the user is looking at the app: the main window
// is visible (not hidden to the tray) and has the keyboard focus (the app is the
// active one). Both mygo queries are safe from any goroutine.
func (a *app) windowActive() bool {
	win := a.win
	if win == nil {
		// No window exists yet (startup) or it is gone (quitting): do not
		// interrupt the user about work they cannot see anyway.
		return true
	}
	return win.IsVisible() && win.IsFocused()
}

// uiLanguage returns the language notification copy follows: the persisted
// UI language, Chinese when it is missing or the store fails (the product
// default), matching the shell.
func (a *app) uiLanguage() string {
	if a.services == nil {
		return session.LanguageChinese
	}
	cfg, err := a.services.Settings().Load()
	if err != nil || strings.TrimSpace(cfg.Language) == "" {
		return session.LanguageChinese
	}
	return cfg.Language
}

// focusSession is the notification click action: show the window, focus it and,
// when the session is still known, open that session so the user lands where
// the event happened. It goes through exported shell/controller APIs only, so
// the shell keeps owning its screen state.
func (a *app) focusSession(sessionID string) {
	if a.win != nil {
		a.win.Show()
		a.win.Focus()
	}
	if sessionID == "" || a.shellApp == nil || a.shellApp.Ctrl == nil {
		return
	}
	if a.shellApp.Screen == shell.Session && a.shellApp.Ctrl.CurrentSessionID() == sessionID {
		return
	}
	snap, err := a.shellApp.Ctrl.OpenSession(context.Background(), sessionID)
	if err != nil {
		log.Printf("zeokeel: open session %q from notification: %v", sessionID, err)
		return
	}
	a.shellApp.Screen = shell.Session
	a.shellApp.SetSnapshot(snap)
	if a.win != nil {
		a.win.Invalidate()
	}
}
