package store

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// forkExportTinyPNG decodes the 1x1 transparent PNG the attachment tests use.
func forkExportTinyPNG(t *testing.T) []byte {
	t.Helper()
	const encoded = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode png fixture: %v", err)
	}
	return raw
}

// mustForkExportAttachment writes body into the session's attachment directory and
// records a row for it.
func mustForkExportAttachment(t *testing.T, s *Store, sessionID, messageID, name, mime, kind string, body []byte) *Attachment {
	t.Helper()
	dir, err := s.AttachmentDir(sessionID)
	if err != nil {
		t.Fatalf("AttachmentDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create attachment dir: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write attachment %s: %v", name, err)
	}
	att, err := s.CreateAttachment(Attachment{
		SessionID: sessionID,
		MessageID: messageID,
		Name:      name,
		Path:      path,
		MIME:      mime,
		Kind:      kind,
		Size:      int64(len(body)),
	})
	if err != nil {
		t.Fatalf("CreateAttachment(%s): %v", name, err)
	}
	return att
}

// TestForkCopiesAttachments pins the fork attachment contract: rows are copied
// under fresh ids with message links remapped, the bytes live in the fork's own
// directory, the source is untouched, and deleting the fork removes only the
// fork's attachment state.
func TestForkAttachmentCopyKeepsTextAndImage(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "fork-attachment-ws", t.TempDir())
	src := mustSession(t, s, ws.ID, "attached source")

	user, err := s.AppendMessage(Message{
		SessionID: src.ID, Role: "user", Content: "see files", TurnID: "turn-1",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	textBody := []byte("hello attachment")
	textAtt := mustForkExportAttachment(t, s, src.ID, user.ID, "note.txt",
		"text/plain", AttachmentKindText, textBody)
	pngBody := forkExportTinyPNG(t)
	imgAtt := mustForkExportAttachment(t, s, src.ID, user.ID, "pixel.png",
		"image/png", AttachmentKindImage, pngBody)
	pendingBody := []byte("# staged, not sent")
	pendingAtt := mustForkExportAttachment(t, s, src.ID, "", "draft.md",
		"text/markdown", AttachmentKindText, pendingBody)

	srcAtts, err := s.ListAttachments(src.ID)
	if err != nil || len(srcAtts) != 3 {
		t.Fatalf("source attachments = %d, %v; want 3", len(srcAtts), err)
	}
	want := map[string]struct {
		id      string
		body    []byte
		mime    string
		kind    string
		pending bool
	}{
		"note.txt":  {id: textAtt.ID, body: textBody, mime: "text/plain", kind: AttachmentKindText},
		"pixel.png": {id: imgAtt.ID, body: pngBody, mime: "image/png", kind: AttachmentKindImage},
		"draft.md":  {id: pendingAtt.ID, body: pendingBody, mime: "text/markdown", kind: AttachmentKindText, pending: true},
	}

	fork, err := s.ForkSession(src.ID)
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}

	forkMessages, err := s.ListMessages(fork.ID)
	if err != nil || len(forkMessages) != 1 {
		t.Fatalf("fork messages = %d, %v; want 1", len(forkMessages), err)
	}
	forkUserID := forkMessages[0].ID
	if forkUserID == user.ID {
		t.Fatal("fork reused the source message id")
	}

	forkAtts, err := s.ListAttachments(fork.ID)
	if err != nil {
		t.Fatalf("ListAttachments(fork): %v", err)
	}
	if len(forkAtts) != len(srcAtts) {
		t.Fatalf("fork attachments = %d, want %d", len(forkAtts), len(srcAtts))
	}
	forkDir := filepath.Join(s.Dir(), "attachments", fork.ID)
	for _, a := range forkAtts {
		w, ok := want[a.Name]
		if !ok {
			t.Fatalf("unexpected fork attachment %q", a.Name)
		}
		if a.ID == w.id {
			t.Fatalf("fork reused the source attachment id for %q", a.Name)
		}
		if a.SessionID != fork.ID {
			t.Fatalf("fork attachment %q session = %q, want %q", a.Name, a.SessionID, fork.ID)
		}
		if a.MIME != w.mime || a.Kind != w.kind {
			t.Fatalf("fork attachment %q = %q/%q, want %q/%q", a.Name, a.MIME, a.Kind, w.mime, w.kind)
		}
		if a.Size != int64(len(w.body)) {
			t.Fatalf("fork attachment %q size = %d, want %d", a.Name, a.Size, len(w.body))
		}
		if filepath.Dir(a.Path) != forkDir {
			t.Fatalf("fork attachment %q path = %q, want under %q", a.Name, a.Path, forkDir)
		}
		if w.pending {
			if a.MessageID != "" {
				t.Fatalf("pending attachment %q gained message %q", a.Name, a.MessageID)
			}
		} else if a.MessageID != forkUserID {
			t.Fatalf("fork attachment %q message = %q, want fork user %q", a.Name, a.MessageID, forkUserID)
		}
		got, err := s.ReadAttachmentData(a, 0)
		if err != nil {
			t.Fatalf("ReadAttachmentData(fork %q): %v", a.Name, err)
		}
		if !bytes.Equal(got, w.body) {
			t.Fatalf("fork attachment %q bytes = %q, want %q", a.Name, got, w.body)
		}
	}

	// The source rows and files are untouched.
	if again, err := s.ListAttachments(src.ID); err != nil || len(again) != 3 {
		t.Fatalf("source attachments after fork = %d, %v; want 3", len(again), err)
	}
	for _, a := range srcAtts {
		got, err := s.ReadAttachmentData(a, 0)
		if err != nil {
			t.Fatalf("source attachment %q unreadable after fork: %v", a.Name, err)
		}
		if !bytes.Equal(got, want[a.Name].body) {
			t.Fatalf("source attachment %q changed after fork", a.Name)
		}
	}

	// Deleting the fork removes only the fork's rows and bytes.
	if err := s.DeleteSession(fork.ID); err != nil {
		t.Fatalf("DeleteSession(fork): %v", err)
	}
	if rows, err := s.ListAttachments(fork.ID); err != nil || len(rows) != 0 {
		t.Fatalf("fork attachments after delete = %d, %v; want 0", len(rows), err)
	}
	if _, err := os.Stat(forkDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fork attachment dir still exists after delete: %v", err)
	}
	if rows, err := s.ListAttachments(src.ID); err != nil || len(rows) != 3 {
		t.Fatalf("source attachments after fork delete = %d, %v; want 3", len(rows), err)
	}
	for _, a := range srcAtts {
		if _, err := s.ReadAttachmentData(a, 0); err != nil {
			t.Fatalf("source attachment %q unreadable after fork delete: %v", a.Name, err)
		}
	}
}

