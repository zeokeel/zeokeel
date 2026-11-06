package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zeokeel/zeokeel/old/internal/state"
)

// TestTurnMarksRenderPerTurn pins the per-turn transcript markers: every
// stopped/failed turn keeps its own closing line anchored after that turn and
// before the next user message, so historical interruptions stay visible after
// a reopen instead of only the last turn.
func TestTurnMarksRenderPerTurn(t *testing.T) {
	snap := state.NewSnapshot()
	snap.Phase = state.PhaseIdle
	snap.TurnReason = state.ReasonFailed
	snap.Messages = []state.Message{
		{Role: state.RoleUser, MsgID: "u1", Text: "first prompt", Seq: 1},
		{Role: state.RoleAssistant, MsgID: "a1", Text: "first answer", Seq: 2},
		{Role: state.RoleUser, MsgID: "u2", Text: "second prompt", Seq: 3},
		{Role: state.RoleAssistant, MsgID: "a2", Text: "second answer", Seq: 4},
	}
	snap.TurnMarks = map[string]state.StatusReason{
		"u1": state.ReasonStopped,
		"u2": state.ReasonFailed,
	}
	h := newLiveSnapshotApp(t, Session, snap)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	stopped, ok := tt.Find("Stopped")
	if !ok {
		t.Fatalf("first turn stopped marker missing: %v", tt.Texts())
	}
	failed, ok := tt.Find("Turn failed")
	if !ok {
		t.Fatalf("second turn failed marker missing: %v", tt.Texts())
	}
	if stopped.Y >= failed.Y {
		t.Fatalf("markers out of turn order: stopped at %v, failed at %v", stopped.Y, failed.Y)
	}
	// Each marker sits after its own answer: first marker above the second
	// prompt, second marker below the second answer.
	secondPrompt, ok := tt.Find("second prompt")
	if !ok {
		t.Fatalf("second prompt missing: %v", tt.Texts())
	}
	if stopped.Y >= secondPrompt.Y {
		t.Fatalf("first turn marker (%v) rendered after the next turn prompt (%v)", stopped.Y, secondPrompt.Y)
	}
	secondAnswer, ok := tt.Find("second answer")
	if !ok {
		t.Fatalf("second answer missing: %v", tt.Texts())
	}
	if failed.Y <= secondAnswer.Y {
		t.Fatalf("second turn marker (%v) rendered before its answer (%v)", failed.Y, secondAnswer.Y)
	}
}

// TestTurnMarksCompletedTurnHasNoMarker pins that a completed turn in the same
// transcript adds no marker even when earlier turns were marked.
func TestTurnMarksCompletedTurnHasNoMarker(t *testing.T) {
	snap := state.NewSnapshot()
	snap.Phase = state.PhaseIdle
	snap.Messages = []state.Message{
		{Role: state.RoleUser, MsgID: "u1", Text: "first prompt", Seq: 1},
		{Role: state.RoleAssistant, MsgID: "a1", Text: "first answer", Seq: 2},
		{Role: state.RoleUser, MsgID: "u2", Text: "second prompt", Seq: 3},
		{Role: state.RoleAssistant, MsgID: "a2", Text: "second answer", Seq: 4},
	}
	snap.TurnMarks = map[string]state.StatusReason{"u1": state.ReasonInterrupted}
	h := newLiveSnapshotApp(t, Session, snap)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	interrupted, ok := tt.Find("Interrupted")
	if !ok {
		t.Fatalf("interrupted marker missing: %v", tt.Texts())
	}
	secondPrompt, ok := tt.Find("second prompt")
	if !ok {
		t.Fatalf("second prompt missing: %v", tt.Texts())
	}
	if interrupted.Y >= secondPrompt.Y {
		t.Fatalf("first turn marker rendered into the completed turn: %v", tt.Texts())
	}
	// The completed last turn must not show a terminal marker.
	for _, unwanted := range []string{"Stopped", "Turn failed"} {
		if tt.HasText(unwanted) {
			t.Fatalf("completed turn rendered %q: %v", unwanted, tt.Texts())
		}
	}
}

// TestTurnMarksOldSessionFallback pins the old-data path: sessions persisted
// before TurnEnd existed have no marks, so the terminal reason still shows once
// at the end of the transcript (the previous behavior).
func TestTurnMarksOldSessionFallback(t *testing.T) {
	snap := state.NewSnapshot()
	snap.Phase = state.PhaseIdle
	snap.TurnReason = state.ReasonStopped
	snap.Messages = []state.Message{
		{Role: state.RoleUser, MsgID: "u1", Text: "only prompt", Seq: 1},
	}
	h := newLiveSnapshotApp(t, Session, snap)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText("Stopped") {
		t.Fatalf("old session fallback marker missing: %v", tt.Texts())
	}
	// The fallback must appear at most once.
	count := 0
	for _, el := range tt.Texts() {
		if el == "Stopped" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("fallback marker rendered %d times", count)
	}
}
