package uiroot

// The host notifier bridge: backend modules raise transient toasts, the desktop
// toasts UI module installs a handler and renders them. The service is
// transport-only: it never touches mygo directly.
//
// Notifications flagged through the optional contracts.SystemNotifier half are
// also mirrored to the operating system's notification center through a sink the
// desktop host installs once mygo is ready (Root.SetOSNotifier). The mirror is
// best effort: without a sink (tests, headless, a platform without notification
// support) notifications stay in-app, and the ui.notifications.system setting can
// turn the mirror off without touching the toast overlay.

import (
	"encoding/base64"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// The notification ID carries the session tag so a click can open the
// conversation it belongs to. The tag is the raw-URL base64 of the session id:
// '@' cannot appear in it, so the last separator splits sequence from tag, and
// the tag survives the OS round trip (a click on a notification from an earlier
// run, which may have launched this instance, still resolves).
const (
	notificationIDPrefix    = "notice-"
	notificationSessionMark = "@"
)

// notifierService is the Root's contracts.Notifier implementation. It is safe
// for concurrent use; handler calls never hold the mutex and a panicking
// handler is contained like the other host bridges.
type notifierService struct {
	mu      sync.Mutex
	handler func(contracts.Notification)
	active  map[string]contracts.Notification
	nextID  atomic.Uint64
	// settings is the optional host settings store: the ui.notifications.system
	// switch is read per notification so turning it off applies without a
	// restart. Nil keeps the default (on).
	settings contracts.SettingsService
	// appName is the OS notification's title.
	appName string
	// osNotify shows one notification on the OS. Nil means the platform has no
	// usable notification support (the settings page disables its switch).
	osNotify func(id, title, body string) error
}

// newNotifierService returns an empty notifier bridge. settings and appName feed
// the optional OS mirror; both may be zero values.
func newNotifierService(settings contracts.SettingsService, appName string) *notifierService {
	return &notifierService{
		active:   make(map[string]contracts.Notification),
		settings: settings,
		appName:  strings.TrimSpace(appName),
	}
}

// SetHandler installs the toast handler (nil clears it).
func (s *notifierService) SetHandler(fn func(contracts.Notification)) {
	s.mu.Lock()
	s.handler = fn
	s.mu.Unlock()
}

// setOSNotifier installs the OS notification sink (nil clears it). The desktop
// host calls it through Root.SetOSNotifier once mygo is ready.
func (s *notifierService) setOSNotifier(notify func(id, title, body string) error) {
	s.mu.Lock()
	s.osNotify = notify
	s.mu.Unlock()
}

// SystemNotificationsSupported implements contracts.SystemNotifier: true when
// the host installed a working OS notification sink.
func (s *notifierService) SystemNotificationsSupported() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.osNotify != nil
}

// Notify posts one toast and returns it with its generated ID. The text is
// trimmed; a notification with no text still gets an ID but is not delivered.
func (s *notifierService) Notify(level, text string) contracts.Notification {
	return s.notify(level, text, "", nil, false, "")
}

// NotifySystem posts one toast that is also mirrored to the OS notification
// center (contracts.SystemNotifier): background run completions/failures and
// approval waits. Ordinary toasts never reach the OS.
func (s *notifierService) NotifySystem(level, text string) contracts.Notification {
	return s.notify(level, text, "", nil, true, "")
}

// NotifySystemFor implements contracts.SessionNotifier: NotifySystem tagged with
// the conversation the notification belongs to, so a click can open it. The
// session is carried on the notification and encoded in its ID (see
// Root.NotificationSession); an empty sessionID behaves like NotifySystem.
func (s *notifierService) NotifySystemFor(level, text, sessionID string) contracts.Notification {
	return s.notify(level, text, "", nil, true, strings.TrimSpace(sessionID))
}

// NotifyAction posts one toast with an optional action button ("Undo") that
// runs action on the UI thread when pressed. It implements the optional
// contracts.ActionNotifier half, so callers that support undo can offer it
// without making plain notifiers grow the capability.
func (s *notifierService) NotifyAction(level, text, actionLabel string, action func()) contracts.Notification {
	return s.notify(level, text, actionLabel, action, false, "")
}

