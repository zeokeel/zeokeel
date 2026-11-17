package session_test

// 2026-11 UI review B7: the team overlay's member row and the child switcher's
// row name a run state with the same inline StatusText helper and vocabulary
// (uikit.RunStateLabel), not a tint badge — layer ② of the status display model.
// With the conversation idle there is no run-band badge, so the only "运行中" is
// the member/child row's inline status text.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestMemberAndChildStatusShareInlineVocabulary(t *testing.T) {
	team := &fakeTeam{members: []contracts.TeamMember{
		{ID: "m1", Name: "审查员", RunState: contracts.TeamRunRunning, SessionID: "sess-1"},
	}}
	children := &fakeChildAgents{agents: []contracts.ChildAgent{
		{ID: "m1", Name: "审查员", Kind: contracts.ChildAgentTeammate, Status: contracts.ChildAgentRunning},
	}}
	h := newAgentSurfaceHarness(t, headerChat("状态一致"), agentServices{
		team: team, children: children, board: &fakeAgentBoard{},
	})
	tester := h.tester()
	tester.Frame()

	// Team overlay: the running member's status is inline text (RoleText).
	h.click(tester, "智能体团队")
	if _, ok := tester.AccessByRole(ui.RoleText, "运行中"); !ok {
		t.Fatalf("the team member running status is not inline StatusText; texts = %v", tester.Texts())
	}

	// Child switcher: the same state reads the same word, also inline.
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	h.click(tester, "子智能体 1")
	if _, ok := tester.AccessByRole(ui.RoleText, "运行中"); !ok {
		t.Fatalf("the child running status is not inline StatusText; texts = %v", tester.Texts())
	}
}
