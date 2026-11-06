package store

import (
	"errors"
	"testing"
	"time"
)

// replaceTestSession seeds a session with messages and returns it together with
// the ids in append order.
func replaceTestSession(t *testing.T, s *Store, contents []string) (*Session, []Message) {
	t.Helper()
	ws := mustWorkspace(t, s, "alpha", "/tmp/alpha")
	sess := mustSession(t, s, ws.ID, "compaction")
	base := time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)
	out := make([]Message, 0, len(contents))
	for i, content := range contents {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msg, err := s.AppendMessage(Message{
			SessionID: sess.ID,
			Role:      role,
			Content:   content,
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatalf("AppendMessage(%d): %v", i, err)
		}
		out = append(out, *msg)
	}
	return sess, out
}

// TestReplaceCompactedMessages proves the durable half of a manual /compact:
// the replaced prefix's rows disappear, the kept tail keeps its message ids and
// dense ordinals are restored, and the summary message lands in their place
// with the kept tail unchanged.
func TestReplaceCompactedMessages(t *testing.T) {
	s := openTestStore(t)
	sess, msgs := replaceTestSession(t, s, []string{
		"old question one", "old answer one",
		"old question two", "old answer two",
		"recent question", "recent answer",
	})
	replaced := []string{msgs[0].ID, msgs[1].ID, msgs[2].ID, msgs[3].ID}

	summary, err := s.ReplaceCompactedMessages(sess.ID, replaced, Message{
		Role:    "assistant",
		Content: "Earlier conversation compacted (4 messages)",
	}, nil)
	if err != nil {
		t.Fatalf("ReplaceCompactedMessages: %v", err)
	}
	if summary == nil || summary.ID == "" {
		t.Fatalf("summary = %+v, want a stored message", summary)
	}
	if summary.Ordinal != 1 {
		t.Fatalf("summary ordinal = %d, want 1", summary.Ordinal)
	}
	// The summary sits before the kept tail on the conversation's timeline, so
	// storeHistory's tool-call interleaving cannot emit old calls above it.
	if !summary.CreatedAt.Before(msgs[4].CreatedAt) {
		t.Fatalf("summary created_at = %v, want before the kept tail %v",
			summary.CreatedAt, msgs[4].CreatedAt)
	}

	got, err := s.ListMessages(sess.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	wantContents := []string{
		"Earlier conversation compacted (4 messages)",
		"recent question",
		"recent answer",
	}
	if len(got) != len(wantContents) {
		t.Fatalf("messages after replacement = %d, want %d: %+v", len(got), len(wantContents), got)
	}
	for i, want := range wantContents {
		if got[i].Content != want {
			t.Fatalf("message %d = %q, want %q", i, got[i].Content, want)
		}
		if got[i].Ordinal != int64(i+1) {
			t.Fatalf("message %d ordinal = %d, want dense %d", i, got[i].Ordinal, i+1)
		}
	}
	// The kept tail keeps its rows: same ids (attachments, feedback and turn
	// markers keep pointing at them).
	if got[1].ID != msgs[4].ID || got[2].ID != msgs[5].ID {
		t.Fatalf("kept tail ids = %q/%q, want %q/%q", got[1].ID, got[2].ID, msgs[4].ID, msgs[5].ID)
	}

	// Appending after a replacement continues the dense ordinal sequence.
	next, err := s.AppendMessage(Message{SessionID: sess.ID, Role: "user", Content: "newer"})
	if err != nil {
		t.Fatalf("AppendMessage after replacement: %v", err)
	}
	if next.Ordinal != 4 {
		t.Fatalf("message appended after replacement has ordinal %d, want 4", next.Ordinal)
	}
}

// TestReplaceCompactedMessagesToolCalls proves the replaced range's tool-call
// rows are removed while the calls that survive in the compacted tail stay.
func TestReplaceCompactedMessagesToolCalls(t *testing.T) {
	s := openTestStore(t)
	sess, msgs := replaceTestSession(t, s, []string{
		"old question", "old answer", "recent question", "recent answer",
	})
	base := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	for _, call := range []ToolCall{
		{ID: "old-call", SessionID: sess.ID, Name: "bash", Args: "{}", Status: "done", StartedAt: base},
		{ID: "kept-call", SessionID: sess.ID, Name: "bash", Args: "{}", Status: "done", StartedAt: base.Add(2 * time.Minute)},
	} {
		if err := s.RecordToolCall(call); err != nil {
			t.Fatalf("RecordToolCall(%s): %v", call.ID, err)
		}
	}
	replaced := []string{msgs[0].ID, msgs[1].ID}
	if _, err := s.ReplaceCompactedMessages(sess.ID, replaced, Message{
		Role: "assistant", Content: "summary",
	}, []string{"kept-call"}); err != nil {
		t.Fatalf("ReplaceCompactedMessages: %v", err)
	}

	calls, err := s.ToolCallsForSession(sess.ID)
	if err != nil {
		t.Fatalf("ToolCallsForSession: %v", err)
	}
	if len(calls) != 1 || calls[0].ID != "kept-call" {
		t.Fatalf("tool calls after replacement = %+v, want only kept-call", calls)
	}
	if _, err := s.GetToolCall("old-call"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetToolCall(old-call) = %v, want ErrNotFound", err)
	}
}

// TestReplaceCompactedMessagesValidation rejects a scope that is not the
// current prefix (compaction always replaces earlier history) and leaves the
// session untouched when it does.
func TestReplaceCompactedMessagesValidation(t *testing.T) {
	s := openTestStore(t)
	sess, msgs := replaceTestSession(t, s, []string{"one", "two", "three"})
	before, err := s.ListMessages(sess.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	cases := []struct {
		name     string
		replaced []string
	}{
		{"empty", nil},
		{"unknown id", []string{"does-not-exist"}},
		{"not a prefix", []string{msgs[1].ID, msgs[2].ID}},
		{"out of order prefix", []string{msgs[1].ID, msgs[0].ID}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.ReplaceCompactedMessages(sess.ID, tc.replaced, Message{
				Role: "assistant", Content: "summary",
			}, nil); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("ReplaceCompactedMessages(%v) = %v, want ErrInvalidArgument", tc.replaced, err)
			}
		})
	}
	if _, err := s.ReplaceCompactedMessages("missing-session", []string{msgs[0].ID}, Message{
		Role: "assistant", Content: "summary",
	}, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReplaceCompactedMessages(missing session) error = %v, want ErrNotFound", err)
	}

	after, err := s.ListMessages(sess.ID)
	if err != nil {
		t.Fatalf("ListMessages after rejected replacements: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("rejected replacements changed the message count: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if after[i].ID != before[i].ID || after[i].Ordinal != before[i].Ordinal {
			t.Fatalf("rejected replacement changed message %d: %+v -> %+v", i, before[i], after[i])
		}
	}
}

// TestReplaceCompactedMessagesAllAndEmptyTail covers replacing the entire
// conversation (no kept messages): the summary becomes the only row and all
// tool calls go.
func TestReplaceCompactedMessagesAllAndEmptyTail(t *testing.T) {
	s := openTestStore(t)
	sess, msgs := replaceTestSession(t, s, []string{"one", "two"})
	ids := []string{msgs[0].ID, msgs[1].ID}
	if err := s.RecordToolCall(ToolCall{
		ID: "call", SessionID: sess.ID, Name: "bash", Status: "done", StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordToolCall: %v", err)
	}
	if _, err := s.ReplaceCompactedMessages(sess.ID, ids, Message{
		Role: "assistant", Content: "everything compacted",
	}, nil); err != nil {
		t.Fatalf("ReplaceCompactedMessages: %v", err)
	}
	got, err := s.ListMessages(sess.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(got) != 1 || got[0].Content != "everything compacted" || got[0].Ordinal != 1 {
		t.Fatalf("messages after full replacement = %+v, want one summary at ordinal 1", got)
	}
	calls, err := s.ToolCallsForSession(sess.ID)
	if err != nil {
		t.Fatalf("ToolCallsForSession: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("tool calls after full replacement = %+v, want none", calls)
	}
}
