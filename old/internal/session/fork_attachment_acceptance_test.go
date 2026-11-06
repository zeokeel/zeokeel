package session_test

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/state"
)

// forkAttachmentChipsForPrompt returns the attachment chips on the snapshot's
// user message with text.
func forkAttachmentChipsForPrompt(t *testing.T, snap *state.Snapshot, text string) []state.Attachment {
	t.Helper()
	if snap == nil {
		t.Fatal("snapshot is nil")
	}
	for _, m := range snap.Messages {
		if m.Role == state.RoleUser && m.Text == text {
			return m.Attachments
		}
	}
	t.Fatalf("snapshot has no user message %q: %+v", text, snap.Messages)
	return nil
}

// forkAttachmentPNG decodes the 1x1 PNG used by the image attachment tests.
func forkAttachmentPNG(t *testing.T) []byte {
	t.Helper()
	const encoded = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode png fixture: %v", err)
	}
	return raw
}

// TestForkAttachmentReplaySnapshot pins the user-visible fork contract: text
// and image attachments follow the copied user message into the fork's
// snapshot, rows and private files, and remain replayable after a restart.
func TestForkAttachmentReplaySnapshot(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	ctx := context.Background()

	srcID, err := h.ctrl.CreateSession(ctx, "attached fork")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	textBody := "FORK-ATTACHMENT-MARKER-9c1d"
	textPath := writeAttachmentFixture(t, t.TempDir(), "note.txt", textBody)
	pngBody := forkAttachmentPNG(t)
	pngPath := writeAttachmentFixture(t, t.TempDir(), "pixel.png", string(pngBody))
	if _, err := h.ctrl.AttachFiles(ctx, srcID, []string{textPath, pngPath}); err != nil {
		t.Fatalf("AttachFiles: %v", err)
	}
	const prompt = "start with the files"
	if err := h.ctrl.Submit(ctx, srcID, prompt); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	srcAtts, err := h.store.ListAttachments(srcID)
	if err != nil || len(srcAtts) != 2 {
		t.Fatalf("source attachments = %d, %v; want 2", len(srcAtts), err)
	}
	srcChips := forkAttachmentChipsForPrompt(t, h.ctrl.Snapshot(), prompt)
	if len(srcChips) != 2 {
		t.Fatalf("source snapshot chips = %+v, want 2", srcChips)
	}

	forkID, err := h.ctrl.ForkSession(ctx, srcID)
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	forkRows, err := h.store.ListAttachments(forkID)
	if err != nil || len(forkRows) != 2 {
		t.Fatalf("fork attachments = %d, %v; want 2", len(forkRows), err)
	}
	forkChips := forkAttachmentChipsForPrompt(t, h.ctrl.Snapshot(), prompt)
	if len(forkChips) != 2 {
		t.Fatalf("fork snapshot chips = %+v, want 2", forkChips)
	}
	byName := make(map[string]state.Attachment, len(forkChips))
	for _, chip := range forkChips {
		byName[chip.Name] = chip
	}
	if byName["note.txt"].Kind != "text" || byName["pixel.png"].Kind != "image" {
		t.Fatalf("fork chips = %+v, want text note.txt and image pixel.png", forkChips)
	}

	srcIDs := make(map[string]bool, len(srcAtts))
	for _, a := range srcAtts {
		srcIDs[a.ID] = true
	}
	var forkUserID string
	for _, m := range h.ctrl.Snapshot().Messages {
		if m.Role == state.RoleUser && m.Text == prompt {
			forkUserID = m.MsgID
		}
	}
	if forkUserID == "" {
		t.Fatalf("fork snapshot has no user message: %+v", h.ctrl.Snapshot().Messages)
	}
	for _, row := range forkRows {
		if row.SessionID != forkID {
			t.Fatalf("fork attachment %q session = %q, want %q", row.Name, row.SessionID, forkID)
		}
		if srcIDs[row.ID] {
			t.Fatalf("fork attachment %q reused a source id", row.Name)
		}
		if row.MessageID != forkUserID {
			t.Fatalf("fork attachment %q message = %q, want %q", row.Name, row.MessageID, forkUserID)
		}
		got, err := h.store.ReadAttachmentData(row, 0)
		if err != nil {
			t.Fatalf("ReadAttachmentData(fork %q): %v", row.Name, err)
		}
		want := []byte(textBody)
		if row.Name == "pixel.png" {
			want = pngBody
		}
		if string(got) != string(want) {
			t.Fatalf("fork attachment %q bytes do not match the source", row.Name)
		}
	}

	// A restart rebuilds the fork snapshot from the store; the chips and the
	// bytes must still be there.
	h.restart()
	snap, err := h.ctrl.OpenSession(ctx, forkID)
	if err != nil {
		t.Fatalf("OpenSession(fork) after restart: %v", err)
	}
	reopened := forkAttachmentChipsForPrompt(t, snap, prompt)
	if len(reopened) != 2 {
		t.Fatalf("reopened fork chips = %+v, want 2", reopened)
	}
	for _, row := range forkRows {
		if _, err := h.store.ReadAttachmentData(row, 0); err != nil {
			t.Fatalf("reopened fork attachment %q unreadable: %v", row.Name, err)
		}
	}

	// The source session keeps its own rows and files.
	if got, err := h.store.ListAttachments(srcID); err != nil || len(got) != 2 {
		t.Fatalf("source attachments after fork = %d, %v; want 2", len(got), err)
	}
}

