package session_test

// Frame tests for the DSH-aligned agent surfaces of the session shell: the
// header's team entry / subagent chip / background-jobs chip (and their
// overlays), the child-agent conversation view with its breadcrumb, and the
// generated-file turn-tail cards. Fakes implement the shared contracts from
// internal/contracts/agentsurface.go so no backend module is needed.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// --- fake ChildAgentService ------------------------------------------------

type childSendCall struct {
	id      string
	message string
}

type fakeChildAgents struct {
	mu      sync.Mutex
	agents  []contracts.ChildAgent
	sends   []childSendCall
	answer  string
	sendErr error
	// sendGate, when set, holds every SendChildAgent call until it is closed:
	// the child-view tests use it to observe an in-flight send.
	sendGate <-chan struct{}
}

func (f *fakeChildAgents) ChildAgents(parentSessionID string) []contracts.ChildAgent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.ChildAgent(nil), f.agents...)
}

func (f *fakeChildAgents) ChildAgent(id string) (contracts.ChildAgent, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, agent := range f.agents {
		if agent.ID == id {
			return agent, true
		}
	}
	return contracts.ChildAgent{}, false
}

func (f *fakeChildAgents) SendChildAgent(_ context.Context, id, message string) (string, error) {
	if gate := f.sendGate; gate != nil {
		<-gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, childSendCall{id: id, message: message})
	if f.sendErr != nil {
		return "", f.sendErr
	}
	return f.answer, nil
}

func (f *fakeChildAgents) sent() []childSendCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]childSendCall(nil), f.sends...)
}

// --- fake JobService --------------------------------------------------------

type fakeJobs struct {
	mu      sync.Mutex
	jobs    []contracts.BackgroundJob
	kills   []string
	killErr error
}

func (f *fakeJobs) Jobs() []contracts.BackgroundJob {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.BackgroundJob(nil), f.jobs...)
}

func (f *fakeJobs) KillJob(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kills = append(f.kills, id)
	return f.killErr
}

func (f *fakeJobs) killed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.kills...)
}

// --- fake FilePreviewer -----------------------------------------------------

type fakeFilePreview struct {
	mu        sync.Mutex
	previews  []string
	natives   []string
	openFiles int
	err       error
}

func (f *fakeFilePreview) PreviewFile(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.previews = append(f.previews, path)
	return f.err
}

func (f *fakeFilePreview) OpenFiles() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openFiles++
	return nil
}

func (f *fakeFilePreview) OpenTerminal() error { return nil }

func (f *fakeFilePreview) OpenNative(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.natives = append(f.natives, path)
	return f.err
}

func (f *fakeFilePreview) opened() (previews, natives []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.previews...), append([]string(nil), f.natives...)
}

// --- fake TaskBoard ---------------------------------------------------------

type fakeAgentBoard struct {
	mu      sync.Mutex
	tasks   []contracts.BoardTask
	listErr error
}

func (f *fakeAgentBoard) List(context.Context) ([]contracts.BoardTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]contracts.BoardTask(nil), f.tasks...), nil
}

func (f *fakeAgentBoard) Create(context.Context, string, string) (contracts.BoardTask, error) {
	return contracts.BoardTask{}, nil
}

func (f *fakeAgentBoard) SetStatus(context.Context, string, string) (contracts.BoardTask, error) {
	return contracts.BoardTask{}, nil
}

func (f *fakeAgentBoard) Delete(context.Context, string) error { return nil }

// --- harness ----------------------------------------------------------------

// agentServices are the optional backends the agent surfaces read; a nil entry
// means the host does not provide that service.
type agentServices struct {
	team      contracts.TeamService
	board     contracts.TaskBoard
	children  contracts.ChildAgentService
	jobs      contracts.JobService
	preview   contracts.FilePreviewer
	approvals contracts.Approvals
}

// newAgentSurfaceHarness activates the session screen with the optional agent
// surface services provided before activation.
func newAgentSurfaceHarness(t *testing.T, chat *fakeChat, services agentServices) *harness {
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
	if services.team != nil {
		provide[contracts.TeamService](t, ctx, contracts.KeyTeam, services.team)
	}
	if services.board != nil {
		provide[contracts.TaskBoard](t, ctx, contracts.KeyTaskBoard, services.board)
	}
	if services.children != nil {
		provide[contracts.ChildAgentService](t, ctx, contracts.KeyChildAgents, services.children)
	}
	if services.jobs != nil {
		provide[contracts.JobService](t, ctx, contracts.KeyJobs, services.jobs)
	}
	if services.preview != nil {
		provide[contracts.FilePreviewer](t, ctx, contracts.KeyFilePreview, services.preview)
	}
	if services.approvals != nil {
		provide[contracts.Approvals](t, ctx, contracts.KeyApprovals, services.approvals)
	}
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
}

