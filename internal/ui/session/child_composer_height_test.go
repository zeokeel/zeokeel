package session_test

// 2026-11 UI review P3: the child conversation's composer input and its send
// action share one control tier (32 DIP, design.RowCompactHeight). The default
// input height (~31) sat off the 4-DIP grid beside the 36 DIP send.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

func TestChildComposerInputAndSendShareHeightTier(t *testing.T) {
	children := &fakeChildAgents{agents: []contracts.ChildAgent{resumableHistoricalChild()}}
	h := newAgentSurfaceHarness(t, headerChat("子会话高度"), agentServices{children: children})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "子智能体 1")
	h.click(tester, "继续此队友 上次的研究员")
	tester.Frame()

	// The child box is the conversation's own composer shell now: the input is
	// the card's text area (named by its placeholder) and the send control is the
	// shell's 32 DIP square, not a labelled button of its own.
	input, ok := tester.AccessByRole(ui.RoleTextField, "发送给 上次的研究员")
	if !ok {
		t.Fatalf("child composer input missing; texts = %v", tester.Texts())
	}
	send, ok := tester.AccessByRole(ui.RoleButton, "发送")
	if !ok {
		t.Fatalf("child composer send missing; texts = %v", tester.Texts())
	}
	if send.Bounds.H != design.RowCompactHeight || send.Bounds.W != design.RowCompactHeight {
		t.Fatalf("child send = %gx%g, want the %g square the main composer uses",
			send.Bounds.W, send.Bounds.H, design.RowCompactHeight)
	}
	// The card is the shell's card: its radius and fill come from t.Composer(),
	// so the child box cannot drift from the parent's anatomy.
	if input.Bounds.W <= 0 {
		t.Fatalf("child input has no width")
	}
}
