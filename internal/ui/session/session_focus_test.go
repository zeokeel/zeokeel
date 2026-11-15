package session_test

// Frame tests for the search-hit handoff (P2-4): the session module provides the
// optional contracts.SessionFocus service, and a message target handed to it
// makes the transcript scroll to that message's row (the search hit's durable
// locator or a live chat message id) instead of opening the conversation at the
// followed end.

import (
	"fmt"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// focusSessionChat is a fake chat with one active conversation of n long-enough
// messages, so the transcript really virtualizes: the first rows are not built
// while the list follows the end.
func focusSessionChat(n int) *fakeChat {
	chat := newFakeChat()
	messages := make([]contracts.ChatMessage, 0, n)
	for index := 0; index < n; index++ {
		messages = append(messages, contracts.ChatMessage{
			ID:   fmt.Sprintf("m%d", index+1),
			Role: contracts.ChatRoleUser,
			Text: fmt.Sprintf("定位测试消息 %d 的正文内容", index+1),
		})
	}
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "定位会话", Messages: messages}, true)
	return chat
}

func TestSessionFocusScrollsTranscriptToMessage(t *testing.T) {
	h := newHarness(t, focusSessionChat(40))
	tester := h.tester()
	tester.Frame()
	// The screen opens at the followed end: the first message is virtualized
	// out, which is what makes the focus assertion meaningful.
	if tester.HasText("定位测试消息 1 的正文内容") {
		t.Fatalf("the transcript already shows the first message; the list is not at the followed end")
	}

	focus, ok := h.ctx.Get(contracts.KeySessionFocus)
	if !ok || focus == nil {
		t.Fatalf("the session module does not provide contracts.SessionFocus")
	}
	focus.FocusMessage("sess-1", contracts.MessageOrdinalLocator(3))
	tester.Frame()
	tester.Frame()

	target, ok := tester.Find("定位测试消息 3 的正文内容")
	if !ok {
		t.Fatalf("the focused message is not in the frame; texts = %v", tester.Texts())
	}
	if target.Y < 0 || target.Y+target.H > 800 {
		t.Fatalf("the focused message row sits outside the window: %+v", target)
	}

	// The target is a one-shot: a later frame keeps the reader's place instead
	// of snapping back to the end.
	tester.Frame()
	if _, ok := tester.Find("定位测试消息 3 的正文内容"); !ok {
		t.Fatalf("a later frame scrolled away from the focused message; texts = %v", tester.Texts())
	}

	// A live chat message id is accepted too (a backend that has real ids).
	focus.FocusMessage("sess-1", "m20")
	tester.Frame()
	tester.Frame()
	if _, ok := tester.Find("定位测试消息 20 的正文内容"); !ok {
		t.Fatalf("a live message id did not scroll the transcript; texts = %v", tester.Texts())
	}
}

func TestSessionFocusIgnoresUnresolvableTargets(t *testing.T) {
	h := newHarness(t, focusSessionChat(40))
	tester := h.tester()
	tester.Frame()
	focus, ok := h.ctx.Get(contracts.KeySessionFocus)
	if !ok || focus == nil {
		t.Fatalf("the session module does not provide contracts.SessionFocus")
	}
	// Unknown session, unknown ordinal and unknown id are no-ops: the screen
	// keeps rendering at the followed end instead of jumping somewhere wrong.
	focus.FocusMessage("sess-missing", contracts.MessageOrdinalLocator(2))
	focus.FocusMessage("sess-1", contracts.MessageOrdinalLocator(999))
	focus.FocusMessage("sess-1", "m999")
	tester.Frame()
	tester.Frame()
	if tester.HasText("定位测试消息 1 的正文内容") {
		t.Fatalf("an unresolvable focus target scrolled the transcript")
	}
	if !tester.HasText("定位测试消息 40 的正文内容") {
		t.Fatalf("the transcript did not stay at the followed end; texts = %v", tester.Texts())
	}
}
