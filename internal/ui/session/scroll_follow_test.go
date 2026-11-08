package session_test

// Frame tests for the transcript's streaming scroll follow: the pane starts at
// its end, follows new content, stops when the user scrolls up and offers the
// floating "back to latest" action; every pane keeps its own offset.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// longTranscriptChat builds a session tall enough to scroll.
func longTranscriptChat() *fakeChat {
	chat := newFakeChat()
	messages := make([]contracts.ChatMessage, 0, 60)
	for i := 0; i < 30; i++ {
		messages = append(messages,
			contracts.ChatMessage{ID: fmt.Sprintf("u%d", i), Role: contracts.ChatRoleUser, Text: fmt.Sprintf("问题 %d", i)},
			contracts.ChatMessage{ID: fmt.Sprintf("a%d", i), Role: contracts.ChatRoleAssistant,
				Text: fmt.Sprintf("回答 %d：%s", i, strings.Repeat("内容 ", 60))},
		)
	}
	chat.addSession(contracts.ChatSession{ID: "sess-scroll", Title: "滚动会话", Messages: messages}, true)
	return chat
}

// TestChatScrollFollowsStreamingEnd covers the streaming follow: the transcript
// starts at its end, follows new content, stops following when the user scrolls
// up and offers the floating "back to latest" action.
func TestChatScrollFollowsStreamingEnd(t *testing.T) {
	chat := longTranscriptChat()
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	// At the end: no back-to-latest affordance.
	h.assertNoTexts(tester, "回到最新")

	// The user scrolls up: the follow stops and the affordance shows.
	tester.Scroll(600, 400, 0, -300)
	tester.Frame()
	h.assertTexts(tester, "回到最新")

	// New content does not pull the view down any more.
	chat.updateSession(contracts.ChatSession{
		ID: "sess-scroll", Title: "滚动会话",
		Messages: append(chat.sessions["sess-scroll"].Messages,
			contracts.ChatMessage{ID: "late", Role: contracts.ChatRoleAssistant, Text: "流式新增的内容"}),
	})
	tester.Frame()
	h.assertTexts(tester, "回到最新")

	// Clicking it returns to the end and resumes the follow.
	h.click(tester, "回到最新")
	h.assertNoTexts(tester, "回到最新")
}

// TestChatScrollRestoredAcrossTabs covers the per-pane scroll state: a chat
// scrolled up keeps its offset when the user inspects the trajectory tab and
// comes back.
func TestChatScrollRestoredAcrossTabs(t *testing.T) {
	chat := longTranscriptChat()
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	tester.Scroll(600, 400, 0, -300)
	tester.Frame()
	h.assertTexts(tester, "回到最新")

	h.click(tester, "轨迹")
	h.assertNoTexts(tester, "回到最新")

	// The tab strip has the focus: Home returns to the chat tab.
	tester.Key(0, ui.KeyHome)
	tester.Frame()
	h.assertTexts(tester, "回到最新")
}
