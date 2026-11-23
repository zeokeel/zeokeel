package uiroot

// Frame tests for the window-level Escape fallback (2026-11 wave D, UX-03/UX-12).
//
// UX-12: the fallback had zero frame tests; the audit's probes A/D/E are
// promoted here to product regression tests. The fallback keeps the approval
// card's "Esc 拒绝" promise when the focus sits outside the conversation's
// shortcut scope (the chrome, say): deny the oldest pending approval, else stop
// the running turn — but only on the conversation screen with no modal open.
//
// UX-03: the fallback must not blind-deny/stop when the session module can
// adjudicate Escape itself (child view returns to parent, FindBar closes). The
// module owns those semantics through the stable session.escape command; the
// fallback delegates to it when present and enabled, and only keeps the
// deny/stop promise itself when the command is absent or disabled. The
// child/FindBar end-to-end semantics live in the session module's command
// (wave C); these tests pin the host's delegation contract with a scripted
// command, plus the fallback's own deny/stop/no-op branches.

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
)

// escChat is a ChatService with one running conversation; it records the
// Cancel calls the fallback's stop branch makes.
type escChat struct {
	running bool
	cancels []string
}

func (c *escChat) Config() contracts.ChatConfig { return contracts.ChatConfig{} }
func (c *escChat) Sessions() []contracts.ChatSessionInfo {
	return []contracts.ChatSessionInfo{{ID: "sess-1", Title: "兜底会话", Running: c.running}}
}
func (c *escChat) Session(id string) (contracts.ChatSession, bool) {
	if id == "sess-1" {
		return contracts.ChatSession{ID: id, Title: "兜底会话", Running: c.running}, true
	}
	return contracts.ChatSession{}, false
}
func (c *escChat) ActiveSession() string         { return "sess-1" }
func (c *escChat) SetActiveSession(string) error { return nil }
func (c *escChat) NewSession(string) (string, error) {
	return "", nil
}
func (c *escChat) DeleteSession(string) error { return nil }
func (c *escChat) Send(string, string) error  { return nil }
func (c *escChat) Cancel(id string) error {
	c.cancels = append(c.cancels, id)
	return nil
}
func (c *escChat) Subscribe(func(string)) (func(), error) { return func() {}, nil }

// escApprovals is a minimal contracts.Approvals with a scripted pending set; it
// records the Resolve decisions the fallback's deny branch makes.
type escApprovals struct {
	pending []contracts.ApprovalRequest
	denied  []string
}

func (a *escApprovals) Mode() string { return "workspace" }
func (a *escApprovals) Request(context.Context, contracts.ApprovalRequest) (contracts.ApprovalDecision, error) {
	return contracts.ApprovalAllow, nil
}
func (a *escApprovals) Pending() []contracts.ApprovalRequest {
	return append([]contracts.ApprovalRequest(nil), a.pending...)
}
func (a *escApprovals) Resolve(id string, decision contracts.ApprovalDecision) error {
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
	return nil
}
func (a *escApprovals) Subscribe(func()) (func(), error) { return func() {}, nil }

// escHarness builds a root with a stub conversation screen (no screen-level
// Escape handler, so the key reaches the window fallback as it does when the
// focus sits on the chrome), an insights screen for the off-screen branch, the
// scripted chat/approvals backends bound through the kernel context, and the
// chrome focus moved off the conversation body.
type escHarness struct {
	root      *Root
	tester    *ui.Tester
	chat      *escChat
	approvals *escApprovals
}

func newEscHarness(t *testing.T, withApproval bool) *escHarness {
	t.Helper()
	chat := &escChat{running: true}
	approvals := &escApprovals{}
	if withApproval {
		approvals.pending = []contracts.ApprovalRequest{
			{ID: "ap-1", Tool: "bash", SessionID: "sess-1", Summary: "rm -rf build"},
		}
	}
	r := New(Config{AppName: "EscTest", Chat: chat})
	mustScreen(t, r, textScreen(sessionScreenID, "会话", "session-body", 20))
	mustScreen(t, r, textScreen("insights", "洞察", "insights-body", 30))
	kernel := cordis.Root()
	t.Cleanup(func() { _ = kernel.DisposeAll() })
	if _, err := kernel.Provide(contracts.KeyChat, contracts.ChatService(chat)); err != nil {
		t.Fatalf("provide chat: %v", err)
	}
	if _, err := kernel.Provide(contracts.KeyApprovals, contracts.Approvals(approvals)); err != nil {
		t.Fatalf("provide approvals: %v", err)
	}
	r.SetKernelContext(kernel)
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	// Move the focus onto the chrome (the sidebar collapse control), outside the
	// conversation screen's shortcut scope, as closing settings onto the sidebar
	// footer does. The stub screen registers no Escape handler, so the key falls
	// through to the window-level fallback.
	if err := tt.Click("折叠侧边栏"); err != nil {
		t.Fatalf("focus the chrome collapse control: %v; texts = %v", err, tt.Texts())
	}
	tt.Frame()
	return &escHarness{root: r, tester: tt, chat: chat, approvals: approvals}
}