// headerChat is one idle conversation with a single user turn.
func headerChat(title string) *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: title,
		Messages: []contracts.ChatMessage{{ID: "u1", Role: contracts.ChatRoleUser, Text: "帮我发布"}},
	}, true)
	return chat
}

// waitFor polls frames until condition holds or the test times out.
func waitFor(t *testing.T, tester *ui.Tester, condition func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Millisecond)
		tester.Frame()
	}
	t.Fatalf("timed out waiting for %s", what)
}

// rectOrder asserts that the named texts appear left to right in one band.
func rectOrder(t *testing.T, tester *ui.Tester, labels ...string) {
	t.Helper()
	var previous float32
	for index, label := range labels {
		rect, ok := tester.Find(label)
		if !ok {
			t.Fatalf("header entry %q missing; texts = %v", label, tester.Texts())
		}
		if index > 0 && rect.X <= previous {
			t.Fatalf("%q at x=%.1f is not right of the previous header entry (x=%.1f): %v", label, rect.X, previous, labels)
		}
		previous = rect.X
	}
}

// --- header ----------------------------------------------------------------

// TestHeaderDSHActionOrder covers the DSH header order: title/breadcrumb, the
// 智能体团队 entry, the 子智能体 chip, the background-jobs chip, then the host
// utilities (the conversation-actions ⋯ of the actions slot). The 对话/轨迹/记忆
// tabs row stays as it was.
func TestHeaderDSHActionOrder(t *testing.T) {
	chat := headerChat("发布会话")
	services := agentServices{
		team: &fakeTeam{members: []contracts.TeamMember{
			{ID: "member-1", Name: "文档审查员", RunState: contracts.TeamRunRunning},
			{ID: "member-2", Name: "测试执行员", RunState: contracts.TeamRunIdle},
		}},
		children: &fakeChildAgents{agents: []contracts.ChildAgent{
			{ID: "subagent-1", Name: "目录审计员", Kind: contracts.ChildAgentSubagent},
			{ID: "member-1", Name: "文档审查员", Kind: contracts.ChildAgentTeammate},
		}},
		jobs: &fakeJobs{jobs: []contracts.BackgroundJob{
			{ID: "job-1", Command: "npm test", State: "running", Running: true},
			{ID: "job-2", Command: "go build ./...", State: "exited"},
			{ID: "job-3", Command: "sleep 100", State: "running", Running: true},
		}},
	}
	h := newAgentSurfaceHarness(t, chat, services)
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "发布会话", "智能体团队", "子智能体 2", "3 个后台任务", "会话操作",
		"对话", "轨迹", "记忆")
	rectOrder(t, tester, "发布会话", "智能体团队", "子智能体 2", "3 个后台任务", "会话操作")
	// The old team chip/menu is gone: no count chip and no inspector commands.
	h.assertNoTexts(tester, "团队 2", "打开团队面板", "打开任务板")
}

// TestHeaderAgentEntriesHiddenWithoutBackends covers the optional lookups: with
// no team / child / job service the header keeps its title and utilities only.
func TestHeaderAgentEntriesHiddenWithoutBackends(t *testing.T) {
	h := newAgentSurfaceHarness(t, headerChat("无后端"), agentServices{})
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "无后端", "会话操作")
	h.assertNoTexts(tester, "智能体团队", "子智能体", "个后台任务")
}

// TestJobsChipHiddenAtZero covers R15: a zero job count renders no header chip
// (the chip is a notification of real work, not a permanent zero badge), while
// the team entry stays for discoverability.
func TestJobsChipHiddenAtZero(t *testing.T) {
	h := newAgentSurfaceHarness(t, headerChat("无任务"), agentServices{jobs: &fakeJobs{}})
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "无任务")
	h.assertNoTexts(tester, "个后台任务", "智能体团队")
}

// TestSubagentEntryShownAtZero pins the v0.2.20 correction: subagents are a
// key orchestration feature, so the entry stays visible at zero (the overlay
// explains the empty state) instead of disappearing.
func TestSubagentEntryShownAtZero(t *testing.T) {
	h := newAgentSurfaceHarness(t, headerChat("无子代理"), agentServices{children: &fakeChildAgents{}})
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "无子代理", "子智能体 0")
}

