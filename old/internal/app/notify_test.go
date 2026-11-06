package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
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

// ---- Background-turn notifications (controller TurnFinishedHook) ----

// gatedNotifyProvider is a scripted provider whose response waits for release,
// so a test decides when a turn finishes and which session is current then.
type gatedNotifyProvider struct {
	release chan struct{}
	text    string
	err     error
}

func newGatedNotifyProvider(text string, err error) *gatedNotifyProvider {
	return &gatedNotifyProvider{release: make(chan struct{}), text: text, err: err}
}

func (p *gatedNotifyProvider) Stream(ctx context.Context, _ runtime.Request) (<-chan runtime.Chunk, error) {
	out := make(chan runtime.Chunk, 1)
	go func() {
		defer close(out)
		select {
		case <-p.release:
			if p.err != nil {
				out <- runtime.Chunk{Err: p.err, Done: true}
				return
			}
			out <- runtime.Chunk{Text: p.text, Done: true}
		case <-ctx.Done():
		}
	}()
	return out, nil
}

// waitNotifyPhase waits for the current session snapshot to reach phase.
func waitNotifyPhase(t *testing.T, ctrl *session.Controller, phase state.Phase) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ctrl.Snapshot().Phase == phase {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("session did not reach %s: %+v", phase, ctrl.Snapshot())
}

// newNotificationController wires a real controller (store/broker/projector) to
// a dispatcher exactly like the app does: snapshots for approval waits and the
// controller TurnFinishedHook for terminal turns of every session.
func newNotificationController(t *testing.T, provider runtime.Provider) (*session.Controller, *notifyHarness) {
	t.Helper()
	ctrl, _ := newWiredController(t, provider, nil)
	h := newNotifyHarness(t)
	h.dispatcher.useTurnFinishedHook()
	ctrl.SetOnTurnFinished(h.dispatcher.observeTurnFinished)
	ctrl.OnUpdate(h.dispatcher.Observe)
	return ctrl, h
}

