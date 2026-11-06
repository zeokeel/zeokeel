package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/state"
)

// TestHistoricalErrorStaysInItsTurn covers the reported ordering bug: turn 1
// fails, the user sends turn 2, and the old error used to be rendered at the
// session tail (below the new user message). A recorded error must stay inside
// the turn that recorded it, above the next user message.
func TestHistoricalErrorStaysInItsTurn(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session

	snap := state.NewSnapshot()
	snap.SessionID = "error-order"
	snap.Phase = state.PhaseIdle
	snap.Messages = []state.Message{
		{MsgID: "u1", Role: state.RoleUser, Text: "first question", Seq: 1, LastSeq: 1},
		{MsgID: "a1", Role: state.RoleAssistant, Text: "partial answer", Seq: 2, LastSeq: 2},
		{MsgID: "u2", Role: state.RoleUser, Text: "second question", Seq: 4, LastSeq: 4},
	}
	snap.Errors = []state.ErrorInfo{{MsgID: "a1", Text: "provider boom", Seq: 3}}
	h.app.SetSnapshot(snap)

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	// The recorded error renders as the productized failure card (PD-09);
	// "provider boom" is unclassified and falls back to the generic title.
	errRect, ok := tt.Find("This turn failed")
	if !ok {
		t.Fatalf("error card missing: %v", tt.Texts())
	}
	first, ok := tt.Find("first question")
	if !ok {
		t.Fatalf("first user message missing: %v", tt.Texts())
	}
	second, ok := tt.Find("second question")
	if !ok {
		t.Fatalf("second user message missing: %v", tt.Texts())
	}
	if errRect.Y < first.Y+first.H {
		t.Fatalf("error y=%v is above the failed turn's user message y=%v", errRect.Y, first.Y)
	}
	if errRect.Y+errRect.H > second.Y+1 {
		t.Fatalf("error rect %+v is not above the next user message %+v", errRect, second)
	}
}

// TestLatestErrorStaysAtTranscriptTail keeps the live-current-turn behavior:
// without a newer user message the error still closes the transcript.
func TestLatestErrorStaysAtTranscriptTail(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Session

	snap := state.NewSnapshot()
	snap.SessionID = "error-tail"
	snap.Messages = []state.Message{
		{MsgID: "u1", Role: state.RoleUser, Text: "only question", Seq: 1, LastSeq: 1},
	}
	snap.Errors = []state.ErrorInfo{{MsgID: "a1", Text: "provider boom", Seq: 2}}
	h.app.SetSnapshot(snap)

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	// The recorded error renders as the productized failure card (PD-09);
	// "provider boom" is unclassified and falls back to the generic title.
	errRect, ok := tt.Find("This turn failed")
	if !ok {
		t.Fatalf("error card missing: %v", tt.Texts())
	}
	question, ok := tt.Find("only question")
	if !ok {
		t.Fatalf("user message missing: %v", tt.Texts())
	}
	if errRect.Y <= question.Y {
		t.Fatalf("error y=%v should be below the turn's content y=%v", errRect.Y, question.Y)
	}
}
