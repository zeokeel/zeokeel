package session_test

// Regression tests for the shell/UI audit (task-24): the child switcher's single
// popover owner, the approval banner that must stay reachable in a child
// conversation, the truthful generated-file cards (successful calls only, the
// structured ToolCall.Path, the fallback parse and the row cap), and the cheap
// P2 cleanups (in-flight child send reset, popovers closed by navigation, jobs
// polling while a job runs).

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// auditTurnChat is one finished turn with the given tool calls.
func auditTurnChat(tools ...contracts.ToolCall) *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "文件回合",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "生成文件"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "完成。", Tools: tools},
		},
	}, true)
	return chat
}

// countExactText counts the frame labels exactly equal to text.
func countExactText(tester *ui.Tester, text string) int {
	count := 0
	for _, label := range tester.Texts() {
		if label == text {
			count++
		}
	}
	return count
}

// enterChildView enters one child conversation through the parent's switcher.
func enterChildView(t *testing.T, h *harness, tester *ui.Tester, chip, name string) {
	t.Helper()
	h.click(tester, chip)
	h.click(tester, "进入子智能体会话 "+name)
}

// TestChildSwitcherSingleOwner covers audit F2: while a child conversation is
// open only the breadcrumb owns the switcher popover (the header 子智能体 chip is
// gone), so the shared list binds once; opening, switching and returning never
// panics and the switcher closes with the child view.
func TestChildSwitcherSingleOwner(t *testing.T) {
	h := newAgentSurfaceHarness(t, headerChat("父会话"), agentServices{children: childAgentsFixture()})
	tester := h.tester()
	tester.Frame()

	enterChildView(t, h, tester, "子智能体 2", "文档审查员")
	h.assertTexts(tester, "父会话", "文档审查员", "任务")
	// The header chip is replaced by the breadcrumb's switcher: one entry only.
	if _, ok := tester.Find("子智能体 2"); ok {
		t.Fatalf("child view still renders the header 子智能体 chip; texts = %v", tester.Texts())
	}

	// The breadcrumb's switcher opens exactly one overlay and the header band
	// keeps rendering (a double-bound list would abort the header build).
	h.click(tester, "切换子智能体会话")
	if got := countExactText(tester, "子智能体会话"); got != 1 {
		t.Fatalf("子智能体会话 headers = %d, want exactly 1; texts = %v", got, tester.Texts())
	}
	h.assertTexts(tester, "文档审查员", "目录审计员", "进入子智能体会话 目录审计员",
		"父会话", "对话", "轨迹", "记忆")

	// Selecting the sibling switches the child conversation and closes the
	// switcher without a second popover owner.
	h.click(tester, "进入子智能体会话 目录审计员")
	if got := countExactText(tester, "子智能体会话"); got != 0 {
		t.Fatalf("switcher stayed open after choosing a child; texts = %v", tester.Texts())
	}
	h.assertTexts(tester, "目录审计员", "一次性子智能体：只读会话，不能继续对话。")

	// The parent crumb returns to the parent conversation and the switcher is
	// closed; the parent's own chip then opens one switcher again.
	h.click(tester, "父会话")
	h.assertTexts(tester, "帮我发布")
	h.assertNoTexts(tester, "子智能体会话")
	h.click(tester, "子智能体 2")
	if got := countExactText(tester, "子智能体会话"); got != 1 {
		t.Fatalf("parent switcher headers = %d, want exactly 1; texts = %v", got, tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	h.assertNoTexts(tester, "子智能体会话")
}

// TestChildViewKeepsApprovalBanner covers audit F16: the parent's pending
// approval banner (the only approval UI) stays visible above the child
// conversation, and Esc in the child view returns to the parent instead of
// silently denying the hidden approval.
func TestChildViewKeepsApprovalBanner(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "审批父会话", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "帮我发布"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "正在运行", Streaming: true},
		},
	}, true)
	approvals := &fakeApprovals{}
	approvals.setPending(contracts.ApprovalRequest{ID: "ap-1", Tool: "bash", Summary: "rm -rf build"})
	h := newAgentSurfaceHarness(t, chat, agentServices{
		children: childAgentsFixture(), approvals: approvals,
	})
	// The host approval module draws the inline card into this slot.
	unregister, err := h.ui.Runtime().RegisterSlotItem(contracts.SlotItem{
		Slot: contracts.SlotSessionBanner, ID: "test.approval-card", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "审批内联卡") },
	})
	if err != nil {
		t.Fatalf("register banner slot item: %v", err)
	}
	t.Cleanup(unregister)

	tester := h.tester()
	tester.Frame()
	enterChildView(t, h, tester, "子智能体 2", "文档审查员")
	h.assertTexts(tester, "审批内联卡", "文档审查员", "任务")

	// Esc must not resolve the approval from the child view: it returns to the
	// parent conversation, where the same banner is visible.
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if denied := approvals.deniedCalls(); len(denied) != 0 {
		t.Fatalf("Esc in the child view denied the hidden approval: %v", denied)
	}
	h.assertTexts(tester, "审批内联卡", "帮我发布")
	if _, ok := tester.Find("队友 · 可继续"); ok {
		t.Fatalf("Esc did not return to the parent conversation; texts = %v", tester.Texts())
	}
}

