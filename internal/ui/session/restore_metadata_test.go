package session_test

// Frame tests for the restored conversation's message metadata: a backfilled
// clock renders, and a turn whose clocks could not be mapped honestly draws no
// isolated ⋯ row (the audit's "every restored turn ends in a lone ⋯").

import (
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestRestoredTurnMetaShowsBackfilledClock covers the restored read model after
// the backend backfill: the user message and the turn's final reply carry the
// durable clocks and both render in the compact meta rows.
func TestRestoredTurnMetaShowsBackfilledClock(t *testing.T) {
	userAt := time.Date(2026, 11, 15, 14, 5, 0, 0, time.Local)
	replyAt := userAt.Add(90 * time.Second)
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "恢复会话",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "恢复的问题", CreatedAt: userAt},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "恢复的回答", CreatedAt: replyAt},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, userAt.Local().Format("15:04"), replyAt.Local().Format("15:04"))
	if _, ok := tester.Find("消息操作"); !ok {
		t.Fatalf("restored reply lost its meta actions; texts = %v", tester.Texts())
	}
}

// TestRestoredTurnWithoutClockHasNoIsolatedMeta covers the honest-empty case: a
// reply whose time could not be mapped and that has no usage renders no meta row
// at all, instead of a lone ⋯ at the top right of the turn.
func TestRestoredTurnWithoutClockHasNoIsolatedMeta(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "无时钟会话",
		Messages: []contracts.ChatMessage{
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "没有时间的回答"},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	if _, ok := tester.Find("消息操作"); ok {
		t.Fatalf("an unmapped restored reply still draws the isolated ⋯; texts = %v", tester.Texts())
	}
}

// TestRestoredUserMessageWithoutClockHasNoMetaRow covers the user bubble: with
// no clock there is no meta row (and so no lone ⋯ under the bubble).
func TestRestoredUserMessageWithoutClockHasNoMetaRow(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "无时钟用户消息",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "没有时间的问题"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "回答"},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	if _, ok := tester.Find("用户消息操作"); ok {
		t.Fatalf("a clock-less restored user message still draws its ⋯ row; texts = %v", tester.Texts())
	}
}

// TestRestoredTurnWithUsageKeepsMetaWithoutClock covers the content-bearing row:
// usage stays visible (and the actions menu with it) even when the clock could
// not be mapped.
func TestRestoredTurnWithUsageKeepsMetaWithoutClock(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "有用量无时钟",
		Messages: []contracts.ChatMessage{
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "有记录的回答",
				Usage: &contracts.MessageUsage{DurationMS: 1500, PromptTokens: 100, CompletionTokens: 20}},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "120 tokens")
	if _, ok := tester.Find("消息操作"); !ok {
		t.Fatalf("a usage-only meta row lost its actions; texts = %v", tester.Texts())
	}
}