// TestExportSessionAttachmentLines pins Controller.ExportSession's attachment
// records: metadata, embedded bytes and, for text files, the verbatim body.
func TestExportSessionAttachmentLines(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	ctx := context.Background()

	id, err := h.ctrl.CreateSession(ctx, "exported attachments")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	textBody := "EXPORTED-ATTACHMENT-MARKER-4b7a"
	textPath := writeAttachmentFixture(t, t.TempDir(), "note.txt", textBody)
	pngBody := forkAttachmentPNG(t)
	pngPath := writeAttachmentFixture(t, t.TempDir(), "pixel.png", string(pngBody))
	if _, err := h.ctrl.AttachFiles(ctx, id, []string{textPath, pngPath}); err != nil {
		t.Fatalf("AttachFiles: %v", err)
	}
	const prompt = "export the files"
	if err := h.ctrl.Submit(ctx, id, prompt); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	var userID string
	for _, m := range h.ctrl.Snapshot().Messages {
		if m.Role == state.RoleUser && m.Text == prompt {
			userID = m.MsgID
		}
	}
	if userID == "" {
		t.Fatalf("snapshot has no user message %q", prompt)
	}

	path := filepath.Join(t.TempDir(), "attached.jsonl")
	if err := h.ctrl.ExportSession(ctx, id, path); err != nil {
		t.Fatalf("ExportSession: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if !strings.Contains(string(raw), textBody) {
		t.Fatalf("export does not embed the text attachment body: %s", raw)
	}

	records := readExportJSONL(t, path)
	attachments := make(map[string]map[string]any)
	for _, rec := range records {
		if kind, _ := rec["type"].(string); kind == "attachment" {
			name, _ := rec["name"].(string)
			attachments[name] = rec
		}
	}
	if len(attachments) != 2 {
		t.Fatalf("export attachment records = %d, want 2 (%+v)", len(attachments), attachments)
	}
	textRec := attachments["note.txt"]
	if textRec == nil || textRec["message_id"] != userID || textRec["kind"] != "text" {
		t.Fatalf("text attachment record = %+v", textRec)
	}
	if textRec["mime"] != "text/plain" || textRec["size"] != float64(len(textBody)) {
		t.Fatalf("text attachment metadata = %+v", textRec)
	}
	encodedText, ok := textRec["data"].(string)
	if !ok {
		t.Fatalf("text attachment has no base64 data: %+v", textRec)
	}
	if decoded, err := base64.StdEncoding.DecodeString(encodedText); err != nil || string(decoded) != textBody {
		t.Fatalf("text attachment data does not round-trip: %q, %v", decoded, err)
	}

	imgRec := attachments["pixel.png"]
	if imgRec == nil || imgRec["message_id"] != userID || imgRec["kind"] != "image" {
		t.Fatalf("image attachment record = %+v", imgRec)
	}
	encodedImage, ok := imgRec["data"].(string)
	if !ok {
		t.Fatalf("image attachment has no base64 data: %+v", imgRec)
	}
	if decoded, err := base64.StdEncoding.DecodeString(encodedImage); err != nil || string(decoded) != string(pngBody) {
		t.Fatalf("image attachment data does not round-trip: %v", err)
	}
}
