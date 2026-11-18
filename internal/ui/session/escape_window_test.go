package session_test

// The conversation's Escape is a window shortcut (A4: K7/K8). The promise the
// composer hint makes — "Esc 停止", "Esc 拒绝审批" — must hold wherever the
// keyboard is: the sidebar, the top band, the composer, or no element at all.
// A handler scoped to the content pane only fires inside it, which is how a
// focus in the sidebar used to swallow the first Escape.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// escapeChat is a running conversation with one user message.
func escapeChat() *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "Esc 跨区", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "正在跑", Streaming: true},
		},
	}, true)
	return chat
}

// TestSessionEscapeStopsRunFromSidebarFocus covers the window-wide route: the
// keyboard focus sits on the shell's sidebar toggle (outside the content pane),
// and one Escape still stops the running turn.
func TestSessionEscapeStopsRunFromSidebarFocus(t *testing.T) {
	chat := escapeChat()
	h := newHarness(t, chat)
	tester := h.tester()

	// Tab until the sidebar's collapse control owns the keyboard: the point is
	// that the focus is outside the conversation's own subtree.
	focused := false
	for i := 0; i < 8 && !focused; i++ {
		tester.Key(0, ui.KeyTab)
		tester.Frame()
		focused = tester.Focused("折叠侧边栏")
	}
	if !focused {
		t.Fatalf("could not move the focus into the sidebar; texts = %v", tester.Texts())
	}

	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if cancels := chat.cancels(); len(cancels) != 1 || cancels[0] != "sess-1" {
		t.Fatalf("Esc from the sidebar did not stop the run: cancels = %v", cancels)
	}
}

// TestSessionEscapeDeniesApprovalFromBandFocus covers the same route for the
// approval promise: with the focus outside the conversation (the shell's sidebar
// toggle), one Escape denies the oldest pending request.
func TestSessionEscapeDeniesApprovalFromBandFocus(t *testing.T) {
	chat := escapeChat()
	approvals := &fakeApprovals{}
	approvals.setPending(contracts.ApprovalRequest{
		ID: "req-1", SessionID: "sess-1", Tool: "bash", Summary: "go build ./...",
	})
	h := newApprovalHarness(t, chat, approvals)
	tester := h.tester()
	tester.Frame()

	// The focus goes to the shell's sidebar toggle, outside the conversation's
	// own subtree. (A band button with a tooltip is deliberately not used: a
	// keyboard-raised tooltip is an overlay and takes the first Escape in mygo,
	// A4-I-01, which would test the tooltip rather than the Escape route.)
	focused := false
	for i := 0; i < 8 && !focused; i++ {
		tester.Key(0, ui.KeyTab)
		tester.Frame()
		focused = tester.Focused("折叠侧边栏")
	}
	if !focused {
		t.Fatalf("could not move the focus into the sidebar; texts = %v", tester.Texts())
	}

	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if denied := approvals.deniedCalls(); len(denied) != 1 || denied[0] != "req-1" {
		t.Fatalf("Esc outside the conversation did not deny the approval: denied = %v", denied)
	}
}
