package regression

// Adversarial audit: the host Notifier bridge (contracts.Notifier over
// uiroot.notifierService) and its UI consumer. Negative cases: empty text,
// repeated/unknown dismissal, a panicking handler, nil handler and concurrent
// Notify/Dismiss.

import (
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakeNotifier is a contracts.Notifier test double: it records handler
// installations, deliveries and dismissals, and lets a test trigger the handler
// synchronously (the toasts module installs it at activation).
type fakeNotifier struct {
	mu        sync.Mutex
	handler   func(contracts.Notification)
	delivered []contracts.Notification
	dismissed []string
}

func (f *fakeNotifier) SetHandler(fn func(contracts.Notification)) {
	f.mu.Lock()
	f.handler = fn
	f.mu.Unlock()
}

func (f *fakeNotifier) Notify(level, text string) contracts.Notification {
	notification := contracts.Notification{ID: "fake-" + text, Level: level, Text: text}
	f.mu.Lock()
	f.delivered = append(f.delivered, notification)
	f.mu.Unlock()
	return notification
}

func (f *fakeNotifier) Dismiss(id string) {
	f.mu.Lock()
	f.dismissed = append(f.dismissed, id)
	f.mu.Unlock()
}

// trigger calls the installed handler like the host bridge would.
func (f *fakeNotifier) trigger(notification contracts.Notification) bool {
	f.mu.Lock()
	handler := f.handler
	f.mu.Unlock()
	if handler == nil {
		return false
	}
	handler(notification)
	return true
}

func (f *fakeNotifier) handlerCleared() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.handler == nil
}

func (f *fakeNotifier) dismissedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.dismissed...)
}

// TestNotifierEmptyTextAndDismiss covers hostile/edge calls on the host bridge:
// empty text still returns an ID but is not delivered, and every Dismiss form is
// an idempotent no-op.
func TestNotifierEmptyTextAndDismiss(t *testing.T) {
	root := uiroot.New(uiroot.Config{})
	notifier := root.Notifier()
	delivered := make(chan contracts.Notification, 4)
	notifier.SetHandler(func(notification contracts.Notification) { delivered <- notification })

	for _, level := range []string{"", contracts.NotifyInfo, "bogus-level"} {
		notification := notifier.Notify(level, "   ")
		if notification.ID == "" {
			t.Fatalf("Notify(%q, empty text) returned no ID", level)
		}
		if notification.Text != "" {
			t.Fatalf("Notify(%q, whitespace) text = %q, want empty", level, notification.Text)
		}
		select {
		case got := <-delivered:
			t.Fatalf("empty notification was delivered: %+v", got)
		default:
		}
	}
	// Dismiss is idempotent for known, unknown, empty and repeated IDs.
	known := notifier.Notify(contracts.NotifyInfo, "  real toast  ")
	if known.Text != "real toast" {
		t.Fatalf("Notify text = %q, want trimmed text", known.Text)
	}
	if got := <-delivered; got.ID != known.ID || got.Level != contracts.NotifyInfo || got.Text != "real toast" {
		t.Fatalf("delivered notification = %+v, want %+v", got, known)
	}
	for _, id := range []string{known.ID, known.ID, "", "notice-does-not-exist"} {
		notifier.Dismiss(id) // must never panic
	}
}

// TestNotifierHandlerPanicIsContained covers a panicking toast handler: it is
// recovered, Notify still returns its notification and a later healthy handler
// keeps receiving toasts.
func TestNotifierHandlerPanicIsContained(t *testing.T) {
	root := uiroot.New(uiroot.Config{})
	notifier := root.Notifier()
	notifier.SetHandler(func(contracts.Notification) { panic("toast handler boom") })

	var notification contracts.Notification
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		notification = notifier.Notify(contracts.NotifyInfo, "panic please")
	}()
	if recovered != nil {
		t.Fatalf("Notify propagated a handler panic: %v", recovered)
	}
	if notification.ID == "" || notification.Text != "panic please" {
		t.Fatalf("Notify after a panicking handler = %+v, want the notification", notification)
	}

	delivered := make(chan contracts.Notification, 1)
	notifier.SetHandler(func(got contracts.Notification) { delivered <- got })
	second := notifier.Notify(contracts.NotifySuccess, "after panic")
	select {
	case got := <-delivered:
		if got.ID != second.ID {
			t.Fatalf("delivered %+v, want %+v", got, second)
		}
	default:
		t.Fatal("a healthy handler installed after a panic received no notification")
	}

	// A nil handler is legal: Notify returns the notification without delivery.
	notifier.SetHandler(nil)
	if third := notifier.Notify(contracts.NotifyWarning, "no handler"); third.ID == "" {
		t.Fatal("Notify with a nil handler returned no ID")
	}
}

// TestNotifierConcurrentNotify covers concurrent Notify/Dismiss calls: unique
// IDs, one delivery each and no race (-race).
func TestNotifierConcurrentNotify(t *testing.T) {
	root := uiroot.New(uiroot.Config{})
	notifier := root.Notifier()
	var mu sync.Mutex
	ids := make(map[string]bool)
	var delivered int
	notifier.SetHandler(func(notification contracts.Notification) {
		mu.Lock()
		ids[notification.ID] = true
		delivered++
		mu.Unlock()
		notifier.Dismiss("notice-nonexistent")
	})

	const writers = 20
	var wg sync.WaitGroup
	for index := 0; index < writers; index++ {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			notification := notifier.Notify(contracts.NotifyInfo, "concurrent "+itoa(int64(index)))
			notifier.Dismiss(notification.ID)
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if delivered != writers || len(ids) != writers {
		t.Fatalf("delivered %d notifications with %d unique IDs, want %d each", delivered, len(ids), writers)
	}
}

// TestNotifierLevelAndTextPassthrough covers the bridge's value handling: the
// level is passed through raw and the text is trimmed.
func TestNotifierLevelAndTextPassthrough(t *testing.T) {
	root := uiroot.New(uiroot.Config{})
	notifier := root.Notifier()
	var got contracts.Notification
	notifier.SetHandler(func(notification contracts.Notification) { got = notification })
	notifier.Notify("  Weird-Level  ", "  hello  ")
	if got.Level != "  Weird-Level  " {
		t.Fatalf("level = %q, want the raw level passthrough", got.Level)
	}
	if got.Text != "hello" {
		t.Fatalf("text = %q, want trimmed text", got.Text)
	}
	if !strings.HasPrefix(got.ID, "notice-") {
		t.Fatalf("notification ID = %q, want a notice- prefix", got.ID)
	}
}
