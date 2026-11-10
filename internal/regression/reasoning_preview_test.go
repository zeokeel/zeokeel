package regression

// Adversarial audit: the assistant thinking block is one official mygo
// disclosure. While a turn streams, the transcript shows a single open disclosure
// (spinner + "思考中…") whose panel is the reasoning Markdown, with the whole
// thinking text in that one place (no bounded raw-tail preview row). After the
// turn finishes the panel auto-collapses into the quiet "思考" row, and a click
// reopens it. An empty reasoning block shows nothing.

import (
	"strings"
	"testing"
	"time"

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
	if err := h.settings.Set(contracts.SettingWorkStepsMode, contracts.WorkStepsVerbose); err != nil {
		t.Fatalf("set work-steps mode: %v", err)
	}
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
	r.tester.Advance(300 * time.Millisecond) // the 200 ms auto-collapse animation
}

// countContaining counts frame labels containing fragment.
func countContaining(tester *ui.Tester, fragment string) int {
	count := 0
	for _, text := range tester.Texts() {
		if strings.Contains(text, fragment) {
			count++
		}
	}
	return count
}

// TestReasoningPreviewStreamingThenCollapsed covers the streaming disclosure:
// one header named 思考中…, open, with the whole reasoning Markdown in its panel
// and no bounded tail preview; after the turn ends it auto-collapses, and a
// click on 思考 reopens it.
func TestReasoningPreviewStreamingThenCollapsed(t *testing.T) {
	full := strings.Repeat("前", 200) + "最新思考片段"
	harness := startReasoningScreen(t, full, true)
	requireHasTexts(t, harness.tester, "思考中…", "最新思考片段")
	header, ok := harness.tester.AccessByRole(ui.RoleDisclosure, "思考中…")
	if !ok || !header.Expanded {
		t.Fatalf("streaming reasoning disclosure: %+v found %v; texts = %v", header, ok, harness.tester.Texts())
	}
	// One open panel holds the reasoning tail exactly once (no second raw-tail
	// preview row) and the whole block once.
	if got := countContaining(harness.tester, "最新思考片段"); got != 1 {
		t.Fatalf("reasoning tail rendered %d times, want the one open panel: %v", got, harness.tester.Texts())
	}
	if got := countContaining(harness.tester, strings.Repeat("前", 200)); got != 1 {
		t.Fatalf("full reasoning rendered %d times, want the one open panel: %v", got, harness.tester.Texts())
	}

	harness.finish(t, "最终答案", full)
	requireNoText(t, harness.tester, "思考中…")
	requireNoText(t, harness.tester, "最新思考片段")
	requireHasText(t, harness.tester, "最终答案")
	// The full reasoning is still available: the row is a disclosure named 思考.
	if info, ok := harness.tester.AccessByRole(ui.RoleDisclosure, "思考"); !ok || info.Expanded {
		t.Fatalf("finished reasoning disclosure: %+v found %v; texts = %v", info, ok, harness.tester.Texts())
	}
	if err := harness.tester.Click("思考"); err != nil {
		t.Fatalf("expand reasoning: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	harness.tester.Advance(300 * time.Millisecond) // the 200 ms open animation
	requireHasText(t, harness.tester, "最新思考片段")
}

// TestReasoningPreviewEmptyAndEnglish covers the empty block (nothing shown) and
// the English copy of the streaming disclosure.
func TestReasoningPreviewEmptyAndEnglish(t *testing.T) {
	empty := startReasoningScreen(t, "", true)
	requireNoText(t, empty.tester, "思考中…")
	requireNoText(t, empty.tester, "思考")

	english := startReasoningScreen(t, "thinking in english", true)
	english.h.ui.Locale().SetLanguage("en")
	english.tester.Frame()
	requireHasTexts(t, english.tester, "Thinking…", "thinking in english")
}
