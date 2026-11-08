package session_test

// Permission-mode tests (written before the implementation, TDD): the session
// screen registers a 权限模式 slash command that cycles the stored permission
// and shows the current mode as a composer badge.

import (
	"sync"
	"testing"

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

	// Ask is the default: the composer shows the quiet session-menu label and
	// the state lives in the menu, not on a resident chip.
	h.assertTexts(tester, "会话设置")
	h.assertNoTexts(tester, "询问")

	h.typeSlash(tester)
	h.clickExact(tester, "权限模式")
	if got := settings.get(contracts.SettingAgentPermission); got != contracts.PermissionWorkspace {
		t.Fatalf("after first cycle = %q, want workspace", got)
	}
	h.assertTexts(tester, "工作区权限")

	h.typeSlash(tester)
	h.clickExact(tester, "权限模式")
	if got := settings.get(contracts.SettingAgentPermission); got != contracts.PermissionFull {
		t.Fatalf("after second cycle = %q, want full", got)
	}
	h.assertTexts(tester, "全部允许")

	h.typeSlash(tester)
	h.clickExact(tester, "权限模式")
	if got := settings.get(contracts.SettingAgentPermission); got != contracts.PermissionAsk {
		t.Fatalf("after third cycle = %q, want ask", got)
	}
	h.assertTexts(tester, "会话设置")
	h.assertNoTexts(tester, "全部允许", "工作区权限")
}

func TestPermissionBadgeReflectsSettings(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, permissionChat(), settings)
	tester := h.tester()
	// The ask default keeps the quiet label; no resident mode chip.
	h.assertTexts(tester, "会话设置")
	h.assertNoTexts(tester, "询问", "工作区", "全部允许")

	settings.set(contracts.SettingAgentPermission, contracts.PermissionWorkspace)
	tester.Frame()
	h.assertTexts(tester, "工作区权限")

	// The one session menu lists every permission mode; the current one is a
	// radio row and choosing another writes it through.
	h.openSessionMenu(tester)
	h.assertTexts(tester, "权限模式", "询问", "工作区", "全部允许", "从不允许")
	h.clickExact(tester, "全部允许")
	if got := settings.get(contracts.SettingAgentPermission); got != contracts.PermissionFull {
		t.Fatalf("menu choice stored %q, want full", got)
	}
	h.assertTexts(tester, "全部允许")

	// Unknown values degrade to the default (ask) label.
	settings.set(contracts.SettingAgentPermission, "unexpected")
	tester.Frame()
	h.assertTexts(tester, "会话设置")
}

func TestPermissionBadgeHiddenWithoutSettings(t *testing.T) {
	h := newPermissionHarness(t, permissionChat(), nil)
	tester := h.tester()
	h.assertNoTexts(tester, "询问", "工作区权限", "全部允许", "禁止工具")
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
	h.assertTexts(tester, "Workspace")

	// The ask default is the quiet label; it does not expose "Ask" on the row.
	settings.set(contracts.SettingAgentPermission, contracts.PermissionAsk)
	tester.Frame()
	h.assertTexts(tester, "Settings")

	settings.set(contracts.SettingAgentPermission, contracts.PermissionFull)
	tester.Frame()
	h.assertTexts(tester, "Allow all")

	settings.set(contracts.SettingAgentPermission, contracts.PermissionNever)
	tester.Frame()
	h.assertTexts(tester, "Tools off")
}