// TestBackgroundSessionTurnCompleteNotifies covers the P1 gap: a turn that ends
// in a session which is no longer current (OnUpdate no longer publishes it)
// still raises one notification, and its click reopens that session.
func TestBackgroundSessionTurnCompleteNotifies(t *testing.T) {
	provider := newGatedNotifyProvider("background result", nil)
	ctrl, h := newNotificationController(t, provider)
	ctx := context.Background()
	other, err := ctrl.CreateSession(ctx, "foreground")
	if err != nil {
		t.Fatalf("CreateSession foreground: %v", err)
	}
	background, err := ctrl.CreateSession(ctx, "background")
	if err != nil {
		t.Fatalf("CreateSession background: %v", err)
	}
	if err := ctrl.Submit(ctx, background, "long background work"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitNotifyPhase(t, ctrl, state.PhaseRunning)
	// Switch away while the turn runs: the completion snapshot is now published
	// for `other`, so only the controller hook can report the background turn.
	if _, err := ctrl.OpenSession(ctx, other); err != nil {
		t.Fatalf("OpenSession foreground: %v", err)
	}
	close(provider.release)
	ctrl.Wait()

	if got := h.notifier.count(); got != 1 {
		t.Fatalf("background completion raised %d notifications, want 1", got)
	}
	notification := h.notifier.last(t)
	if notification.title != "Task complete" {
		t.Fatalf("title = %q, want Task complete", notification.title)
	}
	if notification.onClick == nil {
		t.Fatal("background notification has no click handler")
	}
	notification.onClick()
	if len(h.focused) != 1 || h.focused[0] != background {
		t.Fatalf("click focused %v, want [%s]", h.focused, background)
	}
}

// TestBackgroundSessionTurnFailureNotifies covers the failure half of the same
// gap, and re-checks the privacy rule: the provider's error text stays out of
// the notification.
func TestBackgroundSessionTurnFailureNotifies(t *testing.T) {
	const secret = "provider exploded on private prompt"
	provider := newGatedNotifyProvider("", errors.New(secret))
	ctrl, h := newNotificationController(t, provider)
	ctx := context.Background()
	other, err := ctrl.CreateSession(ctx, "foreground")
	if err != nil {
		t.Fatalf("CreateSession foreground: %v", err)
	}
	background, err := ctrl.CreateSession(ctx, "background")
	if err != nil {
		t.Fatalf("CreateSession background: %v", err)
	}
	if err := ctrl.Submit(ctx, background, secret); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitNotifyPhase(t, ctrl, state.PhaseRunning)
	if _, err := ctrl.OpenSession(ctx, other); err != nil {
		t.Fatalf("OpenSession foreground: %v", err)
	}
	close(provider.release)
	ctrl.Wait()

	if got := h.notifier.count(); got != 1 {
		t.Fatalf("background failure raised %d notifications, want 1", got)
	}
	notification := h.notifier.last(t)
	if notification.title != "Task failed" {
		t.Fatalf("title = %q, want Task failed", notification.title)
	}
	if strings.Contains(notification.body, secret) {
		t.Fatalf("failure notification leaked the error text: %q", notification.body)
	}
	notification.onClick()
	if len(h.focused) != 1 || h.focused[0] != background {
		t.Fatalf("click focused %v, want [%s]", h.focused, background)
	}
}

// TestCurrentSessionTurnNotifiesOnce checks the hook and the snapshot machine
// do not both fire for the session the user is looking at: exactly one
// notification per turn.
func TestCurrentSessionTurnNotifiesOnce(t *testing.T) {
	provider := newGatedNotifyProvider("done", nil)
	ctrl, h := newNotificationController(t, provider)
	ctx := context.Background()
	id, err := ctrl.CreateSession(ctx, "current")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := ctrl.Submit(ctx, id, "work"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitNotifyPhase(t, ctrl, state.PhaseRunning)
	close(provider.release)
	ctrl.Wait()
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("current-session turn raised %d notifications, want exactly 1", got)
	}
}

// TestStoppedTurnDoesNotNotify covers the user stopping a turn: cancellation is
// not a completion and not a failure.
func TestStoppedTurnDoesNotNotify(t *testing.T) {
	provider := newGatedNotifyProvider("too late", nil)
	ctrl, h := newNotificationController(t, provider)
	ctx := context.Background()
	id, err := ctrl.CreateSession(ctx, "stopped")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := ctrl.Submit(ctx, id, "work"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitNotifyPhase(t, ctrl, state.PhaseRunning)
	ctrl.Stop()
	ctrl.Wait()
	if got := h.notifier.count(); got != 0 {
		t.Fatalf("stopped turn raised %d notifications, want 0", got)
	}
}

// TestWiringAfterCompletedTurnDoesNotReplayHistory covers the restart path: a
// dispatcher attached to an already-idle session must stay quiet until the next
// turn, and then report that turn.
func TestWiringAfterCompletedTurnDoesNotReplayHistory(t *testing.T) {
	provider := &wiringProvider{}
	ctrl, _ := newWiredController(t, provider, nil)
	ctx := context.Background()
	id, err := ctrl.CreateSession(ctx, "restored")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := ctrl.Submit(ctx, id, "finished before the app restarted"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitWiringIdle(t, ctrl)

	// "Restart": attach the dispatcher to the idle session and observe its
	// snapshot; history must not be replayed.
	h := newNotifyHarness(t)
	h.dispatcher.useTurnFinishedHook()
	ctrl.SetOnTurnFinished(h.dispatcher.observeTurnFinished)
	ctrl.OnUpdate(h.dispatcher.Observe)
	h.dispatcher.Observe(ctrl.Snapshot())
	if got := h.notifier.count(); got != 0 {
		t.Fatalf("restored idle session replayed %d notifications, want 0", got)
	}
	// The first live turn after the restart notifies.
	if err := ctrl.Submit(ctx, id, "next turn"); err != nil {
		t.Fatalf("Submit after restart: %v", err)
	}
	waitWiringIdle(t, ctrl)
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("live turn after restart raised %d notifications, want 1", got)
	}
}

// TestTurnFinishedHookDedupesSameTurn covers the defensive sessionID+turnID
// dedupe for a repeated hook call of the same turn.
func TestTurnFinishedHookDedupesSameTurn(t *testing.T) {
	h := newNotifyHarness(t)
	h.dispatcher.useTurnFinishedHook()
	outcome := session.TurnOutcome{SessionID: "s1", TurnID: "turn-1", HasOutput: true}
	h.dispatcher.observeTurnFinished(outcome)
	h.dispatcher.observeTurnFinished(outcome)
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("repeated hook for one turn raised %d notifications, want 1", got)
	}
}

// TestTurnFinishedHookSkipsStoppedAndOutputlessTurns covers the hook's outcome
// filters.
func TestTurnFinishedHookSkipsStoppedAndOutputlessTurns(t *testing.T) {
	h := newNotifyHarness(t)
	h.dispatcher.useTurnFinishedHook()
	h.dispatcher.observeTurnFinished(session.TurnOutcome{SessionID: "s1", TurnID: "t1", Stopped: true})
	h.dispatcher.observeTurnFinished(session.TurnOutcome{SessionID: "s1", TurnID: "t2"}) // no output
	if got := h.notifier.count(); got != 0 {
		t.Fatalf("stopped/output-less turns raised %d notifications, want 0", got)
	}
	// A failed turn notifies even when it produced no readable output.
	h.dispatcher.observeTurnFinished(session.TurnOutcome{SessionID: "s1", TurnID: "t3", Failed: true})
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("failed turn raised %d notifications, want 1", got)
	}
}

