package uiroot

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/brand"
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// fakeSettings is an in-memory host settings service for the shell tests that
// need the chrome widths to survive a process restart.
type fakeSettings struct{ values map[string]string }

func (f *fakeSettings) Get(key, def string) string {
	if v, ok := f.values[key]; ok {
		return v
	}
	return def
}

func (f *fakeSettings) Set(key, value string) error {
	f.values[key] = value
	return nil
}

func (f *fakeSettings) All() map[string]string { return f.values }

// TestChromeWidthsPersist covers the ui.Split pane widths: the shell loads the
// saved widths on its first frame, clamps values outside the usable range, and
// writes the dragged widths back so the layout opens as the user left it.
func TestChromeWidthsPersist(t *testing.T) {
	settings := &fakeSettings{values: map[string]string{}}
	r := New(Config{AppName: "Test", Language: "zh", Settings: settings})
	r.loadChromeWidths()
	if r.sidebarWidth != design.SidebarWidth || r.inspectorWidth != design.InspectorWidth {
		t.Fatalf("defaults = %v/%v, want %v/%v", r.sidebarWidth, r.inspectorWidth,
			design.SidebarWidth, design.InspectorWidth)
	}
	r.sidebarWidth, r.inspectorWidth = 320, 360
	r.saveChromeWidths()

	reopened := New(Config{AppName: "Test", Language: "zh", Settings: settings})
	reopened.loadChromeWidths()
	if reopened.sidebarWidth != 320 || reopened.inspectorWidth != 360 {
		t.Fatalf("restored widths = %v/%v, want 320/360", reopened.sidebarWidth, reopened.inspectorWidth)
	}

	// A stale or hand-edited value cannot make a pane unusable.
	settings.values[settingsKeySidebarWidth] = "9999"
	settings.values[settingsKeyInspectorWidth] = "1"
	clamped := New(Config{AppName: "Test", Language: "zh", Settings: settings})
	clamped.loadChromeWidths()
	if clamped.sidebarWidth != maxSidebarWidth || clamped.inspectorWidth != minInspectorWidth {
		t.Fatalf("clamped widths = %v/%v, want %v/%v", clamped.sidebarWidth, clamped.inspectorWidth,
			maxSidebarWidth, minInspectorWidth)
	}
}

// TestInspectorDrawerAnnouncements covers the assistive-technology announcement
// of the inspector state: opening and closing the drawer is spoken, since the
// host-built chrome has no native window/panel announce of its own.
func TestInspectorDrawerAnnouncements(t *testing.T) {
	r := newTestRoot(t)
	// The inspector belongs to the session screen: it reads the session read
	// model, so the host offers it nowhere else.
	mustScreen(t, r, textScreen("session", "会话", "body-session", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotSessionPanelRight, "pane", "inspector-pane", 10))
	tt := newTestTester(t, r, 1000, 720)
	tt.Frame()

	tt.Key(ui.Cmd|ui.Alt, ui.KeyI)
	tt.Frame()
	requireAnnouncement(t, tt, "检查器已打开")

	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	requireAnnouncement(t, tt, "检查器已关闭")
}

// TestNavigationAnnouncement covers the screen-change announcement: a
// hand-built navigator does not announce a page title the way a Router does.
func TestNavigationAnnouncement(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustScreen(t, r, textScreen("other", "其他", "body-other", 20))
	tt := newTestTester(t, r, 1280, 800)
	tt.Frame()
	requireAnnouncement(t, tt, "首页")

	if err := r.Navigator().Push("other"); err != nil {
		t.Fatalf("Push(other): %v", err)
	}
	tt.Frame()
	requireAnnouncement(t, tt, "其他")
}

// TestPaletteListIsLabeled covers the command palette's accessibility: the list
// of commands has a name and each row is an accessible button, so a screen
// reader reads the palette as a named list instead of anonymous rows.
func TestPaletteListIsLabeled(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	mustCommand(t, r, contracts.CommandSpec{ID: "demo.action", Title: "演示命令", Order: 10,
		Run: func() error { return nil }})
	tt := newTestTester(t, r, 1280, 800)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleList, "命令"); !ok {
		t.Fatalf("palette list is not a named list; announcements: %v", tt.Announcements())
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "演示命令"); !ok {
		t.Fatalf("palette row is not an accessible button; texts: %v", tt.Texts())
	}
}

