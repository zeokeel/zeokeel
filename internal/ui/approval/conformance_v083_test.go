package approval_test

// Conformance frame tests for the v0.8.3 DialogFooter migration of the approval
// dialog: the footer is the product's one action bar and it deliberately has NO
// default action — nothing is auto-focused, so a stray Enter (the user often
// presses it right after sending a prompt) decides nothing. Esc remains the
// explicit deny shortcut.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/approval"
)

// TestApprovalFooterHasNoDefaultAction pins the safe-decision contract across
// consecutive requests: the footer is keyed by request, each new dialog renders
// fresh buttons, no button is auto-focused and Enter alone never resolves.
func TestApprovalFooterHasNoDefaultAction(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()

	h.approvals.add(pendingRequest("r1", "bash", "第一个", `{"command":"ls"}`))
	tester.Frame()
	if tester.Focused("拒绝") || tester.Focused("允许") || tester.Focused("本会话内始终允许") {
		t.Fatalf("first request: an action was auto-focused; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if got := h.approvals.resolved(); len(got) != 0 {
		t.Fatalf("first request: decisions after Enter = %v, want none", got)
	}
	h.assertTexts(tester, "需要审批")
	h.click(tester, "拒绝")
	if got := h.approvals.resolved(); len(got) != 1 || got[0] != contracts.ApprovalDeny {
		t.Fatalf("first request: decisions after clicking Deny = %v, want [deny]", got)
	}

	// The next request renders its own footer with the same no-default rule.
	h.approvals.add(pendingRequest("r2", "write_file", "第二个", `{"path":"x"}`))
	tester.Frame()
	h.assertTexts(tester, "第二个")
	if tester.Focused("拒绝") || tester.Focused("允许") {
		t.Fatalf("second request: an action was auto-focused; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if got := h.approvals.resolved(); len(got) != 1 {
		t.Fatalf("second request: decisions after Enter = %v, want still 1", got)
	}
	h.assertTexts(tester, "需要审批")
	h.click(tester, "允许")
	if got := h.approvals.resolved(); len(got) != 2 || got[1] != contracts.ApprovalAllow {
		t.Fatalf("second request: decisions after clicking Allow = %v, want [deny allow]", got)
	}
}
