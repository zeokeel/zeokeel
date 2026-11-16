package session_test

// UX-17 (2026-11 audit, PM-12 extension): while idle with a pending approval
// (possibly from another conversation), the composer's key hint names the Esc
// semantics — Esc denies the approval — instead of only the send keys.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestIdleHintNamesEscDenyApproval covers the idle + cross-session approval
// hint: the visible key line says Esc 拒绝审批.
func TestIdleHintNamesEscDenyApproval(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "空闲审批",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	approvals := &fakeApprovals{}
	approvals.setPending(contracts.ApprovalRequest{
		ID: "ap-other", Tool: "bash", SessionID: "sess-2", Summary: "echo hi",
	})
	h := newApprovalHarness(t, chat, approvals)
	tester := h.tester()
	tester.Frame()
	tester.Frame()

	h.assertTexts(tester, "Esc 拒绝审批")
}

// TestIdleHintWithoutApprovalKeepsSendKeys is the control: no pending approval
// keeps the plain send hint (no Esc promise the idle screen would not keep).
func TestIdleHintWithoutApprovalKeepsSendKeys(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "空闲",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "Enter 发送 · Shift+Enter 换行")
	h.assertNoTexts(tester, "Esc 拒绝审批")
}
