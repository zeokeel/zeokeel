package session_test

// 2026-11 UI review P2: at 1024 wide with the right inspector docked, the session
// header title must stay readable (≥8 glyphs, never a single "…") while the
// metadata entries (workspace/team/subagents/jobs) compact to icon+count instead
// of squeezing the title out. The title keeps a permanent readable floor because
// st.headerWidth lags one frame: on the frame the inspector docks the band is
// already narrow while the measured width still reads wide.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
)

func TestNarrowHeaderInspectorTitleReadable(t *testing.T) {
	const title = "副本 - 看看当前工作目录，然后告诉我"
	chat := headerChat(title)
	services := agentServices{
		team: &fakeTeam{members: []contracts.TeamMember{
			{ID: "member-1", Name: "文档审查员", RunState: contracts.TeamRunRunning},
		}},
		children: &fakeChildAgents{agents: []contracts.ChildAgent{
			{ID: "subagent-1", Name: "目录审计员", Kind: contracts.ChildAgentSubagent},
		}},
		jobs: &fakeJobs{jobs: []contracts.BackgroundJob{
			{ID: "job-1", Command: "npm test", State: "running", Running: true},
		}},
	}
	h := newAgentSurfaceHarness(t, chat, services)
	tester := h.tester()
	if _, err := h.ui.Runtime().RegisterSlotItem(uinative.SlotItem{
		Slot: contracts.SlotSessionPanelRight, ID: "test.inspector", Order: 1,
		Build: func(c *ui.Context) { ui.Text(c, "检查器内容") },
	}); err != nil {
		t.Fatalf("register inspector slot: %v", err)
	}
	tester.SetSize(1024, 720)
	tester.Frame()
	h.ui.SetRightPanelOpen(true)

	// The docking frame itself: headerWidth still reads the previous (wide) frame,
	// yet the title must not collapse to "…".
	tester.Frame()
	assertNarrowHeaderTitle(t, tester, title)

	// Settled: the metadata entries compact to icon+count and nothing overflows.
	for i := 0; i < 4; i++ {
		tester.Frame()
	}
	assertNarrowHeaderTitle(t, tester, title)
	h.assertTexts(tester, "检查器内容")

	// The orchestration entries stay discoverable (their accessible labels) and
	// stay inside the window — compact, not pushed off the right edge.
	for _, label := range []string{"智能体团队", "子智能体 1"} {
		info, ok := tester.AccessByRole(ui.RoleButton, label)
		if !ok {
			t.Fatalf("header entry %q missing; texts = %v", label, tester.Texts())
		}
		if right := info.Bounds.X + info.Bounds.W; right > 1024.5 {
			t.Fatalf("header entry %q overflows the window: right=%.1f", label, right)
		}
	}
}

// assertNarrowHeaderTitle pins the readable floor: the full title text is present
// (not ellipsized away) and its box keeps at least eight TypeM glyphs of room.
func assertNarrowHeaderTitle(t *testing.T, tester *ui.Tester, title string) {
	t.Helper()
	rect, ok := tester.Find(title)
	if !ok {
		t.Fatalf("the session title collapsed (not found as text); texts = %v", tester.Texts())
	}
	if rect.W < 128 {
		t.Fatalf("the session title spans %.1f DIP, want the ≥8-glyph readable floor (≥128)", rect.W)
	}
	if tester.HasText("…") && rect.W < 16 {
		t.Fatalf("the session title is a single ellipsis: %+v", rect)
	}
}
