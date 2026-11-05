package state

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestApplyNil(t *testing.T) {
	s := Apply(nil, nil)
	if s == nil {
		t.Fatal("Apply(nil, nil) = nil")
	}
	if s.Seq != 0 || s.Phase != PhaseIdle || s.Tools == nil || len(s.Messages) != 0 {
		t.Fatalf("empty snapshot = %+v", s)
	}

	next := Apply(s, nil)
	if next != s {
		t.Fatal("Apply(s, nil) should return s unchanged")
	}

	fromNil := Apply(nil, Token{MsgID: "m", Text: "a"})
	if fromNil.Seq != 1 || fromNil.Messages[0].Text != "a" || fromNil.Messages[0].Seq != 1 {
		t.Fatalf("Apply(nil, Token) = %+v", fromNil)
	}
}

// TestMessageSeqFirstAppearance pins the message Seq rule the Chat view will
// sort on: a message created by a Token/Reasoning fold records that event's
// snapshot Seq, later folds keep it, and a hand-built fixture may leave it 0.
func TestMessageSeqFirstAppearance(t *testing.T) {
	// A hand-built snapshot (fixture) leaves Seq 0: the field is only set by a
	// fold, and the view degrades 0 to list order.
	manual := NewSnapshot()
	manual.Messages = []Message{{MsgID: "u1", Role: RoleUser, Text: "hi"}}
	if got := manual.Messages[0].Seq; got != 0 {
		t.Fatalf("hand-built message Seq = %d, want 0", got)
	}

	var s *Snapshot
	s = Apply(s, Token{MsgID: "m1", Text: "Hel"}) // Seq 1 creates m1
	s = Apply(s, Token{MsgID: "m1", Text: "lo"})  // Seq 2 appends to m1
	s = Apply(s, Reasoning{MsgID: "m1", Text: "think"})
	if got := s.Messages[0].Seq; got != 1 {
		t.Fatalf("m1 Seq after appends = %d, want 1 (first appearance)", got)
	}

	s = Apply(s, Reasoning{MsgID: "m2", Text: "why"}) // Seq 4 creates m2
	if got := s.Messages[1].Seq; got != 4 {
		t.Fatalf("reasoning-created m2 Seq = %d, want 4", got)
	}
	s = Apply(s, Token{MsgID: "m2", Text: "!"}) // Seq 5 appends to m2
	if got := s.Messages[1].Seq; got != 4 {
		t.Fatalf("m2 Seq after token append = %d, want 4", got)
	}

	s = Apply(s, Status{Phase: PhaseRunning})   // Seq 6
	s = Apply(s, Token{MsgID: "m3", Text: "x"}) // Seq 7 creates m3
	if got := s.Messages[2].Seq; got != 7 {
		t.Fatalf("m3 Seq = %d, want 7", got)
	}
	// The Seq values are strictly increasing in first-appearance order, on the
	// same axis ToolState.StartedSeq uses.
	for i := 1; i < len(s.Messages); i++ {
		if s.Messages[i].Seq <= s.Messages[i-1].Seq {
			t.Fatalf("message Seqs not increasing: %+v", s.Messages)
		}
	}
}