// TestTeamEntryShownWithEmptyTeam pins the v0.2.20 correction: the team entry
// stays visible even at zero (team orchestration is a key feature), and its
// overlay explains the empty state.
func TestTeamEntryShownWithEmptyTeam(t *testing.T) {
	h := newAgentSurfaceHarness(t, headerChat("空团队"), agentServices{team: &fakeTeam{}})
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "智能体团队")
	h.click(tester, "智能体团队")
	h.assertTexts(tester, "成员", "还没有队友", "共享任务")
}

// TestHeaderChipsCompactAtNarrowWidth covers the narrow band: the entries keep
// their icons and counts (the full labels go), so the title keeps its room.
func TestHeaderChipsCompactAtNarrowWidth(t *testing.T) {
	chat := headerChat("窄窗口的会话标题")
	h := newAgentSurfaceHarness(t, chat, agentServices{
		team: &fakeTeam{members: []contracts.TeamMember{
			{ID: "member-1", RunState: contracts.TeamRunRunning},
			{ID: "member-2", RunState: contracts.TeamRunIdle},
		}},
		children: &fakeChildAgents{agents: []contracts.ChildAgent{{ID: "subagent-1", Name: "审计员"}}},
		jobs:     &fakeJobs{jobs: []contracts.BackgroundJob{{ID: "job-1", State: "running", Running: true}}},
	})
	tester := h.tester()
	tester.SetSize(800, 700)
	tester.Frame()
	tester.Frame()
	tester.Frame()
	// Compact entries keep the icon and the count only: their click targets are
	// narrow (no words), while the full-form target is wider than this bound.
	for _, entry := range []struct {
		label string
		maxW  float32
	}{
		{"智能体团队", 80},
		{"子智能体 1", 80},
		{"1 个后台任务", 80},
	} {
		rect, ok := tester.Find(entry.label)
		if !ok {
			t.Fatalf("narrow header lost the %q entry; texts = %v", entry.label, tester.Texts())
		}
		if rect.W > entry.maxW {
			t.Fatalf("narrow header entry %q is %.1f wide, want the compact form (<= %.0f)", entry.label, rect.W, entry.maxW)
		}
	}
	// The title keeps a readable measure beside the compact entries.
	if title, ok := tester.Find("窄窗口的会话标题"); !ok || title.W < 100 {
		t.Fatalf("narrow header title width = %.1f, want >= 100", title.W)
	}
}

// --- team overlay -----------------------------------------------------------

// TestTeamOverlayShowsMembersAndSharedTasks covers the team overlay: member
// cards (state dot/name/model/last activity/open) and shared task cards
// (subject/status/owner/blockers/write scopes), Esc closing the overlay.
func TestTeamOverlayShowsMembersAndSharedTasks(t *testing.T) {
	chat := headerChat("团队会话")
	activityAt := time.Date(2026, 11, 10, 19, 2, 0, 0, time.Local)
	team := &fakeTeam{members: []contracts.TeamMember{
		{
			ID: "member-1", Name: "文档审查员", RunState: contracts.TeamRunRunning,
			Model: "gpt-5-codex",
			Activity: []contracts.TeamActivity{{
				ID: "a1", MemberID: "member-1", Kind: contracts.TeamActivityFinish,
				Text: "核对 docs 完成", Time: activityAt,
			}},
		},
		{ID: "member-2", Name: "测试执行员", RunState: contracts.TeamRunIdle, Model: "deepseek-flash"},
	}}
	board := &fakeAgentBoard{tasks: []contracts.BoardTask{
		{
			ID: "task-1", Subject: "检查 docs 与 README", Status: contracts.TaskStatusInProgress,
			Owner: "docs-auditor", WriteScopes: []string{"docs/"},
		},
		{
			ID: "task-2", Subject: "运行完整测试并汇报", Status: contracts.TaskStatusPending,
			Owner: "test-runner", BlockedBy: []string{"task-1"},
		},
	}}
	h := newAgentSurfaceHarness(t, chat, agentServices{
		team: team, board: board,
		children: &fakeChildAgents{agents: []contracts.ChildAgent{
			{ID: "member-1", Name: "文档审查员", Kind: contracts.ChildAgentTeammate},
			{ID: "member-2", Name: "测试执行员", Kind: contracts.ChildAgentTeammate},
		}},
	})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "智能体团队")
	h.assertTexts(tester,
		"成员", "文档审查员", "gpt-5-codex", "核对 docs 完成", "测试执行员", "deepseek-flash",
		"共享任务", "检查 docs 与 README", "进行中", "负责人 docs-auditor", "写入 docs/",
		"运行完整测试并汇报", "待处理", "负责人 test-runner", "依赖 task-1",
	)

	// Escape closes the overlay (the native popover's contract).
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	h.assertNoTexts(tester, "共享任务")
}

