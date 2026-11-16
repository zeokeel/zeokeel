package regression

// UX-03 end-to-end integration (2026-11 audit, wave F): the window-level Escape
// fallback (uiroot, wave D) delegates to the session module's stable
// session.escape command (wave C), so one Escape keeps one meaning regardless of
// where the focus sits. These tests activate the REAL session + approval +
// sidebar modules (not a stub command) and pin the audit's probe scenarios:
//
//   - B/B2: reading a child conversation, Esc from the sidebar focus (or with no
//     focus owner right after entering) returns to the parent — it never
//     blind-denies the parent's pending approval nor stops the hidden parent run.
//   - C: an open FindBar plus sidebar focus, Esc closes the bar and never stops
//     the running turn.
//   - idle cross-session: an idle conversation with a pending approval from
//     another session, Esc still denies it (the approval is answerable while idle).
//
// Promoted from audit/probes/ui/p3_window_esc_test.go into a product regression
// test so the contract stays guarded after the audit assets are retired.

import (
	"context"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	approvalui "github.com/zeokeel/zeokeel/internal/ui/approval"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
	sidebarui "github.com/zeokeel/zeokeel/internal/ui/sidebar"
	toastsui "github.com/zeokeel/zeokeel/internal/ui/toasts"
)

// escApprovals is a scripted contracts.Approvals recording deny decisions.
type escApprovals struct {
	mu      sync.Mutex
	pending []contracts.ApprovalRequest
	denied  []string
	subs    []func()
}

func (a *escApprovals) Mode() string { return "workspace" }
func (a *escApprovals) Request(context.Context, contracts.ApprovalRequest) (contracts.ApprovalDecision, error) {
	return contracts.ApprovalDeny, nil
}
func (a *escApprovals) Pending() []contracts.ApprovalRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]contracts.ApprovalRequest(nil), a.pending...)
}
func (a *escApprovals) Resolve(id string, decision contracts.ApprovalDecision) error {
	a.mu.Lock()
	if decision == contracts.ApprovalDeny {
		a.denied = append(a.denied, id)
	}
	kept := a.pending[:0]
	for _, request := range a.pending {
		if request.ID != id {
			kept = append(kept, request)
		}
	}
	a.pending = kept
	subs := append([]func(){}, a.subs...)
	a.mu.Unlock()
	for _, sub := range subs {
		sub()
	}
	return nil
}
func (a *escApprovals) Subscribe(fn func()) (func(), error) {
	if fn == nil {
		return func() {}, nil
	}
	a.mu.Lock()
	a.subs = append(a.subs, fn)
	a.mu.Unlock()
	return func() {}, nil
}
func (a *escApprovals) setPending(requests ...contracts.ApprovalRequest) {
	a.mu.Lock()
	a.pending = requests
	subs := append([]func(){}, a.subs...)
	a.mu.Unlock()
	for _, sub := range subs {
		sub()
	}
}
func (a *escApprovals) deniedCalls() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.denied...)
}

// escChildren is a scripted contracts.ChildAgentService with one one-shot child.
type escChildren struct {
	mu       sync.Mutex
	children []contracts.ChildAgent
}

func (c *escChildren) ChildAgents(parentSessionID string) []contracts.ChildAgent {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []contracts.ChildAgent
	for _, child := range c.children {
		if child.ParentSessionID == parentSessionID {
			out = append(out, child)
		}
	}
	return out
}
func (c *escChildren) ChildAgent(id string) (contracts.ChildAgent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, child := range c.children {
		if child.ID == id {
			return child, true
		}
	}
	return contracts.ChildAgent{}, false
}
func (c *escChildren) SendChildAgent(context.Context, string, string) (string, error) {
	return "", nil
}

// escE2EChat is one running conversation with a user turn and a streaming reply.
func escE2EChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "兜底会话", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "跑一下"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "正在跑", Streaming: true},
		},
	}, true)
	return chat
}

func escE2EApprovals() *escApprovals {
	approvals := &escApprovals{}
	approvals.setPending(contracts.ApprovalRequest{
		ID: "ap-1", Tool: "bash", SessionID: "sess-1", Summary: "rm -rf build",
	})
	return approvals
}

func escE2EChildren() *escChildren {
	return &escChildren{children: []contracts.ChildAgent{{
		ID: "child-1", ParentSessionID: "sess-1", Name: "目录审计员",
		Kind: "subagent", Mode: "one-shot", Status: "done",
		Prompt: "审计目录", Output: "审计完成",
	}}}
}

// newEscE2EHarness activates the real shell + session + approval + sidebar +
// toasts modules with the kernel context bound (the window fallback resolves
// KeyApprovals/KeyChat from it).
func newEscE2EHarness(t *testing.T, chat *fakeChat, approvals *escApprovals, children *escChildren) (*harness, *ui.Tester) {
	t.Helper()
	h := newBareHarness(t)
	h.ui.SetKernelContext(h.ctx)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	provide(t, h.ctx, contracts.KeyNotifier, contracts.Notifier(h.ui.Notifier()))
	if approvals != nil {
		provide(t, h.ctx, contracts.KeyApprovals, contracts.Approvals(approvals))
	}
	if children != nil {
		provide(t, h.ctx, contracts.KeyChildAgents, contracts.ChildAgentService(children))
	}
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	activate(t, h.ctx, toastsui.Module(), toastsui.Config{})
	if approvals != nil {
		activate(t, h.ctx, approvalui.Module(), approvalui.Config{})
	}
	activate(t, h.ctx, sidebarui.Module(), sidebarui.Config{})
	h.push(sessionui.ScreenID)
	tester := h.tester(1200, 800)
	tester.Frame()
	return h, tester
}

