package sidebar_test

// Tests for the sidebar's generic module-screen navigation: every registered
// screen except the sidebar's own screen (the session list) gets one native
// item, so a new screen module (ui/automation) appears without a sidebar edit.
// The Home screen stays registered but is not a navigation destination: the new
// chat action and the session list are the sidebar's landing surface.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

func TestSidebarListsModuleScreens(t *testing.T) {
	h := newHarness(t, &fakeChat{}, sidebar.Config{})
	// The ScreenLister is the live runtime registry, so a screen registered
	// after module activation still shows up on the next frame.
	if _, err := h.uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID:    "automation",
		Title: "自动化",
		Order: 20,
		Build: func(c *ui.Context) {
			ui.Text(c, "自动化屏幕内容")
		},
	}); err != nil {
		t.Fatalf("register automation screen: %v", err)
	}
	tester := h.tester()
	tester.Frame()
	// Generic module screens keep their navigation row; the removed Home
	// destination leaves no label behind (home stays a registered screen only).
	h.assertTexts(tester, "自动化")
	h.assertNoTexts(tester, "首页", "Home")

	if err := tester.Click("自动化"); err != nil {
		t.Fatalf("click automation nav item: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "自动化屏幕内容")
}

func TestSidebarSkipsOwnScreens(t *testing.T) {
	// The sidebar owns the session list (there is no session destination row),
	// and the home screen is registered but no longer a navigation destination
	// at all: the removed Home row must not come back as a generic screen item.
	h := newHarness(t, &fakeChat{}, sidebar.Config{})
	tester := h.tester()
	tester.Frame()
	if got := countText(tester, "首页"); got != 0 {
		t.Fatalf("首页 labels = %d, want 0 (no Home destination); texts = %v", got, tester.Texts())
	}
	// "会话" appears once as the flat list's section label, not as a generic
	// screen destination (the session screen is owned by the sidebar list).
	if got := countText(tester, "会话"); got != 1 {
		t.Fatalf("会话 labels = %d, want 1 (the section label); texts = %v", got, tester.Texts())
	}
}

func TestSidebarListsPluginsScreen(t *testing.T) {
	h := newHarness(t, &fakeChat{}, sidebar.Config{})
	if _, err := h.uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID:    "plugins",
		Title: "插件",
		Order: 30,
		Build: func(c *ui.Context) {
			ui.Text(c, "插件屏幕内容")
		},
	}); err != nil {
		t.Fatalf("register plugins screen: %v", err)
	}
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "插件")
	h.assertNoTexts(tester, "首页")

	if err := tester.Click("插件"); err != nil {
		t.Fatalf("click plugins nav item: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "插件屏幕内容")
}

// TestScreenNavSpecIsTheSingleSource pins the shared navigation table: the
// expanded sidebar rows and the host's collapsed rail both read it, so a screen
// id keeps one glyph and one localized label everywhere. Home stays in the table
// because its screen is still registered (the rail may draw it), even though the
// sidebar no longer renders it as a destination row.
func TestScreenNavSpecIsTheSingleSource(t *testing.T) {
	cases := []struct {
		id       string
		zh, en   string
		hasGlyph bool
	}{
		{"home", "首页", "Home", true},
		{"session", "对话", "Chat", true},
		{"automation", "自动化", "Automation", true},
		{"plugins", "插件", "Plugins", true},
		{"settings", "设置", "Settings", true},
	}
	for _, tc := range cases {
		icon, zh, en := sidebar.ScreenNavSpec(tc.id)
		if icon == nil {
			t.Errorf("ScreenNavSpec(%q) has no glyph", tc.id)
		}
		if zh != tc.zh || en != tc.en {
			t.Errorf("ScreenNavSpec(%q) = %q/%q, want %q/%q", tc.id, zh, en, tc.zh, tc.en)
		}
	}
	// An unregistered screen keeps a neutral glyph and falls back to its id.
	if icon, zh, en := sidebar.ScreenNavSpec("custom-screen"); icon == nil || zh != "" || en != "" {
		t.Fatalf("unknown ScreenNavSpec = %v/%q/%q, want a glyph and empty labels", icon, zh, en)
	}
	if got := sidebar.ScreenNavLabel(nil, "custom-screen"); got != "custom-screen" {
		t.Fatalf("unknown ScreenNavLabel = %q, want the id", got)
	}
}

// TestExpandedSidebarAndRailAgreeOnModuleScreens proves both navigation paths
// agree on the module destinations: the expanded sidebar draws no Home row any
// more, while the host's collapsed rail is chrome whose destination set follows
// the registered screens, and both use the one ScreenNavSpec table.
func TestExpandedSidebarAndRailAgreeOnModuleScreens(t *testing.T) {
	h := newHarness(t, &fakeChat{}, sidebar.Config{})
	for _, spec := range []contracts.ScreenSpec{
		{ID: "automation", Title: "自动化", Order: 20, Build: func(c *ui.Context) { ui.Text(c, "automation screen") }},
		{ID: "plugins", Title: "插件", Order: 30, Build: func(c *ui.Context) { ui.Text(c, "plugins screen") }},
	} {
		if _, err := h.uiRoot.Runtime().RegisterScreen(spec); err != nil {
			t.Fatalf("register %q: %v", spec.ID, err)
		}
	}
	tester := h.tester()
	tester.Frame()

	// The expanded navigation draws the module destinations; the removed Home
	// destination is gone for good.
	for _, id := range []string{"automation", "plugins"} {
		label := sidebar.ScreenNavLabel(h.uiRoot.Locale(), id)
		if !tester.HasText(label) {
			t.Fatalf("expanded navigation missing %q (%s); texts = %v", label, id, tester.Texts())
		}
	}
	h.assertNoTexts(tester, "首页")

	h.uiRoot.SetSidebarCollapsed(true)
	tester.Frame()
	for _, id := range []string{"automation", "plugins"} {
		label := sidebar.ScreenNavLabel(h.uiRoot.Locale(), id)
		if !tester.HasText(label) {
			t.Fatalf("collapsed rail missing %q (%s); texts = %v", label, id, tester.Texts())
		}
	}

	h.uiRoot.SetSidebarCollapsed(false)
	tester.Frame()
	for _, id := range []string{"automation", "plugins"} {
		label := sidebar.ScreenNavLabel(h.uiRoot.Locale(), id)
		if !tester.HasText(label) {
			t.Fatalf("expanded navigation missing %q (%s) after expanding; texts = %v", label, id, tester.Texts())
		}
	}
}
