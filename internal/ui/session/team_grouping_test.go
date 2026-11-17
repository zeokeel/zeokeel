package session_test

// 2026-11 UI review P2: the team overlay's 成员 section is the unfinished roster;
// finished members (restored history AND completed-but-not-closed live rows) sit
// in the separate 已结束 section. The meta counts match the sections: 名成员 =
// unfinished, 运行中 = runs in flight, 已结束 = finished. The 成员 empty state is
// the unified "还没有队友" whether or not finished rows exist.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestTeamOverlayFinishedMemberInFinishedSection(t *testing.T) {
	team := &fakeTeam{members: []contracts.TeamMember{
		{ID: "m-run", Name: "运行员", RunState: contracts.TeamRunRunning, SessionID: "sess-1"},
		// Completed but not closed: live (Historical=false) yet finished.
		{ID: "m-done", Name: "完成员", RunState: contracts.TeamRunDone, SessionID: "sess-1"},
	}}
	h := newAgentSurfaceHarness(t, headerChat("完成团队"), agentServices{
		team: team, board: &fakeAgentBoard{},
	})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "智能体团队")

	// The running member is the one 成员; the completed member is 已结束, and the
	// meta counts the unfinished roster (1), not the total (2).
	h.assertTexts(tester, "1 名成员 · 1 个运行中 · 1 条已结束",
		"成员", "运行员", "已结束", "完成员")
}

func TestTeamOverlayFinishedOnlyEmptyMembers(t *testing.T) {
	team := &fakeTeam{members: []contracts.TeamMember{
		{ID: "m-done", Name: "完成员", RunState: contracts.TeamRunDone, SessionID: "sess-1"},
	}}
	h := newAgentSurfaceHarness(t, headerChat("仅完成"), agentServices{
		team: team, board: &fakeAgentBoard{},
	})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "智能体团队")

	// No unfinished member: 成员 shows the unified empty state, the finished row
	// is in 已结束, and the meta reads 0 名成员 (not 1 over an empty section).
	h.assertTexts(tester, "0 名成员 · 0 个运行中 · 1 条已结束",
		"还没有队友", "已结束", "完成员")
	h.assertNoTexts(tester, "没有正在运行的队友")
}
