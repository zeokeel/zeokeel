package session_test

// TDD tests (RED first) for the @session candidate ranking and ID-prefix
// display in the composer mention menu.

import (
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// textIndexOf returns the index of the first text node containing fragment.
func textIndexOf(texts []string, fragment string) int {
	for index, text := range texts {
		if strings.Contains(text, fragment) {
			return index
		}
	}
	return -1
}

func mentionSessionChat(sessions ...contracts.ChatSession) *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-active", Title: "当前会话"}, true)
	for _, session := range sessions {
		chat.addSession(session, false)
	}
	return chat
}

func TestMentionSessionGroupRanksIDMatchesBeforeTitles(t *testing.T) {
	base := time.Unix(1700000000, 0)
	chat := mentionSessionChat(
		contracts.ChatSession{ID: "deepseek-1234", Title: "Alpha", UpdatedAt: base},
		contracts.ChatSession{ID: "sess-9", Title: "DeepSeek notes", UpdatedAt: base.Add(time.Hour)},
	)
	h := newWorkspaceHarness(t, chat, nil)
	tester := h.tester()
	focusMentionComposer(t, h, tester)
	tester.Type("@deepseek")
	tester.Frame()

	h.assertTexts(tester, "Alpha", "DeepSeek notes")
	texts := tester.Texts()
	alpha := textIndexOf(texts, "Alpha")
	titleMatch := textIndexOf(texts, "DeepSeek notes")
	if alpha < 0 || titleMatch < 0 || alpha > titleMatch {
		t.Fatalf("ID-prefix match did not outrank the title match: %v", texts)
	}
	// The long session ID renders as its prefix.
	h.assertTexts(tester, "deepseek…")
}

func TestMentionSessionGroupNewestFirstAndLimited(t *testing.T) {
	base := time.Unix(1700000000, 0)
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-active", Title: "当前会话"}, true)
	for index := 0; index < 10; index++ {
		chat.addSession(contracts.ChatSession{
			ID:        "sess-" + string(rune('a'+index)),
			Title:     "会话" + string(rune('A'+index)),
			UpdatedAt: base.Add(time.Duration(index) * time.Minute),
		}, false)
	}
	h := newWorkspaceHarness(t, chat, nil)
	tester := h.tester()
	focusMentionComposer(t, h, tester)
	tester.Type("@")
	tester.Frame()

	texts := tester.Texts()
	// 10 candidates, limit 8: the two oldest fall away; newest is first.
	h.assertTexts(tester, "会话J", "会话I", "会话C")
	h.assertNoTexts(tester, "会话A", "会话B")
	if newest, second := textIndexOf(texts, "会话J"), textIndexOf(texts, "会话I"); newest < 0 || second < 0 || newest > second {
		t.Fatalf("mention candidates are not newest-first: %v", texts)
	}
}

func TestMentionSessionGroupShowsIDPrefix(t *testing.T) {
	chat := mentionSessionChat(contracts.ChatSession{
		ID: "very-long-session-id-123", Title: "长会话", UpdatedAt: time.Unix(1700000000, 0),
	})
	h := newWorkspaceHarness(t, chat, nil)
	tester := h.tester()
	focusMentionComposer(t, h, tester)
	tester.Type("@长")
	tester.Frame()
	h.assertTexts(tester, "长会话", "very-lon…")
}
