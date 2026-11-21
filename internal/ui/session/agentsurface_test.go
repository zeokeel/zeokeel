package session_test

// Frame tests for the DSH-aligned agent surfaces of the session shell: the
// header's team entry / subagent chip / background-jobs chip (and their
// overlays), the child-agent conversation view with its breadcrumb, and the
// generated-file turn-tail cards. Fakes implement the shared contracts from
// internal/contracts/agentsurface.go so no backend module is needed.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uikit"
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

	// JobOutput scripting: outputs/outputErrs by id, or an outputFn that may
	// block (the off-thread test) or change per read (the live-refresh test);
	// outputCalls records every read.
	outputs     map[string]string
	outputErrs  map[string]error
	outputFn    func(id string) (string, error)
	outputCalls []string
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

func (f *fakeJobs) JobOutput(id string) (string, error) {
	f.mu.Lock()
	f.outputCalls = append(f.outputCalls, id)
	fn := f.outputFn
	text, scripted := f.outputs[id]
	err := f.outputErrs[id]
	f.mu.Unlock()
	if fn != nil {
		return fn(id)
	}
	if err != nil {
		return "", err
	}
	if !scripted {
		return "", nil
	}
	return text, nil
}

func (f *fakeJobs) killed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.kills...)
}

func (f *fakeJobs) outputReads() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.outputCalls...)
}

