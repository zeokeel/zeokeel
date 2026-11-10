package regression

// Adversarial audit: the conversation font-size setting (ui.chatFontSize). The
// package-internal mapping/cache tests cover the px/cache internals; this
// regression layer checks the user-visible path: the Behavior page selector
// writes small/standard/large (malformed values display standard and are not
// overwritten by rendering), and the conversation screen re-renders its Markdown
// transcript after a size change without errors.

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestChatFontSizeSelectorMapping covers the Behavior page selector.
func TestChatFontSizeSelectorMapping(t *testing.T) {
	harness := startBehaviorUI(t, false)
	harness.openSettingsPage(t, "行为")
	// 消息字号 lives inside the collapsed 更多对话设置 disclosure.
	if err := harness.tester.Click("更多对话设置"); err != nil {
		t.Fatalf("expand more settings: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	harness.tester.Advance(350 * time.Millisecond)
	harness.tester.Frame()
	requireHasTexts(t, harness.tester, "消息字号", "标准")

	// Default: standard label, no stored row.
	if got := harness.h.settings.Get(contracts.SettingChatFontSize, ""); got != "" {
		t.Fatalf("default font-size row = %q, want unset", got)
	}
	clickScrolledSettings(t, harness.tester, "消息字号：标准")
	requireHasTexts(t, harness.tester, "小", "大")
	harness.click(t, "大")
	if got := harness.h.settings.Get(contracts.SettingChatFontSize, ""); got != "large" {
		t.Fatalf("font-size after 大 = %q, want large", got)
	}
	requireHasText(t, harness.tester, "已保存")

	clickScrolledSettings(t, harness.tester, "消息字号：大")
	harness.click(t, "小")
	if got := harness.h.settings.Get(contracts.SettingChatFontSize, ""); got != "small" {
		t.Fatalf("font-size after 小 = %q, want small", got)
	}

	// Explicit standard writes; a malformed stored value displays standard and
	// is not overwritten by rendering.
	harness.click(t, "小")
	clickSettingsMenuItem(t, harness.tester, "标准")
	if got := harness.h.settings.Get(contracts.SettingChatFontSize, ""); got != "standard" {
		t.Fatalf("font-size after 标准 = %q, want standard", got)
	}
	if err := harness.h.settings.Set(contracts.SettingChatFontSize, "garbage"); err != nil {
		t.Fatalf("set garbage font size: %v", err)
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "标准")
	if got := harness.h.settings.Get(contracts.SettingChatFontSize, ""); got != "garbage" {
		t.Fatalf("a frame overwrote the font-size row to %q", got)
	}
}

// TestChatFontSizeAppliesToTranscript covers the conversation screen: a size
// change between frames rebuilds the Markdown options (cache invalidation) and
// the transcript still renders its text without an error line.
func TestChatFontSizeAppliesToTranscript(t *testing.T) {
	harness := startBehaviorUI(t, true)
	harness.h.push("session")
	harness.tester.Frame()
	requireHasTexts(t, harness.tester, "Markdown answer")

	// Change the setting between frames: the session screen re-creates the
	// Markdown options for the new size.
	for _, value := range []string{"large", "small", "standard", "bogus"} {
		if err := harness.h.settings.Set(contracts.SettingChatFontSize, value); err != nil {
			t.Fatalf("set font size %q: %v", value, err)
		}
		harness.tester.Frame()
		requireHasText(t, harness.tester, "Markdown answer")
		requireNoText(t, harness.tester, "读取")
	}
}

// clickScrolledSettings scrolls the settings page until the control is inside the
// visible page body, then clicks it. The Behavior page grew past one viewport
// when the 工作步骤 section landed, so the font-size trigger can start below the
// clip; the settings-package harness has the same helper for its own tests.
func clickScrolledSettings(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	for range 24 {
		rect, ok := tester.Find(label)
		if !ok {
			t.Fatalf("%q not found; texts = %v", label, tester.Texts())
		}
		centerX, centerY := rect.X+rect.W/2, rect.Y+rect.H/2
		if rect.W > 0 && rect.H > 0 && centerX >= 200 && centerX <= 1000 && centerY >= 70 && centerY <= 768 {
			if err := tester.Click(label); err != nil {
				t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
			}
			tester.Frame()
			return
		}
		delta := centerY - 480
		if delta > 220 {
			delta = 220
		}
		if delta < -220 {
			delta = -220
		}
		if delta == 0 {
			delta = 120
		}
		tester.Scroll(700, 500, 0, delta)
		tester.Frame()
	}
	t.Fatalf("%q never became visible; texts = %v", label, tester.Texts())
}

// clickSettingsMenuItem clicks a popover menu item by its menu-item role: the
// page also renders the word on a segmented control, so a plain label click
// would hit the control whose text happens to be off-screen.
func clickSettingsMenuItem(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	roles := []ui.Role{ui.RoleMenuItemRadio, ui.RoleMenuItem, ui.RoleMenuItemCheckBox, ui.RoleOption, ui.RoleRadio}
	for _, role := range roles {
		info, ok := tester.AccessByRole(role, label)
		if !ok {
			continue
		}
		tester.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
		tester.Frame()
		return
	}
	t.Fatalf("menu item %q not found; texts = %v", label, tester.Texts())
}
