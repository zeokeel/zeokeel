package session_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// writeAttachmentFixture writes content to a file under dir and returns its path.
func writeAttachmentFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write attachment %s: %v", name, err)
	}
	return path
}

// userMessage returns the last user message of req.
func lastUserRequestMessage(req runtime.Request) (runtime.Message, bool) {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == runtime.RoleUser {
			return req.Messages[i], true
		}
	}
	return runtime.Message{}, false
}

// TestAttachmentCaps pins the per-turn and per-file admission limits: five
// attachments are accepted, a sixth is refused, and a file over 25 MiB is
// refused before anything is copied into the data directory.
func TestAttachmentCaps(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "caps")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	srcDir := t.TempDir()
	paths := make([]string, 0, 6)
	for i := 0; i < 6; i++ {
		paths = append(paths, writeAttachmentFixture(t, srcDir, fmt.Sprintf("note-%d.txt", i), fmt.Sprintf("note %d", i)))
	}

	atts, err := h.ctrl.AttachFiles(ctx, id, paths[:session.MaxAttachmentsPerTurn])
	if err != nil {
		t.Fatalf("AttachFiles(five): %v", err)
	}
	if len(atts) != session.MaxAttachmentsPerTurn {
		t.Fatalf("attachments = %d, want %d", len(atts), session.MaxAttachmentsPerTurn)
	}
	rows, err := h.store.ListAttachments(id)
	if err != nil {
		t.Fatalf("ListAttachments: %v", err)
	}
	if len(rows) != session.MaxAttachmentsPerTurn {
		t.Fatalf("stored attachments = %d, want %d", len(rows), session.MaxAttachmentsPerTurn)
	}

	if _, err := h.ctrl.AttachFiles(ctx, id, paths[session.MaxAttachmentsPerTurn:]); !errors.Is(err, session.ErrAttachmentLimit) {
		t.Fatalf("sixth attachment error = %v, want ErrAttachmentLimit", err)
	}
	if rows, err := h.store.ListAttachments(id); err != nil || len(rows) != session.MaxAttachmentsPerTurn {
		t.Fatalf("stored attachments after refusal = %d, %v; want %d", len(rows), err, session.MaxAttachmentsPerTurn)
	}

	// A file larger than the 25 MiB per-file cap is refused as a whole batch,
	// so no partial copy is left behind.
	bigID, err := h.ctrl.CreateSession(ctx, "big")
	if err != nil {
		t.Fatalf("CreateSession(big): %v", err)
	}
	bigDir := t.TempDir()
	big := filepath.Join(bigDir, "big.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatalf("create big file: %v", err)
	}
	if err := f.Truncate(session.MaxAttachmentBytes + 1); err != nil {
		f.Close()
		t.Fatalf("truncate big file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close big file: %v", err)
	}
	if _, err := h.ctrl.AttachFiles(ctx, bigID, []string{big}); !errors.Is(err, session.ErrAttachmentTooLarge) {
		t.Fatalf("oversize attachment error = %v, want ErrAttachmentTooLarge", err)
	}
	if rows, err := h.store.ListAttachments(bigID); err != nil || len(rows) != 0 {
		t.Fatalf("stored attachments after oversize refusal = %d, %v; want 0", len(rows), err)
	}
	if snap := h.ctrl.Snapshot(); len(snap.Attachments) != 0 {
		t.Fatalf("snapshot attachments after oversize refusal = %+v", snap.Attachments)
	}
}