// setOutput changes the scripted output between reads (the live-refresh test).
func (f *fakeJobs) setOutput(id, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.outputs == nil {
		f.outputs = map[string]string{}
	}
	f.outputs[id] = text
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
	execution contracts.ExecutionHistoryService
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
	if services.execution != nil {
		provide[contracts.ExecutionHistoryService](t, ctx, contracts.KeyExecutionHistory, services.execution)
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
	// A narrow band keeps the words (VD-21 / AGENTS §3.17: an entry that loses its
	// name stops saying which feature it opens); the entry shrinks its padding and
	// ellipsizes, so the label stays a label rather than an icon alone.
	for _, entry := range []string{"智能体团队", "子智能体 1", "1 个后台任务"} {
		rect, ok := tester.Find(entry)
		if !ok {
			t.Fatalf("narrow header lost the %q entry; texts = %v", entry, tester.Texts())
		}
		// The entry is icon + words + count: about 100 DIP for a five-character
		// name at 12 DIP, and bounded so three of them plus the title still fit.
		if rect.W > 130 {
			t.Fatalf("narrow header entry %q is %.1f wide, want a bounded entry (<= 130)", entry, rect.W)
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

	// Type into the child message input (the shell's text area, named by its
	// placeholder) and send it with the shell's send control.
	input, ok := tester.Find("发送给 文档审查员")
	if !ok {
		t.Fatalf("child message input missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(input.X+input.W/2, input.Y+input.H/2)
	tester.Frame()
	tester.Type("继续检查文档")
	tester.Frame()
	clickRoleButton(t, tester, "发送")

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
			State: "running", Running: true,
			Duration: 3 * time.Second,
		},
		{
			ID: "job-2", Kind: "bash", Command: "go build ./...",
			State: "exited", ExitCode: 0,
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

	// Stopping is two-stage: the first press arms 确认停止, the second stops.
	h.click(tester, "停止后台任务 job-1")
	h.assertTexts(tester, "确认停止")
	if killed := jobs.killed(); len(killed) != 0 {
		t.Fatalf("the first stop press already killed the job: %v", killed)
	}
	h.click(tester, "确认停止后台任务 job-1")
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
		ExitCode: 2, Duration: time.Second,
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
	// 名成员 counts the unfinished roster (the running 在线审查员); the restored
	// 上次的研究员 is counted in 已结束, so the meta matches the sections (P2).
	h.assertTexts(tester,
		"1 名成员 · 1 个运行中 · 1 条已结束",
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
		"上次的研究员", "队友", "已结束 · 只读", "研究发布说明", "上次的答案",
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
	h.assertTexts(tester, "子智能体会话", "上次的子代理", "历史", "子智能体", "已结束 · 只读")
	if _, ok := tester.Find("进入子智能体会话 上次的子代理"); !ok {
		t.Fatalf("restored child has no 进入 action; texts = %v", tester.Texts())
	}
}

// --- per-session task board -----------------------------------------------

// fakeSessionBoard is a contracts.SessionTaskBoard with isolated task lists per
// session (the session UI must read only the active conversation's board).
type fakeSessionBoard struct {
	bySession map[string][]contracts.BoardTask
}

func (f *fakeSessionBoard) List(context.Context) ([]contracts.BoardTask, error) {
	var all []contracts.BoardTask
	for _, tasks := range f.bySession {
		all = append(all, tasks...)
	}
	return all, nil
}

func (f *fakeSessionBoard) Create(context.Context, string, string) (contracts.BoardTask, error) {
	return contracts.BoardTask{}, nil
}

func (f *fakeSessionBoard) SetStatus(context.Context, string, string) (contracts.BoardTask, error) {
	return contracts.BoardTask{}, nil
}

func (f *fakeSessionBoard) Delete(context.Context, string) error { return nil }

func (f *fakeSessionBoard) TaskBoardForSession(sessionID string) contracts.TaskBoard {
	return &fakeBoardView{tasks: f.bySession[sessionID]}
}

// fakeBoardView is one session's read-only slice of the fake board.
type fakeBoardView struct{ tasks []contracts.BoardTask }

func (f *fakeBoardView) List(context.Context) ([]contracts.BoardTask, error) {
	return append([]contracts.BoardTask(nil), f.tasks...), nil
}

func (f *fakeBoardView) Create(context.Context, string, string) (contracts.BoardTask, error) {
	return contracts.BoardTask{}, nil
}

func (f *fakeBoardView) SetStatus(context.Context, string, string) (contracts.BoardTask, error) {
	return contracts.BoardTask{}, nil
}

func (f *fakeBoardView) Delete(context.Context, string) error { return nil }

// TestTeamOverlayShowsOnlyActiveSessionTasks pins the DSH Lead-session board
// semantics on the UI side: the overlay lists the active conversation's tasks and
// never another session's tasks.
func TestTeamOverlayShowsOnlyActiveSessionTasks(t *testing.T) {
	chat := headerChat("会话 A")
	services := agentServices{
		team: &fakeTeam{members: []contracts.TeamMember{{
			ID: "child-a", Name: "研究员", RunState: contracts.TeamRunIdle, SessionID: "sess-1",
		}}},
		board: &fakeSessionBoard{bySession: map[string][]contracts.BoardTask{
			"sess-1": {{ID: "task-1", Subject: "只属于会话 A 的任务", Status: contracts.TaskStatusPending}},
			"sess-2": {{ID: "task-2", Subject: "别的会话的任务", Status: contracts.TaskStatusPending}},
		}},
	}
	h := newAgentSurfaceHarness(t, chat, services)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "智能体团队")
	h.assertTexts(tester, "共享任务", "只属于会话 A 的任务")
	h.assertNoTexts(tester, "别的会话的任务")
}

// --- completion receipts in the transcript ---------------------------------

// TestTranscriptShowsChildCompletionNotice covers the user-visible half of the
// DSH subagent-settled semantics: a finished delegated child renders one quiet
// receipt row in its parent conversation, and another session's child never
// leaks into this transcript.
func TestTranscriptShowsChildCompletionNotice(t *testing.T) {
	finishedAt := time.Now()
	children := &fakeChildAgents{agents: []contracts.ChildAgent{
		{
			ID: "child-a", ParentSessionID: "sess-1", Name: "研究员",
			Kind: contracts.ChildAgentTeammate, Status: contracts.ChildAgentCompleted,
			Output: "报告已完成", Finished: finishedAt,
		},
		{
			ID: "child-b", ParentSessionID: "sess-2", Name: "别的会话成员",
			Kind: contracts.ChildAgentTeammate, Status: contracts.ChildAgentCompleted,
			Output: "别的会话", Finished: finishedAt,
		},
	}}
	h := newAgentSurfaceHarness(t, headerChat("会话"), agentServices{children: children})
	tester := h.tester()
	tester.Frame()
	found := false
	for _, text := range tester.Texts() {
		if strings.Contains(text, "研究员") && strings.Contains(text, "已完成") {
			found = true
		}
	}
	if !found {
		t.Fatalf("completion receipt missing from the transcript; texts = %v", tester.Texts())
	}
	h.assertNoTexts(tester, "别的会话成员")
}

// TestTranscriptShowsFailedChildNoticeAsError pins the status semantics of a
// failed child receipt: the row keeps error text (DESIGN §5.2), and a record
// without error detail does not render a dangling colon.
func TestTranscriptShowsFailedChildNoticeAsError(t *testing.T) {
	finishedAt := time.Now()
	children := &fakeChildAgents{agents: []contracts.ChildAgent{
		{
			ID: "child-fail", ParentSessionID: "sess-1", Name: "失败者",
			Kind: contracts.ChildAgentTeammate, Status: contracts.ChildAgentFailed,
			Error: "API 429", Finished: finishedAt,
		},
		{
			ID: "child-fail-empty", ParentSessionID: "sess-1", Name: "无详情者",
			Kind: contracts.ChildAgentTeammate, Status: contracts.ChildAgentFailed,
			Finished: finishedAt,
		},
	}}
	h := newAgentSurfaceHarness(t, headerChat("会话"), agentServices{children: children})
	tester := h.tester()
	tester.Frame()
	found := false
	for _, text := range tester.Texts() {
		if strings.Contains(text, "失败者") && strings.Contains(text, "运行失败：API 429") {
			found = true
		}
	}
	if !found {
		t.Fatalf("failed receipt text missing; texts = %v", tester.Texts())
	}
	h.assertTexts(tester, "无详情者", "运行失败")
	h.assertNoTexts(tester, "无详情者」运行失败：")
}

// TestTeamOverlayWithoutSessionShowsEmptyTasks pins the first-run state: with no
// active conversation the overlay must show the empty task state, never a
// permanent "reading the board" row (and never the legacy global board).
func TestTeamOverlayWithoutSessionShowsEmptyTasks(t *testing.T) {
	chat := newFakeChat()
	h := newAgentSurfaceHarness(t, chat, agentServices{
		team: &fakeTeam{},
		// Legacy-only board (no SessionTaskBoard): the empty-session overlay must
		// not read it.
		board: &fakeAgentBoard{tasks: []contracts.BoardTask{
			{ID: "task-1", Subject: "旧全局任务", Status: contracts.TaskStatusPending},
		}},
	})
	tester := h.tester()
	tester.Frame()
	if err := tester.Click("智能体团队"); err != nil {
		t.Fatalf("open team overlay: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "共享任务", "还没有共享任务")
	h.assertNoTexts(tester, "读取任务板…", "旧全局任务")
}

// --- team controls (P1-2): stop / close -------------------------------------

// recordingTeam is a contracts.TeamService that records the control calls the
// session header makes (Interrupt/Close) and can update its member state the way
// the backend does after an interrupt.
type recordingTeam struct {
	mu             sync.Mutex
	members        []contracts.TeamMember
	interrupts     []string
	closes         []string
	stopErr        error
	closeErr       error
	afterInterrupt string
}

func (f *recordingTeam) Members() []contracts.TeamMember {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.TeamMember(nil), f.members...)
}

func (f *recordingTeam) Spawn(context.Context, string, string, string) (contracts.TeamMember, error) {
	return contracts.TeamMember{}, nil
}

func (f *recordingTeam) SendAsync(string, string) error { return nil }

func (f *recordingTeam) Send(context.Context, string, string) (string, error) { return "", nil }

func (f *recordingTeam) Wait(context.Context, string) (string, error) { return "", nil }

func (f *recordingTeam) Interrupt(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopErr != nil {
		return f.stopErr
	}
	f.interrupts = append(f.interrupts, id)
	if state := f.afterInterrupt; state != "" {
		for index := range f.members {
			if f.members[index].ID == id {
				f.members[index].RunState = state
			}
		}
	}
	return nil
}

func (f *recordingTeam) Close(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closeErr != nil {
		return f.closeErr
	}
	f.closes = append(f.closes, id)
	return nil
}

func (f *recordingTeam) interrupted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.interrupts...)
}

func (f *recordingTeam) closed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.closes...)
}

// TestTeamMemberRunningShowsStopAndInterrupts covers the running teammate's
// stop action: it calls TeamService.Interrupt off the UI thread, and the row
// then shows the neutral interrupted state (never a failure).
func TestTeamMemberRunningShowsStopAndInterrupts(t *testing.T) {
	chat := headerChat("团队会话")
	team := &recordingTeam{
		members: []contracts.TeamMember{
			{ID: "member-1", Name: "文档审查员", RunState: contracts.TeamRunRunning, Model: "gpt-5-codex"},
		},
		afterInterrupt: contracts.TeamRunInterrupted,
	}
	h := newAgentSurfaceHarness(t, chat, agentServices{team: team, children: &fakeChildAgents{}})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "智能体团队")
	if _, ok := tester.AccessByRole(ui.RoleButton, "停止成员 文档审查员"); !ok {
		t.Fatalf("running member has no stop action; texts = %v", tester.Texts())
	}
	h.click(tester, "停止成员 文档审查员")
	waitFor(t, tester, func() bool { return len(team.interrupted()) == 1 }, "TeamService.Interrupt call")
	if got := team.interrupted(); got[0] != "member-1" {
		t.Fatalf("Interrupt(%q), want member-1", got[0])
	}
	waitFor(t, tester, func() bool { return tester.HasText("已中断") }, "interrupted member status")
	if _, ok := tester.AccessByRole(ui.RoleButton, "停止成员 文档审查员"); ok {
		t.Fatalf("interrupted member still offers stop; texts = %v", tester.Texts())
	}
	h.assertNoTexts(tester, "失败")
	// The interrupted run is neutral, not a failure: the shared run-state
	// mapping (pinned by uikit's status tests) resolves to LabelTertiary.
	tokens := design.Light()
	if kind := uikit.ParseRunState(contracts.TeamRunInterrupted).Kind(); kind != uikit.StatusInterrupted {
		t.Fatalf("interrupted run kind = %v, want StatusInterrupted", kind)
	} else if color := uikit.StatusColor(tokens, kind); color != tokens.LabelTertiary {
		t.Fatalf("interrupted status colour = %v, want LabelTertiary", color)
	}
}

// TestTeamMemberIdleHasNoStop covers the resting state: only a running teammate
// offers stop.
func TestTeamMemberIdleHasNoStop(t *testing.T) {
	chat := headerChat("团队会话")
	team := &recordingTeam{members: []contracts.TeamMember{
		{ID: "member-1", Name: "文档审查员", RunState: contracts.TeamRunIdle},
	}}
	h := newAgentSurfaceHarness(t, chat, agentServices{team: team})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "智能体团队")
	h.assertTexts(tester, "文档审查员", "空闲")
	if _, ok := tester.AccessByRole(ui.RoleButton, "停止成员 文档审查员"); ok {
		t.Fatalf("idle member offers stop; texts = %v", tester.Texts())
	}
}

