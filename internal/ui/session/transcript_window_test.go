package session_test

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// longTranscriptChatWith builds one conversation with count user messages, so a
// test can exercise the transcript List's virtualization and scrolling.
func longTranscriptChatWith(count int) *fakeChat {
	chat := newFakeChat()
	messages := make([]contracts.ChatMessage, count)
	for i := range messages {
		messages[i] = contracts.ChatMessage{
			ID:   fmt.Sprintf("m%03d", i),
			Role: contracts.ChatRoleUser,
			Text: fmt.Sprintf("早期消息 %03d", i),
		}
	}
	chat.addSession(contracts.ChatSession{ID: "sess-long", Title: "长会话", Messages: messages}, true)
	return chat
}

// TestTranscriptListVirtualizesLongConversation covers the official List
// backing the transcript: a 250-message conversation builds only the rows in
// view (the first messages are not in the frame), the list is exposed by name
// for assistive technology, and scrolling up reveals the earlier rows.
func TestTranscriptListVirtualizesLongConversation(t *testing.T) {
	h := newHarness(t, longTranscriptChatWith(250))
	tester := h.tester()
	tester.Frame()

	// The tail is in view; the beginning is virtualized away (not built).
	h.assertTexts(tester, "早期消息 249")
	h.assertNoTexts(tester, "早期消息 000")
	if _, ok := tester.AccessByRole(ui.RoleList, "对话消息"); !ok {
		t.Fatalf("the transcript is not exposed as a named list; texts = %v", tester.Texts())
	}

	// Scrolling to the top reveals the first messages the List builds on demand.
	tester.Scroll(600, 400, 0, -100000)
	tester.Frame()
	h.assertTexts(tester, "早期消息 000")
	h.assertNoTexts(tester, "早期消息 249")
}
