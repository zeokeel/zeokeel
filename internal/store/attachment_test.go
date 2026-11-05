package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestAttachmentRowsRoundTrip pins the durable attachment table: rows keep
// their session/message link, list in order, survive a reopen, and disappear
// with their session.
func TestAttachmentRowsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ws, err := s.CreateWorkspace("attachments", dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess, err := s.CreateSession(ws.ID, "attachment rows")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	msg, err := s.AppendMessage(Message{SessionID: sess.ID, Role: "user", Content: "hello"})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	attachmentDir, err := s.AttachmentDir(sess.ID)
	if err != nil {
		t.Fatalf("AttachmentDir: %v", err)
	}
	if err := os.MkdirAll(attachmentDir, 0o700); err != nil {
		t.Fatalf("create attachment dir: %v", err)
	}
	path := filepath.Join(attachmentDir, "note.txt")
	if err := os.WriteFile(path, []byte("durable"), 0o600); err != nil {
		t.Fatalf("write attachment file: %v", err)
	}

	stored, err := s.CreateAttachment(Attachment{
		SessionID: sess.ID,
		Name:      "note.txt",
		Path:      path,
		MIME:      "text/plain",
		Kind:      AttachmentKindText,
		Size:      7,
	})
	if err != nil {
		t.Fatalf("CreateAttachment: %v", err)
	}
	if stored.ID == "" {
		t.Fatal("CreateAttachment did not assign an id")
	}
	if err := s.SetAttachmentMessage(stored.ID, msg.ID); err != nil {
		t.Fatalf("SetAttachmentMessage: %v", err)
	}
	data, err := s.ReadAttachmentData(*stored, 4)
	if err != nil || string(data) != "dura" {
		t.Fatalf("ReadAttachmentData = %q, %v; want %q", data, err, "dura")
	}

	// Close and reopen the data directory: the row is durable.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	rows, err := s.ListAttachments(sess.ID)
	if err != nil {
		t.Fatalf("ListAttachments: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != stored.ID || rows[0].MessageID != msg.ID || rows[0].Kind != AttachmentKindText {
		t.Fatalf("reopened attachment rows = %+v", rows)
	}

	// Deleting the session removes the row and the per-session directory.
	if err := s.DeleteSession(sess.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if rows, err := s.ListAttachments(sess.ID); err != nil || len(rows) != 0 {
		t.Fatalf("attachments after delete = %d, %v; want 0", len(rows), err)
	}
	if _, err := os.Stat(attachmentDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("attachment dir after delete: %v", err)
	}
}