// clickSidebarRow moves the focus onto the sidebar's session row, outside the
// conversation screen's shortcut scope, so Esc reaches the window fallback.
func clickSidebarRow(t *testing.T, tester *ui.Tester, title string) {
	t.Helper()
	rect, ok := tester.Find(title)
	if !ok {
		t.Fatalf("sidebar row %q missing; texts = %v", title, tester.Texts())
	}
	tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Frame()
	tester.Frame()
}

// enterChildView opens the subagent switcher and enters the child conversation.
func enterChildView(t *testing.T, tester *ui.Tester) {
	t.Helper()
	if err := tester.Click("子智能体 1"); err != nil {
		t.Fatalf("open subagent switcher: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if err := tester.Click("进入子智能体会话 目录审计员"); err != nil {
		t.Fatalf("enter child view: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	tester.Frame()
}

// TestEscE2EChildFromSidebarReturnsToParent is UX-03 scenario B: reading a child
// conversation with the focus on the sidebar, Esc returns to the parent and
// never blind-denies the parent approval nor stops the hidden parent run.
func TestEscE2EChildFromSidebarReturnsToParent(t *testing.T) {
	chat := escE2EChat()
	approvals := escE2EApprovals()
	_, tester := newEscE2EHarness(t, chat, approvals, escE2EChildren())
	enterChildView(t, tester)
	clickSidebarRow(t, tester, "兜底会话")

	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.Frame()

	if got := approvals.deniedCalls(); len(got) != 0 {
		t.Fatalf("UX-03: sidebar-focus Esc blind-denied the parent approval in a child view: %v", got)
	}
	if got := chat.cancelCalls(); len(got) != 0 {
		t.Fatalf("UX-03: sidebar-focus Esc stopped the hidden parent run: %v", got)
	}
	if !tester.HasText("输入消息…") {
		t.Fatalf("UX-03: Esc did not return to the parent conversation; texts = %v", tester.Texts())
	}
}

// TestEscE2EChildAfterEnterNoFocusOwner is UX-03 scenario B2: right after
// entering a child view the switcher entry is replaced and the focus has no
// owner; Esc must still keep the screen semantics (no blind deny).
func TestEscE2EChildAfterEnterNoFocusOwner(t *testing.T) {
	chat := escE2EChat()
	approvals := escE2EApprovals()
	_, tester := newEscE2EHarness(t, chat, approvals, escE2EChildren())
	enterChildView(t, tester)

	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.Frame()

	if got := approvals.deniedCalls(); len(got) != 0 {
		t.Fatalf("UX-03: Esc right after entering a child view blind-denied the parent approval: %v", got)
	}
	if got := chat.cancelCalls(); len(got) != 0 {
		t.Fatalf("UX-03: Esc right after entering a child view stopped the parent run: %v", got)
	}
}

// TestEscE2EFindBarFromSidebarClosesBar is UX-03 scenario C: an open FindBar plus
// sidebar focus, Esc closes the bar and never stops the running turn.
func TestEscE2EFindBarFromSidebarClosesBar(t *testing.T) {
	chat := escE2EChat()
	_, tester := newEscE2EHarness(t, chat, nil, nil)

	tester.Key(ui.Cmd, ui.KeyF)
	tester.Frame()
	if !tester.HasText("Done") {
		t.Fatalf("FindBar did not open; texts = %v", tester.Texts())
	}
	tester.Type("跑")
	tester.Frame()

	clickSidebarRow(t, tester, "兜底会话")
	if !tester.HasText("Done") {
		t.Fatal("precondition broken: clicking the sidebar closed the FindBar")
	}

	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.Frame()

	if tester.HasText("Done") {
		t.Fatal("UX-03: sidebar-focus Esc did not close the open FindBar")
	}
	if got := chat.cancelCalls(); len(got) != 0 {
		t.Fatalf("UX-03: sidebar-focus Esc stopped the run instead of closing the bar: %v", got)
	}
}

// TestEscE2EIdleCrossSessionDenies covers the idle cross-session case: the active
// conversation is idle but a pending approval from another session is still
// answerable, so Esc from the sidebar denies it (the fallback/session command
// keeps the approval card's promise while idle).
func TestEscE2EIdleCrossSessionDenies(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "兜底会话", Running: false,
		Messages: []contracts.ChatMessage{{ID: "u1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	approvals := &escApprovals{}
	approvals.setPending(contracts.ApprovalRequest{
		ID: "ap-other", Tool: "bash", SessionID: "sess-2", Summary: "echo hi",
	})
	_, tester := newEscE2EHarness(t, chat, approvals, nil)
	requireHasText(t, tester, "需要审批")
	clickSidebarRow(t, tester, "兜底会话")

	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.Frame()

	if got := approvals.deniedCalls(); len(got) != 1 || got[0] != "ap-other" {
		t.Fatalf("idle cross-session Esc did not deny the pending approval: %v", got)
	}
	if got := chat.cancelCalls(); len(got) != 0 {
		t.Fatalf("idle Esc stopped a run that was not running: %v", got)
	}
}
