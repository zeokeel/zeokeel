package session_test

// US-05 / audit (a) (2026-11): the composer permission switch is GLOBAL (it sets
// both the approval gate and the OS sandbox), and the permission menu discloses
// the two-layer model with the active session's effective modes — a session-level
// approval override (automation) never lifts the OS sandbox, which always follows
// the global preset. The copy also states the shell auto-allow fact correctly
// (no "shell/网络仍会询问" contradiction with the behavior page).

import (
	"strings"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// disclosureChat wraps the shared fake to expose the wave-A effective-mode read
// model on the active session (the real agent fills these; the plain fake does
// not), so the menu's two-layer disclosure can be exercised.
type disclosureChat struct {
	*fakeChat
	info contracts.ChatSessionInfo
}

func (c *disclosureChat) Sessions() []contracts.ChatSessionInfo {
	infos := c.fakeChat.Sessions()
	for i := range infos {
		if infos[i].ID == c.info.ID {
			infos[i].EffectiveApprovalMode = c.info.EffectiveApprovalMode
			infos[i].EffectiveSandboxMode = c.info.EffectiveSandboxMode
			infos[i].PermissionScope = c.info.PermissionScope
		}
	}
	return infos
}

// TestPermissionMenuTwoLayerDisclosure covers the global marker, the corrected
// shell auto-allow tooltip and the effective-modes disclosure row.
func TestPermissionMenuTwoLayerDisclosure(t *testing.T) {
	base := permissionChat()
	chat := &disclosureChat{fakeChat: base, info: contracts.ChatSessionInfo{
		ID:                    "sess-1",
		EffectiveApprovalMode: contracts.PermissionFull,
		EffectiveSandboxMode:  contracts.PermissionWorkspace,
		PermissionScope:       contracts.PermissionScopeSession,
	}}
	settings := newPermissionSettings(map[string]string{
		contracts.SettingAgentPermission: contracts.PermissionWorkspace,
	})
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	provide[contracts.SettingsService](t, ctx, contracts.KeySettings, settings)
	activate(t, ctx, session.Module(), session.Config{})
	h := &harness{t: t, ctx: ctx, ui: uiRoot, chat: base}
	tester := h.tester()
	tester.Frame()

	// The button names the preset and marks the switch global.
	h.assertTexts(tester, "工作区修改（全局）")
	btn, ok := tester.Access("工作区修改（全局）")
	if !ok {
		t.Fatalf("permission button missing; texts = %v", tester.Texts())
	}
	if !strings.Contains(btn.Description, "自动放行") || strings.Contains(btn.Description, "仍会询问") {
		t.Fatalf("permission tooltip = %q, want the shell auto-allow fact without the 仍会询问 contradiction", btn.Description)
	}

	h.openSessionMenu(tester)
	// The session carries a full approval override, but the OS sandbox still
	// follows the global workspace preset — the two layers are disclosed.
	h.assertTexts(tester, "当前会话：审批＝所有权限（会话级覆盖）")
	h.assertTexts(tester, "OS 沙箱＝工作区修改（跟随全局）")
}

// TestPermissionMenuDisclosureAbsentWithoutReadModel covers a stripped backend
// that does not expose the effective modes: the menu keeps just the presets.
func TestPermissionMenuDisclosureAbsentWithoutReadModel(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	h.openSessionMenu(tester)
	h.assertTexts(tester, "权限模式", "工作区修改", "所有权限", "只读")
	h.assertNoTexts(tester, "当前会话：审批＝")
}
