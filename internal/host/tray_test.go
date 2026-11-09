package host

// Tests for the system tray wiring and close-to-tray policy. The native tray
// needs a desktop session (and fails under xvfb), so the tests inject a fake
// factory/window/handler and assert the options, the menu actions and the close
// interception; the real NewTray call is only exercised by the desktop smoke.

import (
	"bytes"
	"errors"
	"image/png"
	"strings"
	"testing"

	"github.com/egoist/mygo"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeTrayWindow records the window calls made by the tray.
type fakeTrayWindow struct {
	shown, restored, focused, hidden int
	closeRegistered                  bool
	closeFn                          func(*mygo.CloseEvent)
}

func (f *fakeTrayWindow) Show()    { f.shown++ }
func (f *fakeTrayWindow) Restore() { f.restored++ }
func (f *fakeTrayWindow) Focus()   { f.focused++ }
func (f *fakeTrayWindow) Hide()    { f.hidden++ }
func (f *fakeTrayWindow) OnClose(fn func(*mygo.CloseEvent)) func() {
	f.closeRegistered = true
	f.closeFn = fn
	return func() {}
}

// fakeTrayHandle records the tray listeners.
type fakeTrayHandle struct {
	onClick   func()
	destroyed bool
}

func (f *fakeTrayHandle) OnClick(fn func()) func() {
	f.onClick = fn
	return func() {}
}
func (f *fakeTrayHandle) Destroy() { f.destroyed = true }

// fakeTrayFactory captures the options and returns the configured handle/error.
type fakeTrayFactory struct {
	options *mygo.TrayOptions
	handle  trayHandle
	err     error
}

func (f *fakeTrayFactory) create(options mygo.TrayOptions) (trayHandle, error) {
	f.options = &options
	if f.err != nil {
		return nil, f.err
	}
	return f.handle, nil
}

// traySettings builds settings with the close-to-tray value.
func traySettings(t *testing.T, value string) *Settings {
	t.Helper()
	settings, err := NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	if value != "" {
		if err := settings.Set(contracts.SettingCloseToTray, value); err != nil {
			t.Fatalf("Set(closeToTray): %v", err)
		}
	}
	return settings
}

func TestCloseToTrayEnabled(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{"yes", true},
		{" false ", false},
		{"0", false},
		{"no", false},
		{"banana", false},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			if got := closeToTrayEnabled(traySettings(t, test.value)); got != test.want {
				t.Fatalf("closeToTrayEnabled(%q) = %v, want %v", test.value, got, test.want)
			}
		})
	}
	if closeToTrayEnabled(nil) {
		t.Fatal("closeToTrayEnabled(nil) = true, want false")
	}
	// No stored setting means off.
	if closeToTrayEnabled(traySettings(t, "")) {
		t.Fatal("close-to-tray is on by default")
	}
}

func TestCreateTrayWiresIconMenuAndActions(t *testing.T) {
	window := &fakeTrayWindow{}
	tray := &fakeTrayHandle{}
	factory := &fakeTrayFactory{handle: tray}
	var shows, quits int
	handler := trayHandler{
		show: func() { shows++ },
		quit: func() { quits++ },
	}

	created, err := createTray(window, handler, false, nil, factory.create)
	if err != nil {
		t.Fatalf("createTray: %v", err)
	}
	if created != tray {
		t.Fatal("createTray returned a different handle")
	}
	if factory.options == nil {
		t.Fatal("tray factory received no options")
	}
	if factory.options.ID != "zeokeel" || factory.options.ToolTip != appName {
		t.Fatalf("tray options = %+v, want id zeokeel/tooltip %s", factory.options, appName)
	}
	if len(factory.options.Icon) == 0 {
		t.Fatal("tray options have no icon")
	}
	if _, err := png.Decode(bytes.NewReader(factory.options.Icon)); err != nil {
		t.Fatalf("tray icon is not a PNG: %v", err)
	}
	menu := factory.options.Menu
	if menu == nil {
		t.Fatal("tray options have no menu")
	}
	items := menu.Items()
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, item.Label)
	}
	joined := strings.Join(labels, "|")
	if !strings.Contains(joined, "显示 ZeoKeel") || !strings.Contains(joined, "退出") {
		t.Fatalf("tray menu labels = %v, want 显示 ZeoKeel and 退出", labels)
	}
	// The icon click and the "显示 ZeoKeel" item both run the show handler.
	if tray.onClick == nil {
		t.Fatal("tray has no click listener")
	}
	tray.onClick()
	if shows != 1 {
		t.Fatalf("show handler calls after icon click = %d, want 1", shows)
	}
	for _, item := range items {
		if item.Label == "显示 ZeoKeel" {
			item.Click(item, nil)
		}
	}
	if shows != 2 {
		t.Fatalf("show handler calls after menu click = %d, want 2", shows)
	}
	for _, item := range items {
		if item.Label == "退出" {
			item.Click(item, nil)
		}
	}
	if quits != 1 {
		t.Fatalf("quit handler calls after 退出 = %d, want 1", quits)
	}
	// closeToTray=false registers no close interception.
	if window.closeRegistered {
		t.Fatal("close handler was registered with closeToTray disabled")
	}
}

func TestCreateTrayCloseToTrayHidesWindow(t *testing.T) {
	window := &fakeTrayWindow{}
	factory := &fakeTrayFactory{handle: &fakeTrayHandle{}}
	handler := trayHandler{show: func() {}, quit: func() {}}

	if _, err := createTray(window, handler, true, nil, factory.create); err != nil {
		t.Fatalf("createTray: %v", err)
	}
	if !window.closeRegistered || window.closeFn == nil {
		t.Fatal("close handler was not registered for close-to-tray")
	}
	event := &mygo.CloseEvent{}
	window.closeFn(event)
	if !event.DefaultPrevented() {
		t.Fatal("close event was not prevented")
	}
	if window.hidden != 1 {
		t.Fatalf("window Hide calls = %d, want 1", window.hidden)
	}
}

func TestCreateTrayCloseGuardDuringShutdown(t *testing.T) {
	window := &fakeTrayWindow{}
	factory := &fakeTrayFactory{handle: &fakeTrayHandle{}}
	handler := trayHandler{show: func() {}, quit: func() {}}
	keepAlive := false

	if _, err := createTray(window, handler, true, func() bool { return keepAlive }, factory.create); err != nil {
		t.Fatalf("createTray: %v", err)
	}
	event := &mygo.CloseEvent{}
	window.closeFn(event)
	if event.DefaultPrevented() || window.hidden != 0 {
		t.Fatal("close was intercepted although the host is shutting down")
	}
}

func TestCreateTrayFailureIsSafe(t *testing.T) {
	window := &fakeTrayWindow{}
	factory := &fakeTrayFactory{err: errors.New("no tray environment")}
	handler := trayHandler{show: func() {}, quit: func() {}}

	created, err := createTray(window, handler, true, nil, factory.create)
	if err == nil {
		t.Fatal("createTray(failing factory) = nil error")
	}
	if created != nil {
		t.Fatalf("createTray(failing factory) returned %v, want nil", created)
	}
	if factory.options == nil || len(factory.options.Icon) == 0 {
		t.Fatal("tray options were not built before the failure")
	}
	if window.closeRegistered {
		t.Fatal("close handler was registered although the tray failed")
	}
}

func TestSetupTrayRequiresWindow(t *testing.T) {
	if _, err := setupTray(nil, traySettings(t, "true"), nil); err == nil {
		t.Fatal("setupTray(nil window) = nil error")
	}
}
