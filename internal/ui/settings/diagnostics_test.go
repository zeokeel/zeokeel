package settings_test

// Diagnostics page: read-only runtime/storage values and the copy action.

import (
	"context"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

type diagnosisTool struct{ name string }

func (t diagnosisTool) Name() string           { return t.name }
func (t diagnosisTool) Description() string    { return t.name + " tool" }
func (t diagnosisTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t diagnosisTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{Content: "ok"}, nil
}

func TestDiagnosticsPage(t *testing.T) {
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	store := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:      "deepseek-chat",
		contracts.SettingsChatBaseURL:    "https://api.deepseek.com/v1",
		contracts.SettingsChatAPIKey:     "sk-secret-value",
		contracts.SettingWebSearchAPIKey: "sk-web-secret-value",
		// MCP environment rows carry their own credentials; the page must mask
		// them too (the audit caught only chat.apiKey being hidden).
		contracts.SettingMCPServers: `[{"name":"srv","transport":"stdio","command":"run","env":["GITHUB_TOKEN=gh-secret-value","FOO=bar"]}]`,
	})
	chat := &fakeChat{}
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide(t, ctx, contracts.KeySettings, contracts.SettingsService(store))
	provide(t, ctx, contracts.KeyChat, contracts.ChatService(chat))
	provide(t, ctx, contracts.KeyAppInfo, contracts.AppInfo{
		Name: "ZeoKeel", Version: "1.2.3", Commit: "abc124",
		BuildTime: "2026-01-02T03:04:05Z", GoVersion: "go1.27.3",
	})
	provide(t, ctx, contracts.KeyDataDir, "/tmp/zeokeel-diag")
	provide(t, ctx, contracts.KeyToolkit, rigtools.NewRegistry(
		diagnosisTool{"bash"}, diagnosisTool{"read_file"},
	))
	activate(t, ctx, settings.Module(), settings.Config{})
	uiRoot.Navigator().ToggleSettings()
	tester := ui.NewTester(uiRoot.View, 1200, 800)
	tester.Frame()
	// The grouped navigation scrolls; open the page through the stable deep
	// link rather than relying on the item being on the first screen.
	if err := uiRoot.OpenSettingsPage("diagnostics"); err != nil {
		t.Fatalf("open diagnostics page: %v", err)
	}
	tester.Frame()
	for _, want := range []string{"诊断", "ZeoKeel v1.2.3", "abc124", "go1.27.3",
		"/tmp/zeokeel-diag", "zeokeel.db", "工具数", "会话数"} {
		if !tester.HasText(want) {
			t.Fatalf("diagnostics frame missing %q; texts = %v", want, tester.Texts())
		}
	}
	// The setting keys are a per-key disclosure now (a joined line used to be
	// truncated and could not be copied per value).
	if err := tester.Click("设置项 · 5 项"); err != nil {
		t.Fatalf("expand setting keys: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("deepseek-chat") {
		t.Fatalf("expanded setting keys missing the model value; texts = %v", tester.Texts())
	}
	for _, secret := range []string{"sk-secret-value", "sk-web-secret-value", "gh-secret-value"} {
		if tester.HasText(secret) {
			t.Fatalf("diagnostics page leaked %q; texts = %v", secret, tester.Texts())
		}
	}
	if err := tester.Click("复制诊断信息"); err != nil {
		t.Fatalf("copy diagnostics: %v; texts = %v", err, tester.Texts())
	}
	clipboard := tester.Clipboard()
	if !strings.Contains(clipboard, "Commit: abc124") {
		t.Fatalf("clipboard = %q", clipboard)
	}
	for _, secret := range []string{"sk-secret-value", "sk-web-secret-value", "gh-secret-value"} {
		if strings.Contains(clipboard, secret) {
			t.Fatalf("diagnostics copy leaked %q; clipboard = %q", secret, clipboard)
		}
	}
	if !strings.Contains(clipboard, "已设置(隐藏)") {
		t.Fatalf("clipboard = %q, want the masked marker", clipboard)
	}
}
