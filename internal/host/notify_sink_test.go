package host

// The OS notification chain (AUDIT-2026-11-24 A-7): the gateway raises the
// notification, the tray shell (this process) mirrors the ones the caller marked
// System to the OS, and the "system notifications" setting turns that off.
// Before this wiring `mygoOSNotifier` had no caller at all: the switch wrote a
// setting and nothing ever read it.

import (
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// recorderNotifier is a contracts.Notifier that keeps the installed handler.
type recorderNotifier struct {
	mu      sync.Mutex
	handler func(contracts.Notification)
	posted  int
}

func (n *recorderNotifier) SetHandler(fn func(contracts.Notification)) {
	n.mu.Lock()
	n.handler = fn
	n.mu.Unlock()
}

func (n *recorderNotifier) Notify(string, string) contracts.Notification {
	n.mu.Lock()
	n.posted++
	n.mu.Unlock()
	return contracts.Notification{}
}

func (n *recorderNotifier) Dismiss(string) {}

// fire posts one notification through the installed handler.
func (n *recorderNotifier) fire(notification contracts.Notification) bool {
	n.mu.Lock()
	handler := n.handler
	n.mu.Unlock()
	if handler == nil {
		return false
	}
	handler(notification)
	return true
}

// sinkRecorder records deliveries to the (fake) OS sink.
type sinkRecorder struct {
	mu     sync.Mutex
	titles []string
}

func (s *sinkRecorder) deliver(_ string, _ string, body string) error {
	s.mu.Lock()
	s.titles = append(s.titles, body)
	s.mu.Unlock()
	return nil
}

func (s *sinkRecorder) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.titles)
}

// waitForCount waits until the (goroutine) delivery shows up.
func (s *sinkRecorder) waitForCount(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.count() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("os sink received %d notifications, want %d", s.count(), want)
}

func TestSystemNotifierDeliversOnlySystemNotifications(t *testing.T) {
	notifier := &recorderNotifier{}
	sink := &sinkRecorder{}
	installSystemNotifierWith(notifier, nil, nil, sink.deliver)

	if !notifier.fire(contracts.Notification{ID: "t1", Level: "info", Text: "Copied"}) {
		t.Fatal("installSystemNotifierWith did not install a handler")
	}
	if !notifier.fire(contracts.Notification{ID: "t2", Level: "info", Text: "Run finished", System: true}) {
		t.Fatal("handler disappeared")
	}
	sink.waitForCount(t, 1)
	if got := sink.count(); got != 1 {
		t.Fatalf("os sink got %d notifications, want only the System one", got)
	}
}

func TestSystemNotifierRespectsTheSetting(t *testing.T) {
	notifier := &recorderNotifier{}
	sink := &sinkRecorder{}
	settings, err := NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	if err := settings.Set(contracts.SettingSystemNotifications, "false"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	installSystemNotifierWith(notifier, settings, nil, sink.deliver)

	notifier.fire(contracts.Notification{ID: "t1", Level: "info", Text: "Run finished", System: true})
	time.Sleep(50 * time.Millisecond)
	if got := sink.count(); got != 0 {
		t.Fatalf("os sink got %d notifications although the setting is off", got)
	}

	// Turning it back on delivers again.
	if err := settings.Set(contracts.SettingSystemNotifications, "true"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	notifier.fire(contracts.Notification{ID: "t2", Level: "info", Text: "Approval waiting", System: true})
	sink.waitForCount(t, 1)
}
