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
	if fromNil.Seq != 1 || fromNil.Messages[0].Text != "a" {
		t.Fatalf("Apply(nil, Token) = %+v", fromNil)
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

func TestUsageSnapshot(t *testing.T) {
	var s *Snapshot
	s = Apply(s, Status{Phase: PhaseRunning})
	want := Usage{Turns: 2, Steps: 7, In: 1200, Out: 340, CacheHit: 900, ContextPct: 0.42}
	s = Apply(s, want)

	if s.Phase != PhaseRunning {
		t.Fatalf("Phase = %q, want running", s.Phase)
	}
	if s.Usage != want {
		t.Fatalf("Usage = %+v, want %+v", s.Usage, want)
	}
	if s.Seq != 2 {
		t.Fatalf("Seq = %d, want 2", s.Seq)
	}

	// Usage is a full replacement, not an accumulation.
	next := Apply(s, Usage{Steps: 8, In: 1300})
	if next.Usage.In != 1300 || next.Usage.Turns != 0 || next.Usage.CacheHit != 0 {
		t.Fatalf("Usage should be replaced: %+v", next.Usage)
	}

	s = Apply(s, Status{Phase: PhaseIdle})
	if s.Phase != PhaseIdle || s.Usage != want {
		t.Fatalf("status/usage fold: phase=%q usage=%+v", s.Phase, s.Usage)
	}
}

func TestErrorSnapshot(t *testing.T) {
	var s *Snapshot
	s = Apply(s, Token{MsgID: "m1", Text: "partial"})
	s = Apply(s, Error{MsgID: "m1", Err: errors.New("model failed")})

	if s.LastError == nil || s.LastError.MsgID != "m1" || s.LastError.Text != "model failed" {
		t.Fatalf("LastError = %+v", s.LastError)
	}
	// Errors do not erase streamed content.
	if len(s.Messages) != 1 || s.Messages[0].Text != "partial" {
		t.Fatalf("messages after error = %+v", s.Messages)
	}

	s = Apply(s, Error{MsgID: "m2", Err: nil})
	if s.LastError == nil || s.LastError.MsgID != "m2" || s.LastError.Text != "" {
		t.Fatalf("nil error event LastError = %+v", s.LastError)
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
