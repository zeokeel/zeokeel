package uimodtest

// End-to-end proof of the composer's four-way mode selector on the real desktop
// assembly: the mode menu is a native popover; choosing 精简/创作 writes
// chat.mode (and keeps agent.mode normal), the composer selector chip changes,
// and switching back to 普通 restores the standard mode. The old header badge
// is gone: the mode lives in one place (the composer chip).

import (
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/assembly"
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// TestChatModeSelectorEndToEnd drives the composer mode popover on the real
// desktop assembly and reads the resulting settings rows back.
func TestChatModeSelectorEndToEnd(t *testing.T) {
	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()

	settings := &memSettings{values: map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: "http://127.0.0.1:1",
	}}
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	provide(t, root, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, root, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, root, contracts.KeyTheme, uiRoot.Theme())
	provide(t, root, contracts.KeyLocale, uiRoot.Locale())
	provide(t, root, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide(t, root, contracts.KeyDataDir, t.TempDir())
	provide(t, root, contracts.KeySettings, contracts.SettingsService(settings))
	provide(t, root, contracts.KeyLogger, contracts.Logger(discardLogger{}))

	loader, err := assembly.LoadWithBuildInfo(root, assembly.ProductDesktop, assembly.BuildInfo{Version: "0.1.0"})
	if err != nil {
		t.Fatalf("LoadWithBuildInfo(desktop): %v", err)
	}
	defer func() { _ = loader.Close() }()

	chat, err := root.Require(contracts.KeyChat)
	if err != nil {
		t.Fatalf("require chat: %v", err)
	}
	if _, err := chat.NewSession("模式测试会话"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := uiRoot.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session): %v", err)
	}

	tester := ui.NewTester(uiRoot.View, 1280, 800)
	tester.Frame()

	// Initial: standard mode, the composer's single 会话设置 menu owns the mode,
	// and no mode badge (or stale menu copy) is left in the header.
	if !tester.HasText("会话设置") {
		t.Fatalf("mode selector is not on 普通 by default; texts = %v", tester.Texts())
	}
	if tester.HasText("精简模式") || tester.HasText("创作模式") || tester.HasText("计划模式") {
		t.Fatalf("a mode badge is visible before selecting a mode; texts = %v", tester.Texts())
	}

	// Switch to 精简: writes chat.mode=minimal, keeps agent.mode=normal, and the
	// composer chip becomes the single mode indicator.
	clickMode(t, tester, "会话设置", "精简模式")
	if tester.HasText("精简模式") {
		t.Fatalf("the removed header mode badge is still rendered; texts = %v", tester.Texts())
	}
	if !tester.HasText("精简") {
		t.Fatalf("composer mode chip did not switch to 精简; texts = %v", tester.Texts())
	}
	if got := settings.Get(contracts.SettingChatMode, ""); got != contracts.ChatModeMinimal {
		t.Fatalf("chat.mode after choosing 精简 = %q, want minimal", got)
	}
	if got := settings.Get(contracts.SettingAgentMode, ""); got != contracts.AgentModeNormal {
		t.Fatalf("agent.mode after choosing 精简 = %q, want normal", got)
	}
	if !tester.HasText("精简") {
		t.Fatalf("composer selector label did not switch to 精简; texts = %v", tester.Texts())
	}

	// Switch to 创作: writes chat.mode=creator and the chip follows.
	clickMode(t, tester, "精简", "创作模式")
	if tester.HasText("创作模式") || tester.HasText("精简模式") {
		t.Fatalf("a mode badge remains after choosing 创作; texts = %v", tester.Texts())
	}
	if !tester.HasText("创作") {
		t.Fatalf("composer mode chip did not switch to 创作; texts = %v", tester.Texts())
	}
	if got := settings.Get(contracts.SettingChatMode, ""); got != contracts.ChatModeCreator {
		t.Fatalf("chat.mode after choosing 创作 = %q, want creator", got)
	}
	if got := settings.Get(contracts.SettingAgentMode, ""); got != contracts.AgentModeNormal {
		t.Fatalf("agent.mode after choosing 创作 = %q, want normal", got)
	}

	// Back to 普通: chat.mode returns to standard and the chip returns.
	clickMode(t, tester, "创作", "普通模式")
	if tester.HasText("精简") || tester.HasText("创作") {
		t.Fatalf("a mode indicator remains after switching back to 普通; texts = %v", tester.Texts())
	}
	if got := settings.Get(contracts.SettingChatMode, ""); got != contracts.ChatModeStandard {
		t.Fatalf("chat.mode after choosing 普通 = %q, want standard", got)
	}
	if got := settings.Get(contracts.SettingAgentMode, ""); got != contracts.AgentModeNormal {
		t.Fatalf("agent.mode after choosing 普通 = %q, want normal", got)
	}
	if !tester.HasText("会话设置") {
		t.Fatalf("composer selector did not return to the neutral 会话设置 label; texts = %v", tester.Texts())
	}
}

// clickMode opens the mode popover from the given selector label and chooses one
// menu item, repainting after each step.
func clickMode(t *testing.T, tester *ui.Tester, selector, item string) {
	t.Helper()
	if err := tester.Click(selector); err != nil {
		t.Fatalf("click mode selector %q: %v; texts = %v", selector, err, tester.Texts())
	}
	tester.Frame()
	if err := tester.Click(item); err != nil {
		t.Fatalf("click mode menu item %q: %v; texts = %v", item, err, tester.Texts())
	}
	tester.Frame()
}
