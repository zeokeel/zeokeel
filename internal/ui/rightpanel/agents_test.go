package rightpanel_test

// Tests for the Agents inspector tab (team members, steer, interrupt/close).
// Written before the implementation (TDD): the tab, the member rows and the
// steer actions are exercised through the real host frame.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/rightpanel"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakeTeam is a scripted contracts.TeamService: it records the control calls
// and returns the configured members/errors.
type fakeTeam struct {
	mu sync.Mutex

	members []contracts.TeamMember

	spawns   []teamSpawn
	nextID   int
	spawnErr error

	sendAsyncCalls []teamSend
	interrupts     []string
	closes         []string
	sendErr        error
	interruptErr   error
	closeErr       error
}

type teamSend struct {
	id      string
	message string
}

// teamSpawn records one Spawn call of the scripted team backend.
type teamSpawn struct {
	name   string
	prompt string
	model  string
}

func (f *fakeTeam) spawnCalls() []teamSpawn {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]teamSpawn(nil), f.spawns...)
}

func (f *fakeTeam) Members() []contracts.TeamMember {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.TeamMember(nil), f.members...)
}

func (f *fakeTeam) Spawn(_ context.Context, name, prompt, model string) (contracts.TeamMember, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spawns = append(f.spawns, teamSpawn{name: name, prompt: prompt, model: model})
	if f.spawnErr != nil {
		return contracts.TeamMember{}, f.spawnErr
	}
	f.nextID++
	member := contracts.TeamMember{
		ID: fmt.Sprintf("spawned-%d", f.nextID), Name: name, Status: "active",
		RunState: contracts.TeamRunRunning, Model: model,
		Created: time.Now(), Started: time.Now(),
	}
	f.members = append(f.members, member)
	return member, nil
}

// finishes marks one member as a completed run (activity + timing + output),
// which is what triggers the inspector's completion toast.
func (f *fakeTeam) finishes(id, output string, finished time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for index := range f.members {
		if f.members[index].ID != id {
			continue
		}
		f.members[index].RunState = contracts.TeamRunDone
		f.members[index].Last = output
		f.members[index].Finished = finished
		f.members[index].Activity = append(f.members[index].Activity, contracts.TeamActivity{
			ID: "finish-" + id, MemberID: id, Kind: contracts.TeamActivityFinish,
			Text: "「" + f.members[index].Name + "」已完成：" + output, Time: finished,
		})
	}
}

func (f *fakeTeam) SendAsync(id, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendAsyncCalls = append(f.sendAsyncCalls, teamSend{id: id, message: message})
	return f.sendErr
}

func (f *fakeTeam) Send(_ context.Context, id, message string) (string, error) {
	return "", nil
}

func (f *fakeTeam) Wait(_ context.Context, id string) (string, error) { return "", nil }

func (f *fakeTeam) Interrupt(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interrupts = append(f.interrupts, id)
	return f.interruptErr
}

func (f *fakeTeam) Close(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes = append(f.closes, id)
	return f.closeErr
}

func (f *fakeTeam) sends() []teamSend {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]teamSend(nil), f.sendAsyncCalls...)
}

func (f *fakeTeam) setRunState(id, runState string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for index := range f.members {
		if f.members[index].ID == id {
			f.members[index].RunState = runState
		}
	}
}

func (f *fakeTeam) interrupted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.interrupts...)
}

func (f *fakeTeam) closed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.closes...)
}

func newFakeTeam() *fakeTeam {
	now := time.Now()
	return &fakeTeam{members: []contracts.TeamMember{
		{
			ID: "member-1", Name: "研究员", Status: "active",
			RunState: contracts.TeamRunDone, Model: "deepseek-chat",
			Created: now.Add(-2 * time.Minute), Started: now.Add(-90 * time.Second),
			Finished: now.Add(-10 * time.Second), Last: "已整理三份资料",
			Iterations: 3, UsageTokens: 1200,
			Activity: []contracts.TeamActivity{
				{ID: "a1", MemberID: "member-1", Kind: contracts.TeamActivitySpawn,
					Text: "创建队友「研究员」", Time: now.Add(-2 * time.Minute)},
				{ID: "a2", MemberID: "member-1", Kind: contracts.TeamActivityFinish,
					Text: "「研究员」已完成：已整理三份资料", Time: now.Add(-10 * time.Second)},
			},
		},
		{
			ID: "member-2", Name: "审阅者", Status: "interrupted",
			RunState: contracts.TeamRunInterrupted, Model: "gpt-4o",
			Created: now.Add(-3 * time.Minute),
		},
	}}
}