// TestMessageLastSeqTracksVisibleTokens pins the visible-text end the Chat
// view places a message by: a message created by pre-tool Reasoning has
// LastSeq 0 until the first Token, every Token moves it, and a Reasoning fold
// in between never does.
func TestMessageLastSeqTracksVisibleTokens(t *testing.T) {
	var s *Snapshot
	s = Apply(s, Reasoning{MsgID: "m1", Text: "thinking"}) // Seq 1: message exists, no visible text
	if m := s.Messages[0]; m.Seq != 1 || m.LastSeq != 0 {
		t.Fatalf("reasoning-created message = Seq %d LastSeq %d, want 1/0", m.Seq, m.LastSeq)
	}

	s = Apply(s, ToolStart{CallID: "c1", Name: "bash"}) // Seq 2
	s = Apply(s, ToolEnd{CallID: "c1", Result: "ok"})   // Seq 3
	s = Apply(s, Reasoning{MsgID: "m1", Text: "more"})  // Seq 4: still no visible text
	if m := s.Messages[0]; m.LastSeq != 0 {
		t.Fatalf("reasoning moved LastSeq to %d, want 0", m.LastSeq)
	}

	s = Apply(s, Token{MsgID: "m1", Text: "answer"}) // Seq 5: visible text after the tool
	if m := s.Messages[0]; m.Seq != 1 || m.LastSeq != 5 {
		t.Fatalf("message after first token = Seq %d LastSeq %d, want 1/5", m.Seq, m.LastSeq)
	}
	if got, want := s.Messages[0].Text, "answer"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}

	s = Apply(s, Token{MsgID: "m1", Text: " more"}) // Seq 6: later tokens keep moving LastSeq
	if m := s.Messages[0]; m.Seq != 1 || m.LastSeq != 6 {
		t.Fatalf("message after second token = Seq %d LastSeq %d, want 1/6", m.Seq, m.LastSeq)
	}
	s = Apply(s, Reasoning{MsgID: "m1", Text: "late thought"}) // Seq 7
	if m := s.Messages[0]; m.LastSeq != 6 {
		t.Fatalf("late reasoning moved LastSeq to %d, want 6", m.LastSeq)
	}

	// A token-created message ends at its creation seq until more text arrives.
	s = Apply(s, Token{MsgID: "m2", Text: "x"}) // Seq 8
	if m := s.Messages[1]; m.Seq != 8 || m.LastSeq != 8 {
		t.Fatalf("token-created message = Seq %d LastSeq %d, want 8/8", m.Seq, m.LastSeq)
	}
}

func TestTokenAccumulation(t *testing.T) {
	var s *Snapshot
	s = Apply(s, Token{SessionID: "sess-1", MsgID: "m1", Text: "Hel"})
	before := s
	s = Apply(s, Token{MsgID: "m1", Text: "lo"})
	s = Apply(s, Token{MsgID: "m2", Text: "world"})
	s = Apply(s, Reasoning{MsgID: "m1", Text: "think"})

	if s.SessionID != "sess-1" {
		t.Fatalf("SessionID = %q, want sess-1", s.SessionID)
	}
	if s.Seq != 4 {
		t.Fatalf("Seq = %d, want 4", s.Seq)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(s.Messages))
	}
	m1 := s.Messages[0]
	if m1.MsgID != "m1" || m1.Role != RoleAssistant {
		t.Fatalf("first message = %+v", m1)
	}
	if m1.Text != "Hello" {
		t.Fatalf("first text = %q, want Hello", m1.Text)
	}
	if m1.Reasoning != "think" {
		t.Fatalf("first reasoning = %q, want think", m1.Reasoning)
	}
	m2 := s.Messages[1]
	if m2.MsgID != "m2" || m2.Text != "world" || m2.Reasoning != "" {
		t.Fatalf("second message = %+v", m2)
	}
	if m1.Seq != 1 || m2.Seq != 3 {
		t.Fatalf("message Seqs = %d,%d, want first-appearance 1,3", m1.Seq, m2.Seq)
	}

	// The previously published snapshot must not observe later folds.
	if before.Seq != 1 || len(before.Messages) != 1 || before.Messages[0].Text != "Hel" {
		t.Fatalf("earlier snapshot mutated: %+v", before)
	}
}

func TestMessageOrderIsFirstAppearance(t *testing.T) {
	var s *Snapshot
	for _, ev := range []Event{
		Token{MsgID: "b", Text: "1"},
		Token{MsgID: "a", Text: "2"},
		Token{MsgID: "b", Text: "3"},
		Token{MsgID: "c", Text: "4"},
	} {
		s = Apply(s, ev)
	}
	got := []string{s.Messages[0].MsgID, s.Messages[1].MsgID, s.Messages[2].MsgID}
	want := []string{"b", "a", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("message order = %v, want %v", got, want)
		}
	}
}