// TestAttachmentTextInjected proves a text attachment reaches the provider as
// part of the user's prompt, truncated to 64 KiB, while the snapshot records
// the attachment against the user message instead of expanding it in the UI.
func TestAttachmentTextInjected(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	h := newHarness(t, provider)
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "text")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	marker := "SHOULD-NOT-APPEAR"
	body := strings.Repeat("A", int(session.MaxTextAttachmentBytes)) + marker
	src := writeAttachmentFixture(t, t.TempDir(), "notes.txt", body)
	if _, err := h.ctrl.AttachFiles(ctx, id, []string{src}); err != nil {
		t.Fatalf("AttachFiles: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, "summarize this"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	req, ok := provider.requestAt(0)
	if !ok {
		t.Fatal("provider received no request")
	}
	msg, ok := lastUserRequestMessage(req)
	if !ok {
		t.Fatalf("provider request has no user message: %+v", req.Messages)
	}
	if !strings.Contains(msg.Content, "summarize this") {
		t.Fatalf("user content missing prompt: %q", msg.Content)
	}
	if !strings.Contains(msg.Content, strings.Repeat("A", 1024)) {
		t.Fatal("user content missing text attachment body")
	}
	if strings.Contains(msg.Content, marker) {
		t.Fatal("text attachment was not truncated at 64 KiB")
	}
	if !strings.Contains(msg.Content, "notes.txt") {
		t.Fatalf("user content missing attachment name: %q", msg.Content)
	}

	var chip *state.Message
	for i := range h.ctrl.Snapshot().Messages {
		m := h.ctrl.Snapshot().Messages[i]
		if m.Role == state.RoleUser && m.Text == "summarize this" {
			chip = &h.ctrl.Snapshot().Messages[i]
			break
		}
	}
	if chip == nil {
		t.Fatalf("snapshot has no user message: %+v", h.ctrl.Snapshot().Messages)
	}
	if len(chip.Attachments) != 1 || chip.Attachments[0].Name != "notes.txt" {
		t.Fatalf("user message attachments = %+v, want notes.txt", chip.Attachments)
	}
	if len(h.ctrl.Snapshot().Attachments) != 0 {
		t.Fatalf("pending attachments after submit = %+v, want none", h.ctrl.Snapshot().Attachments)
	}
}

// TestAttachmentImageContentPart proves an image attachment becomes an
// image data URL content part on the provider's user message.
func TestAttachmentImageContentPart(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	h := newHarness(t, provider)
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "image")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// A 1x1 transparent PNG.
	const pngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
	raw, err := base64.StdEncoding.DecodeString(pngBase64)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	src := writeAttachmentFixture(t, t.TempDir(), "pixel.png", string(raw))
	if _, err := h.ctrl.AttachFiles(ctx, id, []string{src}); err != nil {
		t.Fatalf("AttachFiles: %v", err)
	}
	rows, err := h.store.ListAttachments(id)
	if err != nil || len(rows) != 1 {
		t.Fatalf("stored attachments = %d, %v; want 1", len(rows), err)
	}
	if rows[0].Kind != store.AttachmentKindImage || rows[0].MIME != "image/png" {
		t.Fatalf("stored image = %+v, want image/png", rows[0])
	}

	if err := h.ctrl.Submit(ctx, id, "look at this"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	req, ok := provider.requestAt(0)
	if !ok {
		t.Fatal("provider received no request")
	}
	msg, ok := lastUserRequestMessage(req)
	if !ok {
		t.Fatalf("provider request has no user message: %+v", req.Messages)
	}
	if len(msg.Images) != 1 {
		t.Fatalf("user message images = %+v, want one ImagePart", msg.Images)
	}
	if !strings.HasPrefix(msg.Images[0].URL, "data:image/png;base64,") {
		t.Fatalf("image URL = %q, want image/png data URL", msg.Images[0].URL)
	}
	encoded := strings.TrimPrefix(msg.Images[0].URL, "data:image/png;base64,")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode image data URL: %v", err)
	}
	if string(decoded) != string(raw) {
		t.Fatal("image data URL does not round-trip the attachment bytes")
	}
}

// TestAttachmentPersistsReopen proves pending attachments survive a restart
// and that sent attachments stay attached to their user message across a
// second restart.
func TestAttachmentPersistsReopen(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	h := newHarness(t, provider)
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "persist")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	src := writeAttachmentFixture(t, t.TempDir(), "draft.md", "# persisted\n")
	if _, err := h.ctrl.AttachFiles(ctx, id, []string{src}); err != nil {
		t.Fatalf("AttachFiles: %v", err)
	}

	// Pending attachments survive a restart.
	h.restart()
	snap, err := h.ctrl.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession after restart: %v", err)
	}
	if len(snap.Attachments) != 1 || snap.Attachments[0].Name != "draft.md" {
		t.Fatalf("reopened pending attachments = %+v, want draft.md", snap.Attachments)
	}

	// Sending links the row to the user message; a second restart keeps the
	// chip on that message.
	if err := h.ctrl.Submit(ctx, id, "use the draft"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	h.restart()
	snap, err = h.ctrl.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession second restart: %v", err)
	}
	if len(snap.Attachments) != 0 {
		t.Fatalf("reopened sent attachments still pending: %+v", snap.Attachments)
	}
	found := false
	for _, m := range snap.Messages {
		if m.Role != state.RoleUser || m.Text != "use the draft" {
			continue
		}
		found = true
		if len(m.Attachments) != 1 || m.Attachments[0].Name != "draft.md" {
			t.Fatalf("reopened user message attachments = %+v, want draft.md", m.Attachments)
		}
	}
	if !found {
		t.Fatalf("reopened snapshot missing user message: %+v", snap.Messages)
	}
}

// TestAttachmentCleanupOnDelete proves deleting a session removes both the
// attachment rows and the per-session attachment directory.
func TestAttachmentCleanupOnDelete(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "cleanup")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	src := writeAttachmentFixture(t, t.TempDir(), "cleanup.txt", "remove me")
	if _, err := h.ctrl.AttachFiles(ctx, id, []string{src}); err != nil {
		t.Fatalf("AttachFiles: %v", err)
	}

	attachmentDir := filepath.Join(h.dir, "attachments", id)
	if entries, err := os.ReadDir(attachmentDir); err != nil || len(entries) != 1 {
		t.Fatalf("attachment dir entries = %v, %v; want 1", entries, err)
	}
	if rows, err := h.store.ListAttachments(id); err != nil || len(rows) != 1 {
		t.Fatalf("stored attachments = %d, %v; want 1", len(rows), err)
	}

	if err := h.ctrl.DeleteSession(ctx, id); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := os.Stat(attachmentDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("attachment dir still exists after delete: %v", err)
	}
	if rows, err := h.store.ListAttachments(id); err != nil || len(rows) != 0 {
		t.Fatalf("stored attachments after delete = %d, %v; want 0", len(rows), err)
	}
}
