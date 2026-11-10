package session_test

// Tests for the session goal pause/resume controls.

import (
	"context"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// pauseTracker is a GoalTracker + GoalPauser fake with a mutable status.
type pauseTracker struct {
	mu          sync.Mutex
	handler     func(contracts.Goal, bool)
	id          string
	text        string
	status      string
	pauseCalls  int
	resumeCalls int
	pauseFail   bool
	resumeFail  bool
}

func (p *pauseTracker) SetHandler(fn func(contracts.Goal, bool)) {
	p.mu.Lock()
	p.handler = fn
	p.mu.Unlock()
}
func (p *pauseTracker) Set(_ context.Context, text string) (string, error) {
	p.mu.Lock()
	p.id, p.text, p.status = "goal-"+text, text, "active"
	handler := p.handler
	p.mu.Unlock()
	p.notify(handler)
	return p.id, nil
}
func (p *pauseTracker) Get() (string, string, string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.id, p.text, p.status, p.id != ""
}
func (p *pauseTracker) Clear() bool {
	p.mu.Lock()
	had := p.id != ""
	p.id, p.text, p.status = "", "", ""
	handler := p.handler
	p.mu.Unlock()
	if had && handler != nil {
		handler(contracts.Goal{}, false)
	}
	return had
}
func (p *pauseTracker) Pause() bool {
	p.mu.Lock()
	p.pauseCalls++
	fail := p.pauseFail
	had := p.id != ""
	if had && !fail {
		p.status = "paused"
	}
	handler := p.handler
	p.mu.Unlock()
	if had && !fail {
		p.notify(handler)
	}
	return had && !fail
}
func (p *pauseTracker) Resume() bool {
	p.mu.Lock()
	p.resumeCalls++
	fail := p.resumeFail
	had := p.id != ""
	if had && !fail {
		p.status = "active"
	}
	handler := p.handler
	p.mu.Unlock()
	if had && !fail {
		p.notify(handler)
	}
	return had && !fail
}
func (p *pauseTracker) notify(handler func(contracts.Goal, bool)) {
	p.mu.Lock()
	goal := contracts.Goal{ID: p.id, Text: p.text, Status: p.status}
	p.mu.Unlock()
	if handler != nil {
		handler(goal, true)
	}
}
func (p *pauseTracker) setGoal(id, text, status string) {
	p.mu.Lock()
	p.id, p.text, p.status = id, text, status
	handler := p.handler
	goal := contracts.Goal{ID: id, Text: text, Status: status}
	p.mu.Unlock()
	if handler != nil {
		handler(goal, true)
	}
}
func (p *pauseTracker) calls() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pauseCalls, p.resumeCalls
}

func newGoalPauseHarness(t *testing.T, chat *fakeChat, settings *permissionSettings, goal contracts.GoalTracker) (*harness, *ui.Tester) {
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
	provide[contracts.GoalTracker](t, ctx, contracts.KeyGoalTracker, goal)
	activate(t, ctx, session.Module(), session.Config{})
	h := &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
	return h, h.tester()
}

func TestGoalPauseResumeChipAndActions(t *testing.T) {
	chat := autoContinueChat()
	goal := &pauseTracker{}
	h, tester := newGoalPauseHarness(t, chat, nil, goal)
	goal.setGoal("g1", "ship it", "active")
	tester.Frame()
	h.assertTexts(tester, "目标")
	h.assertNoTexts(tester, "暂停目标", "继续目标", "删除目标记录")

	openGoalActions(t, h, tester, "目标")
	h.assertTexts(tester, "暂停目标", "删除目标记录")
	h.assertNoTexts(tester, "继续目标")

	h.click(tester, "暂停目标")
	tester.Frame()
	if pauseCalls, _ := goal.calls(); pauseCalls != 1 {
		t.Fatalf("Pause calls = %d", pauseCalls)
	}
	h.assertTexts(tester, "目标已暂停", "ship it")

	openGoalActions(t, h, tester, "目标已暂停")
	h.assertTexts(tester, "继续目标", "删除目标记录")
	h.assertNoTexts(tester, "暂停目标")

	h.click(tester, "继续目标")
	tester.Frame()
	if _, resumeCalls := goal.calls(); resumeCalls != 1 {
		t.Fatalf("Resume calls = %d", resumeCalls)
	}
	h.assertTexts(tester, "目标")
}

func TestGoalPauseFailureDanger(t *testing.T) {
	chat := autoContinueChat()
	goal := &pauseTracker{pauseFail: true}
	h, tester := newGoalPauseHarness(t, chat, nil, goal)
	goal.setGoal("g1", "ship it", "active")
	tester.Frame()
	openGoalActions(t, h, tester, "目标")
	h.click(tester, "暂停目标")
	tester.Frame()
	h.assertTexts(tester, "暂停目标失败", "目标")
}

func TestGoalPauseHiddenWithoutPauser(t *testing.T) {
	chat := autoContinueChat()
	goal := &fakeGoalTracker{}
	setFakeGoal(goal, "g1", "ship it")
	h, tester := newGoalPauseHarness(t, chat, nil, goal)
	tester.Frame()
	openGoalActions(t, h, tester, "目标")
	h.assertTexts(tester, "删除目标记录")
	h.assertNoTexts(tester, "暂停目标", "继续目标")
}

func TestGoalAutoContinueSkipsPausedGoal(t *testing.T) {
	chat := autoContinueChat()
	goal := &pauseTracker{}
	_, tester := newGoalPauseHarness(t, chat, autoContinueSettings(true), goal)
	goal.setGoal("g1", "ship it", "paused")
	tester.Frame()
	settleRun(t, chat, tester)
	if sends := autoSends(chat); len(sends) != 0 {
		t.Fatalf("auto-continue ran for a paused goal: %v", sends)
	}
}

func TestGoalPauseEnglish(t *testing.T) {
	chat := autoContinueChat()
	goal := &pauseTracker{}
	h, tester := newGoalPauseHarness(t, chat, nil, goal)
	goal.setGoal("g1", "ship it", "paused")
	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Goal paused")
	openGoalActions(t, h, tester, "Goal paused")
	h.assertTexts(tester, "Resume goal", "Delete goal record")
}
