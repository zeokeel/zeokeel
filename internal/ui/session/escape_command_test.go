package session_test

// UX-03 (2026-11 audit): the session screen registers the stable command
// session.escape with the unified Esc arbitration (find bar → child view →
// deny approval → dialogs → suggestion menu → stop run), so the window-level
// fallback (wave D) can call one command instead of duplicating the order.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// runSessionEscape finds and runs the stable escape command, reporting whether
// it is registered.
func runSessionEscape(t *testing.T, h *harness) bool {
	t.Helper()
	for _, cmd := range h.ui.Runtime().Commands() {
		if cmd.ID == "session.escape" {
			if cmd.Enabled != nil && !cmd.Enabled() {
				t.Fatalf("session.escape registered but disabled")
			}
			if err := cmd.Run(); err != nil {
				t.Fatalf("session.escape run: %v", err)
			}
			return true
		}
	}
	return false
}

// TestSessionEscapeCommandRegistered covers the stable command's existence: the
// window fallback can resolve it by ID.
func TestSessionEscapeCommandRegistered(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "命令",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	if !runSessionEscape(t, h) {
		t.Fatalf("session.escape command not registered; commands = %v", h.ui.Runtime().Commands())
	}
}

// TestSessionEscapeArbitrationOrder is the decisive order assertion: with the
// find bar open, a pending approval AND a running turn, successive escapes
// resolve one layer each — close the bar (run alive, approval untouched), deny
// the approval (run alive), then stop the run.
func TestSessionEscapeArbitrationOrder(t *testing.T) {
	chat := findbarChat()
	session, _ := chat.Session("sess-1")
	session.Running = true
	session.Messages = append(session.Messages, contracts.ChatMessage{
		ID: "a-run", Role: contracts.ChatRoleAssistant, Text: "正在跑", Streaming: true,
	})
	chat.updateSession(session)
	approvals := &fakeApprovals{}
	approvals.setPending(contracts.ApprovalRequest{ID: "ap-1", Tool: "bash", SessionID: "sess-1", Summary: "rm -rf build"})
	h := newApprovalHarness(t, chat, approvals)
	tester := h.tester()
	tester.Frame()
	openFindBar(t, h, tester)
	h.assertTexts(tester, "Find")

	// 1) The find bar is innermost: Esc closes it; nothing else happens.
	if !runSessionEscape(t, h) {
		t.Fatal("session.escape not registered")
	}
	tester.Frame()
	h.assertNoTexts(tester, "Find")
	if got := len(approvals.deniedCalls()); got != 0 {
		t.Fatalf("first escape denied the approval (want the find bar first): %v", approvals.deniedCalls())
	}
	if got := len(chat.cancels()); got != 0 {
		t.Fatalf("first escape stopped the run: %v", chat.cancels())
	}

	// 2) The pending approval owns the next Esc: deny it, keep the run alive.
	runSessionEscape(t, h)
	tester.Frame()
	if got := approvals.deniedCalls(); len(got) != 1 || got[0] != "ap-1" {
		t.Fatalf("second escape did not deny the approval: %v", got)
	}
	if got := len(chat.cancels()); got != 0 {
		t.Fatalf("second escape stopped the run: %v", chat.cancels())
	}

	// 3) Nothing left to answer: the last Esc stops the running turn.
	runSessionEscape(t, h)
	tester.Frame()
	if got := chat.cancels(); len(got) != 1 || got[0] != "sess-1" {
		t.Fatalf("third escape did not stop the run: %v", got)
	}
}

// TestSessionEscapeIdleDeniesCrossSessionApproval covers the idle branch: with
// no run active, a pending approval from another conversation is still denied
// by the unified escape (the window fallback's blind-deny fix).
func TestSessionEscapeIdleDeniesCrossSessionApproval(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "空闲",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	approvals := &fakeApprovals{}
	approvals.setPending(contracts.ApprovalRequest{ID: "ap-other", Tool: "bash", SessionID: "sess-2", Summary: "echo hi"})
	h := newApprovalHarness(t, chat, approvals)
	tester := h.tester()
	tester.Frame()

	runSessionEscape(t, h)
	tester.Frame()
	if got := approvals.deniedCalls(); len(got) != 1 || got[0] != "ap-other" {
		t.Fatalf("idle escape did not deny the cross-session approval: %v", got)
	}
}

// TestSessionEscapeDismissesSuggestionMenu covers the menu layer: an open slash
// menu is dismissed by the escape command without sending or stopping anything.
func TestSessionEscapeDismissesSuggestionMenu(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "菜单",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	h.typeSlash(tester)
	h.assertTexts(tester, "帮助") // the slash menu lists /help

	runSessionEscape(t, h)
	tester.Frame()
	h.assertNoTexts(tester, "帮助")
	if got := len(chat.sends()); got != 0 {
		t.Fatalf("escape sent the draft: %v", chat.sends())
	}
}
