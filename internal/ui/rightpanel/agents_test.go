package rightpanel_test

// Tests for the Agents inspector tab (team members, steer, interrupt/close).
// Written before the implementation (TDD): the tab, the member rows and the
// steer actions are exercised through the real host frame.

import (
	"context"
	"errors"
	"sync"
	"testing"

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

func (f *fakeTeam) Members() []contracts.TeamMember {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.TeamMember(nil), f.members...)
}

func (f *fakeTeam) Spawn(_ context.Context, name, prompt, model string) (contracts.TeamMember, error) {
	return contracts.TeamMember{ID: "spawned", Name: name}, nil
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
	return &fakeTeam{members: []contracts.TeamMember{
		{ID: "member-1", Name: "研究员", Status: "active", Last: "已整理三份资料"},
		{ID: "member-2", Name: "审阅者", Status: "interrupted", Last: ""},
	}}
}

// newAgentsHarness builds the product frame with the optional team backend.
func newAgentsHarness(t *testing.T, chat *fakeChat, cfg rightpanel.Config, team contracts.TeamService, language string) *harness {
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
	if team != nil {
		provide[contracts.TeamService](t, ctx, contracts.KeyTeam, team)
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
	h.assertTexts(tester, "团队")

	h.click(tester, "团队")
	h.assertTexts(tester, "团队服务未启用")
}

func TestAgentsListRendersMembers(t *testing.T) {
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, newFakeTeam(), "zh")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "团队")

	h.assertTexts(tester, "研究员", "member-1", "运行中", "已整理三份资料",
		"审阅者", "member-2", "已中断", "还没有结果")
}

func TestAgentsCustomTabLabel(t *testing.T) {
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{AgentsTab: "Crew"}, newFakeTeam(), "zh")
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "Crew")
	h.assertNoTexts(tester, "团队")
}

func TestAgentsSelectThenSend(t *testing.T) {
	team := newFakeTeam()
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, team, "zh")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "团队")

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
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, team, "zh")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "团队")
	selectAgent(t, h, tester, "研究员")

	h.click(tester, "中断")
	if got := team.interrupted(); len(got) != 1 || got[0] != "member-1" {
		t.Fatalf("Interrupt calls = %v, want [member-1]", got)
	}
	h.assertTexts(tester, "已中断")

	selectAgent(t, h, tester, "审阅者")
	h.click(tester, "关闭")
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
	h.click(tester, "团队")
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
	team.interruptErr = errors.New("not running")
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, team, "zh")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "团队")
	selectAgent(t, h, tester, "研究员")
	h.click(tester, "中断")
	h.assertTexts(tester, "中断失败：not running")
}

func TestAgentsEnglishCopy(t *testing.T) {
	h := newAgentsHarness(t, newFakeChat(), rightpanel.Config{}, newFakeTeam(), "en")
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "Agents")
	h.click(tester, "Agents")

	h.assertTexts(tester, "研究员", "member-1", "Running", "已整理三份资料",
		"审阅者", "Interrupted", "No result yet",
		"Select a teammate to steer or interrupt")
	selectAgent(t, h, tester, "研究员")
	h.assertTexts(tester, "Message", "Send", "Interrupt", "Close")
}
