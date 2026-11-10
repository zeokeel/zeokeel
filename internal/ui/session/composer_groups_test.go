package session_test

// Tests for the H7 grouped/adaptive composer toolbar and the H8 anchored
// slash/@ suggestion overlays.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func groupsChat() *fakeChat {
	chat := newFakeChat()
	// A configured model keeps the per-model thinking section of the session
	// menu visible (it is hidden when no model is configured).
	chat.config = contracts.ChatConfig{Model: "group-model"}
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "分组测试",
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "问题"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "回答"},
		},
	}, true)
	return chat
}

// TestComposerToolbarGroupsLowFrequencyControls covers H7: the prompt presets
// (and the other low-frequency choices) live in the 会话设置 group instead of
// separate chips, while the reasoning strength lives one level down in the model
// chip's 推理等级 entry.
func TestComposerToolbarGroupsLowFrequencyControls(t *testing.T) {
	settings := newPermissionSettings(nil)
	h := newPermissionHarness(t, groupsChat(), settings)
	tester := h.tester()

	h.assertTexts(tester, "会话设置")
	h.assertNoTexts(tester, "推理：默认", "预设")
	h.openSessionMenu(tester)
	h.assertTexts(tester, "权限模式", "对话模式", "插入预设", "打开行为设置…")
	// Reasoning is not repeated in this menu: the model chip owns it.
	h.assertNoTexts(tester, "推理等级")
	// The preset list grows with user data: it is one submenu deeper so the
	// outer menu fits a narrow window.
	h.click(tester, "插入预设")
	h.assertTexts(tester, "简洁回答", "代码审查", "逐步推理")
}

// TestComposerToolbarCompactsAtNarrowWidths covers the calm composer row at
// 900/1024: one attach menu, the model chip, one 会话设置 menu and the fixed
// send control; the standalone slash entry and the old adaptive chips are gone,
// so no unlabeled » overflow is needed.
func TestComposerToolbarCompactsAtNarrowWidths(t *testing.T) {
	for _, width := range []int{900, 1024} {
		settings := newPermissionSettings(nil)
		h := newPermissionHarness(t, groupsChat(), settings)
		tester := h.tester()
		tester.SetSize(width, 800)
		// The adaptive group reads the composer's last laid-out width.
		tester.Frame()
		tester.Frame()

		h.assertTexts(tester, "会话设置", "附件", "发送")
		h.assertNoTexts(tester, "命令", "更多工具", "路径", "询问", "»")
		attach, ok := tester.Find("附件")
		if !ok {
			t.Fatalf("%d: attach control missing; texts = %v", width, tester.Texts())
		}
		if attach.X < 0 || attach.X+attach.W > float32(width) {
			t.Fatalf("%d: attach control at %.1f..%.1f escaped the window", width, attach.X, attach.X+attach.W)
		}
		// The single session menu carries every low-frequency choice.
		h.openSessionMenu(tester)
		h.assertTexts(tester, "权限模式", "只读", "工作区可写", "所有权限", "对话模式", "插入预设", "打开行为设置…")
		h.assertNoTexts(tester, "询问", "从不允许", "推理等级")
	}
}

// TestSlashMenuIsAnchoredOverlay covers H8: the slash menu is an overlay above
// the composer and no longer pushes the transcript layout.
func TestSlashMenuIsAnchoredOverlay(t *testing.T) {
	chat := groupsChat()
	h := newHarness(t, chat)
	registerTestCommands(t, h, func() {})
	tester := h.tester()
	focusComposer(t, tester)
	messageBefore, ok := tester.Find("回答")
	if !ok {
		t.Fatalf("assistant text missing; texts = %v", tester.Texts())
	}

	tester.Type("/")
	tester.Frame()
	h.assertTexts(tester, "命令菜单", "打开偏好设置")
	messageAfter, ok := tester.Find("回答")
	if !ok {
		t.Fatalf("assistant text vanished; texts = %v", tester.Texts())
	}
	if messageBefore.Y != messageAfter.Y {
		t.Fatalf("slash menu moved the transcript: Y %0.1f → %0.1f", messageBefore.Y, messageAfter.Y)
	}

	// The menu sits above the composer, not in the layout flow.
	heading, _ := tester.Find("打开偏好设置")
	composer, _ := tester.Find("输入消息…")
	if heading.Y+heading.H > composer.Y {
		t.Fatalf("slash menu at %.1f..%.1f overlaps or sits below the composer at %.1f",
			heading.Y, heading.Y+heading.H, composer.Y)
	}
}

// TestMentionMenuIsAnchoredOverlay covers H8 for @: the reference menu is an
// overlay above the composer and keeps the transcript in place.
func TestMentionMenuIsAnchoredOverlay(t *testing.T) {
	chat := groupsChat()
	chat.addSession(contracts.ChatSession{ID: "sess-2", Title: "另一个会话"}, false)
	h := newHarness(t, chat)
	tester := h.tester()
	focusComposer(t, tester)
	messageBefore, _ := tester.Find("回答")

	tester.Type("@")
	tester.Frame()
	h.assertTexts(tester, "@ 引用菜单")
	if !tester.HasText("会话") {
		t.Fatalf("mention chat group missing; texts = %v", tester.Texts())
	}
	messageAfter, ok := tester.Find("回答")
	if !ok {
		t.Fatalf("assistant text vanished; texts = %v", tester.Texts())
	}
	if messageBefore.Y != messageAfter.Y {
		t.Fatalf("@ menu moved the transcript: Y %0.1f → %0.1f", messageBefore.Y, messageAfter.Y)
	}
}