func TestToolLifecycleTransitions(t *testing.T) {
	args := json.RawMessage(`{"cmd":"ls"}`)
	var s *Snapshot

	s = Apply(s, ToolStart{CallID: "c1", Name: "bash", Args: args})
	st := s.Tools["c1"]
	if st.Status != ToolRunning || st.Name != "bash" || string(st.Args) != `{"cmd":"ls"}` {
		t.Fatalf("after ToolStart: %+v", st)
	}
	if st.StartedSeq != 1 || st.UpdatedSeq != 1 {
		t.Fatalf("after ToolStart seqs: started=%d updated=%d", st.StartedSeq, st.UpdatedSeq)
	}
	afterStart := s

	s = Apply(s, ApprovalRequest{CallID: "c1", Name: "bash", Args: args, Kind: ApprovalCommand})
	st = s.Tools["c1"]
	if st.Status != ToolAwaitingApproval {
		t.Fatalf("after ApprovalRequest: status = %q", st.Status)
	}
	if st.StartedSeq != 1 || st.UpdatedSeq != 2 {
		t.Fatalf("after ApprovalRequest seqs: started=%d updated=%d", st.StartedSeq, st.UpdatedSeq)
	}

	// A resolved approval is followed by the actual execution.
	s = Apply(s, ToolStart{CallID: "c1", Name: "bash", Args: args})
	if got := s.Tools["c1"].Status; got != ToolRunning {
		t.Fatalf("after approved ToolStart: status = %q", got)
	}
	// The real execution start (after approval) is the most recent ToolStart.
	if got := s.Tools["c1"].StartedSeq; got != 3 {
		t.Fatalf("StartedSeq after re-start = %d, want 3", got)
	}

	s = Apply(s, ToolEnd{CallID: "c1", Result: "file-a", Duration: 250 * time.Millisecond})
	st = s.Tools["c1"]
	if st.Status != ToolDone || st.Result != "file-a" || st.IsError || st.Duration != 250*time.Millisecond {
		t.Fatalf("after ToolEnd: %+v", st)
	}
	if st.UpdatedSeq != 4 {
		t.Fatalf("after ToolEnd UpdatedSeq = %d, want 4", st.UpdatedSeq)
	}

	// A failed call.
	s = Apply(s, ToolEnd{CallID: "c2", Result: "boom", IsError: true, Duration: time.Second})
	st = s.Tools["c2"]
	if st.Status != ToolError || !st.IsError || st.Result != "boom" {
		t.Fatalf("after failed ToolEnd: %+v", st)
	}
	if st.CallID != "c2" || st.StartedSeq != 0 {
		t.Fatalf("synthetic failed call = %+v", st)
	}

	// Approval arriving before ToolStart still creates a visible waiting call.
	s = Apply(s, ApprovalRequest{CallID: "c3", Name: "write_file", Kind: ApprovalFileWrite})
	st = s.Tools["c3"]
	if st.Status != ToolAwaitingApproval || st.Name != "write_file" || st.Args != nil {
		t.Fatalf("approval-only call = %+v", st)
	}
	if st.ReviewReason != "" {
		t.Fatalf("ordinary approval gained a review reason: %+v", st)
	}

	// An automatic-review denial recommendation keeps its reason on the call so
	// the approval card (and a replayed snapshot) can explain it. The call stays
	// awaiting the user's decision.
	s = Apply(s, ApprovalRequest{
		CallID: "c4", Name: "bash", Kind: ApprovalCommand,
		ReviewReason: "deletes pre-existing files",
	})
	st = s.Tools["c4"]
	if st.Status != ToolAwaitingApproval || st.ReviewReason != "deletes pre-existing files" {
		t.Fatalf("review fallback approval = %+v", st)
	}

	// Earlier snapshots keep their own tool state.
	if afterStart.Tools["c1"].Status != ToolRunning {
		t.Fatalf("earlier snapshot mutated: %+v", afterStart.Tools["c1"])
	}
	if _, ok := afterStart.Tools["c2"]; ok {
		t.Fatal("earlier snapshot gained a tool")
	}
}