// TestExportSessionIncludesAttachments pins the attachment records in the
// JSONL export: metadata plus embedded bytes, with text attachments also
// readable verbatim, and a missing blob degrading to metadata only.
func TestExportSessionAttachmentRecordsAndPayload(t *testing.T) {
	s := openTestStore(t)
	ws := mustWorkspace(t, s, "export-attachment-ws", t.TempDir())
	sess := mustSession(t, s, ws.ID, "export attachments")
	user, err := s.AppendMessage(Message{
		SessionID: sess.ID, Role: "user", Content: "export the files",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	textBody := []byte("embedded attachment body")
	textAtt := mustForkExportAttachment(t, s, sess.ID, user.ID, "note.txt",
		"text/plain", AttachmentKindText, textBody)
	pngBody := forkExportTinyPNG(t)
	imgAtt := mustForkExportAttachment(t, s, sess.ID, user.ID, "pixel.png",
		"image/png", AttachmentKindImage, pngBody)
	goneAtt := mustForkExportAttachment(t, s, sess.ID, user.ID, "gone.txt",
		"text/plain", AttachmentKindText, []byte("vanished"))
	if err := os.Remove(goneAtt.Path); err != nil {
		t.Fatalf("remove gone attachment: %v", err)
	}

	data, err := s.ExportSessionJSONL(sess.ID)
	if err != nil {
		t.Fatalf("ExportSessionJSONL: %v", err)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatal("export does not end with a newline")
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	attachments := make(map[string]map[string]any)
	for i, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("export line %d is not JSON: %v", i+1, err)
		}
		if rec["type"] == "attachment" {
			name, _ := rec["name"].(string)
			attachments[name] = rec
		}
	}
	if len(attachments) != 3 {
		t.Fatalf("attachment records = %d, want 3 (%+v)", len(attachments), attachments)
	}

	textRec := attachments["note.txt"]
	if textRec == nil {
		t.Fatalf("export has no note.txt attachment record: %+v", attachments)
	}
	if textRec["id"] != textAtt.ID || textRec["session_id"] != sess.ID || textRec["message_id"] != user.ID {
		t.Fatalf("text attachment identity = %+v", textRec)
	}
	if textRec["mime"] != "text/plain" || textRec["kind"] != AttachmentKindText {
		t.Fatalf("text attachment type = %+v, want text/plain text", textRec)
	}
	if textRec["size"] != float64(len(textBody)) {
		t.Fatalf("text attachment size = %v, want %d", textRec["size"], len(textBody))
	}
	if textRec["encoding"] != "base64" {
		t.Fatalf("text attachment encoding = %v, want base64", textRec["encoding"])
	}
	encodedText, ok := textRec["data"].(string)
	if !ok {
		t.Fatalf("text attachment has no base64 data: %+v", textRec)
	}
	decodedText, err := base64.StdEncoding.DecodeString(encodedText)
	if err != nil || !bytes.Equal(decodedText, textBody) {
		t.Fatalf("text attachment data = %q, %v; want %q", decodedText, err, textBody)
	}
	if textRec["text"] != string(textBody) {
		t.Fatalf("text attachment text = %v, want %q", textRec["text"], textBody)
	}

	imgRec := attachments["pixel.png"]
	if imgRec == nil || imgRec["id"] != imgAtt.ID || imgRec["kind"] != AttachmentKindImage {
		t.Fatalf("image attachment record = %+v", imgRec)
	}
	encodedImage, ok := imgRec["data"].(string)
	if !ok {
		t.Fatalf("image attachment has no base64 data: %+v", imgRec)
	}
	decodedImage, err := base64.StdEncoding.DecodeString(encodedImage)
	if err != nil || !bytes.Equal(decodedImage, pngBody) {
		t.Fatalf("image attachment data does not round-trip: %v", err)
	}
	if _, ok := imgRec["text"]; ok {
		t.Fatalf("image attachment should not embed text: %+v", imgRec)
	}

	// A missing file keeps its record but has no embedded payload.
	goneRec := attachments["gone.txt"]
	if goneRec == nil || goneRec["id"] != goneAtt.ID {
		t.Fatalf("missing-file attachment record = %+v", goneRec)
	}
	if _, ok := goneRec["data"]; ok {
		t.Fatalf("missing-file attachment has data: %+v", goneRec)
	}

	// The pre-existing record types are still present and valid.
	counts := make(map[string]int)
	for _, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("export line is not JSON: %v", err)
		}
		kind, _ := rec["type"].(string)
		counts[kind]++
	}
	if counts["session"] != 1 || counts["message"] != 1 {
		t.Fatalf("export record counts = %+v, want 1 session and 1 message", counts)
	}
}
