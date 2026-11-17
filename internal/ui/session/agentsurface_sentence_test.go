package session_test

// UD-05 / UD-15 (2026-11 audit): the agent surfaces' empty states and other
// full sentences never use the 11px micro-label step (TypeXXXS, 14 DIP line
// box). Sentences sit at ≥ the 12/18 metadata step — the shared uikit.EmptyState
// single-line form for empty states — while legitimate micro-labels (section
// headings, counts, status words, clocks, mono ID tokens) keep the 11px step.

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// assertSentenceStep requires the text's line box at ≥ the TypeXXS metadata
// step (18 DIP): a sentence is never the 14 DIP micro-label box.
func assertSentenceStep(t *testing.T, tester *ui.Tester, text string) {
	t.Helper()
	rect, ok := tester.Find(text)
	if !ok {
		t.Fatalf("sentence %q missing; texts = %v", text, tester.Texts())
	}
	_, line, _ := design.TypeMetrics(design.TypeXXS)
	if rect.H < line {
		t.Fatalf("UD-05: sentence %q line box = %.0f, want ≥%.0f (metadata step, not the 11px micro step)",
			text, rect.H, line)
	}
}

// assertMicroStep requires the text to stay on the TypeXXXS micro-label step:
// its line box remains below the 18 DIP metadata step (the rendered micro box
// is 14–15 DIP with layout rounding), so legitimate micro-labels are provably
// not bumped by the sentence fix.
func assertMicroStep(t *testing.T, tester *ui.Tester, text string) {
	t.Helper()
	rect, ok := tester.Find(text)
	if !ok {
		t.Fatalf("micro-label %q missing; texts = %v", text, tester.Texts())
	}
	_, sentenceLine, _ := design.TypeMetrics(design.TypeXXS)
	if rect.H >= sentenceLine {
		t.Fatalf("micro-label %q line box = %.0f, want the micro step (<%.0f) unchanged",
			text, rect.H, sentenceLine)
	}
}

// TestTeamEmptyStatesSentenceStep covers the team overlay's two empty states
// (no teammates, no shared tasks): both are sentences at the metadata step,
// while the section headings stay micro-labels.
func TestTeamEmptyStatesSentenceStep(t *testing.T) {
	h := newAgentSurfaceHarness(t, headerChat("空团队"), agentServices{
		team: &fakeTeam{}, board: &fakeAgentBoard{},
	})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "智能体团队")
	tester.Frame()

	assertSentenceStep(t, tester, "还没有队友")
	assertSentenceStep(t, tester, "还没有共享任务")
	assertMicroStep(t, tester, "成员")
	assertMicroStep(t, tester, "共享任务")
}

// TestTeamNoRunningTeammateSentenceStep covers the finished-only team: the 成员
// section's unified empty state ("还没有队友", finished rows live in 已结束) is a
// sentence, not a micro-label (2026-11 UI review P2).
func TestTeamNoRunningTeammateSentenceStep(t *testing.T) {
	h := newAgentSurfaceHarness(t, headerChat("已结束团队"), agentServices{
		team: &fakeTeam{members: []contracts.TeamMember{
			{ID: "m-old", Name: "旧队友", Historical: true},
		}},
		board: &fakeAgentBoard{},
	})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "智能体团队")
	tester.Frame()

	assertSentenceStep(t, tester, "还没有队友")
	// The restored record's status word stays a micro-label.
	assertMicroStep(t, tester, "历史 · 只读")
}

// TestTaskBoardDisabledSentenceStep covers the no-board overlay: "task board
// is not enabled" is a full sentence at the metadata step.
func TestTaskBoardDisabledSentenceStep(t *testing.T) {
	h := newAgentSurfaceHarness(t, headerChat("无任务板"), agentServices{
		team: &fakeTeam{}, // no board service
	})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "智能体团队")
	tester.Frame()

	assertSentenceStep(t, tester, "任务板未启用")
}

// TestWorkbenchEmptyAndHintSentenceStep covers the workbench: the member empty
// state and the Esc footer hint are sentences; the counted section heading
// stays a micro-label.
func TestWorkbenchEmptyAndHintSentenceStep(t *testing.T) {
	h := newAgentSurfaceHarness(t, headerChat("工作台"), agentServices{
		team: &fakeTeam{}, board: &fakeAgentBoard{},
	})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "智能体团队")
	h.click(tester, "打开工作台")
	tester.Frame()

	assertSentenceStep(t, tester, "还没有队友")
	assertSentenceStep(t, tester, "Esc 关闭工作台；团队浮层保持打开。")
	assertMicroStep(t, tester, "成员 · 0")
}

// childEmptyFixture is one idle continuable teammate with no output and no
// activity: the child view's honest empty states.
func childEmptyFixture(status string) *fakeChildAgents {
	return &fakeChildAgents{agents: []contracts.ChildAgent{{
		ID: "member-1", Name: "文档审查员", Kind: contracts.ChildAgentTeammate,
		Mode: contracts.ChildAgentContinuable, Status: status,
		Prompt: "核对文档",
	}}}
}

// TestChildEmptyOutputAndActivitySentenceStep covers the child view's output
// and activity empty states (idle and running variants): sentences at the
// metadata step, section headings unchanged.
func TestChildEmptyOutputAndActivitySentenceStep(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
		want   string
	}{
		{"idle", contracts.ChildAgentIdle, "还没有输出"},
		{"running", contracts.ChildAgentRunning, "正在运行…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newAgentSurfaceHarness(t, headerChat("子会话"), agentServices{
				children: childEmptyFixture(tc.status),
			})
			tester := h.tester()
			tester.Frame()
			h.click(tester, "子智能体 1")
			h.click(tester, "进入子智能体会话 文档审查员")
			tester.Frame()

			assertSentenceStep(t, tester, tc.want)
			assertSentenceStep(t, tester, "还没有活动记录")
			assertMicroStep(t, tester, "输出")
			assertMicroStep(t, tester, "活动")
		})
	}
}

// TestChildMemoryPaneSentenceStep covers the child's 记忆 tab: the honest
// "no memory panel" statement is a sentence at the metadata step.
func TestChildMemoryPaneSentenceStep(t *testing.T) {
	h := newAgentSurfaceHarness(t, headerChat("子会话记忆"), agentServices{
		children: childEmptyFixture(contracts.ChildAgentIdle),
	})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "子智能体 1")
	h.click(tester, "进入子智能体会话 文档审查员")
	h.click(tester, "记忆")
	tester.Frame()

	assertSentenceStep(t, tester, "子智能体会话没有记忆面板。")
}

// TestGeneratedFilesMoreLineSentenceStep covers the turn-tail file card's
// truncation notice: "N more files" is a count sentence at the metadata step,
// while the file name tokens keep the micro step.
func TestGeneratedFilesMoreLineSentenceStep(t *testing.T) {
	chat := newFakeChat()
	tools := make([]contracts.ToolCall, 0, 9)
	for index := 0; index < 9; index++ {
		tools = append(tools, contracts.ToolCall{
			ID: fmt.Sprintf("t%d", index), Name: "write_file", Status: "ok",
			Arguments: fmt.Sprintf(`{"path":"f%d.md","content":"# f%d"}`, index, index),
		})
	}
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "多文件回合",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "生成九个文件"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "九个文件已生成。", Tools: tools},
		},
	}, true)
	h := newAgentSurfaceHarness(t, chat, agentServices{})
	tester := h.tester()
	tester.Frame()

	assertSentenceStep(t, tester, "还有 1 个文件")
	assertMicroStep(t, tester, "f0.md")
}