// fakeSubagents is a scripted synchronous delegation backend: the Agents pane
// only reads its SubagentActivities lister half (Run is not used by the pane).
type fakeSubagents struct {
	mu         sync.Mutex
	activities []contracts.SubagentActivity
}

func newFakeSubagents(activities ...contracts.SubagentActivity) *fakeSubagents {
	return &fakeSubagents{activities: activities}
}

func (f *fakeSubagents) Run(context.Context, contracts.SubagentRequest) (contracts.SubagentResult, error) {
	return contracts.SubagentResult{}, nil
}

func (f *fakeSubagents) SubagentActivities() []contracts.SubagentActivity {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.SubagentActivity(nil), f.activities...)
}

func (f *fakeSubagents) setActivities(activities ...contracts.SubagentActivity) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activities = activities
}

func (f *fakeSubagents) update(id string, apply func(*contracts.SubagentActivity)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for index := range f.activities {
		if f.activities[index].ID == id {
			apply(&f.activities[index])
		}
	}
}

// newAgentsHarness builds the product frame with the optional team backend.
func newAgentsHarness(t *testing.T, chat *fakeChat, cfg rightpanel.Config, team contracts.TeamService, language string) *harness {
	t.Helper()
	return newAgentsHarnessWithActivities(t, chat, cfg, team, nil, language)
}

// newAgentsHarnessWithActivities is newAgentsHarness with the optional
// synchronous delegation lister (contracts.KeySubagents) provided too.
func newAgentsHarnessWithActivities(t *testing.T, chat *fakeChat, cfg rightpanel.Config,
	team contracts.TeamService, subagents contracts.Subagents, language string) *harness {
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
	notifier := newFakeNotifier()
	provide[contracts.Notifier](t, ctx, contracts.KeyNotifier, notifier)
	if team != nil {
		provide[contracts.TeamService](t, ctx, contracts.KeyTeam, team)
	}
	if subagents != nil {
		provide[contracts.Subagents](t, ctx, contracts.KeySubagents, subagents)
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
	h := &harness{t: t, ctx: ctx, uiRoot: uiRoot, chat: chat, notifier: notifier}
	h.fiber = activate(t, ctx, rightpanel.Module(), cfg)
	return h
}

func selectAgent(t *testing.T, h *harness, tester *ui.Tester, name string) {
	t.Helper()
	if err := tester.Click(name); err != nil {
		t.Fatalf("select teammate %q: %v; texts = %v", name, err, tester.Texts())
	}
	tester.Frame()
}

func typeSteer(t *testing.T, h *harness, tester *ui.Tester, message string) {
	t.Helper()
	rect, ok := tester.Find("发送消息")
	if !ok {
		t.Fatalf("steer input not found; texts = %v", tester.Texts())
	}
	tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Frame()
	tester.Type(message)
	tester.Frame()
}

// --- tests ------------------------------------------------------------------

func TestAgentsTabEmptyStateWithoutService(t *testing.T) {
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, nil, "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")
	h.assertTexts(tester, "团队服务未启用")
}

func TestAgentsListRendersMembers(t *testing.T) {
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, newFakeTeam(), "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")

	h.assertTexts(tester, "研究员", "member-1", "已完成", "已整理三份资料",
		"审阅者", "member-2", "已中断", "运行已中断，等待下一条消息")
}

func TestAgentsCustomTabLabel(t *testing.T) {
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{AgentsTab: "Crew"}, newFakeTeam(), "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "Crew")
	h.assertTexts(tester, "Crew")
	h.assertNoTexts(tester, "团队")
}

func TestAgentsSelectThenSend(t *testing.T) {
	team := newFakeTeam()
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, team, "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")

	selectAgent(t, h, tester, "研究员")
	h.assertTexts(tester, "发送", "中断", "关闭")
	typeSteer(t, h, tester, "继续整理")
	h.click(tester, "发送")

	sends := team.sends()
	if len(sends) != 1 || sends[0].id != "member-1" || sends[0].message != "继续整理" {
		t.Fatalf("SendAsync calls = %+v, want member-1/继续整理", sends)
	}
	h.assertTexts(tester, "已发送")
}

