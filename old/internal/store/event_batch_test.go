package store

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestAppendEventsBatchAssignsContiguousSeqs pins the batch insert contract:
// one transaction, arrival order preserved, contiguous sequence numbers, and
// per-event payload/timestamp round-trip.
func TestAppendEventsBatchAssignsContiguousSeqs(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "alpha", "/tmp/alpha")
	sess := mustSession(t, s, ws.ID, "batched")

	events := make([]Event, 0, 5)
	for i := 0; i < 5; i++ {
		events = append(events, Event{
			Type:    "token",
			Payload: json.RawMessage(`{"text":"chunk` + string(rune('0'+i)) + `"}`),
		})
	}
	written, err := s.AppendEvents(sess.ID, events)
	if err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}
	if len(written) != len(events) {
		t.Fatalf("AppendEvents returned %d events, want %d", len(written), len(events))
	}
	for i := range written {
		if written[i].Seq != int64(i+1) {
			t.Fatalf("written[%d].Seq = %d, want %d", i, written[i].Seq, i+1)
		}
		if written[i].SessionID != sess.ID {
			t.Fatalf("written[%d].SessionID = %q, want %q", i, written[i].SessionID, sess.ID)
		}
	}

	persisted, err := s.ListEvents(sess.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(persisted) != len(events) {
		t.Fatalf("persisted %d events, want %d", len(persisted), len(events))
	}
	for i, ev := range persisted {
		if ev.Seq != int64(i+1) {
			t.Fatalf("persisted[%d].Seq = %d, want %d", i, ev.Seq, i+1)
		}
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			t.Fatalf("persisted[%d] payload %q: %v", i, ev.Payload, err)
		}
		want := "chunk" + string(rune('0'+i))
		if payload.Text != want {
			t.Fatalf("persisted[%d] text = %q, want %q", i, payload.Text, want)
		}
	}

	// Appending after the batch continues the sequence instead of restarting it.
	next, err := s.AppendEvent(sess.ID, Event{Type: "status"})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if next.Seq != int64(len(events)+1) {
		t.Fatalf("next seq = %d, want %d", next.Seq, len(events)+1)
	}
}

// TestAppendEventsRejectsInvalidPayloadAtomically pins the all-or-nothing
// contract: a malformed payload must write none of the batch.
func TestAppendEventsRejectsInvalidPayloadAtomically(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "alpha", "/tmp/alpha")
	sess := mustSession(t, s, ws.ID, "atomic")

	_, err := s.AppendEvents(sess.ID, []Event{
		{Type: "token", Payload: json.RawMessage(`{"text":"ok"}`)},
		{Type: "token", Payload: json.RawMessage(`{not json`)},
	})
	if err == nil {
		t.Fatal("AppendEvents with an invalid payload = nil error")
	}
	persisted, err := s.ListEvents(sess.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(persisted) != 0 {
		t.Fatalf("invalid batch wrote %d events, want 0", len(persisted))
	}
}

// TestAppendEventsEmptyBatchIsNoop pins the no-op guard (a stalled stream can
// flush an empty buffer).
func TestAppendEventsEmptyBatchIsNoop(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "alpha", "/tmp/alpha")
	sess := mustSession(t, s, ws.ID, "empty")
	written, err := s.AppendEvents(sess.ID, nil)
	if err != nil {
		t.Fatalf("AppendEvents(nil): %v", err)
	}
	if written != nil {
		t.Fatalf("AppendEvents(nil) = %v, want nil", written)
	}
	if persisted, err := s.ListEvents(sess.ID); err != nil || len(persisted) != 0 {
		t.Fatalf("ListEvents after empty batch = %v, %v; want 0, nil", persisted, err)
	}
}

// TestAppendTranscriptRecordsBatch pins the single-open/single-fsync batch
// mirror: every record becomes one JSONL line in order and remains readable.
func TestAppendTranscriptRecordsBatch(t *testing.T) {
	dir := t.TempDir()
	records := []any{
		map[string]any{"seq": 1, "type": "token"},
		map[string]any{"seq": 2, "type": "token"},
		map[string]any{"seq": 3, "type": "status"},
	}
	if err := AppendTranscriptRecords(dir, "sess-batch", records); err != nil {
		t.Fatalf("AppendTranscriptRecords: %v", err)
	}
	lines, err := ReadTranscript(dir, "sess-batch")
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	if len(lines) != len(records) {
		t.Fatalf("ReadTranscript returned %d lines, want %d", len(lines), len(records))
	}
	for i, line := range lines {
		var got struct {
			Seq int `json:"seq"`
		}
		if err := json.Unmarshal(line, &got); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if got.Seq != i+1 {
			t.Fatalf("line %d seq = %d, want %d", i, got.Seq, i+1)
		}
	}
	data, err := os.ReadFile(transcriptPath(dir, "sess-batch"))
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	if got := strings.Count(string(data), "\n"); got != len(records) {
		t.Fatalf("transcript has %d newlines, want %d", got, len(records))
	}
}
