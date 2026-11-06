package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/session"
)

// TestMoreMenuItemsAndAbout opens the sidebar's More popover and checks the
// three items, the Settings item and the About dialog.
func TestMoreMenuItemsAndAbout(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	tt := ui.NewTester(h.app.View, 1600, 1000)

	if err := tt.Click("More"); err != nil {
		t.Fatalf("click More: %v", err)
	}
	for _, item := range []string{"Settings", "About", "Quit"} {
		if !tt.HasText(item) {
			t.Fatalf("More menu missing %q: %q", item, tt.Texts())
		}
	}

	// Settings opens the modal and closes the popover.
	if err := tt.Click("Settings"); err != nil {
		t.Fatalf("click Settings: %v", err)
	}
	if h.app.Screen != Settings {
		t.Fatalf("screen = %v, want Settings", h.app.Screen)
	}
	if h.app.moreOpen {
		t.Fatal("More popover stayed open after Settings")
	}
	tt.Key(0, ui.KeyEscape)

	// About opens a dialog; Quit calls the app's quit hook.
	if err := tt.Click("More"); err != nil {
		t.Fatalf("click More: %v", err)
	}
	if err := tt.Click("About"); err != nil {
		t.Fatalf("click About: %v", err)
	}
	if !tt.HasText(BuildVersionString()) {
		t.Fatalf("About dialog missing: %q", tt.Texts())
	}
	if err := tt.Click("Close"); err != nil {
		t.Fatalf("close About: %v", err)
	}

	quits := 0
	old := quitApp
	quitApp = func() { quits++ }
	t.Cleanup(func() { quitApp = old })
	if err := tt.Click("More"); err != nil {
		t.Fatalf("click More: %v", err)
	}
	if err := tt.Click("Quit"); err != nil {
		t.Fatalf("click Quit: %v", err)
	}
	if quits != 1 {
		t.Fatalf("Quit ran %d times, want 1", quits)
	}
}

// TestMoreMenuLocalizes checks that the persisted language switches the
// sidebar and popover labels.
func TestMoreMenuLocalizes(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	if err := h.ctrl.Settings().Save(session.Settings{Language: session.LanguageChinese}); err != nil {
		t.Fatalf("Save language: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)

	if !tt.HasText("新建会话") || !tt.HasText("更多") {
		t.Fatalf("sidebar did not switch to Chinese: %q", tt.Texts())
	}
	if err := tt.Click("更多"); err != nil {
		t.Fatalf("click 更多: %v", err)
	}
	for _, item := range []string{"设置", "关于", "退出"} {
		if !tt.HasText(item) {
			t.Fatalf("Chinese More menu missing %q: %q", item, tt.Texts())
		}
	}
	if err := tt.Click("关于"); err != nil {
		t.Fatalf("click 关于: %v", err)
	}
	if !tt.HasText(BuildVersionString()) {
		t.Fatalf("About dialog missing: %q", tt.Texts())
	}
	if err := tt.Click("关闭"); err != nil {
		t.Fatalf("close About: %v", err)
	}
}

// TestSessionRowContextMenu checks the row's right-click menu, the other
// entry point to Rename and Delete.
func TestSessionRowContextMenu(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	tt := ui.NewTester(h.app.View, 1600, 1000)

	if err := tt.RightClick("live"); err != nil {
		t.Fatalf("right-click session row: %v", err)
	}
	for _, item := range []string{"Rename", "Delete"} {
		if !tt.HasText(item) {
			t.Fatalf("context menu missing %q: %q", item, tt.Texts())
		}
	}
	// The shell draws this menu in-window, not as a system menu.
	if tt.Menu() != nil {
		t.Fatalf("a system context menu opened: %q", tt.Menu())
	}
	tt.Key(0, ui.KeyEscape)
	if tt.HasText("Rename") {
		t.Fatalf("Esc left the context menu open: %q", tt.Texts())
	}
}