// TestGeneratedFileCardsOnlySuccessfulCalls covers audit F3: failed, cancelled,
// running and unknown-status file calls never become delivered/edited files.
func TestGeneratedFileCardsOnlySuccessfulCalls(t *testing.T) {
	chat := auditTurnChat(
		contracts.ToolCall{ID: "t1", Name: "write_file", Status: "ok",
			Path: "docs/ok.md", Arguments: `{"path":"docs/ok.md"}`},
		contracts.ToolCall{ID: "t6", Name: "write_file", Status: "denied",
			Path: "docs/denied.md"},
		contracts.ToolCall{ID: "t2", Name: "write_file", Status: "error",
			Path: "docs/failed.md"},
		contracts.ToolCall{ID: "t3", Name: "edit_file", Status: "cancelled",
			Path: "docs/cancelled.md"},
		contracts.ToolCall{ID: "t4", Name: "write_file", Status: "running",
			Path: "docs/running.md"},
		contracts.ToolCall{ID: "t5", Name: "write_file", Status: "",
			Path: "docs/unknown.md"},
	)
	h := newAgentSurfaceHarness(t, chat, agentServices{preview: &fakeFilePreview{}})
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "交付文件", "ok.md")
	h.assertNoTexts(tester, "failed.md", "cancelled.md", "running.md", "unknown.md", "denied.md")
	if _, ok := tester.Find("用系统应用打开 docs/failed.md"); ok {
		t.Fatalf("a failed write still offers the native-open action; texts = %v", tester.Texts())
	}
}

// TestGeneratedFileCardsStructuredPath covers audit F4: ToolCall.Path is the
// primary source (so a bounded/truncated Arguments does not lose the card), the
// Arguments parse stays for older records, and a truncated argument without a
// path never invents one.
func TestGeneratedFileCardsStructuredPath(t *testing.T) {
	truncated := `{"path":"docs/big.md","content":"` + strings.Repeat("x", 5000)
	lost := `{"path":"docs/lost.md","content":"` + strings.Repeat("y", 5000)
	chat := auditTurnChat(
		contracts.ToolCall{ID: "t1", Name: "write_file", Status: "ok",
			Path: "docs/big.md", Arguments: truncated},
		contracts.ToolCall{ID: "t2", Name: "edit_file", Status: "ok",
			Arguments: `{"path":"README.md","old_string":"a","new_string":"b"}`},
		contracts.ToolCall{ID: "t3", Name: "write_file", Status: "ok",
			Arguments: lost},
	)
	h := newAgentSurfaceHarness(t, chat, agentServices{preview: &fakeFilePreview{}})
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "交付文件", "big.md", "已编辑 1 个文件", "README.md")
	h.assertNoTexts(tester, "lost.md")
}

