package session_test

// Tests for the DSH-style session status strip under the composer: rounds,
// tool steps, average output speed, context-tokens-over-window, cache hit and
// remaining context. The strip renders only accurate values from the session
// read model and hides whole items (and itself) when the data is unknown.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// statusChat scripts one session with the given messages after a first user
// prompt, so the composer renders and the strip has a conversation to sum.
func statusChat(messages ...contracts.ChatMessage) *modelChat {
	chat := newModelChat()
	chat.config = contracts.ChatConfig{Model: "gpt-4o"}
	all := append([]contracts.ChatMessage{
		{ID: "u1", Role: contracts.ChatRoleUser, Text: "第一问"},
	}, messages...)
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "状态条", Messages: all}, true)
	return chat
}

// statusCatalog knows the configured model's 128K context window.
func statusCatalog() *fakeCatalog {
	return &fakeCatalog{models: []contracts.ModelInfo{
		{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128000},
	}}
}

// statusUsageMessages is the scripted conversation of the main test: two rounds,
// three tool calls, 7,400 generated output tokens over 90s (82 tok/s averaged
// over the turn wall time, tool time included), the newest model request saw a
// 120,000-token context of the 128,000 window (6% left) and the cache hit is
// 95% of the prompt tokens. The session's billing prompt totals (220,000) are
// deliberately larger than the window and must never be compared with it.
func statusUsageMessages() []contracts.ChatMessage {
	return []contracts.ChatMessage{
		{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "一",
			Tools: []contracts.ToolCall{
				{ID: "t1", Name: "read_file", Status: "ok"},
				{ID: "t2", Name: "grep", Status: "ok"},
			},
			Usage: &contracts.MessageUsage{
				PromptTokens: 100000, CompletionTokens: 5000, TotalTokens: 105000,
				CachedTokens: 98000, DurationMS: 60000, ContextTokens: 90000,
			}},
		{ID: "u2", Role: contracts.ChatRoleUser, Text: "第二问"},
		{ID: "a2", Role: contracts.ChatRoleAssistant, Text: "二",
			Tools: []contracts.ToolCall{{ID: "t3", Name: "bash", Status: "ok"}},
			Usage: &contracts.MessageUsage{
				PromptTokens: 120000, CompletionTokens: 2400, TotalTokens: 122400,
				CachedTokens: 110000, DurationMS: 30000, ContextTokens: 120000,
			}},
	}
}

// TestSessionStatusStripRendersUsage covers the full line: rounds, steps,
// average output speed, newest ContextTokens over the catalogue window, cache
// hit and remaining context, with the muted single line below the composer.
func TestSessionStatusStripRendersUsage(t *testing.T) {
	chat := statusChat(statusUsageMessages()...)
	h := newModelHarness(t, chat, statusCatalog())
	tester := h.tester()

	const line = "2 轮 · 3 步 · 82 tok/s · 120K/128K tok · 缓存命中 95% · 剩余 6%"
	h.assertTexts(tester, line)
	// Cumulative billing prompt tokens never reach the context ratio.
	h.assertNoTexts(tester, "220K/128K", "100K/128K")

	// The strip is one quiet line directly under the composer card.
	strip, ok := tester.Find(line)
	if !ok {
		t.Fatalf("status strip missing; texts = %v", tester.Texts())
	}
	send, ok := tester.Find("发送")
	if !ok {
		t.Fatalf("send control missing; texts = %v", tester.Texts())
	}
	if strip.Y <= send.Y {
		t.Fatalf("status strip y=%.1f is not below the composer send control y=%.1f", strip.Y, send.Y)
	}
}

// TestSessionStatusStripEnglishCopy covers the English wording and plurals.
func TestSessionStatusStripEnglishCopy(t *testing.T) {
	chat := statusChat(statusUsageMessages()...)
	h := newModelHarness(t, chat, statusCatalog())
	tester := h.tester()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()

	h.assertTexts(tester, "2 rounds · 3 steps · 82 tok/s · 120K/128K tok · Cache hit 95% · 6% left")
}

