package home_test

// First-run preflight tests (E-04): the desktop assembly always supplies a
// default model id, so Home must also treat a missing API key as an incomplete
// setup and deepen-link the fixing action to the Model settings page.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/home"
)

// stubSettingsPage registers one minimal settings page so the shell has a real
// surface to deep-link to (settings.Module is not activated in this harness).
func stubSettingsPage(t *testing.T, h *harness, id, section, title, body string) {
	t.Helper()
	unregister, err := h.ui.Runtime().RegisterSettingsPage(contracts.SettingsPageSpec{
		ID:      id,
		Section: section,
		Title:   title,
		Order:   10,
		Build: func(c *ui.Context) {
			ui.Text(c, body)
		},
	})
	if err != nil {
		t.Fatalf("register settings page %q: %v", id, err)
	}
	t.Cleanup(unregister)
}

func TestHomeSetupCardShownForMissingAPIKeyWithConversations(t *testing.T) {
	h := newHarness(t, &fakeChat{
		config:   contracts.ChatConfig{Model: "test-model"},
		sessions: []contracts.ChatSessionInfo{{ID: "s1", Title: "旧会话"}},
	})
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	h.assertTexts(tester, "完成 ZeoKeel 的初始设置", "尚未配置 API Key", "配置模型")
}

func TestHomeEmptyStateShowsMissingAPIKey(t *testing.T) {
	h := newHarness(t, &fakeChat{config: contracts.ChatConfig{Model: "test-model"}})
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	h.assertTexts(tester, "还缺少：API Key", "配置 API Key")
	h.assertNoTexts(tester, "还缺少：模型")
}

func TestHomeComposerWarnsAboutMissingAPIKey(t *testing.T) {
	h := newHarness(t, &fakeChat{config: contracts.ChatConfig{Model: "test-model"}})
	activate(t, h, home.Module(), home.Config{})
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	h.assertTexts(tester, "尚未配置 API Key：发送可能失败")

	// A configured key hides the warning; a local model server then reads as
	// ready instead of permanently warning.
	h = newHarness(t, &fakeChat{config: contracts.ChatConfig{
		Model: "test-model", APIKey: "sk-test",
	}})
	activate(t, h, home.Module(), home.Config{})
	tester = ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()
	h.assertNoTexts(tester, "尚未配置 API Key", "完成 ZeoKeel 的初始设置")
}

func TestHomeConfigureActionDeepLinksToModelPage(t *testing.T) {
	h := newHarness(t, &fakeChat{config: contracts.ChatConfig{Model: "test-model"}})
	activate(t, h, home.Module(), home.Config{})
	stubSettingsPage(t, h, "model", "常用", "模型", "模型页桩")
	stubSettingsPage(t, h, "archive", "高级", "归档", "归档页桩")
	tester := ui.NewTester(h.ui.View, 1200, 800)
	tester.Frame()

	// Open the settings surface on another page first: the fixing action must
	// deep link to Model, not reopen the last page (the old ToggleSettings
	// behavior).
	if err := h.ui.OpenSettingsPage("archive"); err != nil {
		t.Fatalf("open archive page: %v", err)
	}
	tester.Frame()
	if !tester.HasText("归档页桩") {
		t.Fatalf("archive stub page did not render; texts = %v", tester.Texts())
	}
	if err := tester.Click("关闭设置"); err != nil {
		t.Fatalf("close settings: %v", err)
	}
	tester.Frame()
	if err := tester.Click("配置 API Key"); err != nil {
		t.Fatalf("click configure: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("模型页桩") {
		t.Fatalf("configure action did not open the model page; texts = %v", tester.Texts())
	}
	if tester.HasText("归档页桩") {
		t.Fatalf("configure action reopened the last page; texts = %v", tester.Texts())
	}
}
