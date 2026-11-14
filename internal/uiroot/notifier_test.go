package uiroot

// Tests (written before the implementation, TDD) for the host notifier bridge:
// ids, trimming, handler containment, dismissal idempotency and concurrency.

import (
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func (s *notifierService) activeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.active)
}

func TestNotifierNotifyDeliversTrimmedNotification(t *testing.T) {
	root := New(Config{})
	notifier := root.Notifier()
	var (
		mu       sync.Mutex
		received []contracts.Notification
	)
	notifier.SetHandler(func(notification contracts.Notification) {
		mu.Lock()
		received = append(received, notification)
		mu.Unlock()
	})

	notification := notifier.Notify(contracts.NotifySuccess, "  已保存  ")
	if notification.ID != "notice-1" {
		t.Fatalf("Notify id = %q, want notice-1", notification.ID)
	}
	if notification.Text != "已保存" || notification.Level != contracts.NotifySuccess {
		t.Fatalf("Notify = %+v, want the trimmed success copy", notification)
	}
	mu.Lock()
	delivered := append([]contracts.Notification(nil), received...)
	mu.Unlock()
	if len(delivered) != 1 || delivered[0].ID != notification.ID || delivered[0].Text != "已保存" {
		t.Fatalf("handler notifications = %+v, want the posted toast", delivered)
	}
	if pending := notifier.activeCount(); pending != 1 {
		t.Fatalf("active notifications = %d, want 1", pending)
	}

	second := notifier.Notify(contracts.NotifyError, "失败")
	if second.ID != "notice-2" {
		t.Fatalf("second id = %q, want notice-2", second.ID)
	}
}

func TestNotifierEmptyTextIsNotDelivered(t *testing.T) {
	root := New(Config{})
	notifier := root.Notifier()
	calls := atomic.Int32{}
	notifier.SetHandler(func(contracts.Notification) { calls.Add(1) })

	notification := notifier.Notify(contracts.NotifyInfo, "   ")
	if notification.ID == "" {
		t.Fatal("Notify returned no id for an empty text")
	}
	if calls.Load() != 0 {
		t.Fatalf("handler calls for an empty notification = %d, want 0", calls.Load())
	}
	if pending := notifier.activeCount(); pending != 0 {
		t.Fatalf("active notifications = %d, want 0 for an empty text", pending)
	}
}

func TestNotifierDismissIsIdempotent(t *testing.T) {
	root := New(Config{})
	notifier := root.Notifier()
	notification := notifier.Notify(contracts.NotifyWarning, "注意")
	notifier.Dismiss(notification.ID)
	notifier.Dismiss(notification.ID) // second call must not panic
	notifier.Dismiss("notice-unknown")
	if pending := notifier.activeCount(); pending != 0 {
		t.Fatalf("active notifications after dismiss = %d, want 0", pending)
	}
}

func TestNotifierContainsHandlerPanic(t *testing.T) {
	root := New(Config{})
	notifier := root.Notifier()
	notifier.SetHandler(func(contracts.Notification) { panic("toast handler boom") })

	var recovered any
	var notification contracts.Notification
	func() {
		defer func() { recovered = recover() }()
		notification = notifier.Notify(contracts.NotifyError, "boom")
	}()
	if recovered != nil {
		t.Fatalf("Notify propagated the handler panic: %v", recovered)
	}
	if notification.ID == "" {
		t.Fatal("Notify returned no id after a contained handler panic")
	}
}

func TestNotifierConcurrentUse(t *testing.T) {
	root := New(Config{})
	notifier := root.Notifier()
	notifier.SetHandler(func(notification contracts.Notification) {
		if notification.ID == "" {
			t.Error("handler received a notification without an id")
		}
	})

	var wg sync.WaitGroup
	ids := make(chan string, 32)
	for worker := 0; worker < 8; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := 0; index < 4; index++ {
				notification := notifier.Notify(contracts.NotifyInfo, "并发")
				ids <- notification.ID
				if worker%2 == 0 {
					notifier.Dismiss(notification.ID)
				}
			}
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate notification id %q", id)
		}
		seen[id] = true
	}
}

// osCall records one OS notification mirror.
type osCall struct {
	id, title, body string
}

// fakeNotifierSettings is a minimal contracts.SettingsService for the OS-mirror
// tests: only the system-notification switch matters there.
type fakeNotifierSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func (s *fakeNotifierSettings) Get(key, def string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return def
}

func (s *fakeNotifierSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[key] = value
	return nil
}

func (s *fakeNotifierSettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out
}

