package sidebar_test

// Tests for the sidebar's generic module-screen navigation: every registered
// screen except the sidebar's own destinations gets one native item, so a new
// screen module (ui/automation) appears without a sidebar edit.

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
	h.assertTexts(tester, "首页", "自动化")

	if err := tester.Click("自动化"); err != nil {
		t.Fatalf("click automation nav item: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "自动化屏幕内容")
}

func TestSidebarSkipsOwnScreens(t *testing.T) {
	// home and session are the sidebar's own destinations (Home item + session
	// list) and must not be duplicated as generic screen items.
	h := newHarness(t, &fakeChat{}, sidebar.Config{})
	tester := h.tester()
	tester.Frame()
	// "首页" appears once (the Home destination); the registered session screen
	// title "会话" is not rendered as a separate nav item.
	h.assertTexts(tester, "首页")
	if got := countText(tester, "首页"); got != 1 {
		t.Fatalf("首页 labels = %d, want 1; texts = %v", got, tester.Texts())
	}
	// "会话" appears once as the flat list's section label, not as a generic
	// screen destination (the session screen is owned by the sidebar list).
	if got := countText(tester, "会话"); got != 1 {
		t.Fatalf("会话 labels = %d, want 1 (the section label); texts = %v", got, tester.Texts())
	}
}
