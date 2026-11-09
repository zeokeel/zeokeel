package session_test

// Frame tests for the session surface's layout fixes: the compact header, the
// composer's primary action at narrow widths and the hover-only message action
// row.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestComposerPrimaryActionVisibleWhenNarrow covers the composer fix: the
// send/stop control lives outside the toolbar's overflow, so it stays inside
// the window at the 1024 product width.
func TestComposerPrimaryActionVisibleWhenNarrow(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "窄窗测试"}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.SetSize(1024, 720)
	tester.Frame()

	sendRect, ok := tester.Find("发送")
	if !ok {
		t.Fatalf("send control missing at 1024; texts = %v", tester.Texts())
	}
	if sendRect.X <= 0 || sendRect.X+sendRect.W > 1024 {
		t.Fatalf("send control (x=%.1f w=%.1f) is not inside the 1024 window", sendRect.X, sendRect.W)
	}
	if sendRect.H < 20 {
		t.Fatalf("send control height = %.1f, want a visible primary button", sendRect.H)
	}
}

// TestHeaderKeepsTitleAtNarrowWidth covers the header fix: the title keeps its
// own width at 1024 with a long goal and a running badge, and the goal actions
// live in the chip's popover instead of four flat buttons.
func TestHeaderKeepsTitleAtNarrowWidth(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "一个很长很长的会话标题用于窄窗测试", Running: true,
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	goal := &pauseTracker{}
	h, tester := newGoalPauseHarness(t, chat, nil, goal)
	goal.setGoal("g1", "一个非常长的目标描述，用来验证窄窗下标题仍然可读", contracts.GoalStatusActive)
	tester.SetSize(1024, 720)
	tester.Frame()

	// Title still keeps a readable measure; the chip and the overflow menu are
	// the only header actions, and the goal buttons are not flat in the row.
	titleRect, ok := tester.Find("一个很长很长的会话标题用于窄窗测试")
	if !ok || titleRect.W < 120 {
		t.Fatalf("header title width = %.1f, want the title to keep its room", titleRect.W)
	}
	h.assertTexts(tester, "目标", "更多", "运行中")
	h.assertNoTexts(tester, "暂停目标", "完成目标", "结束目标（保留记录）", "删除目标记录")

	// The goal text shows truncated at a bounded measure (full text in the
	// chip's tooltip), so it stays visible without eating the title's room.
	goalText := "一个非常长的目标描述，用来验证窄窗下标题仍然可读"
	h.assertTexts(tester, goalText)
	if rect, ok := tester.Find(goalText); !ok || rect.W > 260 {
		t.Fatalf("goal text width = %.1f, want the truncated measure under 260", rect.W)
	}

	// At 1280 the same header shows the same two actions.
	tester.SetSize(1280, 800)
	tester.Frame()
	h.assertTexts(tester, "目标", "更多")
	h.assertNoTexts(tester, "暂停目标", "完成目标", "结束目标（保留记录）", "删除目标记录")
}

// TestMessageActionRowIsResident covers H15: the copy action is resident under
// every actionable assistant turn (no hover or pin needed) and lands on the
// native clipboard.
func TestMessageActionRowIsResident(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "动作行",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "问题"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "可以复制的回答"},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "复制消息")
	h.click(tester, "复制消息")
	if got := tester.Clipboard(); got != "可以复制的回答" {
		t.Fatalf("clipboard = %q", got)
	}
	// No rating backend in this harness: no ⋯ disclosure next to the copy.
	h.assertNoTexts(tester, "消息操作")
}

// TestMessageActionsEntryIsDiscoverable covers H15: the resident copy is always
// there and the ⋯ entry unfolds the rating actions (accessible by pointer or
// keyboard), so the actions never depend on hover.
func TestMessageActionsEntryIsDiscoverable(t *testing.T) {
	chat := feedbackSessionChat()
	h := newFeedbackHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "复制消息", "消息操作")
	h.assertNoTexts(tester, "有帮助", "没帮助")

	// ⋯ unfolds the rating actions; choosing one reports through the backend.
	h.click(tester, "消息操作")
	h.assertTexts(tester, "有帮助", "没帮助")
	h.click(tester, "有帮助")
	if call := chat.lastCall(t); call.messageID != "msg-assistant" || call.feedback != contracts.FeedbackGood {
		t.Fatalf("feedback call = %+v, want the good rating", call)
	}
	h.assertNoTexts(tester, "已反馈")

	// ⋯ collapses the rating actions again; copy stays resident.
	h.click(tester, "消息操作")
	h.assertNoTexts(tester, "有帮助", "没帮助")
	h.assertTexts(tester, "复制消息")
	h.click(tester, "复制消息")
	if got := tester.Clipboard(); got != "an answer" {
		t.Fatalf("clipboard = %q, want the assistant text", got)
	}
}

// TestRegenerateLivesInMessageActionRow covers S-19: regenerate moved from a
// heavy secondary button under the message into the finished reply's action
// row, as a 32x32 icon action aligned with copy.
func TestRegenerateLivesInMessageActionRow(t *testing.T) {
	chat := newExtendedChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "重生成",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "问题"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "回答"},
		},
	}, true)
	h := newCommandHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	regenerate, ok := tester.Find("重新生成")
	if !ok {
		t.Fatalf("regenerate action missing; texts = %v", tester.Texts())
	}
	copyAction, ok := tester.Find("复制消息")
	if !ok {
		t.Fatalf("copy action missing; texts = %v", tester.Texts())
	}
	if regenerate.W > 48 || regenerate.H > 48 {
		t.Fatalf("regenerate target = %.0fx%.0f, want an icon action", regenerate.W, regenerate.H)
	}
	// Same action row as copy: same height and vertically overlapping band.
	if regenerate.Y+regenerate.H < copyAction.Y || regenerate.Y > copyAction.Y+copyAction.H {
		t.Fatalf("regenerate at %+v is not in the copy action row %+v", regenerate, copyAction)
	}
	if regenerate.X < 600 {
		t.Fatalf("regenerate x = %.1f, want it right-aligned with the message actions", regenerate.X)
	}
}

// TestRegenerateActionCallsBackend covers the action-row control's action.
func TestRegenerateActionCallsBackend(t *testing.T) {
	chat := newExtendedChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-regen", Title: "重生成",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "问题"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "回答"},
		},
	}, true)
	h := newCommandHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "重新生成")
	_, calls := chat.calls()
	if len(calls) != 1 || calls[0] != "sess-regen" {
		t.Fatalf("Regenerate calls = %v, want [sess-regen]", calls)
	}
	// The action row is rebuilt from the read model after the call; the control
	// stays discoverable while the turn is idle.
	if _, ok := tester.Find("重新生成"); !ok {
		t.Fatalf("regenerate action disappeared; texts = %v", tester.Texts())
	}
}
