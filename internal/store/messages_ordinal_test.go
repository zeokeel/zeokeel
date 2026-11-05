package store

import (
	"testing"
	"time"
)

// TestAppendMessageAssignsContiguousOrdinal pins AR-07: every persisted message
// gets a per-session ordinal starting at 1, and ListMessages returns insertion
// order based on that column.
func TestAppendMessageAssignsContiguousOrdinal(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "alpha", "/tmp/alpha")
	sess := mustSession(t, s, ws.ID, "ordinals")

	for i, content := range []string{"first", "second", "third"} {
		out, err := s.AppendMessage(Message{SessionID: sess.ID, Role: "user", Content: content})
		if err != nil {
			t.Fatalf("AppendMessage(%d): %v", i, err)
		}
		if out.Ordinal != int64(i+1) {
			t.Fatalf("AppendMessage(%d).Ordinal = %d, want %d", i, out.Ordinal, i+1)
		}
	}
	got, err := s.ListMessages(sess.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	for i, msg := range got {
		if msg.Ordinal != int64(i+1) {
			t.Fatalf("ListMessages[%d].Ordinal = %d, want %d", i, msg.Ordinal, i+1)
		}
	}
}

// TestListMessagesOrderIgnoresCreatedAt pins that ordering reads only the
// ordinal column: a caller-supplied timestamp (possibly skewed by a clock
// change or import) must not reorder messages.
func TestListMessagesOrderIgnoresCreatedAt(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "alpha", "/tmp/alpha")
	sess := mustSession(t, s, ws.ID, "skewed")

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	contents := []string{"first", "second", "third"}
	stamps := []time.Time{base.Add(2 * time.Hour), base.Add(1 * time.Hour), base}
	for i := range contents {
		if _, err := s.AppendMessage(Message{
			SessionID: sess.ID, Role: "user", Content: contents[i], CreatedAt: stamps[i],
		}); err != nil {
			t.Fatalf("AppendMessage(%d): %v", i, err)
		}
	}
	got, err := s.ListMessages(sess.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(got) != len(contents) {
		t.Fatalf("messages = %d, want %d", len(got), len(contents))
	}
	for i := range contents {
		if got[i].Content != contents[i] {
			t.Fatalf("message %d = %q, want insertion order %q (created_at must not drive ordering)", i, got[i].Content, contents[i])
		}
	}
}

// TestOrdinalOrderSurvivesReopen pins that reopening an index keeps the same
// message order (the ordinal column is persisted, not derived from events).
func TestOrdinalOrderSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws := mustWorkspace(t, s, "alpha", "/tmp/alpha")
	sess := mustSession(t, s, ws.ID, "reopen")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, content := range []string{"one", "two", "three"} {
		if _, err := s.AppendMessage(Message{
			SessionID: sess.ID, Role: "user", Content: content, CreatedAt: base.Add(-time.Duration(i) * time.Hour),
		}); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer again.Close()
	got, err := again.ListMessages(sess.ID)
	if err != nil {
		t.Fatalf("ListMessages after reopen: %v", err)
	}
	want := []string{"one", "two", "three"}
	for i := range want {
		if got[i].Content != want[i] {
			t.Fatalf("reopened order[%d] = %q, want %q", i, got[i].Content, want[i])
		}
	}
}
