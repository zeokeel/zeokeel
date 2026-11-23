package session_test

// UX-02 (2026-11 audit): the composer dock and the inline approval card have a
// height budget. At the supported minimum window 900×600 with the full stack
// (todo dock + goal dock + a long-diff approval) the composer and the approval
// decision buttons stay inside the window and the transcript keeps some height;
// the default 1512×944 window satisfies the same assertions. A pending approval
// docks first so its decision is reachable without scrolling the dock.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
	"github.com/zeokeel/zeokeel/internal/ui/approval"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// longWriteArguments builds a write_file payload whose content is 200 lines —
// enough to hit the DiffPreview cap (the audit's trigger condition).
func longWriteArguments(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= 200; i++ {
		fmt.Fprintf(&b, "line %03d: the quick brown fox jumps over the lazy dog\n", i)
	}
	payload, err := json.Marshal(map[string]string{
		"path":    "src/generated/output.go",
		"content": b.String(),
	})
	if err != nil {
		t.Fatalf("marshal write_file arguments: %v", err)
	}
	return string(payload)
}

// longApprovalSummary is the backend's 200-rune summary cap fully used.
func longApprovalSummary() string {
	return strings.Repeat("这是一段很长的审批摘要说明文字", 14) // 196 runes ≤ 200
}

// dockGeometryChat is one conversation with a finished turn plus a running
// streaming reply (the queue+stop composer anatomy).
func dockGeometryChat(running bool) *fakeChat {
	chat := newFakeChat()
	messages := []contracts.ChatMessage{
		{ID: "u1", Role: contracts.ChatRoleUser, Text: "帮我生成输出文件", CreatedAt: time.Unix(1700000000, 0)},
		{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "好的，正在生成。", CreatedAt: time.Unix(1700000001, 0)},
	}
	if running {
		messages = append(messages, contracts.ChatMessage{
			ID: "a2", Role: contracts.ChatRoleAssistant, Text: "继续生成…", Streaming: true,
			CreatedAt: time.Unix(1700000002, 0),
		})
	}
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "高度预算", Running: running, Messages: messages,
	}, true)
	return chat
}

// newDockGeometryHarness activates the session screen with the full dock stack:
// the approvals service + the real approval banner module, the todo store and
// the per-session goal service.
func newDockGeometryHarness(t *testing.T, chat *fakeChat, approvals *fakeApprovals,
	todo *fakeTodoService, goals *fakeGoalService) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, uinative.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	provide[contracts.Approvals](t, ctx, contracts.KeyApprovals, approvals)
	if todo != nil {
		provide[contracts.TodoService](t, ctx, contracts.KeyTodo, todo)
	}
	if goals != nil {
		provide[contracts.GoalService](t, ctx, contracts.KeyGoal, goals)
	}
	activate(t, ctx, session.Module(), session.Config{})
	activate(t, ctx, approval.Module(), approval.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
}

// fullDockStack seeds the todo list, the goal and the long-diff approval.
func fullDockStack(t *testing.T, approvals *fakeApprovals, todo *fakeTodoService, goals *fakeGoalService) {
	t.Helper()
	approvals.setPending(contracts.ApprovalRequest{
		ID: "ap-long", Tool: "write_file", SessionID: "sess-1",
		Summary:   longApprovalSummary(),
		Arguments: longWriteArguments(t),
	})
	todo.set(
		contracts.TodoItem{ID: "t1", Content: "第一步：读取配置", Status: contracts.TodoStatusCompleted},
		contracts.TodoItem{ID: "t2", Content: "第二步：生成输出", Status: contracts.TodoStatusInProgress},
		contracts.TodoItem{ID: "t3", Content: "第三步：运行测试", Status: contracts.TodoStatusPending},
	)
	goals.seed("sess-1", "发布前完成全部检查并输出报告")
}

// assertDockGeometry checks the blueprint's rect assertions for one frame: the
// composer input and its action control, the approval footer and the banner
// are inside the window, and the transcript keeps a positive height.
func assertDockGeometry(t *testing.T, tester *ui.Tester, width, height int, actionLabel string) {
	t.Helper()
	list, ok := tester.AccessByRole(ui.RoleList, "对话消息")
	if !ok {
		t.Fatalf("%dx%d: transcript list missing; texts = %v", width, height, tester.Texts())
	}
	input, okInput := tester.Find("输入消息…")
	action, okAction := tester.Find(actionLabel)
	// The approval card's positive action reads 允许一次 since the S4 rewrite
	// (B-spec §3.3.2: 拒绝 · 允许一次 · ⋯).
	allow, okAllow := tester.Find("允许一次")
	banner, okBanner := tester.Find("需要审批")
	if !okInput {
		t.Fatalf("%dx%d: composer input missing; texts = %v", width, height, tester.Texts())
	}
	if !okAction {
		t.Fatalf("%dx%d: composer %s missing; texts = %v", width, height, actionLabel, tester.Texts())
	}
	if !okAllow || !okBanner {
		t.Fatalf("%dx%d: approval card parts missing; texts = %v", width, height, tester.Texts())
	}
	inWindow := func(r ui.Rect) bool {
		return r.Y >= 0 && r.Y+r.H <= float32(height)+0.5 && r.X >= 0 && r.X+r.W <= float32(width)+0.5
	}
	if !inWindow(input) {
		t.Errorf("%dx%d: composer input OUTSIDE the window: %+v (bottom=%.1f)", width, height, input, input.Y+input.H)
	}
	if !inWindow(action) {
		t.Errorf("%dx%d: composer %s OUTSIDE the window: %+v (bottom=%.1f)", width, height, actionLabel, action, action.Y+action.H)
	}
	if !inWindow(allow) {
		t.Errorf("%dx%d: approval 允许 OUTSIDE the window: %+v (bottom=%.1f)", width, height, allow, allow.Y+allow.H)
	}
	if !inWindow(banner) {
		t.Errorf("%dx%d: approval banner OUTSIDE the window: %+v", width, height, banner)
	}
	if list.Bounds.H <= 0 {
		t.Errorf("%dx%d: transcript squeezed to zero height", width, height)
	}
	t.Logf("%dx%d geometry: transcript.H=%.1f input.bottom=%.1f %s.bottom=%.1f allow.bottom=%.1f card.span=%.1f",
		width, height, list.Bounds.H, input.Y+input.H, actionLabel, action.Y+action.H, allow.Y+allow.H,
		allow.Y+allow.H-banner.Y)
}

