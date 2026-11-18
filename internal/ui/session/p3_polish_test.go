package session_test

// TDD frame tests for the P3 polish round of the conversation screen:
//   - P3-6: a finished child's transcript receipt ends with an "open child
//     conversation" action (icon button, named for assistive technology), the
//     receipt body stays bounded to two lines and keeps its full text in the
//     tooltip;
//   - P3-7: the conversation ⋯ menu carries the goal entry at zero state (设置
//     目标) and the edit entry when a goal exists (编辑目标), without a
//     permanent Run Band chip;
//   - P3-8: the cold-resume child composer's disabled (empty-draft) send button
//     uses the neutral disabled-primary look, and a close activity row does not
//     print its kind word twice ("关闭 关闭「…」").

import (
	"image/color"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// --- P3-6 -------------------------------------------------------------------

// childReceiptNotice builds the derived receipt text the transcript renders for
// one finished child.
func childReceiptNotice(kind, name, output string) string {
	kindLabel := "子智能体"
	if kind == contracts.ChildAgentTeammate {
		kindLabel = "队友"
	}
	return kindLabel + "「" + name + "」已完成：" + output
}

// TestTranscriptChildReceiptOpensChildConversation covers P3-6 for both child
// families: the receipt row ends with the named open action, and clicking it
// opens the child's conversation in the main content area.
func TestTranscriptChildReceiptOpensChildConversation(t *testing.T) {
	cases := []struct {
		name  string
		agent contracts.ChildAgent
		// opened is the child-view marker the click must land on.
		opened string
	}{
		{
			name: "subagent",
			agent: contracts.ChildAgent{
				ID: "subagent-1", ParentSessionID: "sess-1", Name: "目录审计员",
				Kind: contracts.ChildAgentSubagent, Mode: contracts.ChildAgentOneShot,
				Status:   contracts.ChildAgentCompleted,
				Finished: time.Date(2026, 11, 15, 9, 30, 0, 0, time.Local),
				Prompt:   "检查目录结构", Output: "目录已检查完",
			},
			opened: "检查目录结构",
		},
		{
			name: "teammate",
			agent: contracts.ChildAgent{
				ID: "member-1", ParentSessionID: "sess-1", Name: "文档审查员",
				Kind: contracts.ChildAgentTeammate, Mode: contracts.ChildAgentContinuable,
				Status:   contracts.ChildAgentCompleted,
				Finished: time.Date(2026, 11, 15, 9, 31, 0, 0, time.Local),
				Prompt:   "核对文档", Output: "文档无误",
			},
			opened: "核对文档",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			chat := headerChat("发布会话")
			agent := test.agent
			children := &fakeChildAgents{agents: []contracts.ChildAgent{agent}}
			h := newAgentSurfaceHarness(t, chat, agentServices{children: children})
			tester := h.tester()
			tester.Frame()

			notice := childReceiptNotice(agent.Kind, agent.Name, agent.Output)
			h.assertTexts(tester, notice)

			// The row is bounded to two lines, so the tooltip must keep the whole
			// receipt readable for a pointer/assistive-technology user.
			if info, ok := tester.Access(notice); !ok || info.Description != notice {
				t.Fatalf("receipt tooltip = %q (found %t); want the full receipt text %q",
					info.Description, ok, notice)
			}

			open, ok := tester.AccessByRole(ui.RoleButton, "打开子会话")
			if !ok {
				t.Fatalf("the receipt has no open-child action; texts = %v", tester.Texts())
			}
			tester.ClickAt(open.Bounds.X+open.Bounds.W/2, open.Bounds.Y+open.Bounds.H/2)
			tester.Frame()

			h.assertTexts(tester, "发布会话", agent.Name, "任务", test.opened)
			h.assertNoTexts(tester, "帮我发布")
		})
	}
}

// TestTranscriptPlainNoticeHasNoOpenAction covers the boundary: only derived
// child receipts link to a child conversation; a plain notice keeps its quiet
// one-row look.
func TestTranscriptPlainNoticeHasNoOpenAction(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "通知", Messages: []contracts.ChatMessage{
		{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"},
		{ID: "notice-1", Role: contracts.ChatRoleNotice, Text: "普通通知"},
	}}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "普通通知")
	if _, ok := tester.AccessByRole(ui.RoleButton, "打开子会话"); ok {
		t.Fatalf("a plain notice got an open-child action; texts = %v", tester.Texts())
	}
}

// --- P3-7 -------------------------------------------------------------------

// TestConversationMenuSetGoalAtZeroState covers P3-7 without a goal: the ⋯ menu
// offers 设置目标 and opening it shows the goal dialog's create form.
func TestConversationMenuSetGoalAtZeroState(t *testing.T) {
	h, _ := newGoalHarness(t, goalChat())
	tester := h.tester()
	tester.Frame()

	openConversationActions(t, h, tester)
	h.assertTexts(tester, "设置目标")
	h.assertNoTexts(tester, "编辑目标")
	h.click(tester, "设置目标")
	tester.Frame()

	// The goal dialog's create form, not a stubbed entry: the objective input
	// and the explanatory copy are on screen, and no round-cap editor is (that
	// field belongs to editing an existing goal).
	h.assertTexts(tester, "目标内容", "为当前会话设置长期目标")
	h.assertNoTexts(tester, "自动续跑上限（轮）")
}

