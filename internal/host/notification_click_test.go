package host

// Tests for OS notification click routing: a tagged notification opens its
// conversation (switching the active session and showing the session screen), an
// unknown/deleted session only surfaces the window, a click before startup
// finished is buffered until markReady, and every failure path stays quiet.

import (
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// clickChat is a minimal contracts.ChatService for the click-routing tests.
type clickChat struct {
	sessions []contracts.ChatSessionInfo
	active   string
	setErr   error
	setCalls []string
}

func newClickChat(ids ...string) *clickChat {
	chat := &clickChat{}
	for _, id := range ids {
		chat.sessions = append(chat.sessions, contracts.ChatSessionInfo{ID: id})
	}
	return chat
}

func (c *clickChat) Config() contracts.ChatConfig { return contracts.ChatConfig{} }
func (c *clickChat) Sessions() []contracts.ChatSessionInfo {
	return append([]contracts.ChatSessionInfo(nil), c.sessions...)
}
func (c *clickChat) Session(sessionID string) (contracts.ChatSession, bool) {
	for _, session := range c.sessions {
		if session.ID == sessionID {
			return contracts.ChatSession{ID: sessionID}, true
		}
	}
	return contracts.ChatSession{}, false
}
func (c *clickChat) ActiveSession() string { return c.active }
func (c *clickChat) SetActiveSession(sessionID string) error {
	c.setCalls = append(c.setCalls, sessionID)
	if c.setErr != nil {
		return c.setErr
	}
	c.active = sessionID
	return nil
}
func (c *clickChat) NewSession(string) (string, error) { return "", nil }
func (c *clickChat) DeleteSession(string) error        { return nil }
func (c *clickChat) Send(string, string) error         { return nil }
func (c *clickChat) Cancel(string) error               { return nil }
func (c *clickChat) Subscribe(func(string)) (func(), error) {
	return func() {}, nil
}

// clickNavigator records the session screen pushes.
type clickNavigator struct {
	current string
	pushed  []string
}

func (n *clickNavigator) Current() string { return n.current }
func (n *clickNavigator) Push(screenID string) error {
	n.pushed = append(n.pushed, screenID)
	n.current = screenID
	return nil
}
func (n *clickNavigator) Back() bool         { return false }
func (n *clickNavigator) SettingsOpen() bool { return false }
func (n *clickNavigator) ToggleSettings()    {}
func (n *clickNavigator) CloseSettings()     {}

// clickLogger records the routing warnings.
type clickLogger struct{ messages []string }

func (l *clickLogger) Printf(format string, args ...any) {
	l.messages = append(l.messages, format)
}

func TestNotificationClickOpensTaggedSession(t *testing.T) {
	chat := newClickChat("sess-1")
	navigator := &clickNavigator{}
	logger := &clickLogger{}
	clicker := newNotificationClicker(notificationClickDeps{
		window:    func() *mygo.Window { return nil },
		chat:      func() contracts.ChatService { return chat },
		navigator: func() contracts.Navigator { return navigator },
		session:   func(string) (string, bool) { return "sess-1", true },
		logger:    logger,
	})
	clicker.markReady()
	clicker.handle("notice-1@tag")

	if len(chat.setCalls) != 1 || chat.setCalls[0] != "sess-1" {
		t.Fatalf("SetActiveSession calls = %v, want [sess-1]", chat.setCalls)
	}
	if chat.active != "sess-1" {
		t.Fatalf("active session = %q, want sess-1", chat.active)
	}
	if len(navigator.pushed) != 1 || navigator.pushed[0] != "session" {
		t.Fatalf("navigator pushes = %v, want [session]", navigator.pushed)
	}
	if len(logger.messages) != 0 {
		t.Fatalf("a successful click logged warnings: %v", logger.messages)
	}
}

func TestNotificationClickUnknownSessionOnlyFronts(t *testing.T) {
	chat := newClickChat("other")
	navigator := &clickNavigator{}
	logger := &clickLogger{}
	clicker := newNotificationClicker(notificationClickDeps{
		chat:      func() contracts.ChatService { return chat },
		navigator: func() contracts.Navigator { return navigator },
		session:   func(string) (string, bool) { return "sess-deleted", true },
		logger:    logger,
	})
	clicker.markReady()
	clicker.handle("notice-1@tag")

	if len(chat.setCalls) != 0 {
		t.Fatalf("SetActiveSession calls = %v, want none for a deleted session", chat.setCalls)
	}
	if len(navigator.pushed) != 0 {
		t.Fatalf("navigator pushes = %v, want none", navigator.pushed)
	}
	if len(logger.messages) != 1 || !strings.Contains(logger.messages[0], "no longer exists") {
		t.Fatalf("warnings = %v, want one 'no longer exists' note", logger.messages)
	}
}

func TestNotificationClickUntaggedOnlyFronts(t *testing.T) {
	chat := newClickChat("sess-1")
	navigator := &clickNavigator{}
	clicker := newNotificationClicker(notificationClickDeps{
		chat:      func() contracts.ChatService { return chat },
		navigator: func() contracts.Navigator { return navigator },
		session:   func(string) (string, bool) { return "", false },
	})
	clicker.markReady()
	clicker.handle("notice-1")

	if len(chat.setCalls) != 0 || len(navigator.pushed) != 0 {
		t.Fatalf("an untagged click routed somewhere: set=%v pushed=%v", chat.setCalls, navigator.pushed)
	}
}

func TestNotificationClickBuffersUntilStartupFinishes(t *testing.T) {
	chat := newClickChat("sess-1")
	navigator := &clickNavigator{}
	clicker := newNotificationClicker(notificationClickDeps{
		chat:      func() contracts.ChatService { return chat },
		navigator: func() contracts.Navigator { return navigator },
		session:   func(string) (string, bool) { return "sess-1", true },
	})
	// The click of the notification that launched the app can arrive before the
	// assembly/window exist: nothing is routed yet, the target is kept.
	clicker.handle("notice-1@tag")
	if len(chat.setCalls) != 0 {
		t.Fatalf("a pre-ready click routed immediately: %v", chat.setCalls)
	}
	clicker.markReady()
	if len(chat.setCalls) != 1 || chat.setCalls[0] != "sess-1" {
		t.Fatalf("SetActiveSession calls after markReady = %v, want [sess-1]", chat.setCalls)
	}
	if len(navigator.pushed) != 1 {
		t.Fatalf("navigator pushes = %v, want [session]", navigator.pushed)
	}
}

func TestNotificationClickSwitchFailureFallsBack(t *testing.T) {
	chat := newClickChat("sess-1")
	chat.setErr = errors.New("backend busy")
	navigator := &clickNavigator{}
	logger := &clickLogger{}
	clicker := newNotificationClicker(notificationClickDeps{
		chat:      func() contracts.ChatService { return chat },
		navigator: func() contracts.Navigator { return navigator },
		session:   func(string) (string, bool) { return "sess-1", true },
		logger:    logger,
	})
	clicker.markReady()
	clicker.handle("notice-1@tag")

	if len(navigator.pushed) != 0 {
		t.Fatalf("navigator pushes = %v, want none after a failed switch", navigator.pushed)
	}
	if len(logger.messages) != 1 || !strings.Contains(logger.messages[0], "switch to session") {
		t.Fatalf("warnings = %v, want one switch note", logger.messages)
	}
}

func TestNotificationClickNilDepsIsSafe(t *testing.T) {
	clicker := newNotificationClicker(notificationClickDeps{})
	clicker.handle("notice-1@tag")
	clicker.markReady()
}
