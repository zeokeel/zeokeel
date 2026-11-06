package session

import (
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// newBatchTestController returns a minimal Controller backed by a real store
// and one persisted session. Only the persistence plumbing under test is set:
// appendEvent needs nothing else.
func newBatchTestController(t *testing.T) (*Controller, *store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ws, err := st.CreateWorkspace("batch", dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess, err := st.CreateSession(ws.ID, "batch test")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return &Controller{store: st}, st, sess.ID
}

func eventTypes(t *testing.T, st *store.Store, sessionID string) []string {
	t.Helper()
	events, err := st.ListEvents(sessionID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = ev.Type
	}
	return out
}

// TestEventBatchFlushesOnBoundary pins RT-10: streamed tokens are buffered, and
// the first non-streaming event flushes them before its own insert so the
// persisted trace keeps arrival order and contiguous sequence numbers.
func TestEventBatchFlushesOnBoundary(t *testing.T) {
	c, st, sessionID := newBatchTestController(t)

	for _, ev := range []state.Event{
		state.Token{SessionID: sessionID, MsgID: "m1", Text: "Hel"},
		state.Token{SessionID: sessionID, MsgID: "m1", Text: "lo"},
		state.Reasoning{SessionID: sessionID, MsgID: "m1", Text: "think"},
	} {
		if err := c.appendEvent(sessionID, ev); err != nil {
			t.Fatalf("appendEvent(%T): %v", ev, err)
		}
	}
	if got := eventTypes(t, st, sessionID); len(got) != 0 {
		t.Fatalf("events persisted before a boundary event = %v, want none", got)
	}

	if err := c.appendEvent(sessionID, state.Status{Phase: state.PhaseIdle}); err != nil {
		t.Fatalf("appendEvent(status): %v", err)
	}
	events, err := st.ListEvents(sessionID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	want := []string{EventToken, EventToken, EventReasoning, EventStatus}
	if len(events) != len(want) {
		t.Fatalf("events = %d (%v), want %d", len(events), eventTypes(t, st, sessionID), len(want))
	}
	for i, ev := range events {
		if ev.Type != want[i] {
			t.Fatalf("event %d type = %q, want %q (all: %v)", i, ev.Type, want[i], eventTypes(t, st, sessionID))
		}
		if ev.Seq != int64(i+1) {
			t.Fatalf("event %d seq = %d, want %d", i, ev.Seq, i+1)
		}
	}

	// The JSONL mirror must receive the same batch, once, in order.
	lines, err := store.ReadTranscript(st.Dir(), sessionID)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	if len(lines) != len(want) {
		t.Fatalf("transcript lines = %d, want %d", len(lines), len(want))
	}
}

// TestEventBatchFlushesOnSizeThreshold pins the eager flush before a boundary
// event: a long stream must not accumulate unbounded memory.
func TestEventBatchFlushesOnSizeThreshold(t *testing.T) {
	c, st, sessionID := newBatchTestController(t)
	chunk := strings.Repeat("x", eventBatchMaxBytes/2+1)
	if err := c.appendEvent(sessionID, state.Token{SessionID: sessionID, MsgID: "m1", Text: chunk}); err != nil {
		t.Fatalf("appendEvent: %v", err)
	}
	if err := c.appendEvent(sessionID, state.Token{SessionID: sessionID, MsgID: "m1", Text: chunk}); err != nil {
		t.Fatalf("appendEvent: %v", err)
	}
	if got := eventTypes(t, st, sessionID); len(got) != 2 {
		t.Fatalf("events after exceeding the byte cap = %v, want 2 persisted tokens", got)
	}
}

// TestEventBatchFlushesAfterDelay pins the stall path: a buffered token is
// persisted by the delayed flush even when the turn sends nothing else.
func TestEventBatchFlushesAfterDelay(t *testing.T) {
	c, st, sessionID := newBatchTestController(t)
	if err := c.appendEvent(sessionID, state.Token{SessionID: sessionID, MsgID: "m1", Text: "late"}); err != nil {
		t.Fatalf("appendEvent: %v", err)
	}
	if got := eventTypes(t, st, sessionID); len(got) != 0 {
		t.Fatalf("events before delay = %v, want none", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := eventTypes(t, st, sessionID); len(got) == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("buffered token was not flushed within the delay window")
}

// TestEventBatchInterleavedBoundaryOrder pins order across multiple flushes:
// tokens, a tool call, more tokens and a turn end must replay in arrival order.
func TestEventBatchInterleavedBoundaryOrder(t *testing.T) {
	c, st, sessionID := newBatchTestController(t)
	appendEv := func(ev state.Event) {
		t.Helper()
		if err := c.appendEvent(sessionID, ev); err != nil {
			t.Fatalf("appendEvent(%T): %v", ev, err)
		}
	}
	appendEv(state.Token{SessionID: sessionID, MsgID: "m1", Text: "a"})
	appendEv(state.ToolStart{CallID: "c1", Name: "read_file"})
	appendEv(state.Token{SessionID: sessionID, MsgID: "m1", Text: "b"})
	appendEv(state.ToolEnd{CallID: "c1", Result: "ok"})
	appendEv(state.Token{SessionID: sessionID, MsgID: "m1", Text: "c"})
	appendEv(state.TurnEnd{Reason: "completed"})

	want := []string{
		EventToken, EventToolStart, EventToken, EventToolEnd, EventToken, EventTurnEnd,
	}
	events, err := st.ListEvents(sessionID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", eventTypes(t, st, sessionID), want)
	}
	for i, ev := range events {
		if ev.Type != want[i] {
			t.Fatalf("event %d = %q, want %q (all: %v)", i, ev.Type, want[i], eventTypes(t, st, sessionID))
		}
		if ev.Seq != int64(i+1) {
			t.Fatalf("event %d seq = %d, want %d", i, ev.Seq, i+1)
		}
	}
}

// TestWorkspaceChangesEventRoundTrip pins FP-05's event plumbing: the display
// event encodes to the store, decodes back and folds into the snapshot, so an
// app can emit it after a turn without a schema change.
func TestWorkspaceChangesEventRoundTrip(t *testing.T) {
	ev := state.WorkspaceChanges{
		TurnID: "turn-1",
		Changes: []state.WorkspaceChange{
			{Path: "internal/app/app.go", Status: "modified"},
			{Path: "docs/new.md", Status: "added"},
		},
	}
	rec, err := encodeEvent(ev)
	if err != nil {
		t.Fatalf("encodeEvent: %v", err)
	}
	if rec.Type != EventWorkspaceChanges {
		t.Fatalf("event type = %q, want %q", rec.Type, EventWorkspaceChanges)
	}
	back, ok := decodeEvent(rec)
	if !ok {
		t.Fatal("decodeEvent did not recognise the workspace/changes event")
	}
	decoded, ok := back.(state.WorkspaceChanges)
	if !ok {
		t.Fatalf("decoded event = %T, want state.WorkspaceChanges", back)
	}
	if decoded.TurnID != "turn-1" || len(decoded.Changes) != 2 || decoded.Changes[0].Path != "internal/app/app.go" {
		t.Fatalf("decoded event = %+v", decoded)
	}

	snap := state.Apply(state.NewSnapshot(), ev)
	if snap.WorkspaceChangesTurn != "turn-1" || len(snap.WorkspaceChanges) != 2 {
		t.Fatalf("snapshot workspace changes = %+v (turn %q)", snap.WorkspaceChanges, snap.WorkspaceChangesTurn)
	}
}
