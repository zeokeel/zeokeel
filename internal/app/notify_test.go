package app

import (
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
)

// fakeNotification is one notification a fakeNotifier recorded.
type fakeNotification struct {
	title   string
	body    string
	onClick func()
}

// fakeNotifier records the notifications the dispatcher raises, and can pretend
// the platform has no notification service (available=false) to exercise the
// degradation path.
type fakeNotifier struct {
	mu        sync.Mutex
	available bool
	shown     []fakeNotification
}

func newFakeNotifier() *fakeNotifier { return &fakeNotifier{available: true} }

func (f *fakeNotifier) Show(title, body string, onClick func()) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.available {
		return false
	}
	f.shown = append(f.shown, fakeNotification{title: title, body: body, onClick: onClick})
	return true
}

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.shown)
}

func (f *fakeNotifier) last(t *testing.T) fakeNotification {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.shown) == 0 {
		t.Fatal("no notification was shown")
	}
	return f.shown[len(f.shown)-1]
}

// notifyHarness wires a dispatcher with controllable window activity, language
// and focus action, the same seams the production wiring uses.
type notifyHarness struct {
	notifier   *fakeNotifier
	dispatcher *notificationDispatcher
	active     bool
	lang       string
	focused    []string
}

func newNotifyHarness(t *testing.T) *notifyHarness {
	t.Helper()
	h := &notifyHarness{notifier: newFakeNotifier(), lang: session.LanguageEnglish}
	h.dispatcher = newNotificationDispatcher(
		h.notifier,
		func() bool { return h.active },
		func() string { return h.lang },
		func(sessionID string) { h.focused = append(h.focused, sessionID) },
	)
	return h
}

// notifySnapshot builds one session snapshot; options add messages, tools or an
// error without any real runtime.
func notifySnapshot(sessionID string, phase state.Phase, options ...func(*state.Snapshot)) *state.Snapshot {
	snap := &state.Snapshot{SessionID: sessionID, Phase: phase, Tools: map[string]state.ToolState{}}
	for _, option := range options {
		option(snap)
	}
	return snap
}

// withAssistantOutput adds the assistant text a finished turn leaves behind.
func withAssistantOutput(text string) func(*state.Snapshot) {
	return func(snap *state.Snapshot) {
		snap.Messages = append(snap.Messages, state.Message{
			MsgID: "msg-assistant", Role: state.RoleAssistant, Text: text,
		})
	}
}

// withUserMessage adds the prompt the user sent, which alone is not output.
func withUserMessage(text string) func(*state.Snapshot) {
	return func(snap *state.Snapshot) {
		snap.Messages = append(snap.Messages, state.Message{
			MsgID: "msg-user", Role: state.RoleUser, Text: text,
		})
	}
}

// withError records the terminal error of a failed turn.
func withError(text string) func(*state.Snapshot) {
	return func(snap *state.Snapshot) {
		snap.LastError = &state.ErrorInfo{MsgID: "msg-error", Text: text}
	}
}

func TestNotificationTurnComplete(t *testing.T) {
	h := newNotifyHarness(t)
	// The first snapshot of a session is only a baseline.
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseRunning))
	if got := h.notifier.count(); got != 0 {
		t.Fatalf("first snapshot raised %d notifications, want 0", got)
	}
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseIdle, withAssistantOutput("done")))
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("completed turn raised %d notifications, want 1", got)
	}
	notification := h.notifier.last(t)
	if notification.title != "Task complete" {
		t.Errorf("title = %q, want Task complete", notification.title)
	}
	if !strings.Contains(notification.body, "finished its turn") {
		t.Errorf("body = %q, want the English completion copy", notification.body)
	}
	// The click brings the app forward and opens the session the event belongs
	// to.
	if notification.onClick == nil {
		t.Fatal("notification has no click handler")
	}
	notification.onClick()
	if len(h.focused) != 1 || h.focused[0] != "s1" {
		t.Fatalf("click focused %v, want [s1]", h.focused)
	}
}

func TestNotificationTurnFailed(t *testing.T) {
	h := newNotifyHarness(t)
	const secret = "secret user prompt and tool output"
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseRunning))
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseFailed, withError(secret)))
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("failed turn raised %d notifications, want 1", got)
	}
	notification := h.notifier.last(t)
	if notification.title != "Task failed" {
		t.Errorf("title = %q, want Task failed", notification.title)
	}
	// Privacy: the notification stays on lock screens and in the notification
	// center, so neither the prompt nor the error text may leak into it.
	if strings.Contains(notification.body, secret) || strings.Contains(notification.title, secret) {
		t.Fatalf("notification leaked turn content: %q / %q", notification.title, notification.body)
	}
}