func TestApplyCopiesArguments(t *testing.T) {
	args := json.RawMessage(`{"cmd":"ls"}`)
	s0 := Apply(nil, ToolStart{CallID: "c1", Name: "bash", Args: args})
	s1 := Apply(s0, ToolEnd{CallID: "c1", Result: "ok"})

	// Mutating the producer's buffer must not leak into either snapshot.
	args[2] = 'X'
	if got := string(s0.Tools["c1"].Args); got != `{"cmd":"ls"}` {
		t.Fatalf("ToolStart args aliased producer buffer: %s", got)
	}
	if got := string(s1.Tools["c1"].Args); got != `{"cmd":"ls"}` {
		t.Fatalf("ToolEnd args aliased producer buffer: %s", got)
	}
}

// TestUsageSnapshot pins the cumulative usage contract: the snapshot adds
// usage deltas (one per completed model call), ContextPct is a gauge, and the
// whole-turn samples older traces carry (Turns=1 each) sum to the same totals.
func TestUsageSnapshot(t *testing.T) {
	var s *Snapshot
	s = Apply(s, Status{Phase: PhaseRunning})
	// The provider's first call: the turn counts here, tokens/cache arrive.
	s = Apply(s, Usage{Turns: 1, Steps: 1, In: 900, Out: 200, CacheHit: 700, ContextPct: 0.42})
	// The second call is another step with more tokens; the gauge moves on.
	s = Apply(s, Usage{Steps: 1, In: 300, Out: 140, CacheHit: 200, ContextPct: 0.51})
	want := Usage{Turns: 1, Steps: 2, In: 1200, Out: 340, CacheHit: 900, ContextPct: 0.51}
	if s.Phase != PhaseRunning {
		t.Fatalf("Phase = %q, want running", s.Phase)
	}
	if s.Usage != want {
		t.Fatalf("Usage = %+v, want cumulative %+v", s.Usage, want)
	}
	// A zero ContextPct does not erase the last gauge reading; a new one replaces it.
	s = Apply(s, Usage{Steps: 1})
	if s.Usage.ContextPct != 0.51 || s.Usage.Steps != 3 {
		t.Fatalf("gauge/step fold = %+v", s.Usage)
	}
	// The cost advisory is a gauge too: it stays until a newer advisory.
	s = Apply(s, Usage{Advisory: "cost advisory: 25 steps"})
	if s.Usage.Advisory != "cost advisory: 25 steps" {
		t.Fatalf("advisory fold = %+v", s.Usage)
	}
	s = Apply(s, Usage{Steps: 1})
	if s.Usage.Advisory != "cost advisory: 25 steps" {
		t.Fatalf("step delta cleared the advisory: %+v", s.Usage)
	}
	s = Apply(s, Usage{Advisory: "cost advisory: 50 steps"})
	if s.Usage.Advisory != "cost advisory: 50 steps" {
		t.Fatalf("newer advisory did not replace the old one: %+v", s.Usage)
	}
	// Backward compatibility: an old trace's whole-turn sample (Turns=1,
	// steps/tokens of that turn) sums exactly like a delta batch.
	var legacy *Snapshot
	legacy = Apply(legacy, Usage{Turns: 1, Steps: 3, In: 500, Out: 80})
	legacy = Apply(legacy, Usage{Turns: 1, Steps: 4, In: 700, Out: 120})
	if legacy.Usage.Turns != 2 || legacy.Usage.Steps != 7 || legacy.Usage.In != 1200 || legacy.Usage.Out != 200 {
		t.Fatalf("legacy usage fold = %+v, want two-turn cumulative totals", legacy.Usage)
	}
}

