package agent

// Tests for the background-run completion notification (non-active sessions).

import (
	"sync"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeNotifier records every toast the backend posts.
type fakeNotifier struct {
	mu            sync.Mutex
	notifications []contracts.Notification
}

func (f *fakeNotifier) SetHandler(func(contracts.Notification)) {}

func (f *fakeNotifier) Notify(level, text string) contracts.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	notification := contracts.Notification{Level: level, Text: text}
	f.notifications = append(f.notifications, notification)
	return notification
}

func (f *fakeNotifier) Dismiss(string) {}

// SystemNotificationsSupported implements contracts.SessionNotifier.
func (f *fakeNotifier) SystemNotificationsSupported() bool { return true }

// NotifySystemFor implements contracts.SessionNotifier: the session tag lets a
// notification click open the conversation.
func (f *fakeNotifier) NotifySystemFor(level, text, sessionID string) contracts.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	notification := contracts.Notification{Level: level, Text: text, SessionID: sessionID}
	f.notifications = append(f.notifications, notification)
	return notification
}

// taken returns and clears the recorded notifications.
func (f *fakeNotifier) taken() []contracts.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]contracts.Notification(nil), f.notifications...)
	f.notifications = nil
	return out
}

// TestBackgroundRunFinishedNotifiesNonActiveSession covers the success path.
func TestBackgroundRunFinishedNotifiesNonActiveSession(t *testing.T) {
	svc := newTestService(t, Config{}, newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"}), nil)
	provider := &captureProvider{}
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	notifier := &fakeNotifier{}
	svc.notifier = notifier
	activeID, _ := svc.NewSession("active")
	backgroundID, _ := svc.NewSession("background task")
	if err := svc.SetActiveSession(activeID); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	if err := svc.Send(backgroundID, "work in the background"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, backgroundID)
	notifications := notifier.taken()
	if len(notifications) != 1 {
		t.Fatalf("notifications = %+v, want one for the background run", notifications)
	}
	if notifications[0].Level != contracts.NotifySuccess {
		t.Fatalf("notification level = %q, want success", notifications[0].Level)
	}
	if notifications[0].SessionID != backgroundID {
		t.Fatalf("notification session = %q, want %q", notifications[0].SessionID, backgroundID)
	}
	if !containsText(notifications[0].Text, "background task") || !containsText(notifications[0].Text, "已完成") {
		t.Fatalf("notification text = %q, want the session title and completion copy", notifications[0].Text)
	}
}

// TestBackgroundRunFailureNotifies covers the failure path.
func TestBackgroundRunFailureNotifies(t *testing.T) {
	svc := newTestService(t, Config{}, newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"}), nil)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return failingProvider{}, nil }
	notifier := &fakeNotifier{}
	svc.notifier = notifier
	activeID, _ := svc.NewSession("active")
	backgroundID, _ := svc.NewSession("failing job")
	if err := svc.SetActiveSession(activeID); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	if err := svc.Send(backgroundID, "fail"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, backgroundID)
	notifications := notifier.taken()
	if len(notifications) != 1 || notifications[0].Level != contracts.NotifyError {
		t.Fatalf("notifications = %+v, want one error notification", notifications)
	}
	if !containsText(notifications[0].Text, "运行失败") || notifications[0].SessionID != backgroundID {
		t.Fatalf("notification = %+v, want failure copy and the session id", notifications[0])
	}
}

// TestActiveSessionRunDoesNotNotify: the user is watching that conversation.
func TestActiveSessionRunDoesNotNotify(t *testing.T) {
	svc := newTestService(t, Config{}, newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"}), nil)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return &captureProvider{}, nil }
	notifier := &fakeNotifier{}
	svc.notifier = notifier
	activeID, _ := svc.NewSession("watching")
	if err := svc.SetActiveSession(activeID); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	if err := svc.Send(activeID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, activeID)
	if notifications := notifier.taken(); len(notifications) != 0 {
		t.Fatalf("notifications = %+v, want none for the active session", notifications)
	}
}

// TestCanceledBackgroundRunDoesNotNotify: a user Stop is explicit, not news.
func TestCanceledBackgroundRunDoesNotNotify(t *testing.T) {
	provider := &blockingProvider{started: make(chan struct{}, 1)}
	svc := newTestService(t, Config{}, newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"}), nil)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	notifier := &fakeNotifier{}
	svc.notifier = notifier
	activeID, _ := svc.NewSession("active")
	backgroundID, _ := svc.NewSession("stopped")
	if err := svc.SetActiveSession(activeID); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	if err := svc.Send(backgroundID, "long job"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case <-provider.started:
	case <-time.After(3 * time.Second):
		t.Fatal("run never reached the provider")
	}
	if err := svc.Cancel(backgroundID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	waitIdle(t, svc, backgroundID)
	time.Sleep(20 * time.Millisecond)
	if notifications := notifier.taken(); len(notifications) != 0 {
		t.Fatalf("notifications = %+v, want none after a user Stop", notifications)
	}
}

// containsText is a small substring helper.
func containsText(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return index
		}
	}
	return -1
}