// TestTeamOverlayOpenMemberEntersChildView covers the member card's 打开 action:
// it opens the teammate's child conversation in the main content area.
func TestTeamOverlayOpenMemberEntersChildView(t *testing.T) {
	chat := headerChat("团队会话")
	team := &fakeTeam{members: []contracts.TeamMember{
		{ID: "member-1", Name: "文档审查员", RunState: contracts.TeamRunIdle, Model: "gpt-5-codex"},
	}}
	children := &fakeChildAgents{agents: []contracts.ChildAgent{{
		ID: "member-1", Name: "文档审查员", Kind: contracts.ChildAgentTeammate,
		Mode: contracts.ChildAgentContinuable, Status: contracts.ChildAgentIdle,
		Prompt: "核对文档", Output: "文档无误",
	}}}
	h := newAgentSurfaceHarness(t, chat, agentServices{
		team: team, children: children, board: &fakeAgentBoard{},
	})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "智能体团队")
	h.click(tester, "打开成员 文档审查员")
	h.assertTexts(tester, "团队会话", "文档审查员", "核对文档", "文档无误", "任务", "输出")
	h.assertNoTexts(tester, "帮我发布")
}

// --- child switcher + child view -------------------------------------------

// childAgentsFixture is the switcher's source: one finished one-shot subagent
// and one continuable teammate with an activity entry.
func childAgentsFixture() *fakeChildAgents {
	return &fakeChildAgents{
		answer: "收到，继续检查文档",
		agents: []contracts.ChildAgent{
			{
				ID: "subagent-1", ParentSessionID: "sess-1", Name: "目录审计员",
				Kind: contracts.ChildAgentSubagent, Mode: contracts.ChildAgentOneShot,
				Status: contracts.ChildAgentCompleted, Model: "deepseek-flash",
				Prompt: "检查目录结构", Output: "目录已检查完", UsageTokens: 2100, Iterations: 3,
			},
			{
				ID: "member-1", ParentSessionID: "sess-1", Name: "文档审查员",
				Kind: contracts.ChildAgentTeammate, Mode: contracts.ChildAgentContinuable,
				Status: contracts.ChildAgentIdle, Model: "gpt-5-codex",
				Prompt: "核对文档", Output: "文档无误",
				Activity: []contracts.ChildAgentActivity{{
					ID: "e1", Kind: contracts.TeamActivitySend, Text: "收到任务",
					Time: time.Date(2026, 11, 10, 19, 2, 0, 0, time.Local),
				}},
			},
		},
	}
}

// TestChildSwitcherAndBreadcrumb covers the 子智能体 chip's switcher and the child
// conversation view: the title becomes the parent › child breadcrumb, the body
// switches, and the parent crumb returns to the conversation.
func TestChildSwitcherAndBreadcrumb(t *testing.T) {
	chat := headerChat("发布会话")
	children := childAgentsFixture()
	h := newAgentSurfaceHarness(t, chat, agentServices{children: children})
	tester := h.tester()
	tester.Frame()

	// The chip lists the child agents and enters the chosen one.
	h.click(tester, "子智能体 2")
	h.assertTexts(tester, "子智能体会话", "目录审计员", "文档审查员")
	if _, ok := tester.Find("进入子智能体会话 目录审计员"); !ok {
		t.Fatalf("child switcher rows have no open action; texts = %v", tester.Texts())
	}
	h.click(tester, "进入子智能体会话 目录审计员")

	// Child view: breadcrumb title, prompt/status/output/activity content, the
	// tabs row kept, and the parent transcript out of sight.
	h.assertTexts(tester, "发布会话", "目录审计员", "任务", "检查目录结构",
		"输出", "目录已检查完", "已完成", "只读", "对话", "轨迹", "记忆")
	h.assertNoTexts(tester, "帮我发布")

	// One-shot children have no message composer.
	if _, ok := tester.Find("发送"); ok {
		t.Fatalf("one-shot child view shows a send action; texts = %v", tester.Texts())
	}

	// Clicking the parent crumb returns to the parent conversation.
	h.click(tester, "发布会话")
	h.assertTexts(tester, "帮我发布")
	h.assertNoTexts(tester, "检查目录结构")
}

