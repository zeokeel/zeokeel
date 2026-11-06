package shell

// Chat reasoning disclosure tests (DSH ReasoningRow alignment): a streaming
// "Think" row with a one-line live preview while the reasoning is written, a
// collapsed "Thought for a while" row once the answer starts, click/Enter to
// expand the full reasoning, no row for turns without reasoning, and process
// order user -> reasoning -> tools -> answer.
//
// Run: go test -count=1 -run 'Reasoning' ./internal/shell

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/state"
)

// reasoningTestSnapshot returns a snapshot sized for the transcript fixture:
// the Seq axis is enabled by the messages' Seq fields.
func reasoningTestSnapshot(phase state.Phase) *state.Snapshot {
	snap := state.NewSnapshot()
	snap.SessionID = "reasoning-session"
	snap.Phase = phase
	snap.Seq = 20
	return snap
}

// reasoningUserMsg builds one user message.
func reasoningUserMsg(id string, seq uint64, text string) state.Message {
	return state.Message{MsgID: id, Seq: seq, LastSeq: seq, Role: state.RoleUser, Text: text}
}

// reasoningAssistantMsg builds one assistant message.
func reasoningAssistantMsg(id string, seq, lastSeq uint64, text, reasoning string) state.Message {
	return state.Message{
		MsgID: id, Seq: seq, LastSeq: lastSeq,
		Role: state.RoleAssistant, Text: text, Reasoning: reasoning,
	}
}

// newReasoningTester renders a live session screen showing snap in English.
func newReasoningTester(t *testing.T, snap *state.Snapshot) (*ui.Tester, *liveApp) {
	t.Helper()
	h := newLiveAppWithSnapshot(t, Session, snap)
	h.app.SetLanguageForTest(LangEN)
	return ui.NewTester(h.app.View, 1200, 800), h
}

// reasoningTexts joins the frame's texts for order assertions.
func reasoningTexts(tt *ui.Tester) string { return strings.Join(tt.Texts(), "\n") }

// TestReasoningRunningShowsThinkRowAndPreview is the streaming acceptance: a
// turn whose only content so far is reasoning shows the "Think" disclosure
// with a one-line live preview (the last completed paragraph's first line)
// and no answer.
func TestReasoningRunningShowsThinkRowAndPreview(t *testing.T) {
	snap := reasoningTestSnapshot(state.PhaseRunning)
	snap.Messages = []state.Message{
		reasoningUserMsg("u1", 1, "explain the bug"),
		reasoningAssistantMsg("a1", 2, 2, "",
			"Let me inspect the transcript renderer\nAnd the fold timing\n\nNow I am checking the tool summary"),
	}
	tt, _ := newReasoningTester(t, snap)

	if !tt.HasText("Think") {
		t.Fatalf("running reasoning has no Think row; texts:\n%s", reasoningTexts(tt))
	}
	if !tt.HasText("Let me inspect the transcript renderer") {
		t.Fatalf("running reasoning has no live preview; texts:\n%s", reasoningTexts(tt))
	}
	// The preview is the last *completed* paragraph's first line: the trailing
	// in-progress paragraph is not shown yet.
	if tt.HasText("Now I am checking the tool summary") {
		t.Fatalf("preview leaked the in-progress paragraph; texts:\n%s", reasoningTexts(tt))
	}
	if tt.HasText("Thought for a while") {
		t.Fatalf("a running reasoning row must not show the completed title; texts:\n%s", reasoningTexts(tt))
	}
}

// TestReasoningTokensRevealAnswer: once answer text arrives the answer is
// visible and the reasoning row settles to its completed title while staying
// collapsed.
func TestReasoningTokensRevealAnswer(t *testing.T) {
	snap := reasoningTestSnapshot(state.PhaseRunning)
	snap.Messages = []state.Message{
		reasoningUserMsg("u1", 1, "explain the bug"),
		reasoningAssistantMsg("a1", 2, 9, "The bug is in the fold timing.",
			"First I read the renderer.\n\nThen I found the fold timing."),
	}
	tt, _ := newReasoningTester(t, snap)

	if !tt.HasText("The bug is in the fold timing.") {
		t.Fatalf("answer text is not visible; texts:\n%s", reasoningTexts(tt))
	}
	if !tt.HasText("Thought for a while") {
		t.Fatalf("settled reasoning row has no completed title; texts:\n%s", reasoningTexts(tt))
	}
	if tt.HasText("Then I found the fold timing.") {
		t.Fatalf("collapsed settled reasoning showed its body; texts:\n%s", reasoningTexts(tt))
	}
}