func TestAgentsInterruptAndClose(t *testing.T) {
	team := newFakeTeam()
	team.setRunState("member-1", contracts.TeamRunRunning)
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, team, "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")
	selectAgent(t, h, tester, "研究员")

	h.click(tester, "中断")
	if got := team.interrupted(); len(got) != 1 || got[0] != "member-1" {
		t.Fatalf("Interrupt calls = %v, want [member-1]", got)
	}
	h.assertTexts(tester, "已中断")

	selectAgent(t, h, tester, "审阅者")
	// Closing a teammate is destructive: the button opens the native
	// confirmation and only the dialog's action closes it.
	h.click(tester, "关闭")
	if got := team.closed(); len(got) != 0 {
		t.Fatalf("Close calls before confirming = %v, want none", got)
	}
	h.assertTexts(tester, "关闭队友", "确认关闭")
	h.click(tester, "确认关闭")
	if got := team.closed(); len(got) != 1 || got[0] != "member-2" {
		t.Fatalf("Close calls = %v, want [member-2]", got)
	}
}

func TestAgentsSendErrorShown(t *testing.T) {
	team := newFakeTeam()
	team.sendErr = errors.New("mailbox closed")
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, team, "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")
	selectAgent(t, h, tester, "研究员")
	typeSteer(t, h, tester, "hi")
	h.click(tester, "发送")

	h.assertTexts(tester, "发送失败：mailbox closed")
	if sends := team.sends(); len(sends) != 1 {
		t.Fatalf("SendAsync calls = %d, want the single failed attempt", len(sends))
	}
}

func TestAgentsInterruptErrorShown(t *testing.T) {
	team := newFakeTeam()
	team.setRunState("member-1", contracts.TeamRunRunning)
	team.interruptErr = errors.New("not running")
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, team, "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")
	selectAgent(t, h, tester, "研究员")
	h.click(tester, "中断")
	h.assertTexts(tester, "中断失败：not running")
}

func TestAgentsEnglishCopy(t *testing.T) {
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, newFakeTeam(), "en")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "Agents")
	h.assertTexts(tester, "Agents")

	h.assertTexts(tester, "研究员", "member-1", "Done", "已整理三份资料",
		"审阅者", "Interrupted",
		"Select a teammate to steer, interrupt or inspect its activity")
	selectAgent(t, h, tester, "研究员")
	h.assertTexts(tester, "Message", "Send", "Interrupt", "Close")
}

// --- synchronous subagent delegation rows ------------------------------------

// agentsChat is a chat with one active session, the parent of the scripted
// delegations.
func agentsChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "s1", Title: "会话"}, true)
	return chat
}

// agentsActivities covers every delegation status the pane renders, all owned
// by s1.
func agentsActivities() []contracts.SubagentActivity {
	started := time.Unix(1700000100, 0)
	return []contracts.SubagentActivity{
		{ID: "sa-running", Name: "资料助手", Status: contracts.SubagentStatusRunning,
			Started: started, SessionID: "s1", Model: "deepseek-chat", Prompt: "查找资料"},
		{ID: "sa-done", Name: "代码助手", Status: contracts.SubagentStatusDone,
			Started: started, Finished: started.Add(3 * time.Second), SessionID: "s1",
			Model: "gpt-4o", Fork: true, Prompt: "检查改动", Detail: "结论：通过"},
		{ID: "sa-error", Name: "失败助手", Status: contracts.SubagentStatusError,
			Started: started, Finished: started.Add(time.Second), SessionID: "s1",
			Model: "gpt-4o", Prompt: "改坏的", Detail: "provider exploded"},
		{ID: "sa-cancelled", Name: "取消助手", Status: contracts.SubagentStatusCancelled,
			Started: started, Finished: started.Add(2 * time.Second), SessionID: "s1",
			Model: "deepseek-chat", Prompt: "取消的任务"},
	}
}

// exactTextIndex returns the index of the first text node equal to want.
func exactTextIndex(texts []string, want string) int {
	for index, text := range texts {
		if text == want {
			return index
		}
	}
	return -1
}

// assertNoExactTexts fails when a text node is exactly one of unwanted. The
// pane's read-only note contains the words "发送消息/中断", so the substring
// HasText helper cannot prove a control is absent.
func assertNoExactTexts(t *testing.T, tester *ui.Tester, unwanted ...string) {
	t.Helper()
	texts := tester.Texts()
	for _, want := range unwanted {
		if exactTextIndex(texts, want) >= 0 {
			t.Fatalf("frame unexpectedly shows the text node %q; texts = %v", want, texts)
		}
	}
}