// TestChildViewContinuableSend covers the continuable teammate's message input:
// sending calls ChildAgentService.SendChildAgent and shows the answer.
func TestChildViewContinuableSend(t *testing.T) {
	chat := headerChat("发布会话")
	children := childAgentsFixture()
	h := newAgentSurfaceHarness(t, chat, agentServices{children: children})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "子智能体 2")
	h.click(tester, "进入子智能体会话 文档审查员")
	h.assertTexts(tester, "文档审查员", "任务", "核对文档", "输出", "文档无误", "活动", "收到任务")

	// Type into the child message input and send it.
	input, ok := tester.Find("发送给 文档审查员")
	if !ok {
		t.Fatalf("child message input missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(input.X+input.W/2, input.Y+input.H/2)
	tester.Frame()
	tester.Type("继续检查文档")
	tester.Frame()
	h.click(tester, "发送消息给 文档审查员")

	waitFor(t, tester, func() bool { return len(children.sent()) == 1 }, "SendChildAgent call")
	sent := children.sent()[0]
	if sent.id != "member-1" || sent.message != "继续检查文档" {
		t.Fatalf("SendChildAgent(%q, %q), want (member-1, 继续检查文档)", sent.id, sent.message)
	}
	waitFor(t, tester, func() bool { return tester.HasText("收到，继续检查文档") }, "child answer")
}

// TestChildViewShowsActivityTimeline covers the child activity timeline and the
// trajectory tab inside the child view.
func TestChildViewShowsActivityTimeline(t *testing.T) {
	chat := headerChat("发布会话")
	children := childAgentsFixture()
	h := newAgentSurfaceHarness(t, chat, agentServices{children: children})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "子智能体 2")
	h.click(tester, "进入子智能体会话 文档审查员")
	h.assertTexts(tester, "活动", "收到任务")
	h.click(tester, "轨迹")
	h.assertTexts(tester, "活动", "收到任务")
}

// TestChildViewResetsOnSessionSwitch covers the conversation switch: the child
// view belongs to the parent session on screen, so switching sessions returns
// to the parent conversation.
func TestChildViewResetsOnSessionSwitch(t *testing.T) {
	chat := headerChat("发布会话")
	chat.addSession(contracts.ChatSession{
		ID: "sess-2", Title: "另一个会话",
		Messages: []contracts.ChatMessage{{ID: "u2", Role: contracts.ChatRoleUser, Text: "另一个问题"}},
	}, false)
	h := newAgentSurfaceHarness(t, chat, agentServices{children: childAgentsFixture()})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "子智能体 2")
	h.click(tester, "进入子智能体会话 文档审查员")
	h.assertTexts(tester, "核对文档")

	if err := chat.SetActiveSession("sess-2"); err != nil {
		t.Fatalf("switch session: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "另一个会话", "另一个问题")
	h.assertNoTexts(tester, "核对文档")
}

// TestGeneratedFileCardsGroupedTurn covers the multi-step turn: the cards span
// the whole turn (earlier steps' writes included) and land after the grouped
// row's final answer.
func TestGeneratedFileCardsGroupedTurn(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "多步回合",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "生成文件"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "先写文件。",
				Tools: []contracts.ToolCall{{
					ID: "t1", Name: "write_file", Status: "ok",
					Arguments: `{"path":"docs/report.md"}`,
				}}},
			{ID: "a2", Role: contracts.ChatRoleAssistant, Text: "都完成了。"},
		},
	}, true)
	h := newAgentSurfaceHarness(t, chat, agentServices{preview: &fakeFilePreview{}})
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "交付文件", "report.md", "都完成了。")
	answer, ok := tester.Find("都完成了。")
	card, okCard := tester.Find("交付文件")
	if !ok || !okCard || card.Y <= answer.Y {
		t.Fatalf("交付文件 at y=%.1f is not below the grouped answer at y=%.1f", card.Y, answer.Y)
	}
}

