package session_test

// First-run / landing / error-recovery frame tests (fix wave 1):
//
//   - FR-07: the empty conversation (the product's real landing) shows a setup
//     checklist with deep links, instead of relying on the Home screen's setup
//     card (Home is no longer a navigation destination).
//   - FR-30: the hero's 新对话 button on an already-empty conversation focuses
//     the composer instead of creating another empty session, and a genuinely
//     new session is created untitled so the first prompt still auto-titles it.
//   - FR-31: sending is disabled without a model with a visible reason and a
//     settings deep link; known backend failures become localized headings with
//     a recovery action while the raw text stays as selectable detail.
//   - PM-12: while an approval is pending the running hint names the approval
//     Escape (deny first), never the bare "Esc 停止".

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
)

// firstRunChecklistTexts is the setup checklist's copy: the header plus one row
// per missing prerequisite and its fixing action.
var firstRunChecklistTexts = []string{
	"开始前还需要：",
	"尚未配置模型",
	"配置模型",
	"尚未配置 API Key",
	"配置 API Key",
	"未打开工作区",
	"添加工作区",
}

// TestFirstRunChecklistShowsMissingPrerequisites covers FR-07: the landing (an
// empty conversation) names every missing prerequisite and offers the action
// that fixes it.
func TestFirstRunChecklistShowsMissingPrerequisites(t *testing.T) {
	chat := newFakeChat()
	chat.config = contracts.ChatConfig{} // no model, no API key
	workspace := &fakeWorkspace{noCurrent: true}
	h := newWorkspaceHarness(t, chat, workspace)

	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, firstRunChecklistTexts...)

	// The checklist must also read at the narrow window: every row and its
	// action stay reachable.
	tester.SetSize(704, 720)
	tester.Frame()
	h.assertTexts(tester, firstRunChecklistTexts...)
}

// TestFirstRunChecklistActionsDeepLink covers FR-07's deep links: the model /
// API-key actions open the settings surface on the model page, and the
// workspace action runs the folder picker and creates the workspace.
func TestFirstRunChecklistActionsDeepLink(t *testing.T) {
	chat := newFakeChat()
	chat.config = contracts.ChatConfig{}
	workspace := &fakeWorkspace{noCurrent: true}
	h := newWorkspaceHarness(t, chat, workspace)
	tester := h.tester()
	tester.Frame()

	h.clickExact(tester, "配置模型")
	if !h.ui.Navigator().SettingsOpen() {
		t.Fatal("配置模型 did not open the settings surface")
	}
	// The settings surface is a modal over the screen: close it before the next
	// checklist action (the test drives the real surfaces, not a mock).
	h.ui.Navigator().CloseSettings()
	tester.Frame()

	original := session.WorkspacePicker
	session.WorkspacePicker = func(string) (string, error) { return "/tmp/first-run-workspace", nil }
	t.Cleanup(func() { session.WorkspacePicker = original })

	h.clickExact(tester, "添加工作区")
	waitFor(t, tester, func() bool { return len(workspace.created) == 1 }, "workspace create")
	if got := workspace.created[0]; got != "/tmp/first-run-workspace" {
		t.Fatalf("workspace.Create root = %q, want /tmp/first-run-workspace", got)
	}
}

// TestFirstRunChecklistHiddenWhenReady covers the ready state: a configured
// model + API key and an open workspace show no checklist.
func TestFirstRunChecklistHiddenWhenReady(t *testing.T) {
	chat := newFakeChat()         // configured model + API key by default
	workspace := &fakeWorkspace{} // a current workspace exists
	h := newWorkspaceHarness(t, chat, workspace)
	tester := h.tester()
	tester.Frame()
	h.assertNoTexts(tester, "开始前还需要：", "尚未配置模型", "尚未配置 API Key", "未打开工作区")
}

// TestFirstRunChecklistEnglish covers the checklist copy in English (the
// product's copy is generated per frame; a language switch applies on the next
// frame).
func TestFirstRunChecklistEnglish(t *testing.T) {
	chat := newFakeChat()
	chat.config = contracts.ChatConfig{}
	workspace := &fakeWorkspace{noCurrent: true}
	h := newWorkspaceHarness(t, chat, workspace)
	tester := h.tester()
	tester.Frame()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Before you start:", "No model configured", "Configure model",
		"No API key yet", "Configure API key", "No workspace open", "Add workspace")
}