// TestAgentsMergesTeammatesAndSubagents pins the merged list: teammate rows
// come first and keep their copy, delegation rows follow with the 子代理 marker
// and every status the inspector can show.
func TestAgentsMergesTeammatesAndSubagents(t *testing.T) {
	h := newAgentsHarnessWithActivities(t, agentsChat(), rightpanel.Config{},
		newFakeTeam(), newFakeSubagents(agentsActivities()...), "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")

	// Teammate rows are preserved...
	h.assertTexts(tester, "研究员", "member-1", "已完成", "已整理三份资料",
		"审阅者", "member-2", "已中断", "运行已中断，等待下一条消息")
	// ...and the delegation rows follow with name, marker, status, model and a
	// prompt/output summary.
	h.assertTexts(tester, "资料助手", "代码助手", "失败助手", "取消助手",
		"子代理", "完成", "失败", "已取消",
		"deepseek-chat", "gpt-4o", "全新上下文", "分叉上下文",
		"查找资料", "结论：通过", "provider exploded")

	texts := tester.Texts()
	memberIndex := exactTextIndex(texts, "研究员")
	firstSubagent := exactTextIndex(texts, "资料助手")
	if memberIndex < 0 || firstSubagent < 0 || memberIndex > firstSubagent {
		t.Fatalf("teammates must come before subagents: 研究员 at %d, 资料助手 at %d", memberIndex, firstSubagent)
	}
}