func TestNotificationApprovalIsRaisedOncePerWait(t *testing.T) {
	h := newNotifyHarness(t)
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseRunning))
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseWaitingApproval))
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("approval wait raised %d notifications, want 1", got)
	}
	if got := h.notifier.last(t).title; got != "Approval needed" {
		t.Fatalf("title = %q, want Approval needed", got)
	}
	// Redraws while still waiting must not repeat the notification.
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseWaitingApproval))
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseWaitingApproval))
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("approval redraws raised %d notifications, want 1", got)
	}
	// A second tool call waiting for approval is a new notification.
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseRunning))
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseWaitingApproval))
	if got := h.notifier.count(); got != 2 {
		t.Fatalf("second approval wait kept %d notifications, want 2", got)
	}
}

func TestNotificationWaitingUserUsesApprovalCopy(t *testing.T) {
	h := newNotifyHarness(t)
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseRunning))
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseWaitingUser))
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("waiting-user phase raised %d notifications, want 1", got)
	}
	if got := h.notifier.last(t).title; got != "Approval needed" {
		t.Fatalf("title = %q, want Approval needed", got)
	}
}

func TestNotificationFirstSnapshotOnlyRecordsBaseline(t *testing.T) {
	for _, phase := range []state.Phase{
		state.PhaseIdle, state.PhaseFailed, state.PhaseWaitingApproval, state.PhaseWaitingUser,
	} {
		h := newNotifyHarness(t)
		// Startup, restore or a freshly opened session: the first snapshot must
		// not replay a finished turn or a pending approval as a notification.
		h.dispatcher.Observe(notifySnapshot("s1", phase, withAssistantOutput("old output")))
		if got := h.notifier.count(); got != 0 {
			t.Errorf("first %s snapshot raised %d notifications, want 0", phase, got)
		}
	}
}

func TestNotificationSkippedWhileWindowActive(t *testing.T) {
	h := newNotifyHarness(t)
	h.active = true
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseRunning))
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseIdle, withAssistantOutput("done")))
	if got := h.notifier.count(); got != 0 {
		t.Fatalf("active window raised %d notifications, want 0", got)
	}
	// Walking away later must not raise the finished turn retroactively.
	h.active = false
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseIdle, withAssistantOutput("done")))
	if got := h.notifier.count(); got != 0 {
		t.Fatalf("late idle snapshot raised %d notifications, want 0", got)
	}
}

func TestNotificationIdleWithoutOutputDoesNotNotify(t *testing.T) {
	h := newNotifyHarness(t)
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseRunning, withUserMessage("please do it")))
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseIdle, withUserMessage("please do it")))
	if got := h.notifier.count(); got != 0 {
		t.Fatalf("output-less turn raised %d notifications, want 0", got)
	}
}

func TestNotificationDedupesSameTurn(t *testing.T) {
	h := newNotifyHarness(t)
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseRunning))
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseIdle, withAssistantOutput("done")))
	// Every later repaint of the same terminal snapshot must stay silent.
	for i := 0; i < 3; i++ {
		h.dispatcher.Observe(notifySnapshot("s1", state.PhaseIdle, withAssistantOutput("done")))
	}
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("same turn raised %d notifications, want 1", got)
	}
	// The next turn notifies again.
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseRunning))
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseIdle, withAssistantOutput("done again")))
	if got := h.notifier.count(); got != 2 {
		t.Fatalf("second turn kept %d notifications, want 2", got)
	}
}

func TestNotificationSessionsAreIndependent(t *testing.T) {
	h := newNotifyHarness(t)
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseRunning))
	h.dispatcher.Observe(notifySnapshot("s2", state.PhaseRunning))
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseIdle, withAssistantOutput("done one")))
	h.dispatcher.Observe(notifySnapshot("s2", state.PhaseIdle, withAssistantOutput("done two")))
	if got := h.notifier.count(); got != 2 {
		t.Fatalf("two sessions raised %d notifications, want 2", got)
	}
}

