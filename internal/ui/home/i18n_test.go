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
		"还没有会话", "尚未配置模型。添加服务商与密钥后才能发送消息。", "配置模型")

	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester,
		"A compile-time modular desktop AI agent.",
		"No sessions yet",
		"No model configured yet. Add a provider and API key before sending.",
		"Configure model")
	h.assertNoTexts(tester, "编译期模块化的桌面 AI agent。", "配置模型")

	// Switching back restores the Chinese copy on the next frame.
	h.ui.Locale().SetLanguage("zh")
	tester.Frame()
	h.assertTexts(tester, "编译期模块化的桌面 AI agent。",
		"还没有会话", "配置模型")
	h.assertNoTexts(tester, "No sessions yet", "Configure model")
}

func TestHomeEnglishFallbackHeroHint(t *testing.T) {
	// A chat-less assembly renders the default hero fallback hint; it is
	// localized per frame too.
	h := newHarness(t, nil)
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	// A chat-less assembly gets one honest line instead of the module's
	// implementation note (2026-11 copy audit F-05).
	h.assertTexts(tester, "选择一项开始，或打开工作区。")

	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Pick something to start, or open a workspace.")
	h.assertNoTexts(tester, "选择一项开始，或打开工作区。")
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
