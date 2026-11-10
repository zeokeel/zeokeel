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

	// Initial: standard mode; the chip keeps the stable destination label
	// (会话设置) and no mode word leaks outside the menu (R17).
	if !tester.HasText("会话设置") {
		t.Fatalf("default session menu label missing; texts = %v", tester.Texts())
	}
	if tester.HasText("精简模式") || tester.HasText("创作模式") || tester.HasText("计划模式") {
		t.Fatalf("a mode badge is visible before selecting a mode; texts = %v", tester.Texts())
	}

	// Switch to 精简: writes chat.mode=minimal, keeps agent.mode=normal, and the
	// menu's 对话模式 trailing value names the mode while the chip stays fixed.
	clickMode(t, tester, "会话设置", "精简模式")
	if tester.HasText("精简模式") {
		t.Fatalf("the mode word leaked outside the session menu; texts = %v", tester.Texts())
	}
	if !tester.HasText("会话设置") {
		t.Fatalf("composer session chip lost its stable label; texts = %v", tester.Texts())
	}
	if got := settings.Get(contracts.SettingChatMode, ""); got != contracts.ChatModeMinimal {
		t.Fatalf("chat.mode after choosing 精简 = %q, want minimal", got)
	}
	if got := settings.Get(contracts.SettingAgentMode, ""); got != contracts.AgentModeNormal {
		t.Fatalf("agent.mode after choosing 精简 = %q, want normal", got)
	}
	assertModeTrailing(t, tester, "会话设置", "精简模式")

	// Switch to 创作: writes chat.mode=creator and the trailing value follows.
	clickMode(t, tester, "会话设置", "创作模式")
	if tester.HasText("创作模式") || tester.HasText("精简模式") {
		t.Fatalf("a mode word remains outside the session menu; texts = %v", tester.Texts())
	}
	if got := settings.Get(contracts.SettingChatMode, ""); got != contracts.ChatModeCreator {
		t.Fatalf("chat.mode after choosing 创作 = %q, want creator", got)
	}
	if got := settings.Get(contracts.SettingAgentMode, ""); got != contracts.AgentModeNormal {
		t.Fatalf("agent.mode after choosing 创作 = %q, want normal", got)
	}
	assertModeTrailing(t, tester, "会话设置", "创作模式")

	// Back to 普通: chat.mode returns to standard and no mode trailing remains.
	clickMode(t, tester, "会话设置", "普通模式")
	if tester.HasText("精简") || tester.HasText("创作") {
		t.Fatalf("a mode indicator remains after switching back to 普通; texts = %v", tester.Texts())
	}
	if got := settings.Get(contracts.SettingChatMode, ""); got != contracts.ChatModeStandard {
		t.Fatalf("chat.mode after choosing 普通 = %q, want standard", got)
	}
	if got := settings.Get(contracts.SettingAgentMode, ""); got != contracts.AgentModeNormal {
		t.Fatalf("agent.mode after choosing 普通 = %q, want normal", got)
	}
	// The normal mode's trailing value is its short name (普通); the option row
	// spells 普通模式, so the trailing node appears once.
	if err := tester.Click("会话设置"); err != nil {
		t.Fatalf("open session menu: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if got := exactLabels(tester, "普通"); got != 1 {
		t.Fatalf("normal mode trailing appears %d times, want 1; texts = %v", got, tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
}

// assertModeTrailing opens the session menu and asserts the mode group's
// trailing value names want (the trailing node once plus the option's label and
// text nodes, three occurrences; a non-current mode has only its option's two).
func assertModeTrailing(t *testing.T, tester *ui.Tester, selector, want string) {
	t.Helper()
	if err := tester.Click(selector); err != nil {
		t.Fatalf("open session menu: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if got := exactLabels(tester, want); got != 3 {
		t.Fatalf("mode trailing %q appears %d times, want 3; texts = %v", want, got, tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
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
