package app

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

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

// turnNoticeThrottle is the minimum gap between two "task complete"
// notifications for the same session on the controller-hook path. Goal and
// automation continuations can finish turns seconds apart; one notification per
// window is enough, and the next one is available as soon as the window passes.
// Failures bypass the throttle.
const turnNoticeThrottle = 5 * time.Second

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
//
// Snapshot observation only covers the current session, because the controller
// publishes OnUpdate for that one. Wiring the controller's TurnFinishedHook
// (useTurnFinishedHook + observeTurnFinished) covers terminal turns of *every*
// session, including background ones; wiring the ApprovalRequestedHook
// (useApprovalHook + observeApprovalRequested) covers pending approvals of
// every session. The snapshot machine then only fills the gaps left by the
// hooks, so one event cannot notify twice.
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
	// turnsFromHook reports terminal turns through the controller hook instead
	// of the snapshot phase machine, so background sessions are covered too.
	turnsFromHook bool
	// approvalsFromHook reports pending approvals through the controller hook
	// instead of the snapshot phase machine, so background sessions are covered
	// too.
	approvalsFromHook bool
	// now is the clock of the turn throttle. nil uses time.Now; tests replace it.
	now func() time.Time
	// turnThrottle is the minimum gap between completion notifications for one
	// session on the hook path. Zero disables the throttle (failures bypass it
	// regardless).
	turnThrottle time.Duration

	mu sync.Mutex
	// seen is the last phase observed per session id. Terminal phases are
	// dropped after handling, so the map stays bounded and the next turn starts
	// from a fresh baseline.
	seen map[string]observedPhase
	// lastTurn is the last turn id the hook reported per session: the defensive
	// session+turn dedupe.
	lastTurn map[string]string
	// lastTurnNotice is when the hook path last notified per session, for the
	// completion throttle.
	lastTurnNotice map[string]time.Time
	// approvalCalls is the defensive session+call dedupe of the approval hook.
	approvalCalls map[string]struct{}
}

// approvalDedupeCap bounds the approval-hook dedupe map. The controller already
// reports each call id once, so dropping the history after the cap only makes a
// duplicate notification possible for an id that old, never a missed approval.
const approvalDedupeCap = 1024

// newNotificationDispatcher builds the dispatcher the app wires to the service
// update stream.
func newNotificationDispatcher(n notifier, active func() bool, language func() string, focusSession func(sessionID string)) *notificationDispatcher {
	return &notificationDispatcher{
		notifier:       n,
		active:         active,
		language:       language,
		focusSession:   focusSession,
		turnThrottle:   turnNoticeThrottle,
		seen:           make(map[string]observedPhase),
		lastTurn:       make(map[string]string),
		lastTurnNotice: make(map[string]time.Time),
		approvalCalls:  make(map[string]struct{}),
	}
}

// useTurnFinishedHook moves terminal-turn reporting to the controller's
// TurnFinishedHook, so background sessions are covered too. Production wires it
// together with SetOnTurnFinished; the snapshot phase machine then only reports
// approval waits, which is what keeps one turn from raising two notifications.
// Headless snapshot tests leave it off.
func (d *notificationDispatcher) useTurnFinishedHook() {
	d.turnsFromHook = true
}

// useApprovalHook moves pending-approval reporting to the controller's
// ApprovalRequestedHook, so approvals of background sessions are covered too.
// Production wires it together with SetOnApprovalRequested; the snapshot phase
// machine then no longer raises approval notifications (no double
// notification). Headless snapshot tests leave it off.
func (d *notificationDispatcher) useApprovalHook() {
	d.approvalsFromHook = true
}

