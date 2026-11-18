package regression

// UX-02 cross-wave integration (2026-11 audit, wave F): at the supported minimum
// window 900×600 (and the default 1512×944) an inline approval card with a long
// diff plus the goal/todo dock keeps a height budget — the composer and the
// approval footer stay inside the window and the transcript keeps some height
// (wave C bounded the card's middle section and the dock). Promoted from
// audit/probes/ui/p2_geometry_test.go into a product regression test.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	approvalui "github.com/zeokeel/zeokeel/internal/ui/approval"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
	toastsui "github.com/zeokeel/zeokeel/internal/ui/toasts"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// geoTodo is a scripted contracts.TodoService (three steps, mixed statuses).
type geoTodo struct{ items []contracts.TodoItem }

func (g *geoTodo) Todos() []contracts.TodoItem {
	return append([]contracts.TodoItem(nil), g.items...)
}

// geoLongWriteArguments is a write_file payload whose content hits the diff
// preview height cap (the audit's trigger condition).
func geoLongWriteArguments(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= 200; i++ {
		fmt.Fprintf(&b, "line %03d: the quick brown fox jumps over the lazy dog\n", i)
	}
	payload, err := json.Marshal(map[string]string{"path": "src/generated/output.go", "content": b.String()})
	if err != nil {
		t.Fatalf("marshal write_file arguments: %v", err)
	}
	return string(payload)
}

// geoChat is one running conversation with a non-empty transcript.
func geoChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "高度预算", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "帮我生成输出文件", CreatedAt: time.Unix(1700000000, 0)},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "好的，正在生成。", CreatedAt: time.Unix(1700000001, 0)},
			{ID: "a2", Role: contracts.ChatRoleAssistant, Text: "继续生成…", Streaming: true, CreatedAt: time.Unix(1700000002, 0)},
		},
	}, true)
	return chat
}

// newGeoHarness boots the session + approval + toasts modules with a long-diff
// write_file approval, a three-item todo dock and an active goal dock.
func newGeoHarness(t *testing.T) (*harness, *escApprovals) {
	t.Helper()
	chat := geoChat()
	approvals := &escApprovals{}
	approvals.setPending(contracts.ApprovalRequest{
		ID: "ap-long", Tool: "write_file", SessionID: "sess-1",
		Summary:   strings.Repeat("这是一段很长的审批摘要说明文字", 14),
		Arguments: geoLongWriteArguments(t),
	})
	todo := &geoTodo{items: []contracts.TodoItem{
		{ID: "t1", Content: "第一步：读取配置", Status: contracts.TodoStatusCompleted},
		{ID: "t2", Content: "第二步：生成输出", Status: contracts.TodoStatusInProgress},
		{ID: "t3", Content: "第三步：运行测试", Status: contracts.TodoStatusPending},
	}}
	goals := newRegressionGoalService()
	goals.seed("sess-1", contracts.GoalPhaseActive, contracts.GoalActivationArmed, 2, 0)

	h := newBareHarness(t)
	h.ui.SetKernelContext(h.ctx)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	provide(t, h.ctx, contracts.KeyApprovals, contracts.Approvals(approvals))
	provide(t, h.ctx, contracts.KeyTodo, contracts.TodoService(todo))
	provide(t, h.ctx, contracts.KeyGoal, contracts.GoalService(goals))
	provide(t, h.ctx, contracts.KeyNotifier, contracts.Notifier(h.ui.Notifier()))
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	activate(t, h.ctx, toastsui.Module(), toastsui.Config{})
	activate(t, h.ctx, approvalui.Module(), approvalui.Config{})
	h.push(sessionui.ScreenID)
	return h, approvals
}

// geoRectFits reports whether a rect sits fully inside the window vertically.
func geoRectFits(height int, rect ui.Rect) bool {
	return rect.Y >= 0 && rect.Y+rect.H <= float32(height)
}

// TestApprovalGeometryMinWindowFullDock is the decisive case: 900×600 with the
// full dock stack keeps the composer and approval footer reachable and the
// transcript above zero.
func TestApprovalGeometryMinWindowFullDock(t *testing.T) {
	h, _ := newGeoHarness(t)
	tester := h.tester(900, 600)
	tester.Frame()
	tester.Frame()
	assertGeoBudget(t, tester, 900, 600)
}

