package session_test

// Tests for automatic goal continuation after a run settles.

import (
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// setFakeGoal installs a goal directly on the shared fake tracker and invokes
// its handler like a real goal change.
func setFakeGoal(f *fakeGoalTracker, id, text string) {
	f.mu.Lock()
	f.id, f.text = id, text
	handler := f.handler
	f.mu.Unlock()
	if handler != nil {
		handler(contracts.Goal{ID: id, Text: text, Status: "active"}, true)
	}
}

func clearFakeGoal(f *fakeGoalTracker) {
	f.Clear()
}

func autoContinueSettings(enabled bool) *permissionSettings {
	value := "false"
	if enabled {
		value = "true"
	}
	return newPermissionSettings(map[string]string{contracts.SettingGoalAutoContinue: value})
}

func autoContinueChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "目标会话",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleAssistant, Text: "done"}},
	}, true)
	return chat
}

func newAutoContinueHarness(t *testing.T, chat *fakeChat, settings *permissionSettings, goal *fakeGoalTracker) (*harness, *ui.Tester) {
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
	provide[contracts.SettingsService](t, ctx, contracts.KeySettings, settings)
	provide[contracts.GoalTracker](t, ctx, contracts.KeyGoalTracker, goal)
	activate(t, ctx, session.Module(), session.Config{})
	h := &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
	return h, h.tester()
}

func settleRun(t *testing.T, chat *fakeChat, tester interface {
	Frame()
}) {
	t.Helper()
	chat.updateSession(contracts.ChatSession{ID: "sess-1", Title: "目标会话", Running: true,
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleAssistant, Text: "working"}}})
	tester.Frame()
	chat.updateSession(contracts.ChatSession{ID: "sess-1", Title: "目标会话", Running: false,
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleAssistant, Text: "done"}}})
	tester.Frame()
	tester.Frame()
}

func autoSends(chat *fakeChat) []string {
	var out []string
	for _, send := range chat.sends() {
		if strings.HasPrefix(send.prompt, "请继续推进目标：") {
			out = append(out, send.prompt)
		}
	}
	return out
}

func TestGoalAutoContinueOffDoesNothing(t *testing.T) {
	chat := autoContinueChat()
	goal := &fakeGoalTracker{}
	setFakeGoal(goal, "g1", "ship it")
	_, tester := newAutoContinueHarness(t, chat, autoContinueSettings(false), goal)
	tester.Frame()
	settleRun(t, chat, tester)
	if sends := autoSends(chat); len(sends) != 0 {
		t.Fatalf("auto-continue sent while off: %v", sends)
	}
}

func TestGoalAutoContinueSendsOnceWithGoalText(t *testing.T) {
	chat := autoContinueChat()
	goal := &fakeGoalTracker{}
	setFakeGoal(goal, "g1", "ship it")
	_, tester := newAutoContinueHarness(t, chat, autoContinueSettings(true), goal)
	tester.Frame()
	settleRun(t, chat, tester)
	sends := autoSends(chat)
	if len(sends) != 1 || !strings.Contains(sends[0], "ship it") || !strings.Contains(sends[0], "完成后请更新或清除目标") {
		t.Fatalf("auto sends = %v", sends)
	}
}

func TestGoalAutoContinueStopsAfterFive(t *testing.T) {
	chat := autoContinueChat()
	goal := &fakeGoalTracker{}
	setFakeGoal(goal, "g1", "ship it")
	_, tester := newAutoContinueHarness(t, chat, autoContinueSettings(true), goal)
	tester.Frame()
	for attempt := 0; attempt < 6; attempt++ {
		settleRun(t, chat, tester)
	}
	if got := len(autoSends(chat)); got != 5 {
		t.Fatalf("auto sends = %d, want 5", got)
	}
}

func TestGoalAutoContinueResetsOnGoalChangeAndStopsWhenCleared(t *testing.T) {
	chat := autoContinueChat()
	goal := &fakeGoalTracker{}
	setFakeGoal(goal, "g1", "first")
	_, tester := newAutoContinueHarness(t, chat, autoContinueSettings(true), goal)
	tester.Frame()
	for attempt := 0; attempt < 5; attempt++ {
		settleRun(t, chat, tester)
	}
	if got := len(autoSends(chat)); got != 5 {
		t.Fatalf("auto sends before reset = %d", got)
	}
	setFakeGoal(goal, "g2", "second goal")
	tester.Frame()
	settleRun(t, chat, tester)
	if got := len(autoSends(chat)); got != 6 {
		t.Fatalf("auto sends after goal change = %d, want 6", got)
	}
	clearFakeGoal(goal)
	tester.Frame()
	settleRun(t, chat, tester)
	if got := len(autoSends(chat)); got != 6 {
		t.Fatalf("auto continue after clear = %d", got)
	}
}

func TestGoalAutoContinueSendFailureShowsDanger(t *testing.T) {
	chat := autoContinueChat()
	chat.sendErr = errors.New("agent busy")
	goal := &fakeGoalTracker{}
	setFakeGoal(goal, "g1", "ship it")
	h, tester := newAutoContinueHarness(t, chat, autoContinueSettings(true), goal)
	tester.Frame()
	settleRun(t, chat, tester)
	h.assertTexts(tester, "自动续跑发送失败：agent busy")
	if got := len(autoSends(chat)); got != 0 {
		t.Fatalf("failed auto send recorded: %d", got)
	}
}