// TestTeamMemberCloseConfirmsWithCancelDefault covers the member row's ⋯ danger
// action: close asks first through uikit.ConfirmDanger, whose safe action
// carries the default focus and Enter. Only the explicit confirm calls
// TeamService.Close.
func TestTeamMemberCloseConfirmsWithCancelDefault(t *testing.T) {
	chat := headerChat("团队会话")
	team := &recordingTeam{members: []contracts.TeamMember{
		{ID: "member-1", Name: "文档审查员", RunState: contracts.TeamRunIdle},
	}}
	h := newAgentSurfaceHarness(t, chat, agentServices{team: team})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "智能体团队")
	h.click(tester, "成员操作 文档审查员")
	h.click(tester, "关闭成员")
	h.assertTexts(tester, "关闭成员", "取消", "确认关闭", "无法撤销")
	if !tester.Focused("取消") {
		t.Fatalf("the safe action does not carry the focus; texts = %v", tester.Texts())
	}
	// Enter answers the default (cancel): the teammate is not closed.
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if got := team.closed(); len(got) != 0 {
		t.Fatalf("Enter closed the teammate: %v", got)
	}
	h.assertNoTexts(tester, "确认关闭")

	// Reopen the overlay and answer the confirmation explicitly.
	h.click(tester, "智能体团队")
	h.click(tester, "成员操作 文档审查员")
	h.click(tester, "关闭成员")
	h.click(tester, "确认关闭")
	waitFor(t, tester, func() bool { return len(team.closed()) == 1 }, "TeamService.Close call")
	if got := team.closed(); got[0] != "member-1" {
		t.Fatalf("Close(%q), want member-1", got[0])
	}
	h.assertNoTexts(tester, "确认关闭")
}

