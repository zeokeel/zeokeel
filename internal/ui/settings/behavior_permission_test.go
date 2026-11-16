package settings_test

// Frame tests for US-05 (2026-11 wave D, behavior-page side): the three
// permission states get a VISIBLE plain-language explanation (not a tooltip
// buried on a disabled menu header), the two-layer model (approval gate × OS
// sandbox) is disclosed, the factual contradiction is fixed (shell tools are
// auto-allowed when the sandbox is available — not "仍会询问"), and the active
// session's effective approval/sandbox modes + permission scope are shown from
// the wave-A read model (EffectiveApprovalMode/EffectiveSandboxMode/
// PermissionScope), so a session-level "all permissions" override is visibly
// approval-only while the OS sandbox follows the global mode.

import (
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// permChat is a fakeChat whose session list carries the wave-A permission read
// model fields for the active session.
type permChat struct {
	*fakeChat
	info contracts.ChatSessionInfo
}

func (c *permChat) Sessions() []contracts.ChatSessionInfo {
	return []contracts.ChatSessionInfo{c.info}
}

// newPermHarness mirrors newHarnessFull but provides an arbitrary ChatService
// (the permission read-model fake), so the behavior page sees the wave-A fields.
func newPermHarness(t *testing.T, chat contracts.ChatService, store *fakeSettings) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide(t, ctx, contracts.KeySettings, contracts.SettingsService(store))
	provide(t, ctx, contracts.KeyChat, chat)
	h := &harness{t: t, ctx: ctx, uiRoot: uiRoot, settings: store}
	h.fiber = activate(t, ctx, settings.Module(), settings.Config{})
	uiRoot.Navigator().ToggleSettings()
	return h
}

// TestBehaviorPermissionExplanationVisible covers the visible three-state +
// two-layer explanation and the corrected shell auto-allow fact.
func TestBehaviorPermissionExplanationVisible(t *testing.T) {
	chat := &permChat{
		fakeChat: &fakeChat{active: "s1"},
		info: contracts.ChatSessionInfo{ID: "s1", Title: "会话",
			PermissionScope: contracts.PermissionScopeGlobal, EffectiveApprovalMode: contracts.PermissionWorkspace,
			EffectiveSandboxMode: contracts.PermissionWorkspace},
	}
	h := newPermHarness(t, chat, newFakeSettings(nil))
	tester := openBehaviorPage(t, h)

	h.assertTexts(tester,
		"审批与 OS 沙箱是两层",
		"沙箱可用时 shell 类工具自动放行",
		"会话/自动化级「所有权限」只放宽审批",
	)
	// The old contradictory claim (shell still asks while the sandbox auto-allows)
	// must not appear.
	h.assertNoTexts(tester, "仍会询问")
}

// TestBehaviorPermissionCurrentSessionDisclosure consumes the read model: a
// session-level full override is shown as approval-only while the OS sandbox
// stays at the global workspace mode.
func TestBehaviorPermissionCurrentSessionDisclosure(t *testing.T) {
	chat := &permChat{
		fakeChat: &fakeChat{active: "s1"},
		info: contracts.ChatSessionInfo{ID: "s1", Title: "会话",
			PermissionScope: contracts.PermissionScopeSession, EffectiveApprovalMode: contracts.PermissionFull,
			EffectiveSandboxMode: contracts.PermissionWorkspace},
	}
	h := newPermHarness(t, chat, newFakeSettings(nil))
	tester := openBehaviorPage(t, h)

	h.assertTexts(tester, "审批＝所有权限", "会话级覆盖", "OS 沙箱＝工作区修改")
}
