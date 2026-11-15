package session_test

// Frame tests for the conversation find bar (Cmd/Ctrl+F): the official
// ui.FindBar over the transcript, matching message prose and reasoning, walking
// the matches, scrolling the matched transcript row into view, closing on Esc
// without stopping the running turn, and keeping the query after closing.

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// findbarChat is a long conversation (31 messages, more than one viewport) with
// two matches of "alpha" far apart, so scrolling to a match is observable.
func findbarChat() *fakeChat {
	chat := newFakeChat()
	messages := []contracts.ChatMessage{
		{ID: "u0", Role: contracts.ChatRoleUser, Text: "开场消息"},
	}
	for index := 1; index <= 30; index++ {
		text := fmt.Sprintf("填充消息 %02d", index)
		switch index {
		case 2:
			text = "第一条 alpha 命中"
		case 28:
			text = "第二条 alpha 命中"
		}
		messages = append(messages, contracts.ChatMessage{
			ID: fmt.Sprintf("a%02d", index), Role: contracts.ChatRoleAssistant, Text: text,
		})
	}
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "查找会话", Messages: messages}, true)
	return chat
}

// openFindBar opens the bar with Cmd/Ctrl+F and pends one frame.
func openFindBar(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	tester.Key(ui.Cmd, ui.KeyF)
	tester.Frame()
}

// TestFindBarCmdFOpensAndScrollsToMatch covers the core loop: Cmd/Ctrl+F opens the
// official bar with its field focused, typing counts the message matches and
// scrolls the matched transcript row into view.
func TestFindBarCmdFOpensAndScrollsToMatch(t *testing.T) {
	chat := findbarChat()
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	// The early match is outside the initial (follow-the-end) viewport.
	if tester.HasText("Find") || tester.HasText("Done") {
		t.Fatalf("find bar shown before Cmd+F; texts = %v", tester.Texts())
	}
	h.assertNoTexts(tester, "第一条 alpha 命中")

	openFindBar(t, h, tester)
	h.assertTexts(tester, "Find", "Done")
	if !tester.Focused("Find") {
		t.Fatalf("the find field does not take the focus; texts = %v", tester.Texts())
	}

	tester.Type("alpha")
	tester.Frame()
	h.assertTexts(tester, "1 of 2")
	// The first match's row was scrolled into view (it started off-screen).
	h.assertTexts(tester, "第一条 alpha 命中")
}

// TestFindBarWalksMatchesAndKeepsQuery covers Enter / Shift+Enter walking and the
// close/reopen contract: closing keeps the query and the match position.
func TestFindBarWalksMatchesAndKeepsQuery(t *testing.T) {
	chat := findbarChat()
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	openFindBar(t, h, tester)
	tester.Type("alpha")
	tester.Frame()
	h.assertTexts(tester, "1 of 2", "第一条 alpha 命中")

	// Enter goes to the next match: the second one, scrolled into view.
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	h.assertTexts(tester, "2 of 2", "第二条 alpha 命中")

	// Shift+Enter goes back to the previous match.
	tester.Key(ui.Shift, ui.KeyEnter)
	tester.Frame()
	h.assertTexts(tester, "1 of 2", "第一条 alpha 命中")

	// The bar's Next/Previous buttons move the same choice.
	if err := tester.Click("Next"); err != nil {
		t.Fatalf("click Next: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "2 of 2", "第二条 alpha 命中")
	if err := tester.Click("Previous"); err != nil {
		t.Fatalf("click Previous: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "1 of 2", "第一条 alpha 命中")

	// Escape closes the bar (and the query stays for the next Cmd+F).
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	h.assertNoTexts(tester, "Done")
	openFindBar(t, h, tester)
	h.assertTexts(tester, "1 of 2")
	// Walking again still works, so the query and matches were kept, not reset.
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	h.assertTexts(tester, "2 of 2")
}

// TestFindBarNoMatches covers the honest no-match state.
func TestFindBarNoMatches(t *testing.T) {
	chat := findbarChat()
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	openFindBar(t, h, tester)
	tester.Type("zzz-not-there")
	tester.Frame()
	h.assertTexts(tester, "No matches")
}

// TestFindBarMatchesReasoning covers that the query matches the thinking text
// too, not only the visible reply.
func TestFindBarMatchesReasoning(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "推理查找",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "开始"},
			{
				ID: "a1", Role: contracts.ChatRoleAssistant, Text: "公开正文里没有",
				Reasoning: "隐藏的推理关键词在这里",
			},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	openFindBar(t, h, tester)
	tester.Type("推理关键词")
	tester.Frame()
	h.assertTexts(tester, "1 of 1")
}

// TestFindBarEscapeDoesNotStopRunningTurn pins the safe-action contract: Esc
// closes the find bar and never interrupts the running turn (the next Esc, with
// the bar closed, stops it).
func TestFindBarEscapeDoesNotStopRunningTurn(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "运行中会话", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "帮我检查"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "正在检查…", Streaming: true},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	openFindBar(t, h, tester)
	tester.Type("检查")
	tester.Frame()
	// Both the prompt and the streaming reply contain the query.
	h.assertTexts(tester, "1 of 2")

	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	h.assertNoTexts(tester, "Done")
	if got := chat.cancels(); len(got) != 0 {
		t.Fatalf("Esc in the find bar stopped the turn: %v", got)
	}
}

// TestFindBarDoneCloses covers the bar's own Done action.
func TestFindBarDoneCloses(t *testing.T) {
	chat := findbarChat()
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	openFindBar(t, h, tester)
	tester.Type("alpha")
	tester.Frame()
	if err := tester.Click("Done"); err != nil {
		t.Fatalf("click Done: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertNoTexts(tester, "Done")
}

// TestFindBarEscapeClosesWithFocusOutsideTheField covers Esc when the keyboard
// moved off the bar's field (a Tab to another control of the bar or the page):
// the screen-level Esc closes the bar, and only the bar.
func TestFindBarEscapeClosesWithFocusOutsideTheField(t *testing.T) {
	chat := findbarChat()
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	openFindBar(t, h, tester)
	tester.Type("alpha")
	tester.Frame()
	h.assertTexts(tester, "1 of 2")

	// Move the focus away from the field, then Esc from the screen scope.
	tester.Key(0, ui.KeyTab)
	tester.Frame()
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	h.assertNoTexts(tester, "Done")
	if got := chat.cancels(); len(got) != 0 {
		t.Fatalf("Esc closed the bar and stopped an idle turn: %v", got)
	}
}
