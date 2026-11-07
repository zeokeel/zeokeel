package settings_test

// i18n frame tests: the settings shell's own copy and every settings page follow
// the host language on each frame, including the host-rendered page navigation
// (which re-registers localized page titles when the language changes).

import (
	"context"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// newLocaleHarness builds the settings harness with an explicit host language.
func newLocaleHarness(t *testing.T, chat *fakeChat, store *fakeSettings, cfg settings.Config, language string) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel", Language: language})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })

	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide(t, ctx, contracts.KeySettings, contracts.SettingsService(store))
	provide(t, ctx, contracts.KeyChat, contracts.ChatService(chat))
	provide(t, ctx, contracts.KeyAppInfo, contracts.AppInfo{Name: "ZeoKeel", Version: "1.2.3"})

	h := &harness{t: t, ctx: ctx, uiRoot: uiRoot, settings: store, chat: chat}
	h.fiber = activate(t, ctx, settings.Module(), cfg)
	uiRoot.Navigator().ToggleSettings()
	return h
}

// TestSettingsEnglishPagesAndLanguageSwitch covers the English copy of the shell
// and every settings page owned by this module, then the switch back to zh.
func TestSettingsEnglishPagesAndLanguageSwitch(t *testing.T) {
	h := newLocaleHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()

	// Shell title and host-rendered page navigation.
	h.assertTexts(tester, "Settings", "Model", "Providers", "Appearance", "Skills",
		"Shortcuts", "Diagnostics", "Update", "Import")
	h.assertNoTexts(tester, "模型", "服务商", "外观", "技能", "快捷键", "诊断", "更新", "导入")

	// Model page.
	h.click(tester, "Model")
	h.assertTexts(tester, "Model", "Quick presets", "Connection", "Prompt",
		"Base URL", "API Key", "System prompt", "Save", "Not configured")

	// Providers page.
	h.click(tester, "Providers")
	h.assertTexts(tester, "Providers", "DeepSeek", "OpenAI", "Ollama", "Use")

	// Appearance page.
	h.click(tester, "Appearance")
	h.assertTexts(tester, "Appearance", "Theme", "Dark mode", "Language", "Interface language", "Apply")

	// Shortcuts page.
	h.click(tester, "Shortcuts")
	h.assertTexts(tester, "Shortcuts", "Collapse / expand sidebar", "Cmd/Ctrl + B",
		"Open command palette", "Send message", "New line", "Close dialog / menu",
		"Restore defaults", "Sidebar shortcut")

	// Diagnostics page.
	h.click(tester, "Diagnostics")
	h.assertTexts(tester, "Diagnostics", "Data directory", "Session database",
		"Tools", "Sessions", "Setting keys", "Copy diagnostics")

	// Update and import pages (backends absent, their disabled states localize).
	h.click(tester, "Update")
	h.assertTexts(tester, "Update", "Current version", "Update service is not enabled")
	h.click(tester, "Import")
	h.assertTexts(tester, "Import", "Import service is not enabled")
	h.click(tester, "Skills")
	h.assertTexts(tester, "Skills", "Skills backend is not enabled")

	// Switch back to Chinese: navigation and content follow on the next frame.
	h.uiRoot.Locale().SetLanguage("zh")
	tester.Frame()
	h.assertTexts(tester, "设置", "模型", "服务商", "外观", "技能", "快捷键", "诊断", "更新", "导入")
	h.assertNoTexts(tester, "Settings", "Providers", "Appearance", "Shortcuts", "Diagnostics")
	h.click(tester, "模型")
	h.assertTexts(tester, "快速预设", "连接", "提示词", "保存")
}

// TestSettingsEnglishConfigOverrides pins that explicit assembly labels still
// win over localization in English.
func TestSettingsEnglishConfigOverrides(t *testing.T) {
	h := newLocaleHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{
		Title:             "Custom model",
		ModelLabel:        "Model name",
		BaseURLLabel:      "Endpoint",
		APIKeyLabel:       "Secret",
		SystemPromptLabel: "Instructions",
		SaveLabel:         "Commit",
		AppearanceTitle:   "Look",
	}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Custom model")
	h.assertTexts(tester, "Custom model", "Model name", "Endpoint", "Secret", "Instructions", "Commit")
	h.click(tester, "Look")
	h.assertTexts(tester, "Look", "Theme", "Interface language")
}

// TestSettingsEnglishUpdatePage covers the richer copy of the update page.
func TestSettingsEnglishUpdatePage(t *testing.T) {
	update := &fakeUpdateService{info: contracts.UpdateInfo{
		CurrentVersion: "1.0.0", LatestVersion: "2.0.0", Available: true,
		Notes: "release notes", URL: "https://example.com/download", SHA256: "abcdef0123", Size: 2048,
	}}
	h := newLocaleHarnessWithUpdate(t, "en", update)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Update")
	h.assertTexts(tester, "Update", "Current version", "1.0.0", "Check for updates")
	h.click(tester, "Check for updates")
	waitForText(t, tester, "Latest version")
	h.assertTexts(tester, "Update available", "Release notes", "Copy download link",
		"2.0 KB", "SHA256 abcdef01")
	texts := tester.Texts()
	foundAsset := false
	for _, text := range texts {
		if len(text) >= 6 && text[:6] == "Asset:" {
			foundAsset = true
		}
	}
	if !foundAsset {
		t.Fatalf("update resources are not localized: %v", texts)
	}
}

// fakeUpdateService is a contracts.UpdateService returning a fixed result.
type fakeUpdateService struct{ info contracts.UpdateInfo }

func (s *fakeUpdateService) Check(context.Context) (contracts.UpdateInfo, error) {
	return s.info, nil
}

// newLocaleHarnessWithUpdate builds an English settings harness with the
// update backend provided.
func newLocaleHarnessWithUpdate(t *testing.T, language string, update contracts.UpdateService) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel", Language: language})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	store := newFakeSettings(nil)
	chat := &fakeChat{}
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide(t, ctx, contracts.KeySettings, contracts.SettingsService(store))
	provide(t, ctx, contracts.KeyChat, contracts.ChatService(chat))
	provide(t, ctx, contracts.KeyAppInfo, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"})
	provide[contracts.UpdateService](t, ctx, contracts.KeyUpdate, update)
	h := &harness{t: t, ctx: ctx, uiRoot: uiRoot, settings: store, chat: chat}
	h.fiber = activate(t, ctx, settings.Module(), settings.Config{})
	uiRoot.Navigator().ToggleSettings()
	return h
}