// TestChildViewMissingAgentKeepsWayBack covers a child removed underneath the
// view: the breadcrumb still returns to the parent.
func TestChildViewMissingAgentKeepsWayBack(t *testing.T) {
	chat := headerChat("发布会话")
	children := childAgentsFixture()
	h := newAgentSurfaceHarness(t, chat, agentServices{children: children})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "子智能体 2")
	h.click(tester, "进入子智能体会话 文档审查员")

	children.mu.Lock()
	children.agents = nil
	children.mu.Unlock()
	tester.Frame()
	h.assertTexts(tester, "发布会话", "子智能体已不存在")
	h.click(tester, "发布会话")
	h.assertTexts(tester, "帮我发布")
}

// --- jobs overlay -----------------------------------------------------------

// TestJobsOverlayAndKill covers the background-jobs chip: the overlay rows show
// status/command/duration/exit code and stop a running job through KillJob.
func TestJobsOverlayAndKill(t *testing.T) {
	chat := headerChat("任务会话")
	jobs := &fakeJobs{jobs: []contracts.BackgroundJob{
		{
			ID: "job-1", Kind: "bash", Command: "npm test --watch",
			State: "running", Status: "running", Running: true,
			Duration: 3 * time.Second,
		},
		{
			ID: "job-2", Kind: "bash", Command: "go build ./...",
			State: "exited", Status: "completed", ExitCode: 0,
			Duration: 6200 * time.Millisecond,
		},
	}}
	h := newAgentSurfaceHarness(t, chat, agentServices{jobs: jobs})
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "2 个后台任务")
	h.click(tester, "2 个后台任务")
	h.assertTexts(tester, "后台任务", "npm test --watch", "go build ./...",
		"运行中", "完成", "用时 6.2s", "退出码 0", "退出码 —")

	h.click(tester, "停止后台任务 job-1")
	waitFor(t, tester, func() bool { return len(jobs.killed()) == 1 }, "KillJob call")
	if killed := jobs.killed(); killed[0] != "job-1" {
		t.Fatalf("KillJob(%q), want job-1", killed[0])
	}
}

// TestJobsOverlayShowsFailureExitCode covers the failed-job row: the exit code
// and the failure badge are truthful.
func TestJobsOverlayShowsFailureExitCode(t *testing.T) {
	chat := headerChat("任务会话")
	jobs := &fakeJobs{jobs: []contracts.BackgroundJob{{
		ID: "job-9", Command: "make release", State: "failed",
		Status: "failed", ExitCode: 2, Duration: time.Second,
	}}}
	h := newAgentSurfaceHarness(t, chat, agentServices{jobs: jobs})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "1 个后台任务")
	h.assertTexts(tester, "后台任务", "make release", "失败", "退出码 2")
}

// --- generated-file cards ---------------------------------------------------

// fileTurnChat is one finished turn whose tool calls wrote and edited files.
func fileTurnChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "文件回合",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "生成发布清单"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "检查完成，已生成发布清单。",
				Tools: []contracts.ToolCall{
					{ID: "t1", Name: "write_file", Status: "ok",
						Arguments: `{"path":"docs/report.md","content":"# report"}`},
					{ID: "t2", Name: "edit_file", Status: "ok",
						Arguments: `{"path":"README.md","old_string":"a","new_string":"b"}`},
				}},
		},
	}, true)
	return chat
}

// TestGeneratedFileCards covers the turn-tail cards after the final answer:
// 交付文件 (write_file) and 已编辑 N 个文件 (edit_file), with the per-file
// preview / diff / native-open actions wired to FilePreviewer.
func TestGeneratedFileCards(t *testing.T) {
	chat := fileTurnChat()
	preview := &fakeFilePreview{}
	h := newAgentSurfaceHarness(t, chat, agentServices{preview: preview})
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "交付文件", "report.md", "docs/",
		"已编辑 1 个文件", "README.md")

	// The cards sit after the final answer (below it).
	answer, ok := tester.Find("检查完成，已生成发布清单。")
	if !ok {
		t.Fatalf("final answer missing; texts = %v", tester.Texts())
	}
	card, ok := tester.Find("交付文件")
	if !ok || card.Y <= answer.Y {
		t.Fatalf("交付文件 at y=%.1f is not below the final answer at y=%.1f", card.Y, answer.Y)
	}

	// 在侧边栏预览 calls PreviewFile with the turn's path.
	h.click(tester, "在侧边栏预览 docs/report.md")
	// 查看改动 falls back to PreviewFile (no diff API on the contract).
	h.click(tester, "查看改动 README.md")
	// 用系统应用打开 calls OpenNative.
	h.click(tester, "用系统应用打开 docs/report.md")
	previews, natives := preview.opened()
	if len(previews) != 2 || previews[0] != "docs/report.md" || previews[1] != "README.md" {
		t.Fatalf("PreviewFile calls = %v, want [docs/report.md README.md]", previews)
	}
	if len(natives) != 1 || natives[0] != "docs/report.md" {
		t.Fatalf("OpenNative calls = %v, want [docs/report.md]", natives)
	}
}