// TestTurnFinishedHookRespectsActiveWindow covers the same gate as snapshots.
func TestTurnFinishedHookRespectsActiveWindow(t *testing.T) {
	h := newNotifyHarness(t)
	h.dispatcher.useTurnFinishedHook()
	h.active = true
	h.dispatcher.observeTurnFinished(session.TurnOutcome{SessionID: "s1", TurnID: "t1", HasOutput: true})
	if got := h.notifier.count(); got != 0 {
		t.Fatalf("active window raised %d notifications, want 0", got)
	}
	h.active = false
	h.dispatcher.observeTurnFinished(session.TurnOutcome{SessionID: "s2", TurnID: "t2", HasOutput: true})
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("inactive window raised %d notifications, want 1", got)
	}
}

// TestTurnFinishedHookThrottlesCompletionsPerSession covers the per-session
// completion throttle (goal/automation continuations) and the failure bypass.
func TestTurnFinishedHookThrottlesCompletionsPerSession(t *testing.T) {
	h := newNotifyHarness(t)
	h.dispatcher.useTurnFinishedHook()
	now := time.Unix(1_700_000_000, 0)
	h.dispatcher.now = func() time.Time { return now }
	h.dispatcher.observeTurnFinished(session.TurnOutcome{SessionID: "s1", TurnID: "t1", HasOutput: true})
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("first completion raised %d notifications, want 1", got)
	}
	h.dispatcher.observeTurnFinished(session.TurnOutcome{SessionID: "s1", TurnID: "t2", HasOutput: true})
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("throttled completion raised %d notifications, want 1", got)
	}
	now = now.Add(turnNoticeThrottle + time.Second)
	h.dispatcher.observeTurnFinished(session.TurnOutcome{SessionID: "s1", TurnID: "t3", HasOutput: true})
	if got := h.notifier.count(); got != 2 {
		t.Fatalf("completion after throttle raised %d notifications, want 2", got)
	}
	// A failure is rare and actionable: it bypasses the throttle.
	h.dispatcher.observeTurnFinished(session.TurnOutcome{SessionID: "s1", TurnID: "t4", Failed: true})
	if got := h.notifier.count(); got != 3 {
		t.Fatalf("failure during throttle raised %d notifications, want 3", got)
	}
}

// TestTurnFinishedHookIsInertWithoutWiring covers a dispatcher that only has the
// snapshot path (headless tests, older wiring): hook calls are ignored.
func TestTurnFinishedHookIsInertWithoutWiring(t *testing.T) {
	h := newNotifyHarness(t)
	h.dispatcher.observeTurnFinished(session.TurnOutcome{SessionID: "s1", TurnID: "t1", HasOutput: true})
	if got := h.notifier.count(); got != 0 {
		t.Fatalf("unwired hook raised %d notifications, want 0", got)
	}
}

// TestTurnFinishedHookUsesChineseCopy covers the localized background copy.
func TestTurnFinishedHookUsesChineseCopy(t *testing.T) {
	h := newNotifyHarness(t)
	h.dispatcher.useTurnFinishedHook()
	h.lang = session.LanguageChinese
	h.dispatcher.observeTurnFinished(session.TurnOutcome{SessionID: "s1", TurnID: "t1", HasOutput: true})
	if got := h.notifier.last(t).title; got != "任务完成" {
		t.Fatalf("Chinese background title = %q, want 任务完成", got)
	}
}

// TestNotificationActiveGate covers the app-level gate: quitting suppresses
// notifications even before the window is gone.
func TestNotificationActiveGate(t *testing.T) {
	a := &app{}
	if !a.notificationActive() {
		t.Fatal("notificationActive without quitting/window = false, want true (assume visible)")
	}
	a.quitting = true
	if !a.notificationActive() {
		t.Fatal("notificationActive while quitting = false, want true (suppressed)")
	}
}

// ---- Background-approval notifications (controller ApprovalRequestedHook) ----

