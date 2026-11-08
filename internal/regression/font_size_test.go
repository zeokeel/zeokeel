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
	harness.click(t, "标准")
	requireHasTexts(t, harness.tester, "小", "大")
	harness.click(t, "大")
	if got := harness.h.settings.Get(contracts.SettingChatFontSize, ""); got != "large" {
		t.Fatalf("font-size after 大 = %q, want large", got)
	}
	requireHasText(t, harness.tester, "已保存")

	harness.click(t, "大")
	harness.click(t, "小")
	if got := harness.h.settings.Get(contracts.SettingChatFontSize, ""); got != "small" {
		t.Fatalf("font-size after 小 = %q, want small", got)
	}

	// Explicit standard writes; a malformed stored value displays standard and
	// is not overwritten by rendering.
	harness.click(t, "小")
	harness.click(t, "标准")
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
