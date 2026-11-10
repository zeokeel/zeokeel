package session_test

// Frame tests for the session-surface audit fixes (UI audit 2026-11-11):
// R4 running-composer key hints, R6 identifiable team task cards / child view
// raw IDs / activity wording, R7 the @ menu's no-workspace state with recovery,
// R15 zero-value header entries, and R18 pasting an image as an attachment.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
)

// --- R4: running composer key hints -----------------------------------------

// TestRunningComposerShowsKeyHint covers R4: while a turn runs the composer's
// hint names Enter (queue) and Esc (stop) instead of disappearing, and the stop
// control says the reply is interrupted.
func TestRunningComposerShowsKeyHint(t *testing.T) {
	h := newHarness(t, runningChat())
	tester := h.tester()
	h.assertTexts(tester, "Enter 排队 · Esc 停止")
	stop, ok := tester.Access("停止")
	if !ok {
		t.Fatalf("running composer has no stop control; texts = %v", tester.Texts())
	}
	if !strings.Contains(stop.Description, "会中断当前回复") {
		t.Fatalf("stop control description = %q, want the interruption consequence", stop.Description)
	}
}

// TestRunningComposerHintWithoutQueue covers the queueing-off variant: only Esc
// acts, so the hint says the stop interrupts the reply.
func TestRunningComposerHintWithoutQueue(t *testing.T) {
	h := newPermissionHarness(t, runningChat(), sendWhileRunningSettings(false))
	tester := h.tester()
	h.assertTexts(tester, "Esc 停止 · 会中断当前回复")
}

// --- R6: team task cards ----------------------------------------------------

// TestTeamTasksShowStableIDAndOwnerName covers R6: same-subject tasks stay
// distinguishable by their stable short ID, the board's pseudo owner renders as
// the lead agent, a member ID renders as the member's display name, and more
// than three tasks announce the scroll.
func TestTeamTasksShowStableIDAndOwnerName(t *testing.T) {
	team := &fakeTeam{members: []contracts.TeamMember{
		{ID: "member-1", Name: "文档审查员", RunState: contracts.TeamRunIdle},
	}}
	board := &fakeAgentBoard{tasks: []contracts.BoardTask{
		{ID: "task-1", Subject: "检查发布清单", Status: contracts.TaskStatusPending, Owner: contracts.TaskOwnerUnassigned},
		{ID: "task-2", Subject: "检查发布清单", Status: contracts.TaskStatusPending},
		{ID: "task-3", Subject: "补充说明", Status: contracts.TaskStatusInProgress, Owner: "member-1"},
		{ID: "task-4", Subject: "更新截图", Status: contracts.TaskStatusPending},
	}}
	h := newAgentSurfaceHarness(t, headerChat("团队会话"), agentServices{team: team, board: board})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "智能体团队")
	h.assertTexts(tester,
		"共享任务 · 4 项 · 可滚动",
		"task-1", "task-2", "task-3", "task-4",
		"负责人 主智能体", "负责人 文档审查员", "未指派",
	)
	// The two same-subject tasks are both visible (each card's own ID line tells
	// them apart; the menu item's label and text nodes repeat each subject).
	if got := exactTextCount(tester.Texts(), "检查发布清单"); got != 4 {
		t.Fatalf("same-subject tasks rendered %d subject nodes, want 4 (two cards); texts = %v", got, tester.Texts())
	}
	h.assertNoTexts(tester, "负责人 taskboard")
}

