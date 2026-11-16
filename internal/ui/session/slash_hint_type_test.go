package session_test

// UD-05 (2026-11 audit, session part): the slash menu's command hints are
// sentences — they use the metadata step (TypeXXS 12/18), never the 11px
// micro-label step (DESIGN.md §5.3).

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// TestSlashHintUsesMetadataStep covers the hint sentence's type step.
func TestSlashHintUsesMetadataStep(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "斜杠提示",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "你好"}},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	h.typeSlash(tester)
	tester.Frame()

	hint, ok := tester.Find("新建并打开一个会话")
	if !ok {
		t.Fatalf("slash command hint missing; texts = %v", tester.Texts())
	}
	_, line, _ := design.TypeMetrics(design.TypeXXS)
	if hint.H < line {
		t.Fatalf("UD-05: slash hint line box = %.0f, want ≥%.0f (TypeXXS sentence, not the 11px micro step)",
			hint.H, line)
	}
}
