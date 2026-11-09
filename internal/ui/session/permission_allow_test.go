package session_test

// Tests for the session-scoped always-allow UX: the session menu shows the
// remembered tools the approval dialog's 本会话内始终允许 choice stored, and can
// forget them again through the optional contracts.SessionToolPermissions half.

import (
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// newToolsHarness builds the session screen with the settings store and the
// optional notifier the clear-remembered-tools action uses.
func newToolsHarness(t *testing.T, chat *fakeChat, settings *permissionSettings, notifier contracts.Notifier) *harness {
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
	if settings != nil {
		provide[contracts.SettingsService](t, ctx, contracts.KeySettings, settings)
	}
	if notifier != nil {
		provide[contracts.Notifier](t, ctx, contracts.KeyNotifier, notifier)
	}
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
}

// TestSessionMenuClearsRememberedTools covers the entry: with remembered tools
// the menu shows the count, choosing it forgets them, raises the success toast
// and removes the entry from the next menu.
func TestSessionMenuClearsRememberedTools(t *testing.T) {
	chat := permissionChat()
	chat.setAllowedTools("sess-1", []string{"bash", "write_file"})
	notifier := &fakeSessionNotifier{}
	h := newToolsHarness(t, chat, newPermissionSettings(nil), notifier)
	tester := h.tester()

	h.openSessionMenu(tester)
	h.assertTexts(tester, "权限模式", "清除自动允许的工具（2）")
	h.clickExact(tester, "清除自动允许的工具（2）")

	if got := chat.clearedAllowedSessions(); len(got) != 1 || got[0] != "sess-1" {
		t.Fatalf("ClearAllowedTools calls = %v, want [sess-1]", got)
	}
	if tools := chat.AllowedTools("sess-1"); len(tools) != 0 {
		t.Fatalf("remembered tools after clearing = %v, want none", tools)
	}
	toasts := notifier.toasts()
	if len(toasts) != 1 || toasts[0].level != contracts.NotifySuccess ||
		toasts[0].text != "已清除自动允许的工具" {
		t.Fatalf("toasts = %+v, want the success clear toast", toasts)
	}

	// The entry is gone once nothing is remembered.
	h.openSessionMenu(tester)
	h.assertTexts(tester, "权限模式")
	h.assertNoTexts(tester, "清除自动允许的工具")
}

// TestSessionMenuHidesClearWithoutRememberedTools covers the quiet default: no
// remembered tools means no extra menu row at all.
func TestSessionMenuHidesClearWithoutRememberedTools(t *testing.T) {
	h := newToolsHarness(t, permissionChat(), newPermissionSettings(nil), nil)
	tester := h.tester()
	h.openSessionMenu(tester)
	h.assertTexts(tester, "权限模式", "只读", "工作区可写", "所有权限")
	h.assertNoTexts(tester, "清除自动允许的工具")
}

// TestSessionMenuClearsRememberedToolsEnglish covers the English copy and the
// count in the label.
func TestSessionMenuClearsRememberedToolsEnglish(t *testing.T) {
	chat := permissionChat()
	chat.setAllowedTools("sess-1", []string{"bash"})
	h := newToolsHarness(t, chat, newPermissionSettings(nil), nil)
	tester := h.tester()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()

	h.openSessionMenu(tester)
	h.assertTexts(tester, "Permission mode", "Clear remembered tools (1)")
	h.clickExact(tester, "Clear remembered tools (1)")
	if got := chat.clearedAllowedSessions(); len(got) != 1 || got[0] != "sess-1" {
		t.Fatalf("ClearAllowedTools calls = %v, want [sess-1]", got)
	}
}
