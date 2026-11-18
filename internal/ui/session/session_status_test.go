package session_test

// Tests for the status bar under the composer (S8, B-spec §3.2.6): one 32 DIP
// row in three zones — the run state with the queue on the left, the context
// gauge with its 详情 popover on the right. The bar reports only accurate values
// from the session read model and hides whole items (and itself) when the data
// is unknown; every figure a user reads is at least 12 DIP, the numbers are mono,
// and the details that would turn the bar into a paragraph live in the popover.

import (
	"testing"

	"github.com/egoist/mygo/ui"

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
// three tool calls, 7,400 generated output tokens over 90s (82 tokens/s averaged
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

// TestSessionStatusStripRendersUsage covers the bar's right zone: the newest
// request's context over the catalogue window in one compact figure, the gauge
// under it, and the 详情 control that opens the exact figures. The bar carries no
// sentence — that is what the popover is for.
func TestSessionStatusStripRendersUsage(t *testing.T) {
	chat := statusChat(statusUsageMessages()...)
	h := newModelHarness(t, chat, statusCatalog())
	tester := h.tester()

	h.assertTexts(tester, "120K/128K", "详情")
	// The six-item sentence the old strip printed is gone (VD-10).
	h.assertNoTexts(tester, "轮对话 ·", "tok/s ·", "缓存命中 95% ·")
	// Cumulative billing prompt tokens never reach the context ratio.
	h.assertNoTexts(tester, "220K/128K", "100K/128K")

	// The bar sits directly under the composer card, below the send control.
	strip, ok := tester.Find("120K/128K")
	if !ok {
		t.Fatalf("status bar missing; texts = %v", tester.Texts())
	}
	send, ok := tester.Find("发送")
	if !ok {
		t.Fatalf("send control missing; texts = %v", tester.Texts())
	}
	if strip.Y <= send.Y {
		t.Fatalf("status bar y=%.1f is not below the composer send control y=%.1f", strip.Y, send.Y)
	}

	// The gauge is a real ratio exposed as a progress indicator, never a made-up
	// value.
	progress, ok := tester.AccessByRole(ui.RoleProgress, "")
	if !ok {
		t.Fatalf("status bar has no context gauge; texts = %v", tester.Texts())
	}
	if progress.Now < 0.93 || progress.Now > 0.95 {
		t.Fatalf("context gauge = %v, want 120000/128000", progress.Now)
	}

	// 详情 opens the exact figures: rounds, tool calls, average speed, cache hit,
	// the remaining context and the window itself.
	if err := tester.Click("详情"); err != nil {
		t.Fatalf("open usage details: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "轮次", "工具调用", "平均速度", "82 tokens/s",
		"缓存命中", "95%", "上下文剩余", "6%", "上下文窗口", "128K tokens")
}

// TestSessionStatusStripEnglishCopy covers the English wording.
func TestSessionStatusStripEnglishCopy(t *testing.T) {
	chat := statusChat(statusUsageMessages()...)
	h := newModelHarness(t, chat, statusCatalog())
	tester := h.tester()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()

	h.assertTexts(tester, "120K/128K", "Details")
	if err := tester.Click("Details"); err != nil {
		t.Fatalf("open usage details: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "Turns", "Tool calls", "Average speed", "Cache hit",
		"Context left", "Context window")
}

// TestSessionStatusStripHiddenWithoutUsage covers the empty/failed conversations
// with no token accounting: the bar shows no gauge and no zeroed figures.
func TestSessionStatusStripHiddenWithoutUsage(t *testing.T) {
	chat := statusChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "回答",
		Tools: []contracts.ToolCall{{ID: "t1", Name: "bash", Status: "ok"}},
	})
	h := newModelHarness(t, chat, statusCatalog())
	tester := h.tester()
	h.assertTexts(tester, "回答", "发送")
	h.assertNoTexts(tester, "详情", "/128K", "上下文用量")

	// A conversation with no messages at all hides it too.
	empty := newModelChat()
	empty.addSession(contracts.ChatSession{ID: "sess-2", Title: "空"}, true)
	h = newModelHarness(t, empty, nil)
	tester = h.tester()
	h.assertNoTexts(tester, "详情", "上下文用量")
}

// TestSessionStatusStripWithoutCatalog covers the unknown-window case: without a
// model catalogue there is no context window, so the gauge and the context rows
// are hidden entirely; the other figures stay in the popover.
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
	h.assertNoTexts(tester, "/128K", "900/", "上下文用量")
	h.assertTexts(tester, "详情")
	if err := tester.Click("详情"); err != nil {
		t.Fatalf("open usage details: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "缓存命中", "50%")
	h.assertNoTexts(tester, "上下文剩余", "上下文窗口")
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
	h.assertNoTexts(tester, "剩余", "/64K", "/128K", "上下文用量")
}

// TestSessionStatusStripOmitsContextWithoutContextTokens covers a provider that
// reports usage but no per-request context size: the window is known, yet the
// gauge and the context rows stay hidden instead of guessing.
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
	h.assertNoTexts(tester, "剩余", "/128K", "0/128K", "上下文用量")
	h.assertTexts(tester, "详情")
}

// TestSessionStatusStripHidesUnknownSpeed covers a usage row without wall time
// or output tokens: the speed row is hidden, the other figures remain.
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
	h.assertTexts(tester, "120K/128K")
	if err := tester.Click("详情"); err != nil {
		t.Fatalf("open usage details: %v", err)
	}
	tester.Frame()
	h.assertTexts(tester, "缓存命中", "50%")
	h.assertNoTexts(tester, "tokens/s")
}

// TestSessionStatusStripNamesTheRun covers the left zone: a running turn shows
// the accent spinner and the neutral run word (running is not a warning), and the
// bar names the run once — the transcript's own live line keeps 生成中.
func TestSessionStatusStripNamesTheRun(t *testing.T) {
	chat := statusChat(statusUsageMessages()...)
	session, _ := chat.Session("sess-1")
	session.Running = true
	chat.updateSession(session)
	h := newModelHarness(t, chat, statusCatalog())
	tester := h.tester()

	h.assertTexts(tester, "运行中", "120K/128K")
	// The bar's own state line is a status region named by the run word (the
	// neutral running role: running is not a warning), and it sits under the
	// composer's stop control, not in the top band.
	run, ok := tester.AccessByRole(ui.RoleStatus, "运行中")
	if !ok {
		t.Fatalf("running state is not a status region; texts = %v", tester.Texts())
	}
	stop, ok := tester.Find("停止")
	if !ok || run.Bounds.Y <= stop.Y {
		t.Fatalf("run status at y=%.1f is not under the composer stop control (y=%.1f)",
			run.Bounds.Y, stop.Y)
	}
}
