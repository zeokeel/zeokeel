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
	h.assertTexts(tester, "目标", "会话操作", "运行中")
	h.assertNoTexts(tester, "暂停目标", "完成目标", "结束目标（保留记录）", "删除目标记录")

	// The goal now lives in the strip above the composer, not in the title row:
	// its text stays visible there (ellipsized, full text in the tooltip) and the
	// title keeps its own room.
	goalText := "一个非常长的目标描述，用来验证窄窗下标题仍然可读"
	h.assertTexts(tester, goalText)
	goalRect, ok := tester.Find(goalText)
	if !ok {
		t.Fatalf("goal strip missing; texts = %v", tester.Texts())
	}
	titleRect, ok = tester.Find("一个很长很长的会话标题用于窄窗测试")
	if !ok || goalRect.Y < titleRect.Y {
		t.Fatalf("goal at Y=%.1f is not below the title at Y=%.1f", goalRect.Y, titleRect.Y)
	}

	// At 1280 the same header shows the same two actions.
	tester.SetSize(1280, 800)
	tester.Frame()
	h.assertTexts(tester, "目标", "会话操作")
	h.assertNoTexts(tester, "暂停目标", "完成目标", "结束目标（保留记录）", "删除目标记录")
}

// TestMessageMetaRowIsResident covers H15 after the density refactor: every
// finished reply closes with one compact right-aligned meta row whose ⋯ menu
// carries the actions (copy is no longer a resident icon button, so the row
// stays one quiet line instead of a 32 DIP action strip).
func TestMessageMetaRowIsResident(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "动作行",
		Messages: []contracts.ChatMessage{
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "可以复制的回答"},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "消息操作")
	h.assertNoTexts(tester, "复制消息")
	// The ⋯ entry is a compact 24 DIP target, not the old 32 DIP strip.
	if rect, ok := tester.Find("消息操作"); !ok || rect.H < 20 || rect.H > 28 {
		t.Fatalf("message-actions target = %+v found %v, want the 24 DIP row entry", rect, ok)
	}
	h.click(tester, "消息操作")
	h.assertTexts(tester, "复制消息")
	h.click(tester, "复制消息")
	if got := tester.Clipboard(); got != "可以复制的回答" {
		t.Fatalf("clipboard = %q", got)
	}
}

// TestRegenerateLivesInMessageActionsMenu covers S-19 after the density
// refactor: regenerate is one item of the finished reply's ⋯ menu, not a
// separate icon button under the message.
func TestRegenerateLivesInMessageActionsMenu(t *testing.T) {
	chat := newExtendedChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "重生成",
		Messages: []contracts.ChatMessage{
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "回答"},
		},
	}, true)
	h := newCommandHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	if _, ok := tester.Find("重新生成"); ok {
		t.Fatalf("regenerate rendered as a resident control; texts = %v", tester.Texts())
	}
	h.click(tester, "消息操作")
	h.assertTexts(tester, "重新生成")
}

// TestRegenerateActionCallsBackend covers the ⋯ menu control's action.
func TestRegenerateActionCallsBackend(t *testing.T) {
	chat := newExtendedChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-regen", Title: "重生成",
		Messages: []contracts.ChatMessage{
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "回答"},
		},
	}, true)
	h := newCommandHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "消息操作")
	h.click(tester, "重新生成")
	_, calls := chat.calls()
	if len(calls) != 1 || calls[0] != "sess-regen" {
		t.Fatalf("Regenerate calls = %v, want [sess-regen]", calls)
	}
	// The meta row is rebuilt from the read model after the call; the menu stays
	// discoverable while the turn is idle.
	h.assertTexts(tester, "消息操作")
}