func TestNotifierNotifySystemMirrorsToOS(t *testing.T) {
	root := New(Config{AppName: "ZeoKeelTest"})
	calls := make(chan osCall, 1)
	root.SetOSNotifier(func(id, title, body string) error {
		calls <- osCall{id: id, title: title, body: body}
		return nil
	})
	if !root.SystemNotificationsSupported() {
		t.Fatal("SystemNotificationsSupported() = false after SetOSNotifier")
	}
	var toasts atomic.Int32
	root.Notifier().SetHandler(func(contracts.Notification) { toasts.Add(1) })

	notification := root.Notifier().NotifySystem(contracts.NotifySuccess, "  自动化完成  ")
	select {
	case call := <-calls:
		if call.id != notification.ID {
			t.Fatalf("OS notification id = %q, want the in-app id %q", call.id, notification.ID)
		}
		if call.title != "ZeoKeelTest" {
			t.Fatalf("OS notification title = %q, want the app name", call.title)
		}
		if call.body != "自动化完成" {
			t.Fatalf("OS notification body = %q, want the trimmed copy", call.body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("NotifySystem did not mirror to the OS sink")
	}
	if got := toasts.Load(); got != 1 {
		t.Fatalf("in-app toast handler calls = %d, want 1 (the toast stays)", got)
	}
	if pending := root.Notifier().activeCount(); pending != 1 {
		t.Fatalf("active notifications = %d, want 1", pending)
	}
}

func TestNotifierPlainToastStaysInApp(t *testing.T) {
	root := New(Config{AppName: "ZeoKeelTest"})
	var calls atomic.Int32
	root.SetOSNotifier(func(string, string, string) error {
		calls.Add(1)
		return nil
	})
	root.Notifier().Notify(contracts.NotifySuccess, "已复制")
	// A plain toast never starts the mirror goroutine, so no call can appear.
	if got := calls.Load(); got != 0 {
		t.Fatalf("OS notification calls for a plain toast = %d, want 0", got)
	}
}

func TestNotifierOSNotificationTarget(t *testing.T) {
	notification := contracts.Notification{ID: "notice-7", Text: "完成"}
	sink := func(string, string, string) error { return nil }

	tests := []struct {
		name     string
		settings contracts.SettingsService
		sink     func(id, title, body string) error
		wantOK   bool
		wantBody string
	}{
		{name: "no sink", sink: nil, wantOK: false},
		{name: "default on", sink: sink, wantOK: true, wantBody: "完成"},
		{name: "stored true", settings: &fakeNotifierSettings{values: map[string]string{
			contracts.SettingSystemNotifications: "true"}}, sink: sink, wantOK: true, wantBody: "完成"},
		{name: "stored false", settings: &fakeNotifierSettings{values: map[string]string{
			contracts.SettingSystemNotifications: "false"}}, sink: sink, wantOK: false},
		{name: "stored no", settings: &fakeNotifierSettings{values: map[string]string{
			contracts.SettingSystemNotifications: "NO"}}, sink: sink, wantOK: false},
		{name: "malformed stays on", settings: &fakeNotifierSettings{values: map[string]string{
			contracts.SettingSystemNotifications: "banana"}}, sink: sink, wantOK: true, wantBody: "完成"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newNotifierService(test.settings, "ZeoKeel")
			service.setOSNotifier(test.sink)
			_, id, title, body, ok := service.osNotificationTarget(notification)
			if ok != test.wantOK {
				t.Fatalf("osNotificationTarget ok = %v, want %v", ok, test.wantOK)
			}
			if !ok {
				return
			}
			if id != "notice-7" || title != "ZeoKeel" || body != test.wantBody {
				t.Fatalf("osNotificationTarget = {%q %q %q}, want notice-7/ZeoKeel/%q", id, title, body, test.wantBody)
			}
		})
	}

	// Empty copy and empty id never reach the OS.
	service := newNotifierService(nil, "ZeoKeel")
	service.setOSNotifier(sink)
	if _, _, _, _, ok := service.osNotificationTarget(contracts.Notification{ID: "notice-1"}); ok {
		t.Fatal("an empty text reached the OS")
	}
	if _, _, _, _, ok := service.osNotificationTarget(contracts.Notification{Text: "完成"}); ok {
		t.Fatal("a notification without an id reached the OS")
	}
}

func TestNotifierSystemNotificationSwitchOffSkipsOS(t *testing.T) {
	settings := &fakeNotifierSettings{}
	root := New(Config{AppName: "ZeoKeelTest", Settings: settings})
	var calls atomic.Int32
	root.SetOSNotifier(func(string, string, string) error {
		calls.Add(1)
		return nil
	})
	root.Notifier().NotifySystem(contracts.NotifyError, "失败") // default on
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("OS notification calls with the default setting = %d, want 1", got)
	}
	if err := settings.Set(contracts.SettingSystemNotifications, "false"); err != nil {
		t.Fatalf("Set(system notifications): %v", err)
	}
	root.Notifier().NotifySystem(contracts.NotifyError, "失败")
	root.Notifier().NotifySystemFor(contracts.NotifySuccess, "完成", "sess-1")
	// The decision is synchronous; no goroutine can add another call afterwards
	// except the one above, which already completed.
	time.Sleep(20 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("OS notification calls after turning the setting off = %d, want 1", got)
	}
	// The session-tagged notification still shows in-app while the OS mirror is
	// off: the switch only governs the OS channel.
	if sessionID, ok := root.NotificationSession("notice-2@" + base64.RawURLEncoding.EncodeToString([]byte("sess-1"))); !ok || sessionID != "sess-1" {
		t.Fatalf("tagged notification id did not resolve after the switch off: (%q, %v)", sessionID, ok)
	}
}

