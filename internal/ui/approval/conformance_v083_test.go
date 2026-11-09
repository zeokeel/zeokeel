package approval_test

// Conformance frame tests for the v0.8.3 DialogFooter migration of the approval
// dialog: the footer is the product's one action bar and its Default (Deny) is
// focused for every request, so Enter can never allow a tool by accident.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/approval"
)

// TestApprovalFooterDefaultDeniesEveryRequest pins the safe default across
// consecutive requests: the footer is keyed by request, so each new dialog gets
// fresh buttons and Deny takes the focus again even before any text is read.
func TestApprovalFooterDefaultDeniesEveryRequest(t *testing.T) {
	h := newHarness(t, approval.Config{})
	tester := h.tester()

	h.approvals.add(pendingRequest("r1", "bash", "第一个", `{"command":"ls"}`))
	tester.Frame()
	if !tester.Focused("拒绝") {
		t.Fatalf("first request: safe default focus is not on Deny; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if got := h.approvals.resolved(); len(got) != 1 || got[0] != contracts.ApprovalDeny {
		t.Fatalf("first request: decisions after Enter = %v, want [deny]", got)
	}

	// The next request must focus its own Deny too (fresh, keyed footer).
	h.approvals.add(pendingRequest("r2", "write_file", "第二个", `{"path":"x"}`))
	tester.Frame()
	h.assertTexts(tester, "第二个")
	if !tester.Focused("拒绝") {
		t.Fatalf("second request: safe default focus is not on Deny; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if got := h.approvals.resolved(); len(got) != 2 || got[1] != contracts.ApprovalDeny {
		t.Fatalf("second request: decisions after Enter = %v, want [deny deny]", got)
	}
}