// observeApprovalRequested raises the "approval needed" notification from the
// controller's ApprovalRequestedHook. Unlike Observe it covers every session,
// current or background. The controller reports each call id once; the
// session+call map deduplicates defensively across re-wiring. Approvals are not
// throttled: every waiting call needs the user's attention. The request carries
// the tool name/arguments, which are deliberately never rendered.
func (d *notificationDispatcher) observeApprovalRequested(sessionID string, request state.ApprovalRequest) {
	if d == nil || d.notifier == nil || !d.approvalsFromHook {
		return
	}
	if sessionID == "" {
		sessionID = "default"
	}
	callKey := sessionID + "\x00" + request.CallID
	d.mu.Lock()
	if d.approvalCalls == nil {
		d.approvalCalls = make(map[string]struct{})
	}
	if _, seen := d.approvalCalls[callKey]; seen {
		d.mu.Unlock()
		return
	}
	if len(d.approvalCalls) >= approvalDedupeCap {
		d.approvalCalls = make(map[string]struct{}, approvalDedupeCap)
	}
	d.approvalCalls[callKey] = struct{}{}
	d.mu.Unlock()

	if d.isActive() {
		return
	}
	title, body := notificationText(d.currentLanguage(), noticeApproval)
	// The click callback carries the session id only; the tool name and
	// arguments stay inside the app.
	d.notifier.Show(title, body, d.clickAction(sessionID))
}

// observeTurnFinished raises the terminal-turn notification from the
// controller's TurnFinishedHook. Unlike Observe it covers every session,
// including background ones that never reach OnUpdate. The controller calls it
// once per turn; sessionID+turnID deduplicates defensively and a per-session
// completion throttle keeps goal/automation loops from stacking notifications.
func (d *notificationDispatcher) observeTurnFinished(outcome session.TurnOutcome) {
	if d == nil || d.notifier == nil || !d.turnsFromHook {
		return
	}
	if outcome.Stopped {
		// The user cancelled the turn (or the app is shutting down); a
		// "finished" notification would be noise, and a cancelled turn is not
		// a failure.
		return
	}
	kind := noticeTurnComplete
	switch {
	case outcome.Failed:
		kind = noticeTurnFailed
	case !outcome.HasOutput:
		// Nothing the user could read came out of the turn: matches the
		// snapshot path's output rule.
		return
	}
	sessionID := outcome.SessionID
	if sessionID == "" {
		sessionID = "default"
	}
	now := d.clockNow()
	d.mu.Lock()
	if d.lastTurn == nil {
		d.lastTurn = make(map[string]string)
	}
	if outcome.TurnID != "" && d.lastTurn[sessionID] == outcome.TurnID {
		d.mu.Unlock()
		return
	}
	d.lastTurn[sessionID] = outcome.TurnID
	// Completion throttle per session; failures always notify (rare and
	// actionable). The notification gate below still suppresses everything while
	// the user is looking at the app.
	if kind == noticeTurnComplete && d.turnThrottle > 0 {
		if last, ok := d.lastTurnNotice[sessionID]; ok && now.Sub(last) < d.turnThrottle {
			d.mu.Unlock()
			return
		}
	}
	if d.lastTurnNotice == nil {
		d.lastTurnNotice = make(map[string]time.Time)
	}
	d.lastTurnNotice[sessionID] = now
	d.mu.Unlock()

	if d.isActive() {
		return
	}
	title, body := notificationText(d.currentLanguage(), kind)
	d.notifier.Show(title, body, d.clickAction(outcome.SessionID))
}

// clockNow reads the dispatcher clock.
func (d *notificationDispatcher) clockNow() time.Time {
	if d.now != nil {
		return d.now()
	}
	return time.Now()
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
		// When a controller hook owns a kind, the snapshot machine must not also
		// raise it: the hook covers background sessions and the snapshot may
		// arrive just before/after the hook call, so skipping here is what
		// prevents a duplicate for the current session.
		switch {
		case d.turnsFromHook && kind != noticeApproval:
			ok = false
		case d.approvalsFromHook && kind == noticeApproval:
			ok = false
		}
	}
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

// notificationActive is the desktop-notification gate: no notifications while
// the user is looking at the window and none while the app is shutting down
// (the controller then cancels in-flight turns, whose hook calls report them as
// stopped and are dropped anyway).
func (a *app) notificationActive() bool {
	return a.quitting || a.windowActive()
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
