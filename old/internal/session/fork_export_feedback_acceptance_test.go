package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// readExportJSONL decodes every non-empty line of a JSONL export.
func readExportJSONL(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export %s: %v", path, err)
	}
	if len(data) == 0 {
		t.Fatalf("export %s is empty", path)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Fatalf("export %s does not end with a newline", path)
	}
	var out []map[string]any
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("export line %d is not JSON: %v (%q)", i+1, err, line)
		}
		out = append(out, rec)
	}
	return out
}

// TestForkOpensFork pins Controller.ForkSession: it copies the source, makes
// the copy current, and loads the copied history into the live snapshot.
func TestForkOpensFork(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	ctx := context.Background()

	srcID, err := h.ctrl.CreateSession(ctx, "forkable")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, srcID, "hello fork"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	srcMessages, err := h.store.ListMessages(srcID)
	if err != nil || len(srcMessages) < 2 {
		t.Fatalf("source messages = %d, %v; want at least 2", len(srcMessages), err)
	}

	forkID, err := h.ctrl.ForkSession(ctx, srcID)
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if forkID == "" || forkID == srcID {
		t.Fatalf("fork id = %q, want a fresh id (source %q)", forkID, srcID)
	}
	if got := h.ctrl.CurrentSessionID(); got != forkID {
		t.Fatalf("current session after fork = %q, want %q", got, forkID)
	}

	sess, err := h.ctrl.CurrentSession()
	if err != nil {
		t.Fatalf("CurrentSession: %v", err)
	}
	if sess.Title != "forkable (fork)" {
		t.Fatalf("fork title = %q, want %q", sess.Title, "forkable (fork)")
	}
	if sess.Status != store.SessionIdle {
		t.Fatalf("fork status = %q, want idle", sess.Status)
	}

	snap := h.ctrl.Snapshot()
	if snap == nil || snap.SessionID != forkID {
		t.Fatalf("snapshot = %+v, want session %q", snap, forkID)
	}
	if snap.Phase != state.PhaseIdle {
		t.Fatalf("fork snapshot phase = %q, want idle", snap.Phase)
	}
	if len(snap.Messages) != len(srcMessages) {
		t.Fatalf("fork snapshot messages = %d, want %d", len(snap.Messages), len(srcMessages))
	}
	for i, want := range srcMessages {
		got := snap.Messages[i]
		if got.Role != state.Role(want.Role) || got.Text != want.Content {
			t.Fatalf("fork message %d = %s/%q, want %s/%q",
				i, got.Role, got.Text, want.Role, want.Content)
		}
	}

	// The source still exists with its own history.
	src, err := h.store.GetSession(srcID)
	if err != nil {
		t.Fatalf("GetSession(source): %v", err)
	}
	if src.Title != "forkable" {
		t.Fatalf("source title = %q, want unchanged", src.Title)
	}
	if msgs, err := h.store.ListMessages(srcID); err != nil || len(msgs) != len(srcMessages) {
		t.Fatalf("source messages after fork = %d, %v; want %d, nil", len(msgs), err, len(srcMessages))
	}

	// Forking an unknown session reports not-found through the store error.
	if _, err := h.ctrl.ForkSession(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ForkSession(missing) error = %v, want store.ErrNotFound", err)
	}
}

