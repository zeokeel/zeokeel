package host

// Tests for the tray's app actions (new session / settings): menu order, the
// window focus after a successful action, the safe fallback when an action is
// missing or fails, and the logger warning path. The native tray is replaced by
// the same fake factory/window used by the tray tests.

import (
	"errors"
	"sync"
	"testing"

	"github.com/egoist/mygo"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// trayRecordingLogger records log lines for the action-failure assertions.
type trayRecordingLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *trayRecordingLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, format+"|"+joinArgs(args))
}

func (l *trayRecordingLogger) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := ""
	for _, line := range l.lines {
		out += line + "\n"
	}
	return out
}

func joinArgs(args []any) string {
	out := ""
	for _, arg := range args {
		out += " " + sprintfArg(arg)
	}
	return out
}

func sprintfArg(arg any) string {
	if value, ok := arg.(error); ok {
		return value.Error()
	}
	if value, ok := arg.(string); ok {
		return value
	}
	return ""
}

// newTraySetup runs the production setup path with the fake window/factory so
// the actions can be asserted without a desktop session.
func newTraySetup(t *testing.T, actions trayActions, logger contracts.Logger) (*fakeTrayWindow, *fakeTrayFactory, trayHandle, error) {
	t.Helper()
	window := &fakeTrayWindow{}
	factory := &fakeTrayFactory{handle: &fakeTrayHandle{}}
	tray, err := setupTrayWithFactory(window, traySettings(t, ""), nil, actions, factory.create, logger)
	return window, factory, tray, err
}

// menuItems returns the tray menu items captured by the fake factory.
func menuItems(t *testing.T, factory *fakeTrayFactory) []*mygo.MenuItem {
	t.Helper()
	if factory.options == nil || factory.options.Menu == nil {
		t.Fatal("tray factory captured no menu")
	}
	return factory.options.Menu.Items()
}

func TestTrayMenuActionsOrderAndFocus(t *testing.T) {
	var newCalls, openCalls int
	actions := trayActions{
		NewSession: func() error {
			newCalls++
			return nil
		},
		OpenSettings: func() { openCalls++ },
	}
	window, factory, tray, err := newTraySetup(t, actions, nil)
	if err != nil {
		t.Fatalf("setupTrayWithFactory: %v", err)
	}
	if tray == nil {
		t.Fatal("setupTrayWithFactory returned no tray")
	}
	items := menuItems(t, factory)
	var labels []string
	for _, item := range items {
		labels = append(labels, item.Label)
	}
	want := []string{"显示 ZeoKeel", "新建会话", "设置", "", "退出"}
	if len(labels) != len(want) {
		t.Fatalf("tray menu labels = %v, want %v", labels, want)
	}
	for index := range want {
		if labels[index] != want[index] {
			t.Fatalf("tray menu labels = %v, want %v", labels, want)
		}
	}
	if items[3].Type != mygo.MenuItemSeparator {
		t.Fatalf("menu item 3 type = %q, want separator", items[3].Type)
	}

	items[1].Click(items[1], nil)
	if newCalls != 1 {
		t.Fatalf("NewSession calls = %d, want 1", newCalls)
	}
	if window.shown != 1 || window.restored != 1 || window.focused != 1 {
		t.Fatalf("window after new session = show %d/restore %d/focus %d, want 1/1/1",
			window.shown, window.restored, window.focused)
	}

	items[2].Click(items[2], nil)
	if openCalls != 1 {
		t.Fatalf("OpenSettings calls = %d, want 1", openCalls)
	}
	if window.shown != 2 || window.restored != 2 || window.focused != 2 {
		t.Fatalf("window after settings = show %d/restore %d/focus %d, want 2/2/2",
			window.shown, window.restored, window.focused)
	}
}

func TestTrayNewSessionFailureIsSafe(t *testing.T) {
	logger := &trayRecordingLogger{}
	actions := trayActions{
		NewSession: func() error { return errors.New("session store is locked") },
	}
	window, factory, _, err := newTraySetup(t, actions, logger)
	if err != nil {
		t.Fatalf("setupTrayWithFactory: %v", err)
	}
	items := menuItems(t, factory)
	items[1].Click(items[1], nil)
	if window.shown != 0 || window.restored != 0 || window.focused != 0 {
		t.Fatalf("a failed new-session action focused the window: show %d/restore %d/focus %d",
			window.shown, window.restored, window.focused)
	}
	if !stringsContains(logger.text(), "session store is locked") {
		t.Fatalf("logger lines = %q, want the new-session failure", logger.text())
	}
}

func TestTrayActionsMissingMenuItems(t *testing.T) {
	window, factory, _, err := newTraySetup(t, trayActions{}, nil)
	if err != nil {
		t.Fatalf("setupTrayWithFactory: %v", err)
	}
	if window == nil {
		t.Fatal("setup returned no window")
	}
	items := menuItems(t, factory)
	var labels []string
	for _, item := range items {
		labels = append(labels, item.Label)
	}
	for _, unexpected := range []string{"新建会话", "设置"} {
		for _, label := range labels {
			if label == unexpected {
				t.Fatalf("tray menu = %v, unexpectedly contains %q", labels, unexpected)
			}
		}
	}
	if len(items) != 3 {
		t.Fatalf("tray menu items = %v, want show/separator/quit only", labels)
	}
}

func stringsContains(haystack, needle string) bool {
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