// TestGeneratedFileCardsCapRows covers audit F24: a turn with many files shows a
// bounded row list plus an honest "N more" line; the header count stays the
// deduplicated total.
func TestGeneratedFileCardsCapRows(t *testing.T) {
	var tools []contracts.ToolCall
	for index := 0; index < 12; index++ {
		tools = append(tools, contracts.ToolCall{
			ID: fmt.Sprintf("t%d", index), Name: "write_file", Status: "ok",
			Path: fmt.Sprintf("docs/file-%02d.md", index),
		})
	}
	h := newAgentSurfaceHarness(t, auditTurnChat(tools...), agentServices{preview: &fakeFilePreview{}})
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "交付文件", "12 个", "file-00.md", "file-07.md", "还有 4 个文件")
	rows := 0
	for _, label := range tester.Texts() {
		if strings.HasPrefix(label, "file-") && strings.HasSuffix(label, ".md") {
			rows++
		}
	}
	if rows != 8 {
		t.Fatalf("rendered file rows = %d, want the 8-row cap; texts = %v", rows, tester.Texts())
	}
	if _, ok := tester.Find("file-08.md"); ok {
		t.Fatalf("a file row past the cap rendered; texts = %v", tester.Texts())
	}
}

// TestChildSendingResetsOnReopen covers audit F9: an in-flight SendChildAgent
// cannot leave the next child visit stuck in its sending state.
func TestChildSendingResetsOnReopen(t *testing.T) {
	gate := make(chan struct{})
	closed := false
	closeGate := func() {
		if !closed {
			closed = true
			close(gate)
		}
	}
	t.Cleanup(closeGate)
	children := childAgentsFixture()
	children.sendGate = gate
	h := newAgentSurfaceHarness(t, headerChat("父会话"), agentServices{children: children})
	tester := h.tester()
	tester.Frame()

	enterChildView(t, h, tester, "子智能体 2", "文档审查员")
	input, ok := tester.Find("发送给 文档审查员")
	if !ok {
		t.Fatalf("child message input missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(input.X+input.W/2, input.Y+input.H/2)
	tester.Frame()
	tester.Type("第一条")
	tester.Frame()
	h.click(tester, "发送消息给 文档审查员")
	h.assertTexts(tester, "发送中")

	// Leave and re-enter: the in-flight send belongs to the previous visit and
	// must not disable the new one.
	h.click(tester, "父会话")
	enterChildView(t, h, tester, "子智能体 2", "文档审查员")
	h.assertNoTexts(tester, "发送中")
	input, ok = tester.Find("发送给 文档审查员")
	if !ok {
		t.Fatalf("child message input missing after re-entry; texts = %v", tester.Texts())
	}
	tester.ClickAt(input.X+input.W/2, input.Y+input.H/2)
	tester.Frame()
	tester.Type("第二条")
	tester.Frame()
	info, ok := tester.AccessByRole(ui.RoleButton, "发送消息给 文档审查员")
	if !ok {
		t.Fatalf("child send action missing after re-entry; texts = %v", tester.Texts())
	}
	if info.Disabled {
		t.Fatalf("child send stays disabled after re-entry (stale in-flight state)")
	}
	// Let the first (stale) call finish: its late result must not disturb the
	// reopened view.
	closeGate()
	tester.Frame()
	tester.Frame()
	h.assertNoTexts(tester, "发送中")
}

// TestPopoversCloseOnSessionSwitch covers audit F10: transient agent popovers do
// not survive a conversation switch.
func TestPopoversCloseOnSessionSwitch(t *testing.T) {
	chat := headerChat("会话甲")
	chat.addSession(contracts.ChatSession{
		ID: "sess-2", Title: "会话乙",
		Messages: []contracts.ChatMessage{{ID: "u2", Role: contracts.ChatRoleUser, Text: "另一个问题"}},
	}, false)
	h := newAgentSurfaceHarness(t, chat, agentServices{
		team: &fakeTeam{members: []contracts.TeamMember{
			{ID: "member-1", Name: "评审员", RunState: contracts.TeamRunIdle},
		}},
		children: childAgentsFixture(),
	})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "智能体团队")
	h.assertTexts(tester, "共享任务")
	if err := chat.SetActiveSession("sess-2"); err != nil {
		t.Fatalf("switch session: %v", err)
	}
	tester.Frame()
	h.assertNoTexts(tester, "共享任务")

	// The child switcher of the new conversation closes the same way.
	h.click(tester, "子智能体 2")
	h.assertTexts(tester, "子智能体会话")
	if err := chat.SetActiveSession("sess-1"); err != nil {
		t.Fatalf("switch back: %v", err)
	}
	tester.Frame()
	h.assertNoTexts(tester, "子智能体会话")
}

// TestPopoversCloseOnScreenRevisit covers audit F10: leaving the conversation
// screen and returning reopens it without the transient agent overlays.
func TestPopoversCloseOnScreenRevisit(t *testing.T) {
	chat := headerChat("会话甲")
	h := newAgentSurfaceHarness(t, chat, agentServices{
		team:     &fakeTeam{members: []contracts.TeamMember{{ID: "m1", Name: "审查员"}}},
		board:    &fakeAgentBoard{},
		children: childAgentsFixture(),
	})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "智能体团队")
	h.assertTexts(tester, "共享任务")

	// A second screen keeps the session module out of the build pass; returning
	// starts a fresh visit with no overlay.
	if _, err := h.ui.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID: "plugins", Title: "插件", Order: 50,
		Build: func(c *ui.Context) { ui.Text(c, "插件屏幕") },
	}); err != nil {
		t.Fatalf("register screen: %v", err)
	}
	if err := h.ui.Navigator().Push("plugins"); err != nil {
		t.Fatalf("push plugins: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "插件屏幕")
	if err := h.ui.Navigator().Push("session"); err != nil {
		t.Fatalf("push session: %v", err)
	}
	tester.Frame()
	h.assertNoTexts(tester, "共享任务")

	// The child switcher closes the same way.
	h.click(tester, "子智能体 2")
	h.assertTexts(tester, "子智能体会话")
	if err := h.ui.Navigator().Push("plugins"); err != nil {
		t.Fatalf("push plugins: %v", err)
	}
	tester.Frame()
	if err := h.ui.Navigator().Push("session"); err != nil {
		t.Fatalf("push session: %v", err)
	}
	tester.Frame()
	h.assertNoTexts(tester, "子智能体会话")
}

// TestJobsOverlayRefreshesWhileRunning covers audit F11: while a job runs the
// header schedules the next frame, so a completion shows without another input.
func TestJobsOverlayRefreshesWhileRunning(t *testing.T) {
	jobs := &fakeJobs{jobs: []contracts.BackgroundJob{{
		ID: "job-1", Command: "go build ./...", State: "running", Running: true,
		Duration: time.Second,
	}}}
	h := newAgentSurfaceHarness(t, headerChat("任务会话"), agentServices{jobs: jobs})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "1 个后台任务")
	h.assertTexts(tester, "运行中")

	// The job finishes while the UI is idle: the running poll asks for a later
	// frame, and that frame picks the finished state up on its own.
	jobs.mu.Lock()
	jobs.jobs[0].State = "exited"
	jobs.jobs[0].Running = false
	jobs.jobs[0].ExitCode = 0
	jobs.mu.Unlock()
	tester.Advance(2 * time.Second)
	h.assertNoExactTexts(tester, "运行中")
	h.assertTexts(tester, "完成")
}
