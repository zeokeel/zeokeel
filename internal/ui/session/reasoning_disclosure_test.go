package session_test

// Tests for the assistant thinking block: one official mygo disclosure
// (ui.CollapsibleBase) per reply, open with the official spinner while the model
// thinks, collapsed into a quiet reasoning row once the turn ends, with the
// thinking text rendered as Markdown by the official Markdown view.

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

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

// exactText reports whether an element shows exactly text (a text or a label, as
// a spinner's Label).
func exactText(tester *ui.Tester, text string) bool {
	for _, shown := range tester.Texts() {
		if shown == text {
			return true
		}
	}
	return false
}

// TestReasoningStreamingShowsOneOpenDisclosure covers the streaming thinking
// block: the official spinner is labeled 思考中, the one disclosure is named
// 思考中… and open (no second hand-drawn preview row), its panel holds the whole
// reasoning Markdown once, and the generation indicator is the official spinner.
func TestReasoningStreamingShowsOneOpenDisclosure(t *testing.T) {
	reasoning := strings.Repeat("前", 200) + "最新思考片段"
	h := newVerboseWorkStepsHarness(t, reasoningChat(reasoning, true))
	tester := h.tester()

	if !exactText(tester, "思考中") {
		t.Fatalf("thinking spinner label missing; texts = %v", tester.Texts())
	}
	header, ok := tester.AccessByRole(ui.RoleDisclosure, "思考中…")
	if !ok || !header.Expanded {
		t.Fatalf("streaming reasoning disclosure: %+v found %v", header, ok)
	}
	// One reasoning header: the finished 思考 row does not exist alongside it.
	if _, ok := tester.AccessByRole(ui.RoleDisclosure, "思考"); ok {
		t.Fatalf("a second reasoning header is present; texts = %v", tester.Texts())
	}
	// The open panel is the only place the reasoning text shows: no raw tail
	// preview duplicates it.
	if got := textsContaining(tester.Texts(), strings.Repeat("前", 200)); got != 1 {
		t.Fatalf("full reasoning rendered %d times, want the open panel only; texts = %v", got, tester.Texts())
	}
	if _, ok := tester.AccessByRole(ui.RoleProgress, "生成中"); !ok {
		t.Fatalf("generation spinner missing; texts = %v", tester.Texts())
	}
}

// TestReasoningCollapsesWhenTheTurnEnds covers the finished thinking block: it
// auto-collapses (its text leaves the frame), and the reader can still open it
// again by click; the header switches from 思考中… to 思考.
func TestReasoningCollapsesWhenTheTurnEnds(t *testing.T) {
	chat := reasoningChat("完整思考内容", true)
	h := newVerboseWorkStepsHarness(t, chat)
	tester := h.tester()
	if info, ok := tester.AccessByRole(ui.RoleDisclosure, "思考中…"); !ok || !info.Expanded {
		t.Fatalf("streaming disclosure: %+v found %v", info, ok)
	}

	chat.updateSession(contracts.ChatSession{
		ID: "sess-1", Title: "思考预览", Running: false,
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "问题"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "答案", Reasoning: "完整思考内容", Streaming: false},
		},
	})
	tester.Frame()
	tester.Advance(300 * time.Millisecond) // the 200 ms close animation
	h.assertNoTexts(tester, "思考中…", "完整思考内容")
	header, ok := tester.AccessByRole(ui.RoleDisclosure, "思考")
	if !ok || header.Expanded {
		t.Fatalf("finished reasoning disclosure: %+v found %v; texts = %v", header, ok, tester.Texts())
	}

	h.click(tester, "思考")
	tester.Advance(300 * time.Millisecond)
	h.assertTexts(tester, "完整思考内容")
	if info, _ := tester.AccessByRole(ui.RoleDisclosure, "思考"); !info.Expanded {
		t.Fatalf("manually opened reasoning disclosure not expanded: %+v", info)
	}
}

// TestReasoningPanelRendersMarkdown covers the panel body: the reasoning goes
// through the official Markdown view, so bold and list syntax render instead of
// showing raw markers.
func TestReasoningPanelRendersMarkdown(t *testing.T) {
	reasoning := "**重点结论**\n\n- 先看认证模块\n- 再改登录流程"
	h := newVerboseWorkStepsHarness(t, reasoningChat(reasoning, false))
	tester := h.tester()
	h.click(tester, "思考")
	tester.Advance(300 * time.Millisecond)
	h.assertTexts(tester, "重点结论", "先看认证模块", "再改登录流程")
	h.assertNoTexts(tester, "**重点结论**")
}

// TestReasoningEnglishCopy covers the localized reasoning header and spinner
// names after a live language switch.
func TestReasoningEnglishCopy(t *testing.T) {
	h := newVerboseWorkStepsHarness(t, reasoningChat("think in english", true))
	tester := h.tester()
	h.ui.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Thinking…", "think in english")
	if !exactText(tester, "Thinking") {
		t.Fatalf("English thinking spinner label missing; texts = %v", tester.Texts())
	}
	if _, ok := tester.AccessByRole(ui.RoleDisclosure, "Thinking…"); !ok {
		t.Fatalf("English reasoning disclosure missing; texts = %v", tester.Texts())
	}
	h.assertNoTexts(tester, "思考中…")
}

// TestReasoningHiddenWithoutText keeps the disclosure off replies that have no
// thinking text (models that do not stream it).
func TestReasoningHiddenWithoutText(t *testing.T) {
	h := newVerboseWorkStepsHarness(t, reasoningChat("", true))
	tester := h.tester()
	if exactText(tester, "思考中") {
		t.Fatalf("a reply without reasoning showed a thinking spinner; texts = %v", tester.Texts())
	}
	if _, ok := tester.AccessByRole(ui.RoleDisclosure, "思考中…"); ok {
		t.Fatal("a reply without reasoning showed a thinking disclosure")
	}
}