// TestChildViewHidesRawIDAndDuplicateCompletion covers R6 for the child view:
// the raw child ID moves into the session-ID tooltip, and a finish activity
// whose text already says 已完成 no longer repeats a separate 完成 word.
func TestChildViewHidesRawIDAndDuplicateCompletion(t *testing.T) {
	children := &fakeChildAgents{agents: []contracts.ChildAgent{{
		ID: "member-1", Name: "研究员", Kind: contracts.ChildAgentTeammate,
		Mode: contracts.ChildAgentContinuable, Status: contracts.ChildAgentCompleted,
		Prompt: "研究报告", Output: "检查完成",
		Activity: []contracts.ChildAgentActivity{{
			ID: "e1", Kind: contracts.TeamActivityFinish,
			Text: "「研究员」已完成：发布检查清单已创建",
			Time: time.Date(2026, 11, 11, 3, 35, 0, 0, time.Local),
		}},
	}}}
	h := newAgentSurfaceHarness(t, headerChat("父会话"), agentServices{children: children})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "子智能体 1")
	h.click(tester, "进入子智能体会话 研究员")
	h.assertTexts(tester, "「研究员」已完成：发布检查清单已创建")
	// The internal key is not visible copy any more.
	h.assertNoTexts(tester, "member-1")
	// The finish row no longer repeats 完成 next to its own 已完成 text.
	if got := exactTextCount(tester.Texts(), "完成"); got != 0 {
		t.Fatalf("activity row repeats the completion word %d times; texts = %v", got, tester.Texts())
	}
	// The ID stays reachable as the header line's session-ID tooltip.
	meta, ok := tester.Access("队友 · 可继续")
	if !ok {
		t.Fatalf("child header meta line missing; texts = %v", tester.Texts())
	}
	if !strings.Contains(meta.Description, "member-1") {
		t.Fatalf("child header description = %q, want the session ID in the tooltip", meta.Description)
	}
}

// --- R15: zero-value header entries ----------------------------------------

// TestZeroAgentEntriesStayCompactAtNarrowWidth covers the v0.2.20 correction:
// the team/subagent entries stay visible at zero and compact to icon+count on a
// narrow band, while the zero job entry (a notification, not a key feature)
// stays hidden; the conversation title keeps a readable measure.
func TestZeroAgentEntriesStayCompactAtNarrowWidth(t *testing.T) {
	h := newAgentSurfaceHarness(t, headerChat("窄窗的会话标题"), agentServices{
		team: &fakeTeam{}, children: &fakeChildAgents{}, jobs: &fakeJobs{},
	})
	tester := h.tester()
	tester.SetSize(800, 700)
	for index := 0; index < 3; index++ {
		tester.Frame()
	}
	h.assertTexts(tester, "智能体团队", "子智能体 0")
	h.assertNoTexts(tester, "个后台任务")
	title, ok := tester.Find("窄窗的会话标题")
	if !ok {
		t.Fatalf("narrow header lost the title; texts = %v", tester.Texts())
	}
	if title.W < 100 {
		t.Fatalf("narrow header title width = %.1f, want >= 100", title.W)
	}
}

// --- R7: @ menu no-workspace recovery --------------------------------------

// TestMentionNoWorkspaceIsLocalizedWithRecovery covers R7: the backend's English
// "no current workspace" error becomes the localized state, and the menu offers
// the 打开工作区 / 新建工作区 recovery actions.
func TestMentionNoWorkspaceIsLocalizedWithRecovery(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "无工作区"}, true)
	workspace := &fakeWorkspace{
		noCurrent:  true,
		workspaces: []contracts.Workspace{{ID: "ws-1", Name: "mygo", Root: "/tmp/mygo"}},
	}
	h := newWorkspaceHarness(t, chat, workspace)
	tester := h.tester()

	focusMentionComposer(t, h, tester)
	tester.Type("@")
	tester.Frame()
	h.assertTexts(tester, "当前会话没有工作区", "打开工作区", "新建工作区")
	h.assertNoTexts(tester, "no current workspace", "workspace:")

	// 打开工作区 lists the registered workspaces and opens the chosen one.
	h.click(tester, "打开工作区")
	h.assertTexts(tester, "mygo")
	h.click(tester, "mygo")
	tester.Frame()
	if len(workspace.opened) != 1 || workspace.opened[0] != "ws-1" {
		t.Fatalf("workspace.Open calls = %v, want [ws-1]", workspace.opened)
	}
	if workspace.noCurrent {
		t.Fatal("the chosen workspace did not become current")
	}
}