// TestTeamMemberStopFailureIsLocalized covers the failed stop: it stays inside
// the overlay as an actionable error line instead of a silent no-op.
func TestTeamMemberStopFailureIsLocalized(t *testing.T) {
	chat := headerChat("团队会话")
	team := &recordingTeam{
		members: []contracts.TeamMember{
			{ID: "member-1", Name: "文档审查员", RunState: contracts.TeamRunRunning},
		},
		stopErr: errors.New("manager unavailable"),
	}
	h := newAgentSurfaceHarness(t, chat, agentServices{team: team})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "智能体团队")
	h.click(tester, "停止成员 文档审查员")
	waitFor(t, tester, func() bool { return tester.HasText("停止失败") }, "localized stop failure")
	h.assertTexts(tester, "manager unavailable")
}

// --- child header stop (P1-2) ----------------------------------------------

// TestChildHeaderShowsStopForRunningTeammate covers the child conversation's
// header: a running teammate offers the same stop action there, so the user
// does not have to return to the team overlay to interrupt it.
func TestChildHeaderShowsStopForRunningTeammate(t *testing.T) {
	chat := headerChat("发布会话")
	team := &recordingTeam{
		members: []contracts.TeamMember{
			{ID: "member-1", Name: "文档审查员", RunState: contracts.TeamRunRunning},
		},
		afterInterrupt: contracts.TeamRunInterrupted,
	}
	children := &fakeChildAgents{agents: []contracts.ChildAgent{{
		ID: "member-1", Name: "文档审查员", Kind: contracts.ChildAgentTeammate,
		Mode: contracts.ChildAgentContinuable, Status: contracts.ChildAgentRunning,
		Prompt: "核对文档",
	}}}
	h := newAgentSurfaceHarness(t, chat, agentServices{team: team, children: children})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "子智能体 1")
	h.click(tester, "进入子智能体会话 文档审查员")
	h.assertTexts(tester, "文档审查员", "运行中")
	if _, ok := tester.AccessByRole(ui.RoleButton, "停止成员 文档审查员"); !ok {
		t.Fatalf("running child header has no stop action; texts = %v", tester.Texts())
	}
	h.click(tester, "停止成员 文档审查员")
	waitFor(t, tester, func() bool { return len(team.interrupted()) == 1 }, "child header Interrupt call")
	if got := team.interrupted(); got[0] != "member-1" {
		t.Fatalf("Interrupt(%q), want member-1", got[0])
	}
	children.mu.Lock()
	children.agents[0].Status = contracts.ChildAgentInterrupted
	children.mu.Unlock()
	waitFor(t, tester, func() bool { return tester.HasText("已中断") }, "child interrupted status")
	h.assertNoTexts(tester, "失败")
}