// notify builds and delivers one notification. system mirrors it to the OS
// sink; sessionID tags the conversation a click should open; the in-app toast is
// unchanged by either.
func (s *notifierService) notify(level, text, actionLabel string, action func(), system bool, sessionID string) contracts.Notification {
	notification := contracts.Notification{
		ID:          notificationID(s.nextID.Add(1), sessionID),
		Level:       level,
		SessionID:   sessionID,
		Text:        strings.TrimSpace(text),
		ActionLabel: strings.TrimSpace(actionLabel),
		Action:      action,
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
	if system {
		s.mirrorToOS(notification)
	}
	return notification
}

// osNotificationTarget reports the sink and the id/title/body of the OS mirror,
// or ok=false when it is off: no sink installed, the system notification
// setting turned off, or no copy to show. It is separate from mirrorToOS so the
// decision is unit-testable without the delivery goroutine.
func (s *notifierService) osNotificationTarget(notification contracts.Notification) (
	notify func(id, title, body string) error, id, title, body string, ok bool,
) {
	s.mu.Lock()
	notify, settings, appName := s.osNotify, s.settings, s.appName
	s.mu.Unlock()
	if notify == nil {
		return nil, "", "", "", false
	}
	if settings != nil && !contracts.SystemNotificationsEnabled(settings.Get(contracts.SettingSystemNotifications, "")) {
		return nil, "", "", "", false
	}
	id = strings.TrimSpace(notification.ID)
	body = strings.TrimSpace(notification.Text)
	if id == "" || body == "" {
		return nil, "", "", "", false
	}
	title = appName
	if title == "" {
		title = "ZeoKeel"
	}
	return notify, id, title, body, true
}

// mirrorToOS shows one notification on the OS in the background. The sink's
// call may wait for the platform (the first macOS notification waits for the
// authorization answer), so it must never block the notifying goroutine, which
// may be the UI thread or an agent run.
func (s *notifierService) mirrorToOS(notification contracts.Notification) {
	notify, id, title, body, ok := s.osNotificationTarget(notification)
	if !ok {
		return
	}
	go func() {
		if err := notify(id, title, body); err != nil {
			log.Printf("uiroot: system notification %q: %v", id, err)
		}
	}()
}

// Dismiss removes one notification's bookkeeping entry; it is idempotent and
// never touches the UI (the toasts module removes its own row).
func (s *notifierService) Dismiss(id string) {
	s.mu.Lock()
	delete(s.active, id)
	s.mu.Unlock()
}

// notificationID names one notification: the bridge's sequence, plus the
// session tag when the notification belongs to a conversation.
func notificationID(sequence uint64, sessionID string) string {
	id := notificationIDPrefix + strconv.FormatUint(sequence, 10)
	if sessionID != "" {
		id += notificationSessionMark + base64.RawURLEncoding.EncodeToString([]byte(sessionID))
	}
	return id
}

// decodeNotificationSession extracts the session tag of an ID produced by
// notificationID; ok=false for plain notifications and for IDs without a
// (valid) tag.
func decodeNotificationSession(id string) (string, bool) {
	id = strings.TrimSpace(id)
	index := strings.LastIndex(id, notificationSessionMark)
	if index < 0 || index == len(id)-1 {
		return "", false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(id[index+1:])
	if err != nil || len(decoded) == 0 {
		return "", false
	}
	return string(decoded), true
}

// Notifier returns the host notifier bridge (contracts.KeyNotifier on the
// desktop product).
func (r *Root) Notifier() *notifierService { return r.notifier }

// NotificationSession returns the conversation a notification click should
// open, when the notifier tagged the notification with one
// (contracts.SessionNotifier). It reads the tag from the notification ID, so it
// resolves a click on a notification from an earlier run too — mygo reports the
// click of the notification that launched the app, when no in-process record
// exists any more. Plain notifications return ok=false and the click only
// surfaces the window.
func (r *Root) NotificationSession(notificationID string) (string, bool) {
	return decodeNotificationSession(notificationID)
}

// SetOSNotifier installs the OS notification sink the notifier mirrors
// contracts.SystemNotifier notifications to. The desktop host wires mygo's
// notification API here once the application is ready; nil removes the sink and
// marks the platform unsupported (the settings page disables its switch).
// Call it on the UI thread during startup.
func (r *Root) SetOSNotifier(notify func(id, title, body string) error) {
	if r == nil || r.notifier == nil {
		return
	}
	r.notifier.setOSNotifier(notify)
}

// SystemNotificationsSupported reports whether the desktop host installed a
// working OS notification sink (contracts.SystemNotifier on the notifier).
func (r *Root) SystemNotificationsSupported() bool {
	if r == nil || r.notifier == nil {
		return false
	}
	return r.notifier.SystemNotificationsSupported()
}