// TestApprovalGeometryDefaultWindowFullDock covers the default 1512×944 window
// with the same full stack (the audit found this overflowed too).
func TestApprovalGeometryDefaultWindowFullDock(t *testing.T) {
	h, _ := newGeoHarness(t)
	tester := h.tester(1512, 944)
	tester.Frame()
	tester.Frame()
	assertGeoBudget(t, tester, 1512, 944)
}

// assertGeoBudget checks the composer input/stop and the approval card's whole
// decision bar stay inside the window and the transcript list keeps a positive
// height. The decision bar is the B-spec-v2 §3.3.2 one: the scope sentence, Deny,
// the single accent Allow-once action and the scope menu, with the Esc and
// CmdOrCtrl+Enter key caps stated beside them.
func assertGeoBudget(t *testing.T, tester *ui.Tester, width, height int) {
	t.Helper()
	list, ok := tester.AccessByRole(ui.RoleList, "对话消息")
	if !ok {
		t.Fatalf("%dx%d: transcript list (对话消息) not accessible; texts = %v", width, height, tester.Texts())
	}
	if list.Bounds.H <= 0 {
		t.Fatalf("%dx%d: transcript squeezed to zero height (%.1f)", width, height, list.Bounds.H)
	}
	// The chord is drawn from the platform's notation (Cmd+Enter on macOS,
	// Ctrl+Enter elsewhere), so the expectation is formatted, not spelled.
	for _, label := range []string{
		"输入消息…", "停止",
		"范围：仅此一次", "拒绝", "允许一次", "更多",
		uikit.FormatShortcut("Esc"), uikit.FormatShortcut("CmdOrCtrl+Enter"),
	} {
		rect, found := tester.Find(label)
		if !found {
			t.Fatalf("%dx%d: %q missing from the frame; texts = %v", width, height, label, tester.Texts())
		}
		if !geoRectFits(height, rect) {
			t.Fatalf("%dx%d: %q outside the window: %+v (bottom=%.1f)", width, height, label, rect, rect.Y+rect.H)
		}
	}
	// The scope menu is the session-scoped allow's way in, so it must be on
	// screen too: the widest permission is never hidden behind an off-screen
	// control.
	if _, found := tester.Find("更多"); !found {
		t.Fatalf("%dx%d: the scope menu is missing; texts = %v", width, height, tester.Texts())
	}
}

// TestApprovalGeometryKeepsTheDecisionContract pins the safety semantics beside
// the geometry: the card has no Default action (a stray Enter decides nothing),
// exactly one accent primary, and Escape still resolves the oldest request as a
// deny (the window-level fallback the session screen owns).
func TestApprovalGeometryKeepsTheDecisionContract(t *testing.T) {
	h, approvals := newGeoHarness(t)
	tester := h.tester(900, 600)
	tester.Frame()
	tester.Frame()
	if !tester.HasText("需要审批") {
		t.Fatalf("the approval card did not render; texts = %v", tester.Texts())
	}
	// No action carries the keyboard: nothing is auto-focused, so Enter cannot
	// allow or deny.
	for _, label := range []string{"拒绝", "允许一次", "更多"} {
		if tester.Focused(label) {
			t.Fatalf("%q holds the initial focus; the card must not autofocus an action", label)
		}
	}
	// A stray Enter decides nothing: the request is still pending and nothing was
	// denied.
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if got := approvals.deniedCalls(); len(got) != 0 {
		t.Fatalf("a stray Enter denied %v; want no decision", got)
	}
	if pending := approvals.Pending(); len(pending) != 1 || pending[0].ID != "ap-long" {
		t.Fatalf("a stray Enter resolved the request (pending = %v); want it waiting", pending)
	}
	if !tester.HasText("需要审批") {
		t.Fatalf("a stray Enter dismissed the card; texts = %v", tester.Texts())
	}
	// Escape denies the oldest pending request, wherever the focus sits.
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.Frame()
	if got := approvals.deniedCalls(); len(got) != 1 || got[0] != "ap-long" {
		t.Fatalf("Escape denied %v, want [ap-long]", got)
	}
}