// TestConversationMenuEditGoalWithGoal covers P3-7 with a goal: the same entry
// reads 编辑目标 and opens the prefilled edit form.
func TestConversationMenuEditGoalWithGoal(t *testing.T) {
	h, goals := newGoalHarness(t, goalChat())
	goals.seed("sess-goal", "Ship release")
	tester := h.tester()
	tester.Frame()

	openConversationActions(t, h, tester)
	h.assertTexts(tester, "编辑目标")
	h.assertNoTexts(tester, "设置目标")
	h.click(tester, "编辑目标")
	tester.Frame()

	h.assertTexts(tester, "目标内容", "自动续跑上限（轮）")
	// The edit form is prefilled with the goal's objective.
	if input, ok := tester.Find("Ship release"); !ok {
		t.Fatalf("the edit form is not prefilled with the goal; texts = %v", tester.Texts())
	} else if input.W <= 0 {
		t.Fatalf("the prefilled goal has no width: %+v", input)
	}
}

// TestConversationMenuHasNoGoalEntryWithoutBackend covers the boundary: a
// product without a GoalService cannot set a goal, so the menu stays unchanged.
func TestConversationMenuHasNoGoalEntryWithoutBackend(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "无目标后端", Messages: []contracts.ChatMessage{
		{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"},
	}}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	openConversationActions(t, h, tester)
	h.assertNoTexts(tester, "设置目标", "编辑目标")
}

// --- P3-8 -------------------------------------------------------------------

// sendButtonFill samples the fill of the child composer's send button: the
// button's own accessible entry gives its bounds, and a point inside the
// padding between the edge and the glyph/text is the painted fill.
func sendButtonFill(t *testing.T, tester *ui.Tester, accessibleName string) color.RGBA {
	t.Helper()
	info, ok := tester.AccessByRole(ui.RoleButton, accessibleName)
	if !ok {
		t.Fatalf("send button %q has no accessible entry; texts = %v", accessibleName, tester.Texts())
	}
	img := tester.Image()
	return img.RGBAAt(int(info.Bounds.X)+3, int(info.Bounds.Y+info.Bounds.H/2))
}

// channelSpread is how far a color is from neutral gray: a neutral disabled
// fill stays small, the brand accent (royal blue) is large.
func channelSpread(c color.RGBA) int {
	maxChannel, minChannel := c.R, c.R
	for _, value := range []uint8{c.G, c.B} {
		if value > maxChannel {
			maxChannel = value
		}
		if value < minChannel {
			minChannel = value
		}
	}
	return int(maxChannel - minChannel)
}

// TestColdResumeEmptyDraftSendUsesDisabledLook covers P3-8: the cold-resume
// child composer's send button is the neutral disabled-primary look while the
// draft is empty (white on the old brand-blue wash was unreadable), and comes
// back as the accent primary once there is text to send.
func TestColdResumeEmptyDraftSendUsesDisabledLook(t *testing.T) {
	chat := headerChat("冷恢复会话")
	children := &fakeChildAgents{agents: []contracts.ChildAgent{resumableHistoricalChild()}}
	h := newAgentSurfaceHarness(t, chat, agentServices{children: children})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "子智能体 1")
	h.click(tester, "继续此队友 上次的研究员")
	tester.Frame()

	const label = "发送"
	if info, ok := tester.AccessByRole(ui.RoleButton, label); !ok || !info.Disabled {
		t.Fatalf("the empty-draft send button is not disabled; %+v", info)
	}
	if spread := channelSpread(sendButtonFill(t, tester, label)); spread > 12 {
		t.Fatalf("the disabled send button still paints the brand accent (channel spread %d); want the neutral disabled fill", spread)
	}

	// Once the draft has text the button is the enabled primary again.
	input, ok := tester.Find("发送给 上次的研究员")
	if !ok {
		t.Fatalf("cold-resume composer input missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(input.X+input.W/2, input.Y+input.H/2)
	tester.Frame()
	tester.Type("继续检查文档")
	tester.Frame()
	if spread := channelSpread(sendButtonFill(t, tester, label)); spread <= 40 {
		t.Fatalf("the enabled send button lost its accent fill (channel spread %d); want the brand primary", spread)
	}
}

// closeActivityChild is one child whose activity timeline ends with a close
// entry: the backend's close text already opens with 关闭, so the row must not
// print the kind word again.
func closeActivityChild() contracts.ChildAgent {
	return contracts.ChildAgent{
		ID: "member-1", ParentSessionID: "sess-1", Name: "文档审查员",
		Kind: contracts.ChildAgentTeammate, Mode: contracts.ChildAgentContinuable,
		Status: contracts.ChildAgentIdle, Prompt: "核对文档", Output: "文档无误",
		Activity: []contracts.ChildAgentActivity{{
			ID: "e1", Kind: contracts.TeamActivityClose, Text: "关闭「文档审查员」",
			Time: time.Date(2026, 11, 15, 9, 40, 0, 0, time.Local),
		}},
	}
}

// TestChildActivityCloseKindIsNotRepeated covers P3-8: the close activity row
// reads 关闭「队友」 once, not 关闭 关闭「队友」.
func TestChildActivityCloseKindIsNotRepeated(t *testing.T) {
	children := &fakeChildAgents{agents: []contracts.ChildAgent{closeActivityChild()}}
	h := newAgentSurfaceHarness(t, headerChat("活动去重"), agentServices{children: children})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "子智能体 1")
	h.click(tester, "进入子智能体会话 文档审查员")

	h.assertTexts(tester, "活动", "关闭「文档审查员」")
	h.assertNoExactTexts(tester, "关闭")
}