// TestChildHeaderNoStopForOneShotSubagent covers the read-only child: a one-shot
// subagent has no interrupt API, so the header offers none.
func TestChildHeaderNoStopForOneShotSubagent(t *testing.T) {
	chat := headerChat("发布会话")
	children := &fakeChildAgents{agents: []contracts.ChildAgent{{
		ID: "subagent-1", Name: "目录审计员", Kind: contracts.ChildAgentSubagent,
		Mode: contracts.ChildAgentOneShot, Status: contracts.ChildAgentRunning,
		Prompt: "检查目录",
	}}}
	h := newAgentSurfaceHarness(t, chat, agentServices{children: children})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "子智能体 1")
	h.click(tester, "进入子智能体会话 目录审计员")
	if _, ok := tester.AccessByRole(ui.RoleButton, "停止成员 目录审计员"); ok {
		t.Fatalf("one-shot subagent header offers stop; texts = %v", tester.Texts())
	}
}

// --- execution history (P1-3) ----------------------------------------------

// fakeExecutionHistory is a contracts.ExecutionHistoryService over a scripted
// per-session record set. gate, when set, holds the read until closed so a test
// can prove the frame never waits for the store.
type fakeExecutionHistory struct {
	mu       sync.Mutex
	records  map[string][]contracts.ExecutionRecord
	requests []string
	gate     <-chan struct{}
}