// approvalNotifyProvider is a scripted provider whose first model call returns
// one tool call once released; later calls answer with text. Used with an
// approval gate so the controller publishes an ApprovalRequest and blocks.
type approvalNotifyProvider struct {
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func newApprovalNotifyProvider() *approvalNotifyProvider {
	return &approvalNotifyProvider{release: make(chan struct{})}
}

func (p *approvalNotifyProvider) Stream(ctx context.Context, _ runtime.Request) (<-chan runtime.Chunk, error) {
	p.mu.Lock()
	call := p.calls
	p.calls++
	p.mu.Unlock()

	if call != 0 {
		out := make(chan runtime.Chunk, 1)
		out <- runtime.Chunk{Text: "finished", Done: true}
		close(out)
		return out, nil
	}
	out := make(chan runtime.Chunk, 1)
	go func() {
		defer close(out)
		select {
		case <-p.release:
			out <- runtime.Chunk{ToolCalls: []runtime.ToolCall{{
				ID:        "call-approval",
				Name:      "echo",
				Arguments: json.RawMessage(`{"path":"private-argument.txt"}`),
			}}, Done: true}
		case <-ctx.Done():
		}
	}()
	return out, nil
}

// newApprovalNotificationController builds a real controller whose agent needs a
// human decision for every tool call, wires both controller hooks into a
// dispatcher exactly like the app does, and returns the approval queue.
func newApprovalNotificationController(t *testing.T, provider runtime.Provider) (*session.Controller, *notifyHarness, *runtime.ApprovalQueue) {
	t.Helper()
	gate := runtime.NewApprovalQueue(nil) // every tool call needs a decision
	registry := runtime.NewRegistry()
	if err := registry.Register(&wiringEchoTool{}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	ctrl, cleanup, err := BuildServicesWith(t.TempDir(), func(st *store.Store) (*AgentBundle, error) {
		agent, agentErr := runtime.New(runtime.Config{
			Provider:    provider,
			Tools:       registry,
			History:     &storeHistory{st: st},
			Gate:        gate,
			Instruction: "approval notification test agent",
		})
		if agentErr != nil {
			return nil, agentErr
		}
		return &AgentBundle{Agent: agent, Gate: gate}, nil
	})
	if err != nil {
		t.Fatalf("BuildServicesWith: %v", err)
	}
	t.Cleanup(func() { _ = cleanup() })

	h := newNotifyHarness(t)
	h.dispatcher.useTurnFinishedHook()
	h.dispatcher.useApprovalHook()
	ctrl.SetOnTurnFinished(h.dispatcher.observeTurnFinished)
	ctrl.SetOnApprovalRequested(h.dispatcher.observeApprovalRequested)
	ctrl.OnUpdate(h.dispatcher.Observe)
	return ctrl, h, gate
}

// waitNotificationCount waits until the fake notifier recorded want notices.
func waitNotificationCount(t *testing.T, notifier *fakeNotifier, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if notifier.count() >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d notifications; got %d", want, notifier.count())
}

// TestBackgroundSessionApprovalNotifies covers the P1 gap: an approval request
// in a session that is no longer current (OnUpdate no longer publishes it)
// still raises exactly one "approval needed" notification, with no tool detail,
// and its click reopens that session.
func TestBackgroundSessionApprovalNotifies(t *testing.T) {
	provider := newApprovalNotifyProvider()
	ctrl, h, _ := newApprovalNotificationController(t, provider)
	ctx := context.Background()
	foreground, err := ctrl.CreateSession(ctx, "foreground")
	if err != nil {
		t.Fatalf("CreateSession foreground: %v", err)
	}
	background, err := ctrl.CreateSession(ctx, "background")
	if err != nil {
		t.Fatalf("CreateSession background: %v", err)
	}
	if err := ctrl.Submit(ctx, background, "run the tool"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	// Switch away before the tool call arrives: the approval snapshot is
	// published for `foreground`, so only the approval hook can report it.
	if _, err := ctrl.OpenSession(ctx, foreground); err != nil {
		t.Fatalf("OpenSession foreground: %v", err)
	}
	close(provider.release)
	waitNotificationCount(t, h.notifier, 1)
	// Give a wrongly wired duplicate path a chance to fire.
	time.Sleep(50 * time.Millisecond)
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("background approval raised %d notifications, want 1", got)
	}
	notification := h.notifier.last(t)
	if notification.title != "Approval needed" {
		t.Fatalf("title = %q, want Approval needed", notification.title)
	}
	// Privacy: neither the tool name nor its arguments may reach the system
	// notification (lock screen / notification center).
	for _, secret := range []string{"echo", "private-argument.txt"} {
		if strings.Contains(notification.body, secret) || strings.Contains(notification.title, secret) {
			t.Fatalf("approval notification leaked %q: %q / %q", secret, notification.title, notification.body)
		}
	}
	if notification.onClick == nil {
		t.Fatal("approval notification has no click handler")
	}
	notification.onClick()
	if len(h.focused) != 1 || h.focused[0] != background {
		t.Fatalf("click focused %v, want [%s]", h.focused, background)
	}
}

// TestCurrentSessionApprovalNotifiesOnce covers the current session: the
// snapshot machine and the approval hook must not both fire.
func TestCurrentSessionApprovalNotifiesOnce(t *testing.T) {
	provider := newApprovalNotifyProvider()
	ctrl, h, _ := newApprovalNotificationController(t, provider)
	ctx := context.Background()
	id, err := ctrl.CreateSession(ctx, "current")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := ctrl.Submit(ctx, id, "run the tool"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	close(provider.release)
	waitNotificationCount(t, h.notifier, 1)
	time.Sleep(50 * time.Millisecond)
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("current-session approval raised %d notifications, want exactly 1", got)
	}
	if got := h.notifier.last(t).title; got != "Approval needed" {
		t.Fatalf("title = %q, want Approval needed", got)
	}
}

// TestApprovalHookDedupesSessionAndCall covers the defensive dedupe: one call id
// notifies once per session; the same call id reported for another session is a
// different approval.
func TestApprovalHookDedupesSessionAndCall(t *testing.T) {
	h := newNotifyHarness(t)
	h.dispatcher.useApprovalHook()
	request := state.ApprovalRequest{CallID: "call-1", Name: "bash"}
	h.dispatcher.observeApprovalRequested("s1", request)
	h.dispatcher.observeApprovalRequested("s1", request)
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("duplicate approval call raised %d notifications, want 1", got)
	}
	h.dispatcher.observeApprovalRequested("s2", request)
	if got := h.notifier.count(); got != 2 {
		t.Fatalf("same call id in another session raised %d notifications, want 2", got)
	}
	h.dispatcher.observeApprovalRequested("s1", state.ApprovalRequest{CallID: "call-2"})
	if got := h.notifier.count(); got != 3 {
		t.Fatalf("second approval of the session raised %d notifications, want 3", got)
	}
}

// TestApprovalHookSkipsWhileActive covers the window gate for approvals: a user
// already looking at the app sees the approval card, no system notification.
func TestApprovalHookSkipsWhileActive(t *testing.T) {
	h := newNotifyHarness(t)
	h.dispatcher.useApprovalHook()
	h.active = true
	h.dispatcher.observeApprovalRequested("s1", state.ApprovalRequest{CallID: "call-1"})
	if got := h.notifier.count(); got != 0 {
		t.Fatalf("active window raised %d approval notifications, want 0", got)
	}
	h.active = false
	h.dispatcher.observeApprovalRequested("s2", state.ApprovalRequest{CallID: "call-2"})
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("inactive window raised %d approval notifications, want 1", got)
	}
}