// TestSessionStatusStripHiddenWithoutUsage covers the empty/failed conversations
// with no token accounting: nothing is reported rather than a row of zeros.
func TestSessionStatusStripHiddenWithoutUsage(t *testing.T) {
	chat := statusChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "回答",
		Tools: []contracts.ToolCall{{ID: "t1", Name: "bash", Status: "ok"}},
	})
	h := newModelHarness(t, chat, statusCatalog())
	tester := h.tester()
	h.assertTexts(tester, "回答", "发送")
	h.assertNoTexts(tester, "tok/s", "缓存命中", " 轮 · ")

	// A conversation with no messages at all hides it too.
	empty := newModelChat()
	empty.addSession(contracts.ChatSession{ID: "sess-2", Title: "空"}, true)
	h = newModelHarness(t, empty, nil)
	tester = h.tester()
	h.assertNoTexts(tester, "tok/s", "缓存命中")
}

// TestSessionStatusStripWithoutCatalog covers the unknown-window case: without a
// model catalogue there is no context window, so the context ratio and the
// remaining item are hidden entirely; speed and cache still show.
func TestSessionStatusStripWithoutCatalog(t *testing.T) {
	chat := statusChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "一",
		Usage: &contracts.MessageUsage{
			PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500,
			CachedTokens: 500, DurationMS: 1500, ContextTokens: 900,
		},
	})
	h := newModelHarness(t, chat, nil)
	tester := h.tester()
	h.assertTexts(tester, "1 轮 · 0 步 · 333 tok/s · 缓存命中 50%")
	h.assertNoTexts(tester, "剩余", "/128K tok", "900/")
}

// TestSessionStatusStripOmitsContextWithUnknownModel covers the catalogue that
// does not know the effective model: even with a configured compactor budget,
// no context window is invented (the budget is not a context window).
func TestSessionStatusStripOmitsContextWithUnknownModel(t *testing.T) {
	chat := statusChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "一",
		Usage: &contracts.MessageUsage{
			PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500,
			CachedTokens: 500, DurationMS: 1500, ContextTokens: 900,
		},
	})
	chat.config = contracts.ChatConfig{Model: "custom-model", ContextBudget: 64000}
	h := newModelHarness(t, chat, statusCatalog())
	tester := h.tester()
	h.assertTexts(tester, "1 轮 · 0 步 · 333 tok/s · 缓存命中 50%")
	h.assertNoTexts(tester, "剩余", "/64K tok", "/128K tok")
}

// TestSessionStatusStripOmitsContextWithoutContextTokens covers a provider that
// reports usage but no per-request context size: the window is known, yet the
// context ratio and remaining items stay hidden instead of guessing.
func TestSessionStatusStripOmitsContextWithoutContextTokens(t *testing.T) {
	chat := statusChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "一",
		Usage: &contracts.MessageUsage{
			PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500,
			CachedTokens: 500, DurationMS: 1500,
		},
	})
	h := newModelHarness(t, chat, statusCatalog())
	tester := h.tester()
	h.assertTexts(tester, "1 轮 · 0 步 · 333 tok/s · 缓存命中 50%")
	h.assertNoTexts(tester, "剩余", "/128K tok", "0/128K")
}

// TestSessionStatusStripHidesUnknownSpeed covers a usage row without wall time
// or output tokens: the tok/s item is hidden, the context/cache items remain.
func TestSessionStatusStripHidesUnknownSpeed(t *testing.T) {
	chat := statusChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "一",
		Usage: &contracts.MessageUsage{
			PromptTokens: 120000, TotalTokens: 120000,
			CachedTokens: 60000, ContextTokens: 120000,
		},
	})
	h := newModelHarness(t, chat, statusCatalog())
	tester := h.tester()
	const line = "1 轮 · 0 步 · 120K/128K tok · 缓存命中 50% · 剩余 6%"
	h.assertTexts(tester, line)
	h.assertNoTexts(tester, "tok/s")
}