// TestDockGeometryMinWindowFullStack is the decisive UX-02 probe: 900×600 with
// todo(3) + goal + long-diff approval, running turn.
func TestDockGeometryMinWindowFullStack(t *testing.T) {
	chat := dockGeometryChat(true)
	approvals := &fakeApprovals{}
	todo := &fakeTodoService{}
	goals := newFakeGoalService()
	fullDockStack(t, approvals, todo, goals)
	h := newDockGeometryHarness(t, chat, approvals, todo, goals)
	tester := h.tester()
	tester.SetSize(900, 600)
	tester.Frame()
	tester.Frame()
	h.assertTexts(tester, "需要审批", "将改动", "目标")

	assertDockGeometry(t, tester, 900, 600, "停止")
}

// TestDockGeometryMinWindowIdle is the idle variant (send control).
func TestDockGeometryMinWindowIdle(t *testing.T) {
	chat := dockGeometryChat(false)
	approvals := &fakeApprovals{}
	todo := &fakeTodoService{}
	goals := newFakeGoalService()
	fullDockStack(t, approvals, todo, goals)
	h := newDockGeometryHarness(t, chat, approvals, todo, goals)
	tester := h.tester()
	tester.SetSize(900, 600)
	tester.Frame()
	tester.Frame()
	h.assertTexts(tester, "需要审批", "将改动")

	assertDockGeometry(t, tester, 900, 600, "发送")
}

// TestDockGeometryDefaultWindowFullStack is the wide-window control: the same
// stack at the default 1512×944 must satisfy every rect assertion too.
func TestDockGeometryDefaultWindowFullStack(t *testing.T) {
	chat := dockGeometryChat(true)
	approvals := &fakeApprovals{}
	todo := &fakeTodoService{}
	goals := newFakeGoalService()
	fullDockStack(t, approvals, todo, goals)
	h := newDockGeometryHarness(t, chat, approvals, todo, goals)
	tester := h.tester()
	tester.SetSize(1512, 944)
	tester.Frame()
	tester.Frame()
	h.assertTexts(tester, "需要审批", "将改动")

	assertDockGeometry(t, tester, 1512, 944, "停止")
}

// TestDockApprovalFirstWhenPending covers the dock order: a pending approval
// sits above the todo/goal docks so its decision is reachable without scrolling.
func TestDockApprovalFirstWhenPending(t *testing.T) {
	chat := dockGeometryChat(true)
	approvals := &fakeApprovals{}
	todo := &fakeTodoService{}
	goals := newFakeGoalService()
	fullDockStack(t, approvals, todo, goals)
	h := newDockGeometryHarness(t, chat, approvals, todo, goals)
	tester := h.tester()
	tester.SetSize(1512, 944)
	tester.Frame()
	tester.Frame()

	banner, okBanner := tester.Find("需要审批")
	todoTitle, okTodo := tester.Find("待办清单，已完成 1/3")
	goalBadge, okGoal := tester.Find("目标执行中")
	if !okBanner || !okTodo || !okGoal {
		t.Fatalf("dock parts missing; texts = %v", tester.Texts())
	}
	if !(banner.Y < todoTitle.Y && banner.Y < goalBadge.Y) {
		t.Fatalf("approval not first in the dock: banner.Y=%.1f todo.Y=%.1f goal.Y=%.1f",
			banner.Y, todoTitle.Y, goalBadge.Y)
	}
}

// TestApprovalCardMiddleBounded covers the card's own budget: the long diff
// scrolls inside the card (banner + footer fixed), so the whole card spans a
// bounded height instead of filling the window.
func TestApprovalCardMiddleBounded(t *testing.T) {
	chat := dockGeometryChat(true)
	approvals := &fakeApprovals{}
	fullDockStack(t, approvals, &fakeTodoService{}, newFakeGoalService())
	h := newDockGeometryHarness(t, chat, approvals, nil, nil)
	tester := h.tester()
	tester.SetSize(1512, 944)
	tester.Frame()
	tester.Frame()

	banner, okBanner := tester.Find("需要审批")
	// The approval card's positive action reads 允许一次 since the S4 rewrite
	// (B-spec §3.3.2: 拒绝 · 允许一次 · ⋯).
	allow, okAllow := tester.Find("允许一次")
	if !okBanner || !okAllow {
		t.Fatalf("approval card missing; texts = %v", tester.Texts())
	}
	span := allow.Y + allow.H - banner.Y
	// The demo's bounded card spans ≈240 DIP; allow a generous ceiling that
	// still proves the 681-DIP unbounded card is gone.
	if span > 360 {
		t.Fatalf("approval card spans %.1f DIP, want a bounded middle (≤360)", span)
	}
}
