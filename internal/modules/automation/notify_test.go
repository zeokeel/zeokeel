package automation

// Tests for the automation module's completion notifications: a settled run
// reaches the host notifier (OS-mirrored when supported), while delivery-only,
// ignored and interrupted runs stay silent.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeNotifier records the notifications a module raises.
type fakeNotifier struct {
	mu            sync.Mutex
	notifications []contracts.Notification
	system        bool
}

func (n *fakeNotifier) SetHandler(func(contracts.Notification)) {}

func (n *fakeNotifier) Notify(level, text string) contracts.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	notification := contracts.Notification{Level: level, Text: text}
	n.notifications = append(n.notifications, notification)
	return notification
}

func (n *fakeNotifier) Dismiss(string) {}

// NotifySystem makes fakeNotifier also satisfy contracts.SystemNotifier.
func (n *fakeNotifier) NotifySystem(level, text string) contracts.Notification {
	return n.NotifySystemFor(level, text, "")
}

// NotifySystemFor makes fakeNotifier also satisfy contracts.SessionNotifier and
// records the tagged conversation on the notification.
func (n *fakeNotifier) NotifySystemFor(level, text, sessionID string) contracts.Notification {
	n.mu.Lock()
	n.system = true
	n.notifications = append(n.notifications,
		contracts.Notification{Level: level, Text: text, SessionID: sessionID})
	n.mu.Unlock()
	return contracts.Notification{Level: level, Text: text, SessionID: sessionID}
}

func (n *fakeNotifier) SystemNotificationsSupported() bool { return true }

func (n *fakeNotifier) snapshot() []contracts.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]contracts.Notification(nil), n.notifications...)
}

// TestSettledRunNotifies pins the notification contract: nothing on delivery,
// one success notification on settlement with the task name and conversation.
func TestSettledRunNotifies(t *testing.T) {
	chat := newFakeChat()
	chat.setHold(true)
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	notifier := &fakeNotifier{}
	service.notifier = notifier
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "巡检", Prompt: "run", Schedule: "every 60s", Enabled: true,
	})
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	call := waitCall(t, chat)
	waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) == 1 && task.RunHistory[0].Status == contracts.AutomationRunDelivered
	})
	// A delivered run has not finished: no completion notification.
	if got := notifier.snapshot(); len(got) != 0 {
		t.Fatalf("notifications after delivery = %+v, want none", got)
	}

	chat.complete(call.SessionID)
	waitTerminal(t, service, created.ID)
	notifications := notifier.snapshot()
	if len(notifications) != 1 {
		t.Fatalf("notifications = %+v, want one success", notifications)
	}
	notification := notifications[0]
	if notification.Level != contracts.NotifySuccess {
		t.Fatalf("notification level = %q, want success", notification.Level)
	}
	if !strings.Contains(notification.Text, "巡检") {
		t.Fatalf("notification text = %q, want the task name", notification.Text)
	}
	// The conversation travels as the session tag, not as visible copy: a click
	// routes to it and the raw id does not leak into the notification text.
	if notification.SessionID != call.SessionID {
		t.Fatalf("notification session = %q, want %q", notification.SessionID, call.SessionID)
	}
	if strings.Contains(notification.Text, call.SessionID) {
		t.Fatalf("notification text %q leaked the raw session id", notification.Text)
	}
	if !notifier.system {
		t.Fatal("the OS half of the notifier was not used")
	}
}

// TestFailedSettledRunNotifiesError covers the failure copy and level.
func TestFailedSettledRunNotifiesError(t *testing.T) {
	chat := newFakeChat()
	chat.setHold(true)
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	notifier := &fakeNotifier{}
	service.notifier = notifier
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "失败任务", Prompt: "run", Schedule: "every 60s", Enabled: true,
	})
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	call := waitCall(t, chat)
	waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) == 1 && task.RunHistory[0].Status == contracts.AutomationRunDelivered
	})
	chat.fail(call.SessionID, "provider exploded")
	waitTerminal(t, service, created.ID)
	notifications := notifier.snapshot()
	if len(notifications) != 1 {
		t.Fatalf("notifications = %+v, want one failure", notifications)
	}
	if notifications[0].Level != contracts.NotifyError {
		t.Fatalf("notification level = %q, want error", notifications[0].Level)
	}
	if !strings.Contains(notifications[0].Text, "provider exploded") {
		t.Fatalf("notification text = %q, want the failure cause", notifications[0].Text)
	}
}

