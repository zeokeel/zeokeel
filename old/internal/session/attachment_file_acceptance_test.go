package session_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

// TestBinaryAttachmentReferencedByPath pins FP-14 end to end: a binary
// attachment is persisted as a read-only copy and the model prompt references
// that path instead of silently dropping the file or inlining its bytes.
func TestBinaryAttachmentReferencedByPath(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	h := newHarness(t, provider)
	ctx := context.Background()
	sessionID, err := h.ctrl.CreateSession(ctx, "binary attachment")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "report.pdf")
	content := "%PDF-1.4\nbinary-ish payload \x00\x01\x02 that must not be inlined\n"
	if err := os.WriteFile(src, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if _, err := h.ctrl.AttachFiles(ctx, sessionID, []string{src}); err != nil {
		t.Fatalf("AttachFiles: %v", err)
	}
	if err := h.ctrl.Submit(ctx, sessionID, "summarize the attachment"); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	req, ok := provider.requestAt(0)
	if !ok {
		t.Fatal("provider received no request")
	}
	user, ok := lastUserRequestMessage(req)
	if !ok {
		t.Fatal("request has no user message")
	}
	if !strings.Contains(user.Content, `<attachment name="report.pdf"`) {
		t.Fatalf("prompt does not reference the attachment:\n%s", user.Content)
	}
	if !strings.Contains(user.Content, "path=\"") || !strings.Contains(user.Content, "report.pdf") {
		t.Fatalf("prompt does not carry the read-only attachment path:\n%s", user.Content)
	}
	if !strings.Contains(user.Content, "read_file") {
		t.Fatalf("prompt does not tell the model how to read the file:\n%s", user.Content)
	}
	if strings.Contains(user.Content, "binary-ish payload") {
		t.Fatalf("binary attachment bytes were inlined into the prompt:\n%s", user.Content)
	}

	// The referenced copy must exist under the data directory's attachments
	// folder, so the model can actually open it.
	rows, err := h.store.ListAttachments(sessionID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListAttachments = %d, %v; want 1", len(rows), err)
	}
	if !strings.Contains(filepath.ToSlash(rows[0].Path), "/attachments/") {
		t.Fatalf("stored attachment path = %q, want a copy under attachments/", rows[0].Path)
	}
	if _, err := os.Stat(rows[0].Path); err != nil {
		t.Fatalf("stored attachment copy is missing: %v", err)
	}
	if rows[0].Kind != "file" {
		t.Fatalf("attachment kind = %q, want file", rows[0].Kind)
	}
	// The stored path the prompt references is the persisted read-only copy.
	if !strings.Contains(user.Content, rows[0].Path) {
		t.Fatalf("prompt path does not match the stored copy %q:\n%s", rows[0].Path, user.Content)
	}
}

// TestTextAttachmentStillInlined pins the fast path at the controller level.
func TestTextAttachmentStillInlined(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	h := newHarness(t, provider)
	ctx := context.Background()
	sessionID, err := h.ctrl.CreateSession(ctx, "text attachment")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	src := writeAttachmentFixture(t, t.TempDir(), "notes.txt", "inline me please")
	if _, err := h.ctrl.AttachFiles(ctx, sessionID, []string{src}); err != nil {
		t.Fatalf("AttachFiles: %v", err)
	}
	if err := h.ctrl.Submit(ctx, sessionID, "read the note"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	req, ok := provider.requestAt(0)
	if !ok {
		t.Fatal("provider received no request")
	}
	user, ok := lastUserRequestMessage(req)
	if !ok {
		t.Fatal("request has no user message")
	}
	if !strings.Contains(user.Content, "inline me please") {
		t.Fatalf("small text attachment was not inlined:\n%s", user.Content)
	}
}

// compile-time guard: runtime.UserAttachment keeps the path field used by the
// controller conversion.
var _ = runtime.UserAttachment{Path: "", MIME: "", Size: 0}