// TestEscapeFallbackDeniesFromChromeFocus is UX-12 scenario A (the AP-24
// promise): focus outside the conversation, a pending approval → Esc denies the
// oldest request and does NOT stop the run.
func TestEscapeFallbackDeniesFromChromeFocus(t *testing.T) {
	h := newEscHarness(t, true)
	h.tester.Key(0, ui.KeyEscape)
	h.tester.Frame()
	h.tester.Frame()
	if got := h.approvals.denied; len(got) != 1 || got[0] != "ap-1" {
		t.Fatalf("window fallback did not deny the pending approval from chrome focus: %v", got)
	}
	if got := h.chat.cancels; len(got) != 0 {
		t.Fatalf("window fallback stopped the run while an approval was pending: %v", got)
	}
}

// TestEscapeFallbackStopsRunWithoutApproval pins the fallback's stop branch: no
// pending approval, a running turn, no session.escape command → Esc stops it.
func TestEscapeFallbackStopsRunWithoutApproval(t *testing.T) {
	h := newEscHarness(t, false)
	h.tester.Key(0, ui.KeyEscape)
	h.tester.Frame()
	h.tester.Frame()
	if got := h.chat.cancels; len(got) != 1 || got[0] != "sess-1" {
		t.Fatalf("window fallback did not stop the running turn: %v", got)
	}
}

// TestEscapeFallbackNoopOffSessionScreen is UX-12 scenario E: on a non-session
// screen Esc neither denies nor cancels (the fallback's nav guard).
func TestEscapeFallbackNoopOffSessionScreen(t *testing.T) {
	h := newEscHarness(t, true)
	if err := h.root.Navigator().Push("insights"); err != nil {
		t.Fatalf("Push(insights): %v", err)
	}
	h.tester.Frame()
	h.tester.Frame()
	h.tester.Key(0, ui.KeyEscape)
	h.tester.Frame()
	h.tester.Frame()
	if got := h.approvals.denied; len(got) != 0 {
		t.Fatalf("Esc on the insights screen denied an approval: %v", got)
	}
	if got := h.chat.cancels; len(got) != 0 {
		t.Fatalf("Esc on the insights screen stopped the run: %v", got)
	}
	if len(h.approvals.pending) != 1 {
		t.Fatalf("Esc on the insights screen resolved the approval (pending=%d)", len(h.approvals.pending))
	}
}

// TestEscapeFallbackModalOwnsEscape is UX-12 scenario D: an open modal consumes
// Escape in its own scope first; the fallback must not deny/stop behind it. The
// settings surface is the host's own modal.
func TestEscapeFallbackModalOwnsEscape(t *testing.T) {
	h := newEscHarness(t, true)
	mustPage(t, h.root, uinative.SettingsPageSpec{
		ID: "appearance", Section: "界面", Title: "外观", Order: 10,
		Build: func(c *ui.Context) { ui.Text(c, "settings-body") },
	})
	h.root.Navigator().ToggleSettings()
	h.tester.Frame()
	h.tester.Frame()
	if !h.root.Navigator().SettingsOpen() {
		t.Fatal("settings surface did not open")
	}
	h.tester.Key(0, ui.KeyEscape)
	h.tester.Frame()
	h.tester.Frame()
	if h.root.Navigator().SettingsOpen() {
		t.Fatal("Esc did not close the settings modal (the modal should own Escape)")
	}
	if got := h.approvals.denied; len(got) != 0 {
		t.Fatalf("Esc reached the fallback behind the settings modal and denied: %v", got)
	}
	if len(h.approvals.pending) != 1 {
		t.Fatalf("Esc behind the settings modal resolved the approval (pending=%d)", len(h.approvals.pending))
	}
}

// TestEscapeFallbackDelegatesToSessionEscape is UX-03: when the session module
// registers the stable session.escape command, the window fallback defers to it
// (the module adjudicates child-view/FindBar/menu semantics) instead of
// blind-denying the parent approval or stopping the hidden run.
func TestEscapeFallbackDelegatesToSessionEscape(t *testing.T) {
	h := newEscHarness(t, true)
	calls := 0
	mustCommand(t, h.root, uinative.CommandSpec{
		ID:    "session.escape",
		Title: "会话 Escape",
		Run:   func() error { calls++; return nil },
	})
	h.tester.Key(0, ui.KeyEscape)
	h.tester.Frame()
	h.tester.Frame()
	if calls != 1 {
		t.Fatalf("UX-03: the fallback did not delegate to session.escape (calls=%d)", calls)
	}
	if got := h.approvals.denied; len(got) != 0 {
		t.Fatalf("UX-03: the fallback blind-denied behind session.escape: %v", got)
	}
	if got := h.chat.cancels; len(got) != 0 {
		t.Fatalf("UX-03: the fallback stopped the run behind session.escape: %v", got)
	}
}

// TestEscapeFallbackDisabledSessionEscapeFallsBack pins the delegation guard: a
// disabled session.escape command cannot adjudicate, so the fallback keeps the
// safe-action promise itself (deny the pending approval).
func TestEscapeFallbackDisabledSessionEscapeFallsBack(t *testing.T) {
	h := newEscHarness(t, true)
	calls := 0
	mustCommand(t, h.root, uinative.CommandSpec{
		ID:      "session.escape",
		Title:   "会话 Escape",
		Enabled: func() bool { return false },
		Run:     func() error { calls++; return nil },
	})
	h.tester.Key(0, ui.KeyEscape)
	h.tester.Frame()
	h.tester.Frame()
	if calls != 0 {
		t.Fatalf("a disabled session.escape ran (calls=%d)", calls)
	}
	if got := h.approvals.denied; len(got) != 1 || got[0] != "ap-1" {
		t.Fatalf("the fallback did not deny when session.escape is disabled: %v", got)
	}
}