// TestHeroNewChatFocusesComposerInsteadOfDuplicating covers FR-30: on an
// already-empty conversation the hero's 新对话 button focuses the composer
// instead of creating a second empty session.
func TestHeroNewChatFocusesComposerInsteadOfDuplicating(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "空会话"}, true)
	h := newHarness(t, chat)
	tester := h.tester()

	before := len(chat.newSessionIDs())
	h.click(tester, "新对话")
	if got := len(chat.newSessionIDs()); got != before {
		t.Fatalf("NewSession calls after clicking the hero button = %d, want %d (no duplicate empty session)", got, before)
	}
	if !tester.Focused("输入消息…") && !tester.Focused("Ask ZeoKeel…") {
		t.Fatalf("the hero button did not focus the composer; texts = %v", tester.Texts())
	}
}

// TestHeroNewChatCreatesUntitledSession covers FR-30's auto-title half: when
// there is no conversation at all, the hero creates one with an empty title so
// the first prompt can name it.
func TestHeroNewChatCreatesUntitledSession(t *testing.T) {
	chat := newFakeChat()
	h := newHarness(t, chat)
	tester := h.tester()

	h.click(tester, "新对话")
	ids := chat.newSessionIDs()
	if len(ids) != 1 {
		t.Fatalf("NewSession calls = %v, want 1", ids)
	}
	created, ok := chat.Session(chat.ActiveSession())
	if !ok {
		t.Fatalf("created session %q is not readable", chat.ActiveSession())
	}
	if created.Title != "" {
		t.Fatalf("new session title = %q, want empty (the backend auto-titles from the first prompt)", created.Title)
	}
}

// TestSendDisabledWithoutModelShowsReasonAndRecovery covers FR-31: without a
// model the send control is disabled, says why and links to the model page;
// Enter must not send either.
func TestSendDisabledWithoutModelShowsReasonAndRecovery(t *testing.T) {
	chat := newFakeChat()
	chat.config = contracts.ChatConfig{} // no model
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "未配置模型",
		Messages: []contracts.ChatMessage{message("m1", contracts.ChatRoleUser, "你好")},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()

	focusComposer(t, tester)
	tester.Type("你好")
	tester.Frame()
	h.assertTexts(tester, "尚未配置模型：发送已禁用", "打开模型设置")
	if info, ok := tester.AccessByRole(ui.RoleButton, "发送"); !ok || !info.Disabled {
		t.Fatalf("send control is not disabled without a model: %+v found %v", info, ok)
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if sends := chat.sends(); len(sends) != 0 {
		t.Fatalf("Enter sent the draft without a model: %v", sends)
	}
	h.clickExact(tester, "打开模型设置")
	if !h.ui.Navigator().SettingsOpen() {
		t.Fatal("打开模型设置 did not open the settings surface")
	}
	h.ui.Navigator().CloseSettings()
	tester.Frame()

	// The warning row must survive the product's narrow composer: its text and
	// action stay visible at the 704-wide window (model chip folded away).
	tester.SetSize(704, 720)
	tester.Frame()
	tester.Frame()
	h.assertTexts(tester, "尚未配置模型：发送已禁用", "打开模型设置")
}

// TestSendFailureMapsNoModelToLocalizedRecovery covers FR-31's error mapping: a
// backend "no model configured" failure becomes a localized heading with the
// model-page action, and the raw backend text survives as selectable detail
// instead of being the visible headline.
func TestSendFailureMapsNoModelToLocalizedRecovery(t *testing.T) {
	chat := newFakeChat() // otherwise configured
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "发送失败",
		Messages: []contracts.ChatMessage{message("m1", contracts.ChatRoleUser, "你好")},
	}, true)
	chat.sendErr = errors.New("agent: no model configured; add one in Settings → Model")
	h := newHarness(t, chat)
	tester := h.tester()

	focusComposer(t, tester)
	tester.Type("再试一次")
	h.click(tester, "发送")
	tester.Frame()
	h.assertTexts(tester,
		"未配置模型：请先在设置 → 模型中添加模型",
		"打开模型设置",
		"agent: no model configured")
	h.assertNoTexts(tester, "发送失败：agent: no model configured")
	h.clickExact(tester, "打开模型设置")
	if !h.ui.Navigator().SettingsOpen() {
		t.Fatal("the recovery action did not open the settings surface")
	}
}

