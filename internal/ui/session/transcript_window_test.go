package session_test

import (
	"fmt"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestTranscriptBuildsTailWindowFirst covers the bounded transcript: a very
// long conversation builds only its newest window of messages (so streaming a
// reply does not re-layout thousands of rows), and the "load earlier" row loads
// the hidden range in steps.
func TestTranscriptBuildsTailWindowFirst(t *testing.T) {
	chat := newFakeChat()
	messages := make([]contracts.ChatMessage, 250)
	for i := range messages {
		messages[i] = contracts.ChatMessage{
			ID:   fmt.Sprintf("m%03d", i),
			Role: contracts.ChatRoleUser,
			Text: fmt.Sprintf("早期消息 %03d", i),
		}
	}
	chat.addSession(contracts.ChatSession{ID: "sess-long", Title: "长会话", Messages: messages}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	// The tail window is built; the first (50) messages are hidden behind the
	// load-earlier row.
	h.assertNoTexts(tester, "早期消息 000")
	h.assertTexts(tester, "早期消息 249", "加载更早消息（50 条）")

	// The transcript follows the end, so the row above the tail is out of view:
	// scroll to the top of the transcript first, as a user would.
	tester.Scroll(600, 400, 0, -100000)
	tester.Frame()
	if err := tester.Click("加载更早消息（50 条）"); err != nil {
		t.Fatalf("click load-earlier: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "早期消息 000", "早期消息 249")
	if tester.HasText("加载更早消息（50 条）") {
		t.Fatalf("load-earlier row still shows after loading every message; texts = %v", tester.Texts())
	}
}
