package session_test

// Frame test for the Memory tab's bottom dock (p2-memory audit): the add row is
// the tab's control, and the host's toast stack must clear it — not only the
// composer it happens to sit above.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestMemoryTabReportsItsAddRowToTheToastAnchor covers contracts.ReportDockTop
// on the Memory tab: the reported top is the add row's own top, which sits above
// the composer, so the toast stack lands above both.
func TestMemoryTabReportsItsAddRowToTheToastAnchor(t *testing.T) {
	store := newFakeMemoryStore()
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "记忆会话",
		Messages: []contracts.ChatMessage{{ID: "u1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	h := newHarnessWithMemory(t, chat, store)
	anchor := &fakeToastAnchor{}
	provide[contracts.ToastAnchorService](t, h.ctx, contracts.KeyToastAnchor, anchor)
	tester := h.tester()
	h.click(tester, "记忆")
	tester.Frame()
	tester.Frame()

	add, ok := tester.Find("添加记忆")
	if !ok {
		t.Fatalf("the memory add action is missing; texts = %v", tester.Texts())
	}
	if anchor.dockTop <= 0 {
		t.Fatalf("the Memory tab reported no dock top")
	}
	// The reported line is at or above the add button: the toast stack hangs
	// above it, never on top of the control.
	if anchor.dockTop > add.Y+1 {
		t.Fatalf("reported dock top %.1f is below the add action at %.1f; the toast would cover it",
			anchor.dockTop, add.Y)
	}
	// And it is the add row, not the composer far below it.
	send, okSend := tester.Find("发送")
	if !okSend {
		t.Fatalf("composer send missing; texts = %v", tester.Texts())
	}
	if anchor.dockTop > add.Y && anchor.dockTop == send.Y {
		t.Fatalf("reported dock top is the composer's, not the add row's")
	}
	if add.Y >= send.Y {
		t.Fatalf("layout: the add action at %.1f should sit above the composer send at %.1f", add.Y, send.Y)
	}
}