// TestStatusReasonFold pins the terminal reason the Chat view shows for a
// stopped or crashed turn: a terminal Status records it, a new running turn
// clears it, and it survives an arbitrary sequence (replay/reopen).
func TestStatusReasonFold(t *testing.T) {
	var s *Snapshot
	s = Apply(s, Status{Phase: PhaseRunning})
	if s.TurnReason != ReasonNone {
		t.Fatalf("TurnReason while running = %q, want empty", s.TurnReason)
	}
	s = Apply(s, Status{Phase: PhaseIdle, Reason: ReasonStopped})
	if s.Phase != PhaseIdle || s.TurnReason != ReasonStopped {
		t.Fatalf("stopped fold = phase %q reason %q", s.Phase, s.TurnReason)
	}
	s = Apply(s, Status{Phase: PhaseRunning})
	if s.TurnReason != ReasonNone {
		t.Fatalf("a new turn did not clear TurnReason: %q", s.TurnReason)
	}
	s = Apply(s, Status{Phase: PhaseIdle, Reason: ReasonInterrupted})
	if s.TurnReason != ReasonInterrupted {
		t.Fatalf("interrupted fold = %q", s.TurnReason)
	}
	// Old traces have no reason: the zero value still reads as completed.
	s = Apply(s, Status{Phase: PhaseIdle})
	if s.TurnReason != ReasonNone {
		t.Fatalf("reasonless terminal status = %q, want none", s.TurnReason)
	}
}

// TestToolEndCancelledFold pins the closed-by-the-turn tool state: a cancelled
// ToolEnd marks the call ToolCancelled without claiming it was an error.
func TestToolEndCancelledFold(t *testing.T) {
	var s *Snapshot
	s = Apply(s, ToolStart{CallID: "c1", Name: "bash"})
	s = Apply(s, ToolEnd{CallID: "c1", Result: "cancelled", Cancelled: true})
	tool := s.Tools["c1"]
	if tool.Status != ToolCancelled || tool.IsError {
		t.Fatalf("cancelled tool = %+v, want status %q and no error flag", tool, ToolCancelled)
	}
	if tool.Result != "cancelled" {
		t.Fatalf("cancelled tool result = %q", tool.Result)
	}
}

func TestErrorSnapshot(t *testing.T) {
	var s *Snapshot
	s = Apply(s, Token{MsgID: "m1", Text: "partial"})
	s = Apply(s, Error{MsgID: "m1", Err: errors.New("model failed")})

	if s.LastError == nil || s.LastError.MsgID != "m1" || s.LastError.Text != "model failed" {
		t.Fatalf("LastError = %+v", s.LastError)
	}
	// Every error is also kept with its Seq so the transcript can place a
	// historical failure inside the turn that recorded it.
	if len(s.Errors) != 1 || s.Errors[0].MsgID != "m1" || s.Errors[0].Text != "model failed" || s.Errors[0].Seq != 2 {
		t.Fatalf("Errors = %+v", s.Errors)
	}
	// Errors do not erase streamed content.
	if len(s.Messages) != 1 || s.Messages[0].Text != "partial" {
		t.Fatalf("messages after error = %+v", s.Messages)
	}

	s = Apply(s, Error{MsgID: "m2", Err: nil})
	if s.LastError == nil || s.LastError.MsgID != "m2" || s.LastError.Text != "" {
		t.Fatalf("nil error event LastError = %+v", s.LastError)
	}
	if len(s.Errors) != 2 || s.Errors[1].MsgID != "m2" || s.Errors[1].Seq != 3 {
		t.Fatalf("Errors after second error = %+v", s.Errors)
	}
	// The list must stay a snapshot-owned copy: folding on top of s must not
	// mutate s's slice.
	s2 := Apply(s, Error{MsgID: "m3", Err: errors.New("third")})
	if len(s.Errors) != 2 || len(s2.Errors) != 3 {
		t.Fatalf("errors aliased across snapshots: old=%d new=%d", len(s.Errors), len(s2.Errors))
	}
}