func (f *fakeExecutionHistory) ExecutionHistory(sessionID string, _ int) []contracts.ExecutionRecord {
	f.mu.Lock()
	f.requests = append(f.requests, sessionID)
	gate := f.gate
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-time.After(500 * time.Millisecond):
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.ExecutionRecord(nil), f.records[sessionID]...)
}

func (f *fakeExecutionHistory) requested() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// historyFixture is the durable record set of sess-1: one finished job and one
// interrupted terminal.
func historyFixture(at time.Time) map[string][]contracts.ExecutionRecord {
	return map[string][]contracts.ExecutionRecord{
		"sess-1": {
			{
				ID: "job-7", SessionID: "sess-1", Kind: contracts.ExecutionKindJob,
				Command: "go test ./...", CWD: "/tmp/ws", Root: "/tmp/ws",
				State: contracts.ExecutionStateFinished, Outcome: "exited",
				Started: at, Ended: at.Add(2 * time.Second),
			},
			{
				ID: "term-3", SessionID: "sess-1", Kind: contracts.ExecutionKindTerminal,
				CWD: "/tmp/ws", Root: "/tmp/ws", Transport: "pipe",
				State: contracts.ExecutionStateInterrupted, Started: at.Add(time.Minute),
			},
		},
	}
}

// TestJobsOverlayShowsDurableExecutionHistory covers the restart case: with no
// live jobs the header entry appears for the persisted records, and the overlay
// shows the history group with the honest states and the no-output note.
func TestJobsOverlayShowsDurableExecutionHistory(t *testing.T) {
	chat := headerChat("任务会话")
	at := time.Date(2026, 11, 15, 9, 0, 0, 0, time.Local)
	history := &fakeExecutionHistory{records: historyFixture(at)}
	h := newAgentSurfaceHarness(t, chat, agentServices{execution: history})
	tester := h.tester()
	tester.Frame()

	waitFor(t, tester, func() bool { return tester.HasText("后台任务") }, "history-only jobs entry")
	if got := history.requested(); len(got) == 0 || got[0] != "sess-1" {
		t.Fatalf("history requested sessions = %v, want sess-1", got)
	}
	h.click(tester, "后台任务")
	tester.Frame()
	h.assertTexts(tester, "历史", "go test ./...", "已完成", "已中断", "输出不会持久化")
	h.assertNoTexts(tester, "失败")
}

// TestJobsOverlayHistoryFiltersLiveJobs covers the de-duplication rule: a live
// job is already listed above, so the history group never repeats it.
func TestJobsOverlayHistoryFiltersLiveJobs(t *testing.T) {
	chat := headerChat("任务会话")
	at := time.Date(2026, 11, 15, 9, 0, 0, 0, time.Local)
	jobs := &fakeJobs{jobs: []contracts.BackgroundJob{{
		ID: "job-1", Kind: "bash", Command: "npm test --watch",
		State: "running", Running: true,
	}}}
	records := historyFixture(at)
	records["sess-1"] = append(records["sess-1"], contracts.ExecutionRecord{
		ID: "job-1", SessionID: "sess-1", Kind: contracts.ExecutionKindJob,
		Command: "npm test --watch", State: contracts.ExecutionStateRunning, Started: at,
	})
	history := &fakeExecutionHistory{records: records}
	h := newAgentSurfaceHarness(t, chat, agentServices{jobs: jobs, execution: history})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "1 个后台任务")
	tester.Frame()

	// The live job keeps its live row (command text) and the history group does
	// not repeat it: its history row would be labelled 后台任务 job-1.
	h.assertTexts(tester, "npm test --watch", "历史", "go test ./...", "已中断")
	h.assertNoExactTexts(tester, "后台任务 job-1")
}

