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

	// Default Chinese copy is unchanged.
	h.assertTexts(tester, "编译期模块化的桌面 AI agent。", "向 ZeoKeel 提问…",
		"开始新对话", "完成 ZeoKeel 的初始设置",
		"尚未配置模型。添加服务商与密钥后才能发送消息。", "配置模型")

	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester,
		"A compile-time modular desktop AI agent.",
		"Ask ZeoKeel…",
		"Start a new conversation",
		"Finish setting up ZeoKeel",
		"No model configured yet. Add a provider and API key before sending messages.",
		"Configure model")
	h.assertNoTexts(tester, "编译期模块化的桌面 AI agent。", "向 ZeoKeel 提问…",
		"开始新对话", "完成 ZeoKeel 的初始设置", "配置模型")

	// Switching back restores the Chinese copy on the next frame.
	h.ui.Locale().SetLanguage("zh")
	tester.Frame()
	h.assertTexts(tester, "编译期模块化的桌面 AI agent。", "向 ZeoKeel 提问…",
		"开始新对话", "完成 ZeoKeel 的初始设置", "配置模型")
	h.assertNoTexts(tester, "Ask ZeoKeel…", "Start a new conversation",
		"Finish setting up ZeoKeel", "Configure model")
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
