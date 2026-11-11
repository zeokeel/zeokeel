package session_test

// Permission-mode tests (written before the implementation, TDD): the session
// screen registers a 权限模式 slash command that cycles the stored permission
// and shows the current mode as a composer badge.

import (
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// permissionSettings is a mutex-protected contracts.SettingsService map.
type permissionSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func newPermissionSettings(values map[string]string) *permissionSettings {
	if values == nil {
		values = map[string]string{}
	}
	return &permissionSettings{values: values}
}

func (s *permissionSettings) Get(key, def string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return def
}

func (s *permissionSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *permissionSettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make(map[string]string, len(s.values))
	for key, value := range s.values {
		copied[key] = value
	}
	return copied
}

func (s *permissionSettings) get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[key]
}

func (s *permissionSettings) set(key, value string) { _ = s.Set(key, value) }

// newPermissionHarness builds the session screen harness with the optional
// settings store the permission mode lives in.
func newPermissionHarness(t *testing.T, chat *fakeChat, settings *permissionSettings) *harness {
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
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
}

// permissionChat is a fake chat with one active conversation (so the composer,
// and therefore the toolbar badge, renders).
func permissionChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "权限测试", Messages: []contracts.ChatMessage{
		{ID: "m1", Role: "user", Text: "你好"},
		{ID: "m2", Role: "assistant", Text: "你好呀"},
	}}, true)
	return chat
}

func findPermissionCommand(t *testing.T, h *harness) contracts.CommandSpec {
	t.Helper()
	for _, command := range h.ui.Runtime().Commands() {
		if command.ID == "zeokeel.session.permission" {
			return command
		}
	}
	t.Fatalf("permission command is not registered; commands = %v", h.ui.Runtime().Commands())
	return contracts.CommandSpec{}
}

// --- tests ------------------------------------------------------------------

func TestPermissionCommandRegistered(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	command := findPermissionCommand(t, h)
	if command.Title != "权限模式" {
		t.Fatalf("permission command title = %q, want 权限模式", command.Title)
	}
	if command.Enabled == nil || !command.Enabled() {
		t.Fatal("permission command is not enabled with a settings store")
	}

	// Without a settings store the command exists but is disabled (hidden from
	// the slash menu by matchingCommands).
	without := newPermissionHarness(t, permissionChat(), nil)
	disabled := findPermissionCommand(t, without)
	if disabled.Enabled != nil && disabled.Enabled() {
		t.Fatal("permission command is enabled without a settings store")
	}
}

func TestPermissionCommandCyclesMode(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()

	// Workspace is the default: the chip keeps the stable destination label and
	// the permission state lives inside the menu (R17).
	h.assertTexts(tester, "会话设置")
	h.assertNoTexts(tester, "询问")

	h.typeSlash(tester)
	h.clickExact(tester, "权限模式")
	if got := settings.get(contracts.SettingAgentPermission); got != contracts.PermissionFull {
		t.Fatalf("after first cycle = %q, want full", got)
	}
	h.assertTexts(tester, "会话设置")
	h.openSessionMenu(tester)
	assertMenuTrailingValue(t, tester, "所有权限")

	h.typeSlash(tester)
	h.clickExact(tester, "权限模式")
	if got := settings.get(contracts.SettingAgentPermission); got != contracts.PermissionReadOnly {
		t.Fatalf("after second cycle = %q, want read-only", got)
	}
	h.assertTexts(tester, "会话设置")

	h.typeSlash(tester)
	h.clickExact(tester, "权限模式")
	if got := settings.get(contracts.SettingAgentPermission); got != contracts.PermissionWorkspace {
		t.Fatalf("after third cycle = %q, want workspace", got)
	}
	h.assertTexts(tester, "会话设置")
	if got := textsContaining(tester.Texts(), "所有权限"); got != 0 {
		t.Fatalf("permission state still on the composer row; texts = %v", tester.Texts())
	}
}

func TestPermissionBadgeReflectsSettings(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	// The chip label is stable; the mode is readable inside the menu.
	h.assertTexts(tester, "会话设置")
	h.assertNoTexts(tester, "询问", "所有权限", "禁止工具")

	// The retired "ask" rows normalize to read-only, not to a separate Ask
	// state; the menu's permission value shows the normalized state.
	settings.set(contracts.SettingAgentPermission, contracts.PermissionAsk)
	tester.Frame()
	h.assertTexts(tester, "会话设置")
	h.openSessionMenu(tester)
	assertMenuTrailingValue(t, tester, "只读")
	settings.set(contracts.SettingAgentPermission, contracts.PermissionNever)
	tester.Frame()
	h.assertNoTexts(tester, "询问", "从不允许")

	settings.set(contracts.SettingAgentPermission, contracts.PermissionWorkspace)
	tester.Frame()
	h.assertTexts(tester, "会话设置")

	// The one session menu lists exactly the three permission modes; the
	// current one is a radio row and choosing another writes it through.
	h.openSessionMenu(tester)
	h.assertTexts(tester, "权限模式", "只读", "工作区可写", "所有权限")
	h.assertNoTexts(tester, "询问", "从不允许")
	h.clickExact(tester, "所有权限")
	if got := settings.get(contracts.SettingAgentPermission); got != contracts.PermissionFull {
		t.Fatalf("menu choice stored %q, want full", got)
	}
	h.assertTexts(tester, "会话设置")

	// Unknown values degrade to the default (workspace) value in the menu.
	settings.set(contracts.SettingAgentPermission, "unexpected")
	tester.Frame()
	h.assertTexts(tester, "会话设置")
	h.openSessionMenu(tester)
	assertMenuTrailingValue(t, tester, "工作区可写")
}

