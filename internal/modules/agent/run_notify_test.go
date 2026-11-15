package agent

// Tests for the background-run completion notification (non-active sessions).

import (
	"context"
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

// foregroundSessionStub is a contracts.ForegroundSession with a scripted answer.
type foregroundSessionStub struct {
	sessionID string
	ok        bool
}

func (f *foregroundSessionStub) ForegroundSession() (string, bool) {
	return f.sessionID, f.ok
}

// backgroundRunService builds a service whose completion notifications go to a
// recording notifier.
func backgroundRunService(t *testing.T) (*service, *fakeNotifier) {
	t.Helper()
	svc := newTestService(t, Config{}, newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"}), nil)
	notifier := &fakeNotifier{}
	svc.notifier = notifier
	return svc, notifier
}

// watchingSession creates one session and makes it the backend's active one.
func watchingSession(t *testing.T, svc *service, title string) string {
	t.Helper()
	sessionID, err := svc.NewSession(title)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetActiveSession(sessionID); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	return sessionID
}

// TestBackgroundRunQuietWhenForegroundSessionMatches covers the desktop rule:
// the run finishes for the conversation actually on screen, so it is not news
// even when the backend's active session is another one.
func TestBackgroundRunQuietWhenForegroundSessionMatches(t *testing.T) {
	svc, notifier := backgroundRunService(t)
	sessionID := watchingSession(t, svc, "watching")
	// The backend's active session has moved on; the foreground rule decides.
	if _, err := svc.NewSession("elsewhere"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	svc.foreground = &foregroundSessionStub{sessionID: sessionID, ok: true}
	svc.notifyBackgroundRunFinished(sessionID, nil, context.Background())
	if notifications := notifier.taken(); len(notifications) != 0 {
		t.Fatalf("notifications = %+v, want none for the foreground conversation", notifications)
	}
}

// TestBackgroundRunNotifiesWhenNoForegroundSession covers the case the desktop
// read model exists but nothing is in front of the user (another screen shown):
// the backend's active session must not quiet the notification.
func TestBackgroundRunNotifiesWhenNoForegroundSession(t *testing.T) {
	svc, notifier := backgroundRunService(t)
	sessionID := watchingSession(t, svc, "watching")
	svc.foreground = &foregroundSessionStub{ok: false}
	svc.notifyBackgroundRunFinished(sessionID, nil, context.Background())
	notifications := notifier.taken()
	if len(notifications) != 1 {
		t.Fatalf("notifications = %+v, want one when no conversation is in the foreground", notifications)
	}
	if notifications[0].SessionID != sessionID {
		t.Fatalf("notification session = %q, want %q", notifications[0].SessionID, sessionID)
	}
}

// TestBackgroundRunNotifiesWhenForegroundSessionDiffers covers a foreground
// conversation other than the finished one: the run is background news even
// when the backend still calls the finished session active.
func TestBackgroundRunNotifiesWhenForegroundSessionDiffers(t *testing.T) {
	svc, notifier := backgroundRunService(t)
	sessionID := watchingSession(t, svc, "watching")
	otherID, err := svc.NewSession("other")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// The finished session is still the backend's active one; only the
	// foreground rule can tell the user moved to another conversation.
	if err := svc.SetActiveSession(sessionID); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	svc.foreground = &foregroundSessionStub{sessionID: otherID, ok: true}
	svc.notifyBackgroundRunFinished(sessionID, nil, context.Background())
	if notifications := notifier.taken(); len(notifications) != 1 {
		t.Fatalf("notifications = %+v, want one for a background conversation", notifications)
	}
}

// TestBackgroundRunQuietForActiveSessionWithoutForegroundService covers the
// fallback: a product without the desktop foreground read model (headless) keeps
// the previous active-session rule.
func TestBackgroundRunQuietForActiveSessionWithoutForegroundService(t *testing.T) {
	svc, notifier := backgroundRunService(t)
	sessionID := watchingSession(t, svc, "watching")
	if svc.foreground != nil {
		t.Fatal("a bare test service has a foreground session read model")
	}
	svc.notifyBackgroundRunFinished(sessionID, nil, context.Background())
	if notifications := notifier.taken(); len(notifications) != 0 {
		t.Fatalf("notifications = %+v, want none for the active session (fallback rule)", notifications)
	}
}