// TestExportSessionJSONL pins Controller.ExportSession's on-disk format: one
// JSON object per line for the session, its messages and its events.
func TestExportSessionJSONL(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	ctx := context.Background()

	id, err := h.ctrl.CreateSession(ctx, "exportable")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, "export me"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := h.ctrl.ExportSession(ctx, id, path); err != nil {
		t.Fatalf("ExportSession: %v", err)
	}
	records := readExportJSONL(t, path)

	counts := make(map[string]int)
	var sessionRec map[string]any
	var messageText []string
	for _, rec := range records {
		kind, _ := rec["type"].(string)
		counts[kind]++
		switch kind {
		case "session":
			sessionRec = rec
		case "message":
			if content, ok := rec["content"].(string); ok {
				messageText = append(messageText, content)
			}
		case "event":
			if _, ok := rec["event_type"].(string); !ok {
				t.Fatalf("event record missing event_type: %+v", rec)
			}
		}
	}
	if counts["session"] != 1 {
		t.Fatalf("session records = %d, want 1 (%+v)", counts["session"], counts)
	}
	if counts["message"] < 2 {
		t.Fatalf("message records = %d, want at least 2 (%+v)", counts["message"], counts)
	}
	if counts["event"] < 1 {
		t.Fatalf("event records = %d, want at least 1 (%+v)", counts["event"], counts)
	}
	if sessionRec == nil || sessionRec["id"] != id || sessionRec["title"] != "exportable" {
		t.Fatalf("session record = %+v, want id %q title %q", sessionRec, id, "exportable")
	}
	joined := strings.Join(messageText, "\n")
	if !strings.Contains(joined, "export me") || !strings.Contains(joined, "echo:export me") {
		t.Fatalf("exported messages = %q, want user and assistant text", messageText)
	}

	// Re-exporting to the same chosen path replaces the file instead of
	// appending a second copy of the history.
	if err := h.ctrl.ExportSession(ctx, id, path); err != nil {
		t.Fatalf("ExportSession (overwrite): %v", err)
	}
	if again := readExportJSONL(t, path); len(again) != len(records) {
		t.Fatalf("re-export line count = %d, want %d", len(again), len(records))
	}

	// A missing session is reported instead of writing a partial file.
	missingPath := filepath.Join(t.TempDir(), "missing.jsonl")
	if err := h.ctrl.ExportSession(ctx, "missing", missingPath); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ExportSession(missing) error = %v, want store.ErrNotFound", err)
	}
	if _, err := os.Stat(missingPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing export left a file behind: %v", err)
	}
}

// TestFeedbackPersistsAndExports pins feedback as durable data: a rating is
// written to the store and snapshot, survives a controller restart, and is
// part of the JSONL export.
func TestFeedbackPersistsAndExports(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	ctx := context.Background()

	id, err := h.ctrl.CreateSession(ctx, "feedback")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, "rate me"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	var msgID string
	for _, m := range h.ctrl.Snapshot().Messages {
		if m.Role == state.RoleAssistant && m.Text != "" {
			msgID = m.MsgID
		}
	}
	if msgID == "" {
		t.Fatalf("no assistant message in snapshot: %+v", h.ctrl.Snapshot().Messages)
	}

	if err := h.ctrl.RecordFeedback(ctx, id, msgID, "good", "clear answer"); err != nil {
		t.Fatalf("RecordFeedback: %v", err)
	}
	if got := h.ctrl.Snapshot().Feedback[msgID]; got != "good" {
		t.Fatalf("snapshot feedback = %q, want good", got)
	}

	rows, err := h.store.ListFeedback(id)
	if err != nil {
		t.Fatalf("ListFeedback: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("feedback rows = %d, want 1", len(rows))
	}
	if rows[0].MessageID != msgID || rows[0].Rating != "good" || rows[0].Note != "clear answer" {
		t.Fatalf("feedback row = %+v, want message %q good/clear answer", rows[0], msgID)
	}

	// The legacy settings key stays in sync with the row so the existing
	// message-action tests (and older builds reading the setting) agree.
	if got, err := h.store.GetSetting("feedback." + id + "." + msgID); err != nil || got != "good" {
		t.Fatalf("legacy feedback setting = %q, %v; want good, nil", got, err)
	}

	// Reopen through a fresh controller over the same store: the rating is
	// still reflected in the snapshot.
	h.restart()
	if _, err := h.ctrl.OpenSession(ctx, id); err != nil {
		t.Fatalf("OpenSession after restart: %v", err)
	}
	if got := h.ctrl.Snapshot().Feedback[msgID]; got != "good" {
		t.Fatalf("reopened snapshot feedback = %q, want good", got)
	}

	path := filepath.Join(t.TempDir(), "feedback.jsonl")
	if err := h.ctrl.ExportSession(ctx, id, path); err != nil {
		t.Fatalf("ExportSession: %v", err)
	}
	records := readExportJSONL(t, path)
	foundFeedback := false
	for _, rec := range records {
		kind, _ := rec["type"].(string)
		if kind != "feedback" {
			continue
		}
		foundFeedback = true
		if rec["message_id"] != msgID || rec["rating"] != "good" || rec["note"] != "clear answer" {
			t.Fatalf("exported feedback = %+v, want message %q good/clear answer", rec, msgID)
		}
	}
	if !foundFeedback {
		t.Fatalf("export has no feedback record: %+v", records)
	}
}