// TestNarrowHeaderAndToolbarWithInspectorDrawer covers N1/N2: opening the right
// inspector at 1024 pads the conversation column (the drawer leaves ~400 DIP),
// the title keeps a readable width, the goal chip drops to its status label and
// the composer drops the slash entry while keeping attach/settings visible.
func TestNarrowHeaderAndToolbarWithInspectorDrawer(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "副本 - 看看当前工作目录，然后告诉我",
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "问题"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "回答"},
		},
	}, true)
	chat.config = contracts.ChatConfig{Model: "snapshot-model"}
	settings := newPermissionSettings(nil)
	goal := &pauseTracker{}
	h, tester := newGoalPauseHarness(t, chat, settings, goal)
	goal.setGoal("g1", "完成 Q4 发布清单并更新文档", contracts.GoalStatusActive)
	// A right-panel contribution makes uiroot dock/draw the inspector.
	if _, err := h.ui.Runtime().RegisterSlotItem(contracts.SlotItem{
		Slot: contracts.SlotSessionPanelRight, ID: "test.inspector", Order: 1,
		Build: func(c *ui.Context) { ui.Text(c, "检查器内容") },
	}); err != nil {
		t.Fatalf("register inspector slot: %v", err)
	}
	h.ui.SetRightPanelOpen(true)
	tester.SetSize(1024, 800)
	for i := 0; i < 4; i++ {
		tester.Frame()
	}
	h.assertTexts(tester, "检查器内容")

	// N1: the title stays readable instead of collapsing to "…".
	title, ok := tester.Find("副本 - 看看当前工作目录，然后告诉我")
	if !ok {
		t.Fatalf("session title missing; texts = %v", tester.Texts())
	}
	if title.W < 100 {
		t.Fatalf("title width %.1f at 1024+inspector, want ≥100 (N1)", title.W)
	}
	goalChip, ok := tester.Find("目标")
	if !ok {
		t.Fatalf("goal chip missing; texts = %v", tester.Texts())
	}
	if goalChip.W > 110 {
		t.Fatalf("goal chip width %.1f, want the status-only chip on a narrow header", goalChip.W)
	}

	// The drawer's full-content backdrop consumes clicks (a click outside
	// dismisses it), so exercise the toolbar menu interaction at the same
	// conversation-column width without the drawer: 704 - 280 sidebar leaves
	// the ~424 DIP column the 1024+drawer layout pads to.
	h.ui.SetRightPanelOpen(false)
	tester.SetSize(704, 800)
	for i := 0; i < 4; i++ {
		tester.Frame()
	}

	// N2: at a very narrow composer the model chip is hidden (the session menu
	// repeats it), the attach menu and the single session menu stay, and the
	// removed toolbar's slash entry/overflow no longer exist.
	h.assertTexts(tester, "附件", "会话设置", "发送")
	h.assertNoTexts(tester, "更多工具", "命令", "snapshot…", "»")
	if got := exactTextCount(tester.Texts(), "/"); got != 0 {
		t.Fatalf("slash entry still rendered on a very narrow composer; texts = %v", tester.Texts())
	}
	attach, ok := tester.Find("附件")
	if !ok {
		t.Fatalf("attach control missing on a very narrow composer; texts = %v", tester.Texts())
	}
	if attach.X < 0 || attach.X+attach.W > 704 {
		t.Fatalf("attach control at %.1f..%.1f escaped the 704 window", attach.X, attach.X+attach.W)
	}
	h.openSessionMenu(tester)
	h.assertTexts(tester, "模型：snapshot-model", "推理等级", "打开模型设置…", "权限模式", "对话模式",
		"插入预设", "打开行为设置…")
}

// TestEscapeClosesAnchoredMenusAndKeepsDraft covers H8's Escape/draft contract
// for the overlay menus.
func TestEscapeClosesAnchoredMenusAndKeepsDraft(t *testing.T) {
	chat := groupsChat()
	chat.addSession(contracts.ChatSession{ID: "sess-2", Title: "另一个会话"}, false)
	h := newHarness(t, chat)
	registerTestCommands(t, h, func() {})
	tester := h.tester()
	focusComposer(t, tester)

	tester.Type("/")
	tester.Frame()
	h.assertTexts(tester, "命令菜单")
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	h.assertNoTexts(tester, "命令菜单")
	// The draft / is kept: typing on reopens the menu on the new draft.
	tester.Type("a")
	tester.Frame()
	h.assertTexts(tester, "命令菜单")

	// Start a fresh draft for the @ menu.
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
	tester.Type("@")
	tester.Frame()
	h.assertTexts(tester, "@ 引用菜单")
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	h.assertNoTexts(tester, "@ 引用菜单")
	// The draft @ is kept: typing a query that matches the other chat reopens
	// the menu on the new draft.
	tester.Type("另")
	tester.Frame()
	h.assertTexts(tester, "@ 引用菜单")
}
