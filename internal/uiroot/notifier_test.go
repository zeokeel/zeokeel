package uiroot

// Tests (written before the implementation, TDD) for the host notifier bridge:
// ids, trimming, handler containment, dismissal idempotency and concurrency.

import (
	"sync"
	"sync/atomic"
	"testing"

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
