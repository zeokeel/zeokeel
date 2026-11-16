package session_test

// UD-04 / UD-07 (2026-11 audit): the agent surfaces' inline row actions use the
// metadata type step (TypeXXS 12/18 — button text is never the 11px micro step)
// and the compact control height (RowCompactHeight 32 — 24 is off the
// 32/36/40/44 ladder).

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// TestAgentRowActionTypeAndHeight covers the team overlay's 打开 action: the
// control sits on the 32 DIP compact rung and its text uses the 12/18 metadata
// step (the old 11px/24 pairing broke both rules).
func TestAgentRowActionTypeAndHeight(t *testing.T) {
	chat := headerChat("团队会话")
	team := &fakeTeam{members: []contracts.TeamMember{
		{ID: "member-1", Name: "文档审查员", RunState: contracts.TeamRunIdle, Model: "gpt-5-codex"},
	}}
	children := &fakeChildAgents{agents: []contracts.ChildAgent{{
		ID: "member-1", Name: "文档审查员", Kind: contracts.ChildAgentTeammate,
		Mode: contracts.ChildAgentContinuable, Status: contracts.ChildAgentIdle,
		Prompt: "核对文档", Output: "文档无误",
	}}}
	h := newAgentSurfaceHarness(t, chat, agentServices{
		team: team, children: children, board: &fakeAgentBoard{},
	})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "智能体团队")
	tester.Frame()

	button, ok := tester.Find("打开成员 文档审查员")
	if !ok {
		t.Fatalf("member open action missing; texts = %v", tester.Texts())
	}
	if button.H != design.RowCompactHeight {
		t.Fatalf("UD-07: row action height = %.0f, want RowCompactHeight %.0f", button.H, design.RowCompactHeight)
	}
	// The visible words sit at the metadata step: their line box is 18 DIP
	// (TypeXXS), never the 14 DIP micro-label box (TypeXXXS).
	label, ok := tester.Find("打开")
	if !ok {
		t.Fatalf("row action text missing; texts = %v", tester.Texts())
	}
	_, line, _ := design.TypeMetrics(design.TypeXXS)
	if label.H < line {
		t.Fatalf("UD-04: row action text line box = %.0f, want ≥%.0f (TypeXXS, not the 11px micro step)",
			label.H, line)
	}
}