// TestSilentRunOutcomesStayUnnotified pins the negative cases: an interrupted
// run is neutral, and a delivery failure never started a run.
func TestSilentRunOutcomesStayUnnotified(t *testing.T) {
	chat := newFakeChat()
	chat.setHold(true)
	service := newTestService(t, t.TempDir(), Config{TickMs: 3600000}, chat)
	notifier := &fakeNotifier{}
	service.notifier = notifier
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "中断任务", Prompt: "run", Schedule: "every 60s", Enabled: true,
	})
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	call := waitCall(t, chat)
	waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) == 1 && task.RunHistory[0].Status == contracts.AutomationRunDelivered
	})
	chat.interrupt(call.SessionID)
	interrupted := waitTerminal(t, service, created.ID)
	if interrupted.RunHistory[0].Status != contracts.AutomationRunInterrupted {
		t.Fatalf("status = %q, want interrupted", interrupted.RunHistory[0].Status)
	}
	if got := notifier.snapshot(); len(got) != 0 {
		t.Fatalf("interrupted run notifications = %+v, want none", got)
	}

	// A delivery failure records a terminal error but never ran the agent.
	chat.setHold(false)
	chat.setSendError(errors.New("chat exploded"))
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow(failing send): %v", err)
	}
	failed := waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) == 2 && task.RunHistory[0].Status == contracts.AutomationRunFailure
	})
	if failed.LastError == "" {
		t.Fatal("delivery failure did not record LastError")
	}
	if got := notifier.snapshot(); len(got) != 0 {
		t.Fatalf("delivery failure notifications = %+v, want none", got)
	}
}

// TestModuleWiresOptionalNotifier covers the module-level context wiring: the
// host-provided notifier is used by settlement (and its absence is harmless).
func TestModuleWiresOptionalNotifier(t *testing.T) {
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	chat := newFakeChat()
	chat.setHold(true)
	notifier := &fakeNotifier{}
	logger := &fakeLogger{}
	for _, provide := range []func() error{
		func() error { _, err := root.Provide(contracts.KeyDataDir, t.TempDir()); return err },
		func() error { _, err := root.Provide(contracts.KeyLogger, contracts.Logger(logger)); return err },
		func() error { _, err := root.Provide(contracts.KeyChat, contracts.ChatService(chat)); return err },
		func() error { _, err := root.Provide(contracts.KeyNotifier, contracts.Notifier(notifier)); return err },
	} {
		if err := provide(); err != nil {
			t.Fatalf("provide: %v", err)
		}
	}
	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeyDataDir).AddBuiltin(contracts.KeyLogger).
		AddBuiltin(contracts.KeyChat).AddBuiltin(contracts.KeyNotifier)
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register automation: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "automation", Module: ModuleID, Config: map[string]any{"tickMs": 3600000},
	}})
	if err != nil {
		t.Fatalf("load automation: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	service, ok := root.Get(contracts.KeyAutomation)
	if !ok {
		t.Fatal("KeyAutomation was not provided")
	}
	created := mustCreate(t, service, contracts.AutomationTask{
		Name: "接线", Prompt: "run", Schedule: "every 60s", Enabled: true,
	})
	if err := service.RunNow(context.Background(), created.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	call := waitCall(t, chat)
	waitTask(t, service, created.ID, func(task contracts.AutomationTask) bool {
		return len(task.RunHistory) == 1 && task.RunHistory[0].Status == contracts.AutomationRunDelivered
	})
	chat.complete(call.SessionID)
	waitTerminal(t, service, created.ID)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(notifier.snapshot()) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("module did not use the host notifier: %+v", notifier.snapshot())
}
