package uiroot

// The host notifier bridge: backend modules raise transient toasts, the desktop
// toasts UI module installs a handler and renders them. The service is
// transport-only: it never touches mygo directly.

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// notifierService is the Root's contracts.Notifier implementation. It is safe
// for concurrent use; handler calls never hold the mutex and a panicking
// handler is contained like the other host bridges.
type notifierService struct {
	mu      sync.Mutex
	handler func(contracts.Notification)
	active  map[string]contracts.Notification
	nextID  atomic.Uint64
}

// newNotifierService returns an empty notifier bridge.
func newNotifierService() *notifierService {
	return &notifierService{active: make(map[string]contracts.Notification)}
}

// SetHandler installs the toast handler (nil clears it).
func (s *notifierService) SetHandler(fn func(contracts.Notification)) {
	s.mu.Lock()
	s.handler = fn
	s.mu.Unlock()
}

// Notify posts one toast and returns it with its generated ID. The text is
// trimmed; a notification with no text still gets an ID but is not delivered.
func (s *notifierService) Notify(level, text string) contracts.Notification {
	notification := contracts.Notification{
		ID:    fmt.Sprintf("notice-%d", s.nextID.Add(1)),
		Level: level,
		Text:  strings.TrimSpace(text),
	}
	s.mu.Lock()
	if notification.Text != "" {
		if s.active == nil {
			s.active = make(map[string]contracts.Notification)
		}
		s.active[notification.ID] = notification
	}
	handler := s.handler
	s.mu.Unlock()
	if notification.Text == "" {
		return notification
	}
	if handler != nil {
		runHandlerSafely("notifier", func() { handler(notification) })
	}
	return notification
}

// Dismiss removes one notification's bookkeeping entry; it is idempotent and
// never touches the UI (the toasts module removes its own row).
func (s *notifierService) Dismiss(id string) {
	s.mu.Lock()
	delete(s.active, id)
	s.mu.Unlock()
}

// Notifier returns the host notifier bridge (contracts.KeyNotifier on the
// desktop product).
func (r *Root) Notifier() *notifierService { return r.notifier }
