package sidebar_test

// i18n frame tests: the sidebar's own copy follows the host locale on every
// frame (English now, Chinese after switching back) without restarting the
// module. Screen titles owned by other modules are intentionally untouched.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// i18nChat adds the optional session-operation halves to the sidebar fake so
// the row menu can render every action.
type i18nChat struct {
	*fakeChat
	renames []string
	forks   []string
	pins    []string
	archive []string
}

func (c *i18nChat) RenameSession(sessionID, title string) error {
	c.renames = append(c.renames, sessionID+":"+title)
	return nil
}

func (c *i18nChat) ForkSession(sessionID string) (string, error) {
	c.forks = append(c.forks, sessionID)
	return "forked", nil
}

func (c *i18nChat) SetSessionPinned(sessionID string, pinned bool) error {
	c.pins = append(c.pins, sessionID)
	return nil
}

func (c *i18nChat) SetSessionArchived(sessionID string, archived bool) error {
	c.archive = append(c.archive, sessionID)
	return nil
}

// newLocaleHarness builds the sidebar harness with an explicit host language so
// the test can switch it between frames.
func newLocaleHarness(t *testing.T, chat contracts.ChatService, cfg sidebar.Config,
	language string, workspace contracts.WorkspaceService, settings contracts.SettingsService) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel", Language: language})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })

	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide(t, ctx, contracts.KeyChat, contracts.ChatService(chat))
	if workspace != nil {
		provide[contracts.WorkspaceService](t, ctx, contracts.KeyWorkspace, workspace)
	}
	if settings != nil {
		provide[contracts.SettingsService](t, ctx, contracts.KeySettings, settings)
	}
	for _, spec := range []contracts.ScreenSpec{
		{ID: "home", Title: "首页", Order: 5, Build: func(c *ui.Context) { ui.Text(c, "home screen") }},
		{ID: "session", Title: "会话", Order: 20, Build: func(c *ui.Context) { ui.Text(c, "session screen") }},
		{ID: "automation", Title: "自动化", Order: 30, Build: func(c *ui.Context) { ui.Text(c, "automation screen") }},
		{ID: "plugins", Title: "插件", Order: 35, Build: func(c *ui.Context) { ui.Text(c, "plugins screen") }},
	} {
		if _, err := uiRoot.Runtime().RegisterScreen(spec); err != nil {
			t.Fatalf("register screen %q: %v", spec.ID, err)
		}
	}
	h := &harness{t: t, ctx: ctx, uiRoot: uiRoot}
	h.fiber = activate(t, ctx, sidebar.Module(), cfg)
	return h
}

// TestSidebarEnglishCopyAndLanguageSwitch covers the primary/search/footer copy,
// the row menu and dialogs in English, then the immediate switch back to zh.
func TestSidebarEnglishCopyAndLanguageSwitch(t *testing.T) {
	settings := newSidebarSettings(nil)
	chat := &i18nChat{fakeChat: &fakeChat{
		sessions: []contracts.ChatSessionInfo{{ID: "s1", Title: "Project Alpha"}},
	}}
	h := newLocaleHarness(t, chat, sidebar.Config{}, "en", nil, settings)
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "New chat", "Search chats and messages", "Settings", "Project Alpha",
		"Home", "Automation", "Plugins")
	h.assertNoTexts(tester, "Show archived", "Hide archived")
	h.assertNoTexts(tester, "新对话", "搜索会话与消息", "设置", "显示已归档", "首页", "自动化", "插件")

	// Row menu: every session action is English.
	openRowMenu(t, tester, "Project Alpha")
	h.assertTexts(tester, "Rename", "Delete", "Duplicate", "Set as current",
		"Pin session", "Archive session")
	h.assertNoTexts(tester, "重命名", "删除", "创建副本", "设为当前", "置顶会话", "归档会话")

	// Rename dialog.
	clickLabel(t, tester, "Rename")
	h.assertTexts(tester, "Rename session", "Session title", "Save", "Cancel")
	h.assertNoTexts(tester, "重命名会话", "会话标题")
	clickLabel(t, tester, "Cancel")
	h.assertNoTexts(tester, "Rename session")

	// Delete confirmation dialog.
	openRowMenu(t, tester, "Project Alpha")
	clickLabel(t, tester, "Delete")
	h.assertTexts(tester, "Delete session", "Cancel", "Delete")
	texts := strings.Join(tester.Texts(), "\n")
	if !strings.Contains(texts, "will be deleted") || !strings.Contains(texts, "cannot be undone") {
		t.Fatalf("delete dialog copy is not English: %v", tester.Texts())
	}
	clickLabel(t, tester, "Cancel")
	h.assertNoTexts(tester, "Delete session")

	// Switching the host language takes effect on the next frame.
	h.uiRoot.Locale().SetLanguage("zh")
	tester.Frame()
	h.assertTexts(tester, "新对话", "搜索会话与消息", "设置",
		"首页", "自动化", "插件")
	h.assertNoTexts(tester, "New chat", "Search chats and messages", "Settings", "Show archived",
		"Home", "Automation", "Plugins", "显示已归档", "隐藏已归档")
	openRowMenu(t, tester, "Project Alpha")
	h.assertTexts(tester, "重命名", "删除", "创建副本", "设为当前", "置顶会话", "归档会话")
}

