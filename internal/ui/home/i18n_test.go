package home_test

// i18n frame tests for the Home screen: hero greeting, setup card and composer
// copy follow the host locale on every frame (English now, Chinese after
// switching back) while zh stays the default.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/ui/home"
)

func TestHomeEnglishCopyAndLanguageSwitch(t *testing.T) {
	h := newHarness(t, &fakeChat{})
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()

	// Default Chinese copy.
	h.assertTexts(tester, "编译期模块化的桌面 AI agent。",
		"还没有会话。开始第一次对话，或先完成下面的准备：", "配置模型", "新对话")

	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester,
		"A compile-time modular desktop AI agent.",
		"No conversations yet. Start the first one, or get these ready first:",
		"Configure model",
		"New chat")
	h.assertNoTexts(tester, "编译期模块化的桌面 AI agent。", "新对话", "配置模型")

	// Switching back restores the Chinese copy on the next frame.
	h.ui.Locale().SetLanguage("zh")
	tester.Frame()
	h.assertTexts(tester, "编译期模块化的桌面 AI agent。",
		"还没有会话。开始第一次对话，或先完成下面的准备：", "配置模型", "新对话")
	h.assertNoTexts(tester, "No conversations yet.", "New chat", "Configure model")
}

func TestHomeEnglishFallbackHeroHint(t *testing.T) {
	// A chat-less assembly renders the default hero fallback hint; it is
	// localized per frame too.
	h := newHarness(t, nil)
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	h.assertTexts(tester, "这是 home 的默认 Hero 空态。")

	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "This is Home's default hero empty state.")
	h.assertNoTexts(tester, "这是 home 的默认 Hero 空态。")
}

// TestHomeEnglishGreetingWithConfiguredChineseDefault covers the assembly
// config carrying the Chinese default greeting: an English UI must fall back to
// the English default instead of showing the configured Chinese string.
func TestHomeEnglishGreetingWithConfiguredChineseDefault(t *testing.T) {
	h := newHarness(t, &fakeChat{})
	activate(t, h, home.Module(), home.Config{Greeting: "编译期模块化的桌面 AI agent。"})
	h.ui.Locale().SetLanguage("en")
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	h.assertTexts(tester, "A compile-time modular desktop AI agent.")
	h.assertNoTexts(tester, "编译期模块化的桌面 AI agent。")
}

// TestHomeCommandFollowsLanguage covers the host command palette entry: the
// home settings command is registered in the current language and re-registered
// when the language switches, like the session commands.
func TestHomeCommandFollowsLanguage(t *testing.T) {
	h := newHarness(t, &fakeChat{})
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()

	command := func() (title, hint string, found bool) {
		for _, spec := range h.ui.Runtime().Commands() {
			if spec.ID == "home.settings" {
				return spec.Title, spec.Hint, true
			}
		}
		return "", "", false
	}
	if title, hint, ok := command(); !ok || title != "设置" || hint != "打开设置" {
		t.Fatalf("zh command = %q/%q (found %v), want 设置/打开设置", title, hint, ok)
	}

	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	if title, hint, ok := command(); !ok || title != "Settings" || hint != "Open settings" {
		t.Fatalf("en command = %q/%q (found %v), want Settings/Open settings", title, hint, ok)
	}

	h.ui.Locale().SetLanguage("zh")
	tester.Frame()
	if title, hint, ok := command(); !ok || title != "设置" || hint != "打开设置" {
		t.Fatalf("zh command after switch back = %q/%q (found %v), want 设置/打开设置", title, hint, ok)
	}
}