func TestPermissionBadgeHiddenWithoutSettings(t *testing.T) {
	h := newPermissionHarness(t, permissionChat(), nil)
	tester := h.tester()
	h.assertNoTexts(tester, "询问", "所有权限", "从不允许")
	// Without a settings store the session menu exists for the built-in
	// presets, but it offers no permission modes at all.
	h.openSessionMenu(tester)
	h.assertTexts(tester, "插入预设")
	h.assertNoTexts(tester, "权限模式", "对话模式")
}

func TestPermissionBadgeEnglishCopy(t *testing.T) {
	settings := newPermissionSettings(map[string]string{
		contracts.SettingAgentPermission: contracts.PermissionWorkspace,
	})
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Session settings")

	// Legacy ask/never rows normalize to read-only inside the menu.
	settings.set(contracts.SettingAgentPermission, contracts.PermissionAsk)
	tester.Frame()
	h.assertTexts(tester, "Session settings")
	h.openSessionMenu(tester)
	assertMenuTrailingValue(t, tester, "Read-only")

	settings.set(contracts.SettingAgentPermission, contracts.PermissionFull)
	tester.Frame()
	h.assertTexts(tester, "Session settings")
	h.openSessionMenu(tester)
	assertMenuTrailingValue(t, tester, "All permissions")
}

// TestSessionPermissionMenuStructure pins the user-reported menu problem: the
// disabled 权限模式 header comes FIRST, directly above exactly three Radio items
// (只读 / 工作区可写 / 所有权限) in that order, with no 询问/从不允许 rows. The
// current mode is the checked radio and choosing another persists it.
func TestSessionPermissionMenuStructure(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	h.openSessionMenu(tester)

	h.assertTexts(tester, "权限模式", "只读", "工作区可写", "所有权限", "对话模式")
	h.assertNoTexts(tester, "询问", "从不允许", "禁止工具", "全部允许")

	header, ok := tester.AccessByRole(ui.RoleMenuItem, "权限模式（所有会话）")
	if !ok {
		t.Fatalf("permission group header has no menu-item role; texts = %v", tester.Texts())
	}
	if !header.Disabled {
		t.Fatal("permission group header is selectable, want disabled")
	}
	modes := []string{"只读", "工作区可写", "所有权限"}
	previousY := header.Bounds.Y
	rects := make(map[string]ui.Rect, len(modes))
	for _, mode := range modes {
		info, ok := tester.AccessByRole(ui.RoleMenuItemRadio, mode)
		if !ok {
			t.Fatalf("permission mode %q is not a radio menu item; texts = %v", mode, tester.Texts())
		}
		if info.Bounds.Y <= previousY {
			t.Fatalf("permission mode %q is not below the group header (header Y=%.1f, item Y=%.1f)",
				mode, header.Bounds.Y, info.Bounds.Y)
		}
		previousY = info.Bounds.Y
		rects[mode] = info.Bounds
	}
	if rects["只读"].Y >= rects["工作区可写"].Y || rects["工作区可写"].Y >= rects["所有权限"].Y {
		t.Fatalf("permission modes out of order: 只读 Y=%.1f, 工作区可写 Y=%.1f, 所有权限 Y=%.1f",
			rects["只读"].Y, rects["工作区可写"].Y, rects["所有权限"].Y)
	}
	// Exactly three permission radios: the retired modes must not be offered.
	for _, retired := range []string{"询问", "从不允许", "Ask", "Never"} {
		if _, ok := tester.AccessByRole(ui.RoleMenuItemRadio, retired); ok {
			t.Fatalf("retired permission mode %q is still a selectable radio row", retired)
		}
	}
	workspace, _ := tester.AccessByRole(ui.RoleMenuItemRadio, "工作区可写")
	if !workspace.Checked {
		t.Fatal("workspace-write (the default) is not the checked permission radio")
	}

	// Choosing 只读 persists the canonical read-only value and moves the check.
	h.clickExact(tester, "只读")
	if got := settings.get(contracts.SettingAgentPermission); got != contracts.PermissionReadOnly {
		t.Fatalf("selecting 只读 stored %q, want %q", got, contracts.PermissionReadOnly)
	}
	settings.set(contracts.SettingAgentPermission, contracts.PermissionFull)
	tester.Frame()
	h.openSessionMenu(tester)
	readOnly, _ := tester.AccessByRole(ui.RoleMenuItemRadio, "只读")
	allPermissions, ok := tester.AccessByRole(ui.RoleMenuItemRadio, "所有权限")
	if !ok || !allPermissions.Checked || readOnly.Checked {
		t.Fatalf("radio marks after choosing all permissions: read-only=%v all=%v/%v",
			readOnly.Checked, allPermissions.Checked, ok)
	}
}