// TestSidebarEnglishWorkspaceBadgesAndEmptyStates covers the workspace labels,
// translated row badges and the empty-state copy.
func TestSidebarEnglishWorkspaceBadgesAndEmptyStates(t *testing.T) {
	workspace := newFakeWorkspace(
		contracts.Workspace{ID: "w1", Name: "ZeoKeel", Root: "/tmp/zeokeel"},
	).withCurrent(contracts.Workspace{ID: "w1", Name: "ZeoKeel", Root: "/tmp/zeokeel"})
	settings := newSidebarSettings(map[string]string{contracts.SettingSessionsShowArchived: "true"})
	chat := &i18nChat{fakeChat: &fakeChat{sessions: []contracts.ChatSessionInfo{
		{ID: "s1", Title: "Alpha", WorkspaceID: "w1", Pinned: true},
		{ID: "s2", Title: "Beta", WorkspaceID: "w1", Archived: true, Running: true},
		{ID: "s3", Title: "Gamma"},
	}}}
	h := newLocaleHarness(t, chat, sidebar.Config{}, "en", workspace, settings)
	tester := h.tester()
	tester.Frame()
	// The archived session (Beta) is owned by the settings archive page now: it
	// never renders in the sidebar, even with the legacy view setting on.
	h.assertTexts(tester, "Workspace", "Ungrouped", "Alpha", "Gamma", "Pinned",
		"New chat in “ZeoKeel”")
	h.assertNoTexts(tester, "Beta", "Archived", "Running", "Show archived", "Hide archived",
		"工作区", "未分组", "置顶", "已归档", "运行中", "在“ZeoKeel”中新建会话")

	// Empty state for a fresh sidebar.
	empty := newLocaleHarness(t, &i18nChat{fakeChat: &fakeChat{}}, sidebar.Config{}, "en", nil, nil)
	emptyTester := empty.tester()
	emptyTester.Frame()
	empty.assertTexts(emptyTester, "No sessions yet")
	clickLabel(t, emptyTester, "Search chats and messages")
	emptyTester.Type("zzz")
	emptyTester.Frame()
	empty.assertTexts(emptyTester, "No matching sessions")
}

// TestSidebarConfigLabelOverridesSurviveEnglish pins that explicit assembly
// labels still win over localization.
func TestSidebarConfigLabelOverridesSurviveEnglish(t *testing.T) {
	h := newLocaleHarness(t, &i18nChat{fakeChat: &fakeChat{}}, sidebar.Config{
		NewChatLabel:  "Start here",
		SettingsLabel: "Preferences",
		SessionsTitle: "Conversations",
	}, "en", nil, nil)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "Start here", "Preferences", "Conversations")
}

// TestSidebarEnglishUntitledSearch covers the localized blank-title filter: in
// English a blank title reads "Untitled session", so searching "Untitled" finds
// it the way "未命名" does in Chinese.
func TestSidebarEnglishUntitledSearch(t *testing.T) {
	h := newLocaleHarness(t, &i18nChat{fakeChat: &fakeChat{
		sessions: []contracts.ChatSessionInfo{{ID: "s1", Title: ""}},
	}}, sidebar.Config{}, "en", nil, nil)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "Untitled session")

	clickLabel(t, tester, "Search chats and messages")
	tester.Type("Untitled")
	tester.Frame()
	h.assertTexts(tester, "Untitled session")
}
