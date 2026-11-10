package session_test

// Tests for the B3 screen chrome: the conversation header/actions/tabs are host
// band slots, the goal strip and the approval banner dock above the composer,
// and Esc answers a pending approval instead of interrupting the turn.

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

// TestConversationActionsMenuItems covers the band's ⋯ entry: a compact More
// icon (not the old 更多 words) opening the clipboard/export/archive menu.
func TestConversationActionsMenuItems(t *testing.T) {
	chat := &archiveChat{fakeChat: newFakeChat()}
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "会话操作测试",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	h := newChatServiceHarness(t, chat, nil)
	tester := h.tester()
	tester.Frame()

	if _, ok := tester.Find("更多"); ok {
		t.Fatalf("the band still shows a 更多 text button; texts = %v", tester.Texts())
	}
	rect, ok := tester.Find("会话操作")
	if !ok {
		t.Fatalf("conversation actions entry missing; texts = %v", tester.Texts())
	}
	if rect.W < 20 || rect.W > 28 || rect.H < 20 || rect.H > 28 {
		t.Fatalf("conversation actions target = %.0fx%.0f, want a 24 DIP icon button", rect.W, rect.H)
	}
	openConversationActions(t, h, tester)
	h.assertTexts(tester, "复制会话 ID", "复制对话", "导出 Markdown", "导出 JSONL", "归档会话")
}

// TestGoalStripAboveComposer covers the goal dock: the status badge, the goal
// text and its ⋯ menu sit on the reading measure right above the composer (they
// left the title row).
func TestGoalStripAboveComposer(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "ship it",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	goal := newLifecycleTracker()
	h, tester := newGoalPauseHarness(t, chat, nil, goal)
	goal.setGoal("g1", "发布清单", contracts.GoalStatusActive)
	tester.Frame()

	h.assertTexts(tester, "目标", "发布清单", "目标操作")
	goalRect, ok := tester.Find("发布清单")
	if !ok {
		t.Fatalf("goal text missing; texts = %v", tester.Texts())
	}
	composer, ok := tester.Find("输入消息…")
	if !ok {
		t.Fatalf("composer missing; texts = %v", tester.Texts())
	}
	if goalRect.Y >= composer.Y {
		t.Fatalf("goal strip at Y=%.1f is not above the composer at Y=%.1f", goalRect.Y, composer.Y)
	}

	// The ⋯ menu keeps the old goal actions.
	openGoalActions(t, h, tester, "目标")
	h.assertTexts(tester, "完成目标", "结束目标（保留记录）", "删除目标记录")
}

// TestSessionBannerSlotAboveComposer covers the approval banner extension point:
// another module's inline card registered in SlotSessionBanner renders in the
// dock above the composer.
func TestSessionBannerSlotAboveComposer(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "审批",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "跑一下"}},
	}, true)
	h := newHarness(t, chat)
	unregister, err := h.ui.Runtime().RegisterSlotItem(contracts.SlotItem{
		Slot: contracts.SlotSessionBanner, ID: "test.approval-card", Order: 10,
		Build: func(c *ui.Context) {
			ui.Text(c, "审批内联卡")
		},
	})
	if err != nil {
		t.Fatalf("register banner slot item: %v", err)
	}
	t.Cleanup(unregister)
	tester := h.tester()
	tester.Frame()
	tester.Frame()

	banner, ok := tester.Find("审批内联卡")
	if !ok {
		t.Fatalf("banner slot item missing; texts = %v", tester.Texts())
	}
	composer, ok := tester.Find("输入消息…")
	if !ok {
		t.Fatalf("composer missing; texts = %v", tester.Texts())
	}
	if banner.Y >= composer.Y {
		t.Fatalf("banner at Y=%.1f is not above the composer at Y=%.1f", banner.Y, composer.Y)
	}
}

// fakeApprovals is the contracts.Approvals bridge for the Esc tests: requests
// are resolved by the UI and recorded.
type fakeApprovals struct {
	mu      sync.Mutex
	pending []contracts.ApprovalRequest
	denied  []string
}

func (f *fakeApprovals) Mode() string { return "ask" }

func (f *fakeApprovals) Request(context.Context, contracts.ApprovalRequest) (contracts.ApprovalDecision, error) {
	return contracts.ApprovalDeny, nil
}

func (f *fakeApprovals) Pending() []contracts.ApprovalRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.ApprovalRequest(nil), f.pending...)
}

func (f *fakeApprovals) Resolve(id string, decision contracts.ApprovalDecision) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if decision == contracts.ApprovalDeny {
		f.denied = append(f.denied, id)
	}
	kept := f.pending[:0]
	for _, request := range f.pending {
		if request.ID != id {
			kept = append(kept, request)
		}
	}
	f.pending = kept
	return nil
}

func (f *fakeApprovals) Subscribe(func()) (func(), error) { return func() {}, nil }

func (f *fakeApprovals) deniedCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.denied...)
}

// setPending queues one approval request for Esc to answer.
func (f *fakeApprovals) setPending(requests ...contracts.ApprovalRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending = requests
}

// newApprovalHarness activates the session screen with the optional approvals
// service the Esc path reads.
func newApprovalHarness(t *testing.T, chat *fakeChat, approvals contracts.Approvals) *harness {
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
	provide[contracts.Approvals](t, ctx, contracts.KeyApprovals, approvals)
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
}

// TestEscDeniesPendingApprovalBeforeInterrupting covers the Esc precedence: with
// an approval waiting, Esc denies the oldest request and leaves the turn
// running; only the next Esc (no approval left) stops the turn.
func TestEscDeniesPendingApprovalBeforeInterrupting(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "审批中断", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "跑一下"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "正在跑", Streaming: true},
		},
	}, true)
	approvals := &fakeApprovals{}
	approvals.setPending(contracts.ApprovalRequest{ID: "ap-1", Tool: "bash", Summary: "rm -rf build"})
	h := newApprovalHarness(t, chat, approvals)
	tester := h.tester()
	focusComposerForRunningTest(t, tester)

	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if got := approvals.deniedCalls(); len(got) != 1 || got[0] != "ap-1" {
		t.Fatalf("denied approvals = %v, want [ap-1]", got)
	}
	if calls := chat.cancels(); len(calls) != 0 {
		t.Fatalf("Esc stopped the running turn instead of denying the approval: cancels = %v", calls)
	}
	if session, ok := chat.Session("sess-1"); !ok || !session.Running {
		t.Fatal("the turn is no longer running after denying an approval")
	}

	// Once no approval waits, Esc interrupts the turn as before.
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if calls := chat.cancels(); len(calls) != 1 || calls[0] != "sess-1" {
		t.Fatalf("second Esc did not stop the turn: cancels = %v", calls)
	}
}