func TestApplyUnknownEventAdvancesSeq(t *testing.T) {
	var s *Snapshot
	s = Apply(s, Token{MsgID: "m", Text: "x"})
	s = Apply(s, bogusEvent{})
	if s.Seq != 2 {
		t.Fatalf("Seq = %d, want 2", s.Seq)
	}
	if len(s.Messages) != 1 || s.Messages[0].Text != "x" {
		t.Fatalf("unknown event changed snapshot: %+v", s)
	}
}

type bogusEvent struct{}

func (bogusEvent) isEvent() {}

func TestApplyDeterministic(t *testing.T) {
	events := []Event{
		Status{Phase: PhaseRunning},
		Token{SessionID: "sess", MsgID: "m1", Text: "Hel"},
		Token{MsgID: "m1", Text: "lo"},
		Reasoning{MsgID: "m1", Text: "because"},
		ToolStart{CallID: "c1", Name: "bash", Args: json.RawMessage(`{"cmd":"ls"}`)},
		ApprovalRequest{CallID: "c2", Name: "write", Kind: ApprovalFileWrite},
		Usage{Turns: 1, Steps: 2, In: 10, Out: 5, CacheHit: 3, ContextPct: 0.1},
		ToolEnd{CallID: "c1", Result: "ok", Duration: time.Millisecond},
		Token{MsgID: "m2", Text: "hi"},
		Error{MsgID: "m2", Err: errors.New("boom")},
		Status{Phase: PhaseIdle},
	}
	fold := func() *Snapshot {
		var s *Snapshot
		for _, ev := range events {
			s = Apply(s, ev)
		}
		return s
	}
	a, b := fold(), fold()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("fold not deterministic:\n a=%+v\n b=%+v", a, b)
	}
	if a.Seq != uint64(len(events)) {
		t.Fatalf("Seq = %d, want %d", a.Seq, len(events))
	}
}

// TestTurnEndFold pins the per-turn marker fold: TurnEnd records one reason per
// user message, later events do not clear earlier marks, and the input snapshot
// is never mutated.
func TestTurnEndFold(t *testing.T) {
	s := NewSnapshot()
	s = Apply(s, TurnEnd{TurnID: "t1", UserMsgID: "u1", Reason: ReasonStopped})
	s = Apply(s, TurnEnd{TurnID: "t2", UserMsgID: "u2", Reason: ReasonFailed})
	if got := s.TurnMarks["u1"]; got != ReasonStopped {
		t.Fatalf("u1 mark = %q, want stopped", got)
	}
	if got := s.TurnMarks["u2"]; got != ReasonFailed {
		t.Fatalf("u2 mark = %q, want failed", got)
	}
	// The previous snapshot keeps its own map (immutable fold).
	prev := s
	s = Apply(s, TurnEnd{TurnID: "t3", UserMsgID: "u3", Reason: ReasonInterrupted})
	if _, ok := prev.TurnMarks["u3"]; ok {
		t.Fatal("TurnEnd mutated the previous snapshot's marks")
	}
	if got := s.TurnMarks["u3"]; got != ReasonInterrupted {
		t.Fatalf("u3 mark = %q, want interrupted", got)
	}
	// A completed turn (ReasonNone) and an empty anchor record nothing.
	s = Apply(s, TurnEnd{UserMsgID: "u4", Reason: ReasonNone})
	s = Apply(s, TurnEnd{UserMsgID: "", Reason: ReasonStopped})
	if len(s.TurnMarks) != 3 {
		t.Fatalf("marks = %+v, want three", s.TurnMarks)
	}
	// A later terminal Status keeps the marks (only the latest-reason field
	// changes), so a reopen shows every historical marker.
	s = Apply(s, Status{Phase: PhaseIdle, Reason: ReasonStopped})
	if len(s.TurnMarks) != 3 {
		t.Fatalf("Status cleared the marks: %+v", s.TurnMarks)
	}
}