// TestAgentsSubagentDetailIsReadOnly pins the selected delegation's detail
// card and that it has no steer/interrupt/close controls, while a teammate
// selection keeps its controls.
func TestAgentsSubagentDetailIsReadOnly(t *testing.T) {
	h := newAgentsHarnessWithActivities(t, agentsChat(), rightpanel.Config{},
		newFakeTeam(), newFakeSubagents(agentsActivities()...), "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")

	if err := tester.Click("代码助手"); err != nil {
		t.Fatalf("select subagent: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "模型", "gpt-4o", "开始", "耗时", "方式", "分叉上下文",
		"任务", "检查改动", "最新输出", "结论：通过",
		"同步委派只读：它是一次阻塞调用，不能发送消息或中断。")
	assertNoExactTexts(t, tester, "发送", "中断", "关闭", "发送消息")

	// A teammate keeps the existing steer/interrupt/close surface.
	selectAgent(t, h, tester, "研究员")
	h.assertTexts(tester, "发送消息", "发送", "中断", "关闭")
}

// TestAgentsSubagentErrorRow pins that a failed delegation shows its status
// and the bounded error detail.
func TestAgentsSubagentErrorRow(t *testing.T) {
	activities := []contracts.SubagentActivity{{
		ID: "sa-error", Name: "失败助手", Status: contracts.SubagentStatusError,
		Started: time.Unix(1700000100, 0), Finished: time.Unix(1700000101, 0),
		SessionID: "s1", Model: "gpt-4o", Prompt: "改坏的", Detail: "provider exploded",
	}}
	h := newAgentsHarnessWithActivities(t, agentsChat(), rightpanel.Config{},
		newFakeTeam(), newFakeSubagents(activities...), "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")
	h.assertTexts(tester, "失败助手", "失败", "provider exploded")

	if err := tester.Click("失败助手"); err != nil {
		t.Fatalf("select failed subagent: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "provider exploded")
	assertNoExactTexts(t, tester, "发送", "中断", "关闭")
}

// TestAgentsSubagentSessionFilter pins that only the active session's
// delegations show in the inspector: another session's background run never
// appears under the conversation on screen.
func TestAgentsSubagentSessionFilter(t *testing.T) {
	started := time.Unix(1700000100, 0)
	subagents := newFakeSubagents(
		contracts.SubagentActivity{ID: "sa-own", Name: "本会话助手",
			Status: contracts.SubagentStatusRunning, Started: started, SessionID: "s1", Model: "m"},
		contracts.SubagentActivity{ID: "sa-other", Name: "其他会话助手",
			Status: contracts.SubagentStatusRunning, Started: started, SessionID: "s2", Model: "m"},
	)
	chat := agentsChat()
	chat.addSession(contracts.ChatSession{ID: "s2", Title: "另一个会话"}, false)
	h := newAgentsHarnessWithActivities(t, chat, rightpanel.Config{},
		newFakeTeam(), subagents, "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")
	h.assertTexts(tester, "本会话助手")
	h.assertNoTexts(tester, "其他会话助手")

	if err := chat.SetActiveSession("s2"); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	chat.notify("s2")
	tester.Frame()
	h.assertTexts(tester, "其他会话助手")
	h.assertNoTexts(tester, "本会话助手")
}

// TestAgentsSubagentRowRefreshesOnNotify pins the live update path: a
// delegation reported running turns into 完成/已取消 etc. on the frame the
// agent service's notification requests (the same subscription the run's
// activity start/finish notifications use).
func TestAgentsSubagentRowRefreshesOnNotify(t *testing.T) {
	started := time.Unix(1700000100, 0)
	subagents := newFakeSubagents(contracts.SubagentActivity{
		ID: "sa-live", Name: "实时助手", Status: contracts.SubagentStatusRunning,
		Started: started, SessionID: "s1", Model: "deepseek-chat", Prompt: "长任务",
	})
	chat := agentsChat()
	h := newAgentsHarnessWithActivities(t, chat, rightpanel.Config{},
		newFakeTeam(), subagents, "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")
	h.assertTexts(tester, "实时助手", "运行中")

	subagents.update("sa-live", func(activity *contracts.SubagentActivity) {
		activity.Status = contracts.SubagentStatusDone
		activity.Finished = started.Add(2 * time.Second)
		activity.Detail = "任务完成"
	})
	chat.notify("s1")
	tester.Frame()
	h.assertTexts(tester, "实时助手", "完成", "任务完成")
}

// TestAgentsActivitiesWithoutTeamService pins the optional-service contract:
// without a team backend the delegation rows still show (read-only), and the
// empty state appears only when both sources are empty.
func TestAgentsActivitiesWithoutTeamService(t *testing.T) {
	h := newAgentsHarnessWithActivities(t, agentsChat(), rightpanel.Config{}, nil,
		newFakeSubagents(agentsActivities()...), "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")
	h.assertTexts(tester, "资料助手", "子代理", "运行中")
	h.assertNoTexts(tester, "团队服务未启用", "还没有队友")

	if err := tester.Click("资料助手"); err != nil {
		t.Fatalf("select subagent: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "同步委派只读：它是一次阻塞调用，不能发送消息或中断。")
	assertNoExactTexts(t, tester, "发送", "中断", "关闭")
}

// TestAgentsEmptyStateOnlyWhenBothSourcesEmpty pins the empty-state rule.
func TestAgentsEmptyStateOnlyWhenBothSourcesEmpty(t *testing.T) {
	subagents := newFakeSubagents()
	h := newAgentsHarnessWithActivities(t, agentsChat(), rightpanel.Config{},
		&fakeTeam{}, subagents, "zh")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "团队")
	h.assertTexts(tester, "还没有队友")

	subagents.setActivities(agentsActivities()[0])
	chat := h.chat
	chat.notify("s1")
	tester.Frame()
	h.assertTexts(tester, "资料助手")
	h.assertNoTexts(tester, "还没有队友")
}

// TestAgentsEnglishSubagentRows covers the English copy of the merged
// delegation rows and their read-only detail.
func TestAgentsEnglishSubagentRows(t *testing.T) {
	started := time.Unix(1700000100, 0)
	subagents := newFakeSubagents(contracts.SubagentActivity{
		ID: "sa-en", Name: "Research helper", Status: contracts.SubagentStatusDone,
		Started: started, Finished: started.Add(2 * time.Second), SessionID: "s1",
		Model: "deepseek-chat", Fork: true, Prompt: "Check the change", Detail: "Looks good",
	})
	h := newAgentsHarnessWithActivities(t, agentsChat(), rightpanel.Config{},
		newFakeTeam(), subagents, "en")
	tester := h.tester()
	tester.Frame()
	h.selectPane(tester, "Agents")
	h.assertTexts(tester, "Research helper", "Subagent", "Completed", "Forked context")

	if err := tester.Click("Research helper"); err != nil {
		t.Fatalf("select subagent: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "Model", "Started", "Duration", "Mode", "Task",
		"Check the change", "Latest output", "Looks good",
		"Synchronous delegation is read-only: it is one blocking call, so it cannot be steered or interrupted.")
	assertNoExactTexts(t, tester, "Send", "Interrupt", "Close", "Message")

	selectAgent(t, h, tester, "研究员")
	h.assertTexts(tester, "Message", "Send", "Interrupt", "Close")
}
