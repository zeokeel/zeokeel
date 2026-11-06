package state

// AR-22 consistency tests: ApplyBatch must produce exactly the snapshot that
// folding the same events one at a time with Apply produces, for every event
// kind, including nil events, unknown event implementations, negative-looking
// sequences (tools before messages, errors overflowing the bound, ...).

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

// unknownTestEvent exercises Apply's "closed set via embedding" fallback: it
// advances Seq and changes nothing.
type unknownTestEvent struct{ label string }

func (unknownTestEvent) isEvent() {}

// referenceFold is the pre-AR-22 algorithm (one Apply per event); the batch
// fold's correctness is defined against it.
func referenceFold(prev *Snapshot, events []Event) *Snapshot {
	snap := prev
	for _, ev := range events {
		snap = Apply(snap, ev)
	}
	return snap
}

func randomEvents(rnd *rand.Rand, n int) []Event {
	msgs := []string{"m-0", "m-1", "m-2"}
	tools := []string{"tool-a", "tool-b"}
	reasons := []StatusReason{ReasonNone, ReasonStopped, ReasonFailed, ReasonInterrupted}
	phases := []Phase{PhaseIdle, PhaseRunning, PhaseWaitingApproval, PhaseFailed}
	texts := []string{"", "a", "bc", "defgh", "1234567890"}
	events := make([]Event, 0, n)
	for i := 0; i < n; i++ {
		if rnd.Intn(20) == 0 {
			events = append(events, nil)
			continue
		}
		switch rnd.Intn(11) {
		case 0, 1, 2: // messages
			msg := msgs[rnd.Intn(len(msgs))]
			text := texts[rnd.Intn(len(texts))]
			if rnd.Intn(2) == 0 {
				events = append(events, Token{SessionID: "s", MsgID: msg, Text: text})
			} else {
				events = append(events, Reasoning{SessionID: "s", MsgID: msg, Text: text})
			}
		case 3:
			events = append(events, ToolStart{CallID: tools[rnd.Intn(len(tools))], Name: "bash", Args: []byte(`{"cmd":"ls"}`)})
		case 4:
			events = append(events, ToolEnd{CallID: tools[rnd.Intn(len(tools))], Result: "ok", IsError: rnd.Intn(4) == 0, Cancelled: rnd.Intn(7) == 0, Duration: 1})
		case 5:
			events = append(events, ApprovalRequest{CallID: tools[rnd.Intn(len(tools))], Name: "write", Args: []byte(`{}`), ReviewReason: "policy"})
		case 6:
			events = append(events, Usage{Turns: rnd.Intn(2), Steps: rnd.Intn(3), In: rnd.Intn(100), Out: rnd.Intn(100), CacheHit: rnd.Intn(50), ContextPct: float64(rnd.Intn(100)), Advisory: texts[rnd.Intn(len(texts))]})
		case 7:
			events = append(events, Status{Phase: phases[rnd.Intn(len(phases))], Reason: reasons[rnd.Intn(len(reasons))]})
		case 8:
			events = append(events, TurnEnd{UserMsgID: msgs[rnd.Intn(len(msgs))], TurnID: "turn", Reason: reasons[rnd.Intn(len(reasons))]})
		case 9:
			events = append(events, Error{MsgID: msgs[rnd.Intn(len(msgs))], Err: errors.New("boom")})
		case 10:
			if rnd.Intn(2) == 0 {
				events = append(events, WorkspaceChanges{TurnID: "turn-1", Changes: []WorkspaceChange{{Path: "a.go", Status: "modified"}}})
			} else {
				events = append(events, unknownTestEvent{label: "x"})
			}
		}
	}
	return events
}

func TestApplyBatchMatchesSequentialApply(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		base := NewSnapshot()
		// Exercise both an empty base and an already-folded base.
		if trial%3 == 0 {
			base = referenceFold(base, randomEvents(rnd, 25))
		}
		events := randomEvents(rnd, 1+rnd.Intn(80))
		want := referenceFold(base, events)
		got := ApplyBatch(base, events)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: ApplyBatch != sequential Apply\nevents=%#v\ngot=%#v\nwant=%#v", trial, events, got, want)
		}
	}
}

func TestApplyBatchChunkedMatchesSingleBatch(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	for trial := 0; trial < 100; trial++ {
		base := NewSnapshot()
		events := randomEvents(rnd, 1+rnd.Intn(120))
		want := referenceFold(base, events)
		// Fold the same events in random chunk sizes; the final snapshot must
		// not depend on batch boundaries.
		got := base
		for i := 0; i < len(events); {
			n := 1 + rnd.Intn(17)
			if i+n > len(events) {
				n = len(events) - i
			}
			got = ApplyBatch(got, events[i:i+n])
			i += n
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: chunked ApplyBatch != sequential Apply", trial)
		}
	}
}

func TestApplyBatchKeepsPreviousSnapshotImmutable(t *testing.T) {
	base := NewSnapshot()
	base = Apply(base, Token{SessionID: "s", MsgID: "m", Text: "hello"})
	before := *base
	beforeMsgs := append([]Message(nil), base.Messages...)
	ApplyBatch(base, []Event{
		Token{SessionID: "s", MsgID: "m", Text: " world"},
		ToolStart{CallID: "t", Name: "bash"},
		Error{MsgID: "m", Err: errors.New("x")},
	})
	if !reflect.DeepEqual(base, &before) {
		t.Fatalf("ApplyBatch mutated the input snapshot: got %#v want %#v", base, &before)
	}
	if !reflect.DeepEqual(base.Messages, beforeMsgs) {
		t.Fatalf("ApplyBatch mutated the input messages slice")
	}
	// The returned snapshot must not share a mutable messages backing array
	// with the input: growing the batch result must not alter base.
	out := ApplyBatch(base, []Event{Token{SessionID: "s", MsgID: "new", Text: "x"}})
	_ = ApplyBatch(out, []Event{Token{SessionID: "s", MsgID: "another", Text: "y"}})
	if len(base.Messages) != 1 || base.Messages[0].Text != "hello" {
		t.Fatalf("base snapshot changed through the batch result: %#v", base.Messages)
	}
}

func TestApplyBatchNilEvents(t *testing.T) {
	base := Apply(nil, Token{MsgID: "m", Text: "a"})
	if got := ApplyBatch(base, []Event{nil, nil, nil}); got != base {
		t.Fatalf("all-nil batch returned a different snapshot")
	}
	got := ApplyBatch(base, []Event{nil, Token{MsgID: "m", Text: "b"}, nil})
	want := Apply(base, Token{MsgID: "m", Text: "b"})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("nil events changed the fold: got %#v want %#v", got, want)
	}
}
