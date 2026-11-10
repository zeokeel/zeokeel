package session_test

// Regression tests for two composer/status-strip details found by the
// interaction driver during the round-2 audit:
//
//   - an Esc-dismissed slash menu must reopen when the user clears the input
//     and types the same "/" again (the dismissal is keyed by draft text);
//   - average output speed is only meaningful for turns with a real wall time,
//     so scripted/sub-100ms turns omit it instead of printing a huge tok/s.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestSlashMenuReopensAfterClearingDraft reproduces the driver sequence: open
// the menu, Escape (menu closed, draft "/"), clear the draft, type "/" again.
func TestSlashMenuReopensAfterClearingDraft(t *testing.T) {
	chat := newModelChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "斜杠"}, true)
	h := newModelHarness(t, chat, nil)
	tester := h.tester()

	h.typeSlash(tester)
	h.assertTexts(tester, "打开设置")

	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	tester.Frame()
	h.assertNoTexts(tester, "打开设置")

	// Clear the draft (select all + backspace) and type the same slash again.
	focusComposer(t, tester)
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
	tester.Frame()
	tester.Type("/")
	tester.Frame()
	h.assertTexts(tester, "打开设置")
}

// TestSessionStatusStripOmitsSpeedForVeryShortTurn covers the scripted-turn
// case: 460 completion tokens over 5ms is not a meaningful throughput number.
func TestSessionStatusStripOmitsSpeedForVeryShortTurn(t *testing.T) {
	chat := statusChat(contracts.ChatMessage{
		ID: "a1", Role: contracts.ChatRoleAssistant, Text: "一",
		Usage: &contracts.MessageUsage{
			PromptTokens: 400, CompletionTokens: 460, TotalTokens: 860,
			CachedTokens: 68, DurationMS: 5, ContextTokens: 300,
		},
	})
	h := newModelHarness(t, chat, nil)
	tester := h.tester()
	h.assertTexts(tester, "1 轮")
	h.assertNoTexts(tester, "tok/s", "缓存命中")
	// The cache hit is still recoverable from the exact tooltip.
	if info, ok := tester.Access("1 轮"); !ok || !strings.Contains(info.Description, "缓存命中 17") {
		t.Fatalf("status tooltip = %q, want the cache hit", info.Description)
	}
}
