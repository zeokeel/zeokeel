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

	input, ok := tester.AccessByRole(ui.RoleTextField, "发送给 上次的研究员")
	if !ok {
		t.Fatalf("child composer input missing; texts = %v", tester.Texts())
	}
	send, ok := tester.AccessByRole(ui.RoleButton, "发送消息给 上次的研究员")
	if !ok {
		t.Fatalf("child composer send missing; texts = %v", tester.Texts())
	}
	if input.Bounds.H != design.RowCompactHeight {
		t.Fatalf("child input height = %g, want the %g control tier", input.Bounds.H, design.RowCompactHeight)
	}
	if send.Bounds.H != design.RowCompactHeight {
		t.Fatalf("child send height = %g, want the %g control tier", send.Bounds.H, design.RowCompactHeight)
	}
	if input.Bounds.H != send.Bounds.H {
		t.Fatalf("input (%g) and send (%g) heights differ", input.Bounds.H, send.Bounds.H)
	}
}