// TestGeneratedFileCardsHiddenWhileRunning covers the tail condition: the cards
// appear after the final answer, not while the turn still streams.
func TestGeneratedFileCardsHiddenWhileRunning(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "运行中", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "生成文件"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "正在写…", Streaming: true,
				Tools: []contracts.ToolCall{
					{ID: "t1", Name: "write_file", Status: "running",
						Arguments: `{"path":"docs/report.md"}`},
				}},
		},
	}, true)
	h := newAgentSurfaceHarness(t, chat, agentServices{preview: &fakeFilePreview{}})
	tester := h.tester()
	tester.Frame()
	h.assertNoTexts(tester, "交付文件", "已编辑")
}

// TestGeneratedFileCardsWithoutPreviewerKeepRows covers the optional previewer:
// the rows stay (the file list is conversation data) but the sidebar actions
// are hidden instead of dead.
func TestGeneratedFileCardsWithoutPreviewerKeepRows(t *testing.T) {
	chat := fileTurnChat()
	h := newAgentSurfaceHarness(t, chat, agentServices{})
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "交付文件", "report.md")
	h.assertNoTexts(tester, "在侧边栏预览", "用系统应用打开")
}

// TestGeneratedFileCardsNoDuplicates covers dedupe: a file written and edited in
// the same turn lands in 交付文件 once.
func TestGeneratedFileCardsNoDuplicates(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "重复",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "生成"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "完成",
				Tools: []contracts.ToolCall{
					{ID: "t1", Name: "write_file", Status: "ok", Arguments: `{"path":"docs/a.md"}`},
					{ID: "t2", Name: "write_file", Status: "ok", Arguments: `{"path":"docs/a.md"}`},
					{ID: "t3", Name: "edit_file", Status: "ok", Arguments: `{"path":"docs/a.md"}`},
				}},
		},
	}, true)
	h := newAgentSurfaceHarness(t, chat, agentServices{preview: &fakeFilePreview{}})
	tester := h.tester()
	tester.Frame()
	count := 0
	for _, text := range tester.Texts() {
		if text == "a.md" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("a.md rendered %d times, want once; texts = %v", count, tester.Texts())
	}
	if _, ok := tester.Find("已编辑"); ok {
		t.Fatalf("a.md was not double-counted but the edited card shows: %v", tester.Texts())
	}
}

// compile-time check that the fixtures satisfy the shared contracts.
var (
	_ contracts.ChildAgentService = (*fakeChildAgents)(nil)
	_ contracts.JobService        = (*fakeJobs)(nil)
	_ contracts.FilePreviewer     = (*fakeFilePreview)(nil)
	_ contracts.TaskBoard         = (*fakeAgentBoard)(nil)
)

// TestRunBandShowsPendingApproval covers the band's approval counter: pending
// requests add one warning badge next to the orchestration entries, while the
// cards themselves stay above the composer.
func TestRunBandShowsPendingApproval(t *testing.T) {
	approvals := &fakeApprovals{pending: []contracts.ApprovalRequest{{
		ID: "ap-1", Tool: "bash", Summary: "rm -rf build",
	}}}
	h := newAgentSurfaceHarness(t, headerChat("待审批会话"), agentServices{
		team: &fakeTeam{}, children: &fakeChildAgents{}, approvals: approvals,
	})
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "智能体团队", "子智能体 0", "待审批 1")
}

// TestRunningToolRowRendersInProductSession covers the running tool row in the
// real session surface (the v0.2.20 residual was that only a component specimen
// showed the running gutter): the transcript draws the running call with its
// status, so the state gutter has a product frame too.
func TestRunningToolRowRendersInProductSession(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "运行工具",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "跑一下构建检查"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "正在检查", Streaming: true,
				Tools: []contracts.ToolCall{{
					ID: "tool-1", Name: "bash", Status: "running",
					Summary: "go test ./... -count=1",
				}}},
		},
	}, true)
	h := newAgentSurfaceHarness(t, chat, agentServices{})
	tester := h.tester()
	tester.Frame()
	status, summary := false, false
	for _, text := range tester.Texts() {
		if strings.Contains(text, "正在运行命令") {
			status = true
		}
		if strings.Contains(text, "go test") {
			summary = true
		}
	}
	if !status || !summary {
		t.Fatalf("running tool row missing (status=%v summary=%v); texts = %v", status, summary, tester.Texts())
	}
}

