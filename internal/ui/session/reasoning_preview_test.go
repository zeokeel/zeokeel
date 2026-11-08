package session_test

// Tests for the live reasoning preview while a reply is streaming.

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func reasoningChat(reasoning string, streaming bool) *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "思考预览", Running: streaming,
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "问题"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "", Reasoning: reasoning, Streaming: streaming},
		},
	}, true)
	return chat
}

func TestReasoningPreviewShowsWhileStreaming(t *testing.T) {
	reasoning := strings.Repeat("前", 200) + "最新思考片段"
	h := newHarness(t, reasoningChat(reasoning, true))
	tester := h.tester()
	h.assertTexts(tester, "思考中…", "最新思考片段")
	// The preview is the bounded tail, not the whole reasoning block.
	if got := textsContaining(tester.Texts(), strings.Repeat("前", 200)); got != 0 {
		t.Fatalf("full reasoning rendered in the preview: %v", tester.Texts())
	}
}

func TestReasoningPreviewDisappearsWhenDone(t *testing.T) {
	chat := reasoningChat("完整思考内容", true)
	h := newHarness(t, chat)
	tester := h.tester()
	h.assertTexts(tester, "思考中…")

	chat.updateSession(contracts.ChatSession{
		ID: "sess-1", Title: "思考预览", Running: false,
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "问题"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "答案", Reasoning: "完整思考内容", Streaming: false},
		},
	})
	tester.Frame()
	h.assertNoTexts(tester, "思考中…", "最新思考片段")
	h.assertTexts(tester, "思考")
	h.click(tester, "思考")
	h.assertTexts(tester, "完整思考内容")
}

func TestReasoningPreviewHiddenWhenEmptyOrEnglish(t *testing.T) {
	h := newHarness(t, reasoningChat("", true))
	tester := h.tester()
	h.assertNoTexts(tester, "思考中…")

	h = newHarness(t, reasoningChat("think in english", true))
	tester = h.tester()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Thinking…", "think in english")
}