func TestNotifierOSNotificationErrorIsContained(t *testing.T) {
	root := New(Config{AppName: "ZeoKeelTest"})
	root.SetOSNotifier(func(string, string, string) error { return errors.New("denied") })
	done := make(chan struct{})
	root.Notifier().SetHandler(func(contracts.Notification) { close(done) })
	root.Notifier().NotifySystem(contracts.NotifyInfo, "完成")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the in-app toast was not delivered when the OS sink failed")
	}
}

func TestNotifierSetOSNotifierNilDisables(t *testing.T) {
	root := New(Config{})
	root.SetOSNotifier(func(string, string, string) error { return nil })
	if !root.SystemNotificationsSupported() {
		t.Fatal("SystemNotificationsSupported() = false with a sink")
	}
	root.SetOSNotifier(nil)
	if root.SystemNotificationsSupported() {
		t.Fatal("SystemNotificationsSupported() = true after removing the sink")
	}
	if (*Root)(nil).SystemNotificationsSupported() {
		t.Fatal("SystemNotificationsSupported() on a nil root = true")
	}
}

func TestNotifierNotifySystemForTagsSession(t *testing.T) {
	root := New(Config{AppName: "ZeoKeelTest"})
	calls := make(chan osCall, 1)
	root.SetOSNotifier(func(id, title, body string) error {
		calls <- osCall{id: id, title: title, body: body}
		return nil
	})
	var (
		mu       sync.Mutex
		received contracts.Notification
		toasts   int
	)
	root.Notifier().SetHandler(func(notification contracts.Notification) {
		mu.Lock()
		received = notification
		toasts++
		mu.Unlock()
	})

	notification := root.Notifier().NotifySystemFor(contracts.NotifySuccess, "自动化完成", "sess-abc")
	if notification.SessionID != "sess-abc" {
		t.Fatalf("notification session = %q, want sess-abc", notification.SessionID)
	}
	mu.Lock()
	got := received
	gotToasts := toasts
	mu.Unlock()
	if gotToasts != 1 || got.ID != notification.ID || got.SessionID != "sess-abc" {
		t.Fatalf("in-app witness = %+v (toasts %d), want the tagged notification", got, gotToasts)
	}
	if sessionID, ok := root.NotificationSession(notification.ID); !ok || sessionID != "sess-abc" {
		t.Fatalf("NotificationSession(%q) = (%q, %v), want sess-abc", notification.ID, sessionID, ok)
	}
	select {
	case call := <-calls:
		if call.id != notification.ID || call.body != "自动化完成" {
			t.Fatalf("OS mirror = %+v, want the tagged notification id and body", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("NotifySystemFor did not mirror to the OS sink")
	}
}

func TestNotificationSessionResolvesAcrossRuns(t *testing.T) {
	// A tagged id resolves without any in-process state: a click on a
	// notification from an earlier run (which may have launched the app) still
	// knows its conversation.
	tagged := notificationID(42, "sess/odd chars@still-encoded")
	fresh := New(Config{})
	if sessionID, ok := fresh.NotificationSession(tagged); !ok || sessionID != "sess/odd chars@still-encoded" {
		t.Fatalf("NotificationSession(%q) = (%q, %v), want the tagged session", tagged, sessionID, ok)
	}
	// Plain notifications (and unrelated ids) carry no session.
	root := New(Config{})
	plain := root.Notifier().Notify(contracts.NotifyInfo, "已保存")
	for _, id := range []string{plain.ID, "", "notice-1", "notice-1@", "notice-1@!!!", "@abc", tagged + "-junk-marker"} {
		if sessionID, ok := root.NotificationSession(id); ok && sessionID == "" {
			t.Fatalf("NotificationSession(%q) returned an empty session", id)
		}
	}
	if _, ok := root.NotificationSession(plain.ID); ok {
		t.Fatalf("a plain notification id %q resolved to a session", plain.ID)
	}
}

func TestNotifierNotifySystemForEmptySessionIsUntagged(t *testing.T) {
	root := New(Config{})
	notification := root.Notifier().NotifySystemFor(contracts.NotifyInfo, "完成", "   ")
	if notification.SessionID != "" {
		t.Fatalf("session = %q, want none", notification.SessionID)
	}
	if strings.Contains(notification.ID, notificationSessionMark) {
		t.Fatalf("id %q has a session marker although the session is empty", notification.ID)
	}
}
