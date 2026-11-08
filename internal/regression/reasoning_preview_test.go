package regression

// Adversarial audit: the live reasoning preview. While a turn streams a thinking
// block the transcript shows "思考中…" plus a bounded tail; after the turn
// finishes the preview disappears but the collapsed "思考" section keeps the full
// text; an empty reasoning block shows nothing.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
)

// reasoningHarness is the session screen with one streaming/finished answer.
type reasoningHarness struct {
	h      *harness
	chat   *fakeChat
	tester *ui.Tester
}

// startReasoningScreen activates the session screen with one answer.
func startReasoningScreen(t *testing.T, reasoning string, streaming bool) *reasoningHarness {
	t.Helper()
	h := newBareHarness(t)
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-think", Title: "Reasoning session", Running: streaming,
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "问题"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "", Reasoning: reasoning, Streaming: streaming},
		}}, true)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	h.push(sessionui.ScreenID)
	harness := &reasoningHarness{h: h, chat: chat, tester: h.tester(1280, 800)}
	harness.tester.Frame()
	return harness
}

// finish flips the answer to a finished, non-streaming turn.
func (r *reasoningHarness) finish(t *testing.T, text, reasoning string) {
	t.Helper()
	r.chat.mu.Lock()
	r.chat.sessions["sess-think"] = contracts.ChatSession{ID: "sess-think", Title: "Reasoning session",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "问题"},
			{ID: "a1", Role: contracts.ChatRoleAssistant, Text: text, Reasoning: reasoning, Streaming: false},
		}}
	r.chat.mu.Unlock()
	r.tester.Frame()
}

// TestReasoningPreviewStreamingThenCollapsed covers the streaming preview tail,
// its disappearance and the retained collapsible reasoning content.
func TestReasoningPreviewStreamingThenCollapsed(t *testing.T) {
	full := strings.Repeat("前", 200) + "最新思考片段"
	harness := startReasoningScreen(t, full, true)
	requireHasTexts(t, harness.tester, "思考中…", "最新思考片段")
	// The preview is the bounded tail, not the whole block.
	fullPrefix := strings.Repeat("前", 200)
	for _, text := range harness.tester.Texts() {
		if strings.Contains(text, fullPrefix) {
			t.Fatalf("full reasoning rendered in the streaming preview: %v", harness.tester.Texts())
		}
	}

	harness.finish(t, "最终答案", full)
	requireNoText(t, harness.tester, "思考中…")
	requireNoText(t, harness.tester, "最新思考片段")
	requireHasText(t, harness.tester, "最终答案")
	// The full reasoning is still available in the collapsed section.
	requireHasText(t, harness.tester, "思考")
	if err := harness.tester.Click("思考"); err != nil {
		t.Fatalf("expand reasoning: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	requireHasText(t, harness.tester, "最新思考片段")
}

// TestReasoningPreviewEmptyAndEnglish covers the empty block (nothing shown) and
// the English copy of the live preview.
func TestReasoningPreviewEmptyAndEnglish(t *testing.T) {
	empty := startReasoningScreen(t, "", true)
	requireNoText(t, empty.tester, "思考中…")
	requireNoText(t, empty.tester, "思考")

	english := startReasoningScreen(t, "thinking in english", true)
	english.h.ui.Locale().SetLanguage("en")
	english.tester.Frame()
	requireHasTexts(t, english.tester, "Thinking…", "thinking in english")
}