// TestMentionNoWorkspaceCreatesWorkspace covers the 新建工作区 recovery: the
// native picker seam returns a folder and the workspace is created from it.
func TestMentionNoWorkspaceCreatesWorkspace(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "无工作区会话"}, true)
	workspace := &fakeWorkspace{noCurrent: true}
	h := newWorkspaceHarness(t, chat, workspace)
	tester := h.tester()

	original := session.WorkspacePicker
	session.WorkspacePicker = func(string) (string, error) { return "/tmp/new-workspace", nil }
	t.Cleanup(func() { session.WorkspacePicker = original })

	focusMentionComposer(t, h, tester)
	tester.Type("@")
	tester.Frame()
	h.assertTexts(tester, "当前会话没有工作区", "新建工作区")
	h.click(tester, "新建工作区")
	waitFor(t, tester, func() bool { return len(workspace.created) == 1 }, "workspace create")
	if got := workspace.created[0]; got != "/tmp/new-workspace" {
		t.Fatalf("workspace.Create root = %q, want /tmp/new-workspace", got)
	}
}

// --- R18: pasting an image as an attachment --------------------------------

// TestPasteImageStagesAttachment covers R18: an image on the clipboard becomes a
// staged composer attachment through the ordinary attachment path, while a text
// paste keeps its native insert behavior.
func TestPasteImageStagesAttachment(t *testing.T) {
	chat := newAttachmentChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "粘贴图片"}, true)
	h := newAttachmentHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	tester.SetClipboardData(transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, tinyPNG))))
	tester.Key(ui.Cmd, ui.KeyV)
	tester.Frame()
	h.assertTexts(tester, "pasted-image-1.png")

	// A text paste still reaches the input normally (the hook only takes image
	// pastes, which arrive with empty text).
	tester.SetClipboard("粘贴的文字")
	tester.Key(ui.Cmd, ui.KeyV)
	tester.Frame()
	h.click(tester, "发送")
	tester.Frame()
	sends := chat.sentWithAttachments()
	if len(sends) != 1 || !strings.Contains(sends[0].prompt, "粘贴的文字") {
		t.Fatalf("SendWithAttachments calls = %+v, want the pasted text in the prompt", sends)
	}
}

// --- goal strip (V25) -------------------------------------------------------

// TestGoalStripClearEntryOnHover covers the tightened goal strip's clear entry:
// the resting strip is one quiet inline line, hovering reveals the clear control
// and it opens the shared destructive confirmation instead of clearing at once.
func TestGoalStripClearEntryOnHover(t *testing.T) {
	h, uiRoot := newGoalHarness(t, goalChat())
	tester := h.tester()
	tester.Frame()
	if _, err := uiRoot.GoalTracker().Set(context.Background(), "发布清单"); err != nil {
		t.Fatalf("Set goal: %v", err)
	}
	tester.Frame()
	if tester.HasText("清除目标") {
		t.Fatalf("the goal strip shows its clear entry at rest; texts = %v", tester.Texts())
	}

	// Move the pointer over the goal text: the clear entry appears.
	goals, ok := tester.Find("发布清单")
	if !ok {
		t.Fatalf("goal text missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(goals.X+goals.W/2, goals.Y+goals.H/2)
	tester.Frame()
	if !tester.HasText("清除目标") {
		t.Fatalf("hovered goal strip has no clear entry; texts = %v", tester.Texts())
	}
	h.click(tester, "清除目标")
	h.assertTexts(tester, "删除目标记录", "取消", "确认删除")
	h.click(tester, "取消")
	if _, _, _, ok := uiRoot.GoalTracker().Get(); !ok {
		t.Fatal("cancelling the clear confirmation removed the goal")
	}
}
