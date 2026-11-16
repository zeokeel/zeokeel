package session_test

// RG-03 (2026-11 audit): a failure the OS file sandbox plausibly caused
// (ErrorKind sandbox_denied, rig's "[sandbox: …]" attribution) gets a localized
// explanation of the two-layer permission model and a one-click way to the
// permission settings — not a generic error line with rig's English diagnostic
// as the headline.

import (
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// newSandboxHarness activates the session screen together with the real
// settings module, so the recovery deep link lands on a registered page.
func newSandboxHarness(t *testing.T, chat *fakeChat) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	provide[contracts.SettingsService](t, ctx, contracts.KeySettings, newPermissionSettings(nil))
	activate(t, ctx, session.Module(), session.Config{})
	activate(t, ctx, settings.Module(), settings.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
}

// TestSandboxDeniedErrorLocalizedWithRecovery covers the message-level mapping:
// the heading, the plain-language explanation (global mode decides the OS wall;
// a session-level "all permissions" does not lift it) and the recovery action
// that opens the behavior settings page.
func TestSandboxDeniedErrorLocalizedWithRecovery(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "沙箱拒绝",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "写入系统目录"},
			{
				ID: "a1", Role: contracts.ChatRoleAssistant,
				Error:     "write /etc/hosts: operation not permitted\n[sandbox: suspected OS sandbox denial]",
				ErrorKind: contracts.MessageErrorKindSandboxDenied,
			},
		},
	}, true)
	h := newSandboxHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "疑似被 OS 沙箱拒绝",
		"当前权限模式可能限制了该操作（全局权限决定 OS 围栏，会话级『所有权限』不解除它）",
		"打开权限设置")

	h.click(tester, "打开权限设置")
	tester.Frame()
	if !h.ui.Navigator().SettingsOpen() {
		t.Fatal("打开权限设置 did not open the settings surface")
	}
	h.assertTexts(tester, "行为与限额")
}

// TestSandboxDeniedEnglishCopy covers the English explanation.
func TestSandboxDeniedEnglishCopy(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "sandbox",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "write"},
			{
				ID: "a1", Role: contracts.ChatRoleAssistant,
				Error:     "operation not permitted\n[sandbox: suspected OS sandbox denial]",
				ErrorKind: contracts.MessageErrorKindSandboxDenied,
			},
		},
	}, true)
	h := newHarness(t, chat)
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "Likely denied by the OS sandbox",
		"The global permission mode may restrict this operation (a session-level 'all permissions' does not lift the OS wall)",
		"Open permission settings")
}

// TestSandboxToolFailureRecovery covers the tool-level path: a failed tool call
// whose summary carries rig's attribution line gets the same recovery outside
// the process fold.
func TestSandboxToolFailureRecovery(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "工具沙箱拒绝",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "跑命令"},
			{
				ID: "a1", Role: contracts.ChatRoleAssistant, Text: "命令失败了",
				Tools: []contracts.ToolCall{{
					ID: "t1", Name: "bash", Status: "error",
					Summary:   "bash: /etc/hosts: Permission denied\n[sandbox: suspected OS sandbox denial]",
					ErrorKind: contracts.MessageErrorKindSandboxDenied,
				}},
			},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "疑似被 OS 沙箱拒绝", "打开权限设置")
}