// TestJobsOverlayHistoryWithoutServiceKeepsLiveOnly covers a host without the
// read model: no empty history section appears.
func TestJobsOverlayHistoryWithoutServiceKeepsLiveOnly(t *testing.T) {
	chat := headerChat("任务会话")
	jobs := &fakeJobs{jobs: []contracts.BackgroundJob{{
		ID: "job-1", Command: "go build ./...", State: "exited",
	}}}
	h := newAgentSurfaceHarness(t, chat, agentServices{jobs: jobs})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "1 个后台任务")
	h.assertTexts(tester, "后台任务", "go build ./...", "完成")
	h.assertNoTexts(tester, "历史", "输出不会持久化")
}

// TestExecutionHistoryLoadIsOffTheUiThreadAndScopedToSession proves the read
// happens outside the frame: while the service is blocked the screen still
// renders, and the scoped read only ever asks for the active conversation.
func TestExecutionHistoryLoadIsOffTheUiThreadAndScopedToSession(t *testing.T) {
	chat := headerChat("任务会话")
	chat.addSession(contracts.ChatSession{
		ID: "sess-2", Title: "另一个会话",
		Messages: []contracts.ChatMessage{{ID: "u2", Role: contracts.ChatRoleUser, Text: "另一个问题"}},
	}, false)
	at := time.Date(2026, 11, 15, 9, 0, 0, 0, time.Local)
	gate := make(chan struct{})
	history := &fakeExecutionHistory{records: historyFixture(at), gate: gate}
	h := newAgentSurfaceHarness(t, chat, agentServices{execution: history})
	tester := h.tester()
	tester.Frame()

	waitFor(t, tester, func() bool { return len(history.requested()) == 1 }, "history read request")
	// The blocked read did not block the frame: the screen still renders.
	h.assertTexts(tester, "任务会话")
	h.assertNoTexts(tester, "后台任务")
	close(gate)
	waitFor(t, tester, func() bool { return tester.HasText("后台任务") }, "history entry after the read")

	// A session switch reads its own records only; sess-2 has none, so the
	// entry disappears instead of showing sess-1's history.
	if err := chat.SetActiveSession("sess-2"); err != nil {
		t.Fatalf("switch to sess-2: %v", err)
	}
	waitFor(t, tester, func() bool {
		requested := history.requested()
		return len(requested) >= 2 && requested[len(requested)-1] == "sess-2"
	}, "history read for sess-2")
	tester.Frame()
	h.assertNoTexts(tester, "后台任务", "go test ./...")
}

// clickRoleButton clicks the centre of the button with this accessible name. The
// child view also lists activity entries whose kind word is 发送, so the button is
// resolved by role and not by the first label that matches the text.
func clickRoleButton(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	info, ok := tester.AccessByRole(ui.RoleButton, label)
	if !ok {
		t.Fatalf("no button %q; texts = %v", label, tester.Texts())
	}
	tester.ClickAt(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
	tester.Frame()
}

// TestGeneratedFileCardsFromApplyPatch pins the multi-file card: one
// apply_patch call fills the delivered row (added file) and the edited row
// (updated/deleted files), each with its own action row.
func TestGeneratedFileCardsFromApplyPatch(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-patch", Title: "补丁回合",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "打补丁"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "补丁已应用。",
				Tools: []contracts.ToolCall{{
					ID: "t1", Name: "apply_patch", Status: "ok",
					Arguments: `{"patch":"*** Begin Patch\n*** Add File: docs/new.md\n+x\n*** Update File: README.md\n@@\n-a\n+b\n*** Delete File: old.md\n*** End Patch\n"}`,
				}}},
		},
	}, true)
	h := newAgentSurfaceHarness(t, chat, agentServices{preview: &fakeFilePreview{}})
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "交付文件", "new.md", "docs/",
		"已编辑 2 个文件", "README.md", "old.md")
}