// --- durable child-agent history -------------------------------------------

// TestTeamOverlayGroupsRestoredHistory covers the durable-history UX: the
// active session's restored rows appear under 已结束 with a 历史 marker, while
// another session's history stays out of both the counts and the overlay.
func TestTeamOverlayGroupsRestoredHistory(t *testing.T) {
	chat := headerChat("历史团队")
	team := &fakeTeam{members: []contracts.TeamMember{
		{ID: "member-1", Name: "在线审查员", RunState: contracts.TeamRunRunning, SessionID: "sess-1"},
		{
			ID: "history-m-1", Name: "上次的研究员", RunState: contracts.TeamRunDone,
			SessionID: "sess-1", Historical: true, Last: "上一轮的答案",
		},
		{
			ID: "history-m-2", Name: "别的会话队友", RunState: contracts.TeamRunDone,
			SessionID: "sess-2", Historical: true,
		},
	}}
	h := newAgentSurfaceHarness(t, chat, agentServices{
		team: team,
		children: &fakeChildAgents{agents: []contracts.ChildAgent{
			{ID: "member-1", Name: "在线审查员", Kind: contracts.ChildAgentTeammate},
			{
				ID: "history-m-1", Name: "上次的研究员", Kind: contracts.ChildAgentTeammate,
				Mode: contracts.ChildAgentContinuable, Status: contracts.ChildAgentCompleted,
				Historical: true, Output: "上一轮的答案",
			},
		}},
		board: &fakeAgentBoard{},
	})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "智能体团队")
	h.assertTexts(tester,
		"2 名成员 · 1 个运行中 · 1 条已结束",
		"成员", "在线审查员", "已结束", "上次的研究员", "历史", "上一轮的答案",
	)
	h.assertNoTexts(tester, "别的会话队友")
}

// TestHistoricalChildViewIsReadOnly covers a restored child conversation: it
// opens with its prompt/answer but has no composer and says it is read-only.
func TestHistoricalChildViewIsReadOnly(t *testing.T) {
	children := &fakeChildAgents{agents: []contracts.ChildAgent{{
		ID: "history-m-1", ParentSessionID: "sess-1", Name: "上次的研究员",
		Kind: contracts.ChildAgentTeammate, Mode: contracts.ChildAgentContinuable,
		Status: contracts.ChildAgentCompleted, Historical: true,
		Prompt: "研究发布说明", Output: "上次的答案",
	}}}
	h := newAgentSurfaceHarness(t, headerChat("历史会话"), agentServices{children: children})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "子智能体 1")
	h.assertTexts(tester, "子智能体会话", "上次的研究员", "历史")
	h.click(tester, "进入子智能体会话 上次的研究员")
	h.assertTexts(tester,
		"上次的研究员", "队友 · 已结束记录", "研究发布说明", "上次的答案",
		"已结束的记录（应用重启后仍保留）：只读，不能再继续对话。")
	h.assertNoTexts(tester, "输入消息…", "发送后等待该队友的回复。")
}

// TestChildSwitcherMarksRestoredHistory covers the switcher row of a restored
// one-shot subagent: the history marker and the finished-record meta line, plus
// the same 进入 action a live row has.
func TestChildSwitcherMarksRestoredHistory(t *testing.T) {
	children := &fakeChildAgents{agents: []contracts.ChildAgent{{
		ID: "history-s-1", ParentSessionID: "sess-1", Name: "上次的子代理",
		Kind: contracts.ChildAgentSubagent, Mode: contracts.ChildAgentOneShot,
		Status: contracts.ChildAgentCompleted, Historical: true, Output: "上次的输出",
	}}}
	h := newAgentSurfaceHarness(t, headerChat("历史会话"), agentServices{children: children})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "子智能体 1")
	h.assertTexts(tester, "子智能体会话", "上次的子代理", "历史", "子智能体 · 已结束记录")
	if _, ok := tester.Find("进入子智能体会话 上次的子代理"); !ok {
		t.Fatalf("restored child has no 进入 action; texts = %v", tester.Texts())
	}
}