// TestThemeFollowsTextScale covers the desktop text-size preference: the theme
// installed at the root scales its FontSize (native controls read it), matching
// the design tokens' TextScale for text the app draws itself.
func TestThemeFollowsTextScale(t *testing.T) {
	r := newTestRoot(t)
	var fontSize float32
	mustScreen(t, r, contracts.ScreenSpec{ID: "home", Title: "首页", Order: 10,
		Build: func(c *ui.Context) { fontSize = c.Theme().FontSize }})
	tt := ui.NewTesterWith(r.View, 800, 600, ui.TesterOptions{
		Preferences: ui.Preferences{TextScale: 1.5, ReduceMotion: true},
	})
	tt.Frame()
	if want := brand.FontSize * 1.5; fontSize != want {
		t.Fatalf("theme FontSize = %v, want %v at 1.5x", fontSize, want)
	}
}

// TestShellLandmarks covers the window's landmark roles (mygo v0.9.0
// RoleBanner/RoleMain/RoleComplementary/RoleContentInfo): screen readers can
// jump to the top bar, the main content, the two side panels and the status
// area, and each landmark carries a name.
func TestShellLandmarks(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("session", "会话", "body-session", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenHeader, "hdr", "screen-title", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotSessionPanelRight, "pane", "inspector-pane", 10))
	mustSlot(t, r, textSlotItem(contracts.SlotScreenFooter, "status", "status-line", 10))
	tt := newTestTester(t, r, 1280, 800)
	tt.Frame()
	r.SetRightPanelOpen(true)
	tt.Frame()

	assertLandmark := func(role ui.Role, name string) {
		t.Helper()
		if _, ok := tt.AccessByRole(role, name); !ok {
			t.Fatalf("missing landmark role %d named %q; texts = %v", role, name, tt.Texts())
		}
	}
	assertLandmark(ui.RoleBanner, "顶栏")
	assertLandmark(ui.RoleMain, "主内容")
	assertLandmark(ui.RoleComplementary, "侧栏")
	assertLandmark(ui.RoleComplementary, "右侧栏")
	assertLandmark(ui.RoleContentInfo, "状态栏")

	// Every landmark of the accessibility tree carries a name, so a screen
	// reader's landmark list is a list of names rather than anonymous groups.
	for _, info := range tt.AccessAll() {
		switch info.Role {
		case ui.RoleBanner, ui.RoleMain, ui.RoleComplementary, ui.RoleContentInfo:
			if info.Name == "" {
				t.Errorf("landmark role %d has no accessible name (bounds %+v)", info.Role, info.Bounds)
			}
		}
	}
	if !tt.HasText("inspector-pane") || !tt.HasText("status-line") {
		t.Fatalf("landmark frame lost its content; texts = %v", tt.Texts())
	}

	// The collapsed rail is the same sidebar landmark, so the name stays.
	r.SetSidebarCollapsed(true)
	tt.Frame()
	assertLandmark(ui.RoleComplementary, "侧栏")

	// A narrow window shows the inspector as the modal drawer; its content is
	// the same named complementary landmark, inside the named dialog.
	r.SetSidebarCollapsed(false)
	tt.SetSize(900, 700)
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleDialog, "检查器"); !ok {
		t.Fatalf("narrow inspector is not the named dialog; texts = %v", tt.Texts())
	}
	assertLandmark(ui.RoleComplementary, "右侧栏")
}

// requireAnnouncement asserts one of the last frame's announcements contains
// want. Announcements accumulate over the tester's frames.
func requireAnnouncement(t *testing.T, tt *ui.Tester, want string) {
	t.Helper()
	for _, announcement := range tt.Announcements() {
		if strings.Contains(announcement, want) {
			return
		}
	}
	t.Fatalf("no announcement contains %q; announcements: %v", want, tt.Announcements())
}