func TestNotificationUnsupportedNotifierDegrades(t *testing.T) {
	h := newNotifyHarness(t)
	h.notifier.available = false
	// Nothing panics, nothing is shown and the dispatcher state machine keeps
	// working.
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseRunning))
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseIdle, withAssistantOutput("done")))
	if got := h.notifier.count(); got != 0 {
		t.Fatalf("unsupported notifier recorded %d notifications, want 0", got)
	}
	h.notifier.available = true
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseRunning))
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseIdle, withAssistantOutput("done")))
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("recovered notifier recorded %d notifications, want 1", got)
	}
}

func TestNotificationChineseCopy(t *testing.T) {
	h := newNotifyHarness(t)
	h.lang = session.LanguageChinese
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseRunning))
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseIdle, withAssistantOutput("done")))
	if got := h.notifier.last(t).title; got != "任务完成" {
		t.Fatalf("Chinese completion title = %q, want 任务完成", got)
	}
	h.lang = session.LanguageChinese
	h.dispatcher.Observe(notifySnapshot("s2", state.PhaseRunning))
	h.dispatcher.Observe(notifySnapshot("s2", state.PhaseFailed, withError("boom")))
	if got := h.notifier.last(t).title; got != "任务失败" {
		t.Fatalf("Chinese failure title = %q, want 任务失败", got)
	}
	h.dispatcher.Observe(notifySnapshot("s3", state.PhaseRunning))
	h.dispatcher.Observe(notifySnapshot("s3", state.PhaseWaitingApproval))
	if got := h.notifier.last(t).title; got != "需要确认" {
		t.Fatalf("Chinese approval title = %q, want 需要确认", got)
	}
}

func TestNotificationEmptySessionIDStillDispatches(t *testing.T) {
	h := newNotifyHarness(t)
	h.dispatcher.Observe(notifySnapshot("", state.PhaseRunning))
	h.dispatcher.Observe(notifySnapshot("", state.PhaseIdle, withAssistantOutput("done")))
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("session-less snapshot raised %d notifications, want 1", got)
	}
}

func TestNotificationDispatcherNilNotifierIsSafe(t *testing.T) {
	dispatcher := newNotificationDispatcher(nil, nil, nil, nil)
	// No notifier configured (headless embedding): observing must be a no-op.
	dispatcher.Observe(notifySnapshot("s1", state.PhaseRunning))
	dispatcher.Observe(notifySnapshot("s1", state.PhaseIdle, withAssistantOutput("done")))
	var nilDispatcher *notificationDispatcher
	nilDispatcher.Observe(notifySnapshot("s1", state.PhaseIdle))
}

func TestSystemNotifierUnsupportedFallsBack(t *testing.T) {
	showCalls := 0
	n := systemNotifier{
		supported: func() bool { return false },
		show: func(string, string, func()) bool {
			showCalls++
			return true
		},
	}
	if n.Show("任务完成", "body", nil) {
		t.Fatal("unsupported notifier reported the notification as shown")
	}
	if showCalls != 0 {
		t.Fatalf("unsupported notifier called the show seam %d times, want 0", showCalls)
	}
}

func TestSystemNotifierDelegatesOptions(t *testing.T) {
	var gotTitle, gotBody string
	var gotClick func()
	n := systemNotifier{
		supported: func() bool { return true },
		show: func(title, body string, onClick func()) bool {
			gotTitle, gotBody, gotClick = title, body, onClick
			return true
		},
	}
	clicked := false
	if !n.Show("Task complete", "The agent finished.", func() { clicked = true }) {
		t.Fatal("supported notifier reported failure")
	}
	if gotTitle != "Task complete" || gotBody != "The agent finished." {
		t.Fatalf("show seam got %q / %q", gotTitle, gotBody)
	}
	if gotClick == nil {
		t.Fatal("click handler was not passed through")
	}
	gotClick()
	if !clicked {
		t.Fatal("click handler did not run")
	}
}

func TestWindowActiveWithoutWindowIsSafe(t *testing.T) {
	a := &app{}
	if !a.windowActive() {
		t.Fatal("windowActive without a window = false, want true (assume visible)")
	}
	// The click action must not panic before a window or shell exists.
	a.focusSession("s1")
	a.focusSession("")
}

func TestUILanguageFallsBackToChinese(t *testing.T) {
	a := &app{}
	if got := a.uiLanguage(); got != session.LanguageChinese {
		t.Fatalf("uiLanguage without services = %q, want zh", got)
	}
}