// TestApprovalHookIsInertWithoutWiring covers a dispatcher without the hook:
// approval-hook calls are ignored (snapshot path stays authoritative).
func TestApprovalHookIsInertWithoutWiring(t *testing.T) {
	h := newNotifyHarness(t)
	h.dispatcher.observeApprovalRequested("s1", state.ApprovalRequest{CallID: "call-1"})
	if got := h.notifier.count(); got != 0 {
		t.Fatalf("unwired approval hook raised %d notifications, want 0", got)
	}
}

// TestSnapshotApprovalSkippedWhenHookWired covers the double-notification guard:
// with the approval hook wired, the snapshot phase machine no longer notifies
// approval transitions itself.
func TestSnapshotApprovalSkippedWhenHookWired(t *testing.T) {
	h := newNotifyHarness(t)
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseRunning))
	h.dispatcher.useApprovalHook()
	h.dispatcher.Observe(notifySnapshot("s1", state.PhaseWaitingApproval))
	if got := h.notifier.count(); got != 0 {
		t.Fatalf("snapshot approval raised %d notifications while the hook owns it, want 0", got)
	}
	h.dispatcher.observeApprovalRequested("s1", state.ApprovalRequest{CallID: "call-1"})
	if got := h.notifier.count(); got != 1 {
		t.Fatalf("hooked approval raised %d notifications, want 1", got)
	}
}

// TestApprovalHookUsesChineseCopy covers the localized background approval copy.
func TestApprovalHookUsesChineseCopy(t *testing.T) {
	h := newNotifyHarness(t)
	h.dispatcher.useApprovalHook()
	h.lang = session.LanguageChinese
	h.dispatcher.observeApprovalRequested("s1", state.ApprovalRequest{CallID: "call-1"})
	if got := h.notifier.last(t).title; got != "需要确认" {
		t.Fatalf("Chinese approval title = %q, want 需要确认", got)
	}
}