// TestToolFailureNoWorkspaceShowsRecovery covers FR-31's second class: a tool
// result denied by the sandbox because no workspace is open is explained in
// Chinese next to the tool, with the open-workspace action; the raw sandbox text
// stays readable.
func TestToolFailureNoWorkspaceShowsRecovery(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "无工作区工具",
		Messages: []contracts.ChatMessage{
			message("m1", contracts.ChatRoleUser, "看看目录"),
			{
				ID: "m2", Role: contracts.ChatRoleAssistant, Text: "我先看看。",
				Tools: []contracts.ToolCall{{
					ID: "t1", Name: "bash", Status: "error",
					Summary:   "sandbox: denied: no roots configured",
					Arguments: `{"command":"ls"}`,
				}},
			},
		},
	}, true)
	h := newVerboseWorkStepsHarness(t, chat)
	tester := h.tester()

	h.assertTexts(tester,
		"未打开工作区：文件与命令工具需要一个工作区",
		"打开工作区",
		"no roots configured")
}

// TestToolFailureRecoveryVisibleInFoldedProcess covers the default work-steps
// mode: a finished turn folds its process, but the recovery hint of a known
// failure stays visible outside the fold (the raw output waits inside it).
func TestToolFailureRecoveryVisibleInFoldedProcess(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "折叠失败",
		Messages: []contracts.ChatMessage{
			message("m1", contracts.ChatRoleUser, "看看目录"),
			{
				ID: "m2", Role: contracts.ChatRoleAssistant, Text: "我先看看。",
				Tools: []contracts.ToolCall{{
					ID: "t1", Name: "bash", Status: "error",
					Summary: "sandbox: denied: no roots configured",
				}},
			},
		},
	}, true)
	h := newHarness(t, chat) // standard work-steps mode: the process folds
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "未打开工作区：文件与命令工具需要一个工作区", "打开工作区")
	h.assertNoTexts(tester, "no roots configured")
}

// TestToolFailureSearchUnconfiguredShowsRecovery covers FR-31's third class: an
// unconfigured web-search endpoint becomes a localized heading with the search
// settings action.
func TestToolFailureSearchUnconfiguredShowsRecovery(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "未配置搜索",
		Messages: []contracts.ChatMessage{
			message("m1", contracts.ChatRoleUser, "搜一下"),
			{
				ID: "m2", Role: contracts.ChatRoleAssistant, Text: "我来搜索。",
				Tools: []contracts.ToolCall{{
					ID: "t1", Name: "web_search", Status: "error",
					Summary: "web search endpoint 未配置（websearch.endpoint）",
				}},
			},
		},
	}, true)
	h := newVerboseWorkStepsHarness(t, chat)
	tester := h.tester()

	h.assertTexts(tester,
		"未配置搜索后端：在设置 → 搜索中填写端点",
		"打开搜索设置",
		"websearch.endpoint")
	h.clickExact(tester, "打开搜索设置")
	if !h.ui.Navigator().SettingsOpen() {
		t.Fatal("the search recovery action did not open the settings surface")
	}
}

// TestRunningHintNamesApprovalEscapeWhenPending covers PM-12: with an approval
// pending, the composer's running hint must not promise "Esc 停止"; the first
// Escape denies the approval.
func TestRunningHintNamesApprovalEscapeWhenPending(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "审批提示", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "跑一下"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "正在跑", Streaming: true},
		},
	}, true)
	approvals := &fakeApprovals{}
	approvals.setPending(contracts.ApprovalRequest{ID: "ap-1", Tool: "bash", Summary: "ls"})
	h := newApprovalHarness(t, chat, approvals)
	tester := h.tester()
	focusComposerForRunningTest(t, tester)

	h.assertTexts(tester, "Enter 排队 · Esc 拒绝审批")
	h.assertNoTexts(tester, "Esc 停止")
}

// TestRunningHintUnchangedWithoutPendingApproval pins the other half of PM-12:
// with no approval waiting the running hint keeps its documented copy.
func TestRunningHintUnchangedWithoutPendingApproval(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "运行提示", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "跑一下"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "正在跑", Streaming: true},
		},
	}, true)
	approvals := &fakeApprovals{}
	h := newApprovalHarness(t, chat, approvals)
	tester := h.tester()
	focusComposerForRunningTest(t, tester)

	h.assertTexts(tester, "Enter 排队 · Esc 停止")
}