// TestReasoningExpandAndCollapse: a collapsed completed row expands on click to
// the full reasoning and collapses again on the next click.
func TestReasoningExpandAndCollapse(t *testing.T) {
	snap := reasoningTestSnapshot(state.PhaseIdle)
	snap.Messages = []state.Message{
		reasoningUserMsg("u1", 1, "why?"),
		reasoningAssistantMsg("a1", 2, 3, "Because the fold ran early.",
			"First paragraph of the hidden reasoning.\n\nSecond paragraph with the real explanation."),
	}
	tt, h := newReasoningTester(t, snap)

	if tt.HasText("Second paragraph with the real explanation.") {
		t.Fatalf("completed reasoning is not collapsed by default; texts:\n%s", reasoningTexts(tt))
	}
	if !tt.HasText("Because the fold ran early.") {
		t.Fatalf("final answer is not visible; texts:\n%s", reasoningTexts(tt))
	}
	if err := tt.Click("Thought for a while"); err != nil {
		t.Fatalf("expand reasoning: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Second paragraph with the real explanation.") {
		t.Fatalf("expanded reasoning body missing; texts:\n%s", reasoningTexts(tt))
	}
	if states := testerAccessStates(t, tt); states["Thought for a while"]&accessExpandedState == 0 {
		t.Fatalf("expanded reasoning row does not expose AccessExpanded: %+v", states)
	}
	// The expanded state is keyed by the message, not by the frame: switching
	// tabs and back keeps it open.
	h.app.uiState().tab = int(liveTrajectory)
	tt.Frame()
	h.app.uiState().tab = int(liveChat)
	tt.Frame()
	if !tt.HasText("Second paragraph with the real explanation.") {
		t.Fatalf("tab switch lost the expanded reasoning state; texts:\n%s", reasoningTexts(tt))
	}
	if err := tt.Click("Thought for a while"); err != nil {
		t.Fatalf("collapse reasoning: %v", err)
	}
	tt.Frame()
	if tt.HasText("Second paragraph with the real explanation.") {
		t.Fatalf("reasoning did not collapse on the second click; texts:\n%s", reasoningTexts(tt))
	}
	if states := testerAccessStates(t, tt); states["Thought for a while"]&accessExpandedState != 0 {
		t.Fatalf("collapsed reasoning row still exposes AccessExpanded: %+v", states)
	}
}

// TestReasoningKeyboardEnterToggles: after a click focuses the disclosure row,
// Enter toggles it, matching the pointer behavior.
func TestReasoningKeyboardEnterToggles(t *testing.T) {
	snap := reasoningTestSnapshot(state.PhaseIdle)
	snap.Messages = []state.Message{
		reasoningUserMsg("u1", 1, "why?"),
		reasoningAssistantMsg("a1", 2, 3, "Done.", "A keyboard-reachable reasoning body."),
	}
	tt, _ := newReasoningTester(t, snap)

	if err := tt.Click("Thought for a while"); err != nil {
		t.Fatalf("focus reasoning row: %v", err)
	}
	tt.Frame()
	if !tt.HasText("A keyboard-reachable reasoning body.") {
		t.Fatalf("click did not expand the reasoning row; texts:\n%s", reasoningTexts(tt))
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if tt.HasText("A keyboard-reachable reasoning body.") {
		t.Fatalf("Enter did not collapse the focused reasoning row; texts:\n%s", reasoningTexts(tt))
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !tt.HasText("A keyboard-reachable reasoning body.") {
		t.Fatalf("second Enter did not expand the reasoning row; texts:\n%s", reasoningTexts(tt))
	}
}

// TestReasoningWithoutReasoningHasNoRow: a turn without reasoning never draws
// the disclosure row (no empty line/space).
func TestReasoningWithoutReasoningHasNoRow(t *testing.T) {
	snap := reasoningTestSnapshot(state.PhaseIdle)
	snap.Messages = []state.Message{
		reasoningUserMsg("u1", 1, "hello"),
		reasoningAssistantMsg("a1", 2, 3, "Hi there.", ""),
	}
	tt, _ := newReasoningTester(t, snap)
	if !tt.HasText("Hi there.") {
		t.Fatalf("answer is not visible; texts:\n%s", reasoningTexts(tt))
	}
	texts := reasoningTexts(tt)
	if strings.Contains(texts, "Think") || strings.Contains(texts, "Thought for a while") {
		t.Fatalf("a turn without reasoning drew a reasoning row; texts:\n%s", texts)
	}
}

// TestReasoningPreviewIsOneLine: a long first line stays a single clipped line
// instead of wrapping and growing the collapsed row.
func TestReasoningPreviewIsOneLine(t *testing.T) {
	long := strings.Repeat("a very long reasoning sentence ", 20)
	snap := reasoningTestSnapshot(state.PhaseRunning)
	snap.Messages = []state.Message{
		reasoningUserMsg("u1", 1, "go"),
		reasoningAssistantMsg("a1", 2, 2, "", long+"\nsecond line"),
	}
	tt, _ := newReasoningTester(t, snap)

	preview := strings.TrimSpace(long)
	rect, ok := tt.Find(preview)
	if !ok {
		t.Fatalf("preview %q is not in the frame; texts:\n%s", preview, reasoningTexts(tt))
	}
	if rect.H > 21 {
		t.Fatalf("preview height = %v, want one line (<= 21 DIP)", rect.H)
	}
	if rect.X < 0 || rect.X+rect.W > 1200 {
		t.Fatalf("preview overflows the window: %+v", rect)
	}
}

// TestReasoningExpandedBodyDoesNotOverflow: the expanded long reasoning wraps
// inside the transcript measure instead of widening the card.
func TestReasoningExpandedBodyDoesNotOverflow(t *testing.T) {
	long := strings.Repeat("wrapped reasoning text ", 200)
	snap := reasoningTestSnapshot(state.PhaseIdle)
	snap.Messages = []state.Message{
		reasoningUserMsg("u1", 1, "go"),
		reasoningAssistantMsg("a1", 2, 3, "Done.", long),
	}
	tt, _ := newReasoningTester(t, snap)
	if err := tt.Click("Thought for a while"); err != nil {
		t.Fatalf("expand reasoning: %v", err)
	}
	tt.Advance(400 * time.Millisecond) // let the body's size transition settle
	tt.Frame()
	rect, ok := tt.Find(strings.TrimSpace(long))
	if !ok {
		t.Fatalf("expanded reasoning body is not in the frame; texts:\n%s", reasoningTexts(tt))
	}
	if rect.X < 0 || rect.X+rect.W > 1200 {
		t.Fatalf("expanded reasoning overflows the window: %+v", rect)
	}
	if rect.H < 40 {
		t.Fatalf("expanded reasoning height = %v, want the wrapped full body", rect.H)
	}
}

// TestReasoningWithToolsOrdering: inside one turn the reasoning row renders
// above the tool summary, and the final answer stays last.
func TestReasoningWithToolsOrdering(t *testing.T) {
	snap := reasoningTestSnapshot(state.PhaseIdle)
	snap.Messages = []state.Message{
		reasoningUserMsg("u1", 1, "run it"),
		reasoningAssistantMsg("a1", 2, 6, "All done.",
			"Tool reasoning before the call."),
	}
	snap.Tools = map[string]state.ToolState{
		"call-1": {CallID: "call-1", Name: "bash", Status: state.ToolDone, StartedSeq: 3},
	}
	tt, _ := newReasoningTester(t, snap)
	texts := tt.Texts()

	indexOf := func(want string) int {
		for i, text := range texts {
			if text == want {
				return i
			}
		}
		return -1
	}
	reasoning := indexOf("Thought for a while")
	summary := indexOf("Ran 1 command")
	answer := indexOf("All done.")
	if reasoning < 0 || summary < 0 || answer < 0 {
		t.Fatalf("reasoning=%d summary=%d answer=%d; texts:\n%s", reasoning, summary, answer, reasoningTexts(tt))
	}
	if !(reasoning < summary && summary < answer) {
		t.Fatalf("process order = reasoning %d, tools %d, answer %d; want reasoning < tools < answer", reasoning, summary, answer)
	}
}

// TestReasoningRunningAccessibleStatus: while the reasoning streams, the row
// exposes an accessible running status (the visible title alone does not say
// the turn is still thinking).
func TestReasoningRunningAccessibleStatus(t *testing.T) {
	snap := reasoningTestSnapshot(state.PhaseRunning)
	snap.Messages = []state.Message{
		reasoningUserMsg("u1", 1, "go"),
		reasoningAssistantMsg("a1", 2, 2, "", "Still thinking about it."),
	}
	tt, _ := newReasoningTester(t, snap)
	nodes := testerAccessNodes(t, tt)
	for _, node := range nodes {
		if strings.Contains(node.Label, "Think") && strings.Contains(strings.ToLower(node.Label), "thinking") {
			return
		}
	}
	t.Fatalf("no accessible running status on the reasoning row: %+v", nodes)
}

// TestReasoningExpandedStreamingAppends: an expanded reasoning body follows the
// live reasoning deltas, so the user can watch the full thought while it is
// still being written.
func TestReasoningExpandedStreamingAppends(t *testing.T) {
	first := reasoningTestSnapshot(state.PhaseRunning)
	first.Messages = []state.Message{
		reasoningUserMsg("u1", 1, "go"),
		reasoningAssistantMsg("a1", 2, 2, "", "First part of the thought is visible."),
	}
	tt, h := newReasoningTester(t, first)
	if err := tt.Click("Think"); err != nil {
		t.Fatalf("expand streaming reasoning: %v", err)
	}
	tt.Frame()
	if !tt.HasText("First part of the thought is visible.") {
		t.Fatalf("expanded streaming body missing the first delta; texts:\n%s", reasoningTexts(tt))
	}

	later := reasoningTestSnapshot(state.PhaseRunning)
	later.Messages = []state.Message{
		reasoningUserMsg("u1", 1, "go"),
		reasoningAssistantMsg("a1", 2, 2, "", "First part of the thought is visible.\n\nSecond part arriving now."),
	}
	h.app.SetSnapshot(later)
	tt.Frame()
	if !tt.HasText("Second part arriving now.") {
		t.Fatalf("expanded streaming body did not follow the new delta; texts:\n%s", reasoningTexts(tt))
	}
}