// TestGeneratedFileCardsUseRecordedPatchFiles pins the structured path: the
// transcript's arguments are bounded (the patch body no longer parses), so the
// card must read the file list the runtime recorded with the call. Deleted
// files are labelled and offer no preview action.
func TestGeneratedFileCardsUseRecordedPatchFiles(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-patch-recorded", Title: "补丁回合",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "打补丁"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "补丁已应用。",
				Tools: []contracts.ToolCall{{
					ID: "t1", Name: "apply_patch", Status: "ok",
					Arguments: `{"patch":"*** Begin Patch\n*** Update File: README.md\n@@\n-a\n…`,
					Generated: []contracts.ToolCallFile{
						{Path: "docs/a.md", Added: true},
						{Path: "README.md"},
						{Path: "old.md", Deleted: true},
					},
				}}},
		},
	}, true)
	h := newAgentSurfaceHarness(t, chat, agentServices{preview: &fakeFilePreview{}})
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "交付文件", "a.md", "docs/", "已编辑 2 个文件", "README.md", "old.md", "· 已删除")
	if tester.HasText("在侧边栏预览 old.md") {
		t.Fatalf("deleted file still offers preview; texts = %v", tester.Texts())
	}
	if !tester.HasText("在侧边栏预览 README.md") {
		t.Fatalf("edited file lost its preview action; texts = %v", tester.Texts())
	}
}

// TestGeneratedFileCardsWriteSupersedesEdit pins the ordering fix: a file edited
// and then written in the same turn is delivered, not edited.
func TestGeneratedFileCardsWriteSupersedesEdit(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-order", Title: "顺序回合",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "改再写"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "完成。",
				Tools: []contracts.ToolCall{
					{ID: "t1", Name: "edit_file", Status: "ok",
						Arguments: `{"path":"docs/a.md","old_string":"a","new_string":"b"}`},
					{ID: "t2", Name: "write_file", Status: "ok",
						Arguments: `{"path":"docs/a.md","content":"final"}`},
				}},
		},
	}, true)
	h := newAgentSurfaceHarness(t, chat, agentServices{preview: &fakeFilePreview{}})
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "交付文件", "a.md")
	if tester.HasText("已编辑 1 个文件") {
		t.Fatalf("edited card survived a later write; texts = %v", tester.Texts())
	}
}

// TestGeneratedFileCardsIgnoreDryRunPatch pins that a dry-run patch reports no
// files (nothing was written).
func TestGeneratedFileCardsIgnoreDryRunPatch(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-dry", Title: "预览回合",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "预览补丁"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "补丁校验通过。",
				Tools: []contracts.ToolCall{{
					ID: "t1", Name: "apply_patch", Status: "ok",
					Arguments: `{"patch":"*** Begin Patch\n*** Add File: docs/a.md\n+x\n*** End Patch","dry_run":true}`,
				}}},
		},
	}, true)
	h := newAgentSurfaceHarness(t, chat, agentServices{preview: &fakeFilePreview{}})
	tester := h.tester()
	tester.Frame()
	if tester.HasText("交付文件") || tester.HasText("已编辑") {
		t.Fatalf("dry-run patch produced file cards; texts = %v", tester.Texts())
	}
}

// TestGeneratedFileCardsAddedThenDeleted pins the merge rule: a path both added
// and deleted by one patch is reported as edited+deleted, not delivered.
func TestGeneratedFileCardsAddedThenDeleted(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-adddel", Title: "增删回合",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "先建后删"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "完成。",
				Tools: []contracts.ToolCall{{
					ID: "t1", Name: "apply_patch", Status: "ok",
					Generated: []contracts.ToolCallFile{{Path: "tmp/x.md", Added: true, Deleted: true}},
				}}},
		},
	}, true)
	h := newAgentSurfaceHarness(t, chat, agentServices{preview: &fakeFilePreview{}})
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "已编辑 1 个文件", "x.md", "· 已删除")
	if tester.HasText("交付文件") {
		t.Fatalf("added-then-deleted file shown as delivered; texts = %v", tester.Texts())
	}
}
