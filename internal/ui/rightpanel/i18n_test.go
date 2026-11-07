package rightpanel_test

// i18n frame tests: the inspector's heading/tabs and every pane's own copy
// follow the host language per frame, and switch back to Chinese immediately.

import (
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/rightpanel"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// newLocaleHarness builds the product frame with an explicit host language.
func newLocaleHarness(t *testing.T, language string, chat *fakeChat, cfg rightpanel.Config,
	workspace contracts.WorkspaceService, terminal contracts.TerminalService) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel", Language: language})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	if workspace != nil {
		provide[contracts.WorkspaceService](t, ctx, contracts.KeyWorkspace, workspace)
	}
	if terminal != nil {
		provide[contracts.TerminalService](t, ctx, contracts.KeyTerminal, terminal)
	}
	if _, err := uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID:    "session",
		Title: "会话",
		Build: func(c *ui.Context) {
			uiRoot.Runtime().RenderSlot(c, contracts.SlotSessionPanelRight)
		},
	}); err != nil {
		t.Fatalf("register session screen: %v", err)
	}
	h := &harness{t: t, ctx: ctx, uiRoot: uiRoot, chat: chat}
	h.fiber = activate(t, ctx, rightpanel.Module(), cfg)
	return h
}

// TestRightpanelEnglishPanes covers the inspector heading, all tabs and the
// copy of the overview/tools/files/changes/terminal/log panes in English.
func TestRightpanelEnglishPanes(t *testing.T) {
	chat := newFakeChat()
	chat.config = contracts.ChatConfig{Model: "deepseek-chat", BaseURL: "https://api.example.com/v1", APIKey: "secret", ContextBudget: 4096, MaxIterations: 5}
	chat.addSession(contracts.ChatSession{
		ID: "s1", Title: "Project", Running: true, Error: "boom",
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "hi"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "ok", Tools: []contracts.ToolCall{
				{ID: "t1", Name: "bash", Status: "running", Summary: "running command"},
				{ID: "t2", Name: "read_file", Status: "ok"},
				{ID: "t3", Name: "web_search", Status: "error"},
			}},
		},
	}, true)
	h := newLocaleHarness(t, "en", chat, rightpanel.Config{}, newDifferWorkspace(), newFakeTerminal())
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "Inspector", "Overview", "Tools", "Log", "Files", "Changes", "Terminal",
		"Project", "Running", "Error", "boom", "Messages", "Updated", "Session ID",
		"Model configuration", "Model", "deepseek-chat", "API Key", "Set (hidden)",
		"Context budget", "4096", "Max iterations", "5")
	h.assertNoTexts(tester, "检查", "概览", "工具", "日志", "文件", "变更", "终端")

	h.click(tester, "Tools")
	h.assertTexts(tester, "Tool calls", "bash", "read_file", "web_search",
		"Running", "Completed", "Failed", "Summary")

	h.click(tester, "Files")
	h.assertTexts(tester, "Workspace", "Refresh", "The directory is empty", "Select a file to preview")

	h.click(tester, "Changes")
	h.assertTexts(tester, "Changes", "Refresh", "No uncommitted changes in the workspace")

	h.click(tester, "Terminal")
	h.assertTexts(tester, "Choose shell", "Clear", "Run commands in the workspace root",
		"Command", "Run", "Simple command runner (no PTY / persistent shell)")

	h.click(tester, "Log")
	h.assertTexts(tester, "The log requires a persistent session store")

	// Switching back to Chinese updates the same frame content.
	h.uiRoot.Locale().SetLanguage("zh")
	tester.Frame()
	h.assertTexts(tester, "检查", "概览", "工具", "日志", "文件", "变更", "终端")
	h.click(tester, "变更")
	h.assertTexts(tester, "变更", "刷新", "工作区没有未提交的变更")
}

// TestRightpanelEnglishEmptyOverview covers the localized no-active-session
// empty state and config label overrides.
func TestRightpanelEnglishEmptyOverview(t *testing.T) {
	h := newLocaleHarness(t, "en", newFakeChat(), rightpanel.Config{
		Title:       "Details",
		OverviewTab: "Info",
		EmptyText:   "Nothing selected",
	}, nil, nil)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "Details", "Info", "Nothing selected", "Tools", "Terminal")
}
