package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// TestAttachmentHistoryReplaysFileReference is the FP-14 history regression: a
// binary/large attachment persisted for an earlier turn must be re-offered to
// the model as the stored read-only path reference on the next turn, not
// silently dropped (the live turn already sends it).
func TestAttachmentHistoryReplaysFileReference(t *testing.T) {
	dir := t.TempDir()
	ctrl, cleanup, err := BuildServices(dir, nil)
	if err != nil {
		t.Fatalf("BuildServices: %v", err)
	}
	t.Cleanup(func() { _ = cleanup() })
	ctx := context.Background()
	sessionID, err := ctrl.CreateSession(ctx, "file attachments")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	stored := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(stored, []byte("%PDF-fake"), 0o600); err != nil {
		t.Fatalf("write stored attachment: %v", err)
	}
	att, err := ctrl.Store().CreateAttachment(store.Attachment{
		SessionID: sessionID,
		Name:      "report.pdf",
		Path:      stored,
		MIME:      "Application/PDF; version=1",
		Kind:      store.AttachmentKindFile,
		Size:      9,
	})
	if err != nil {
		t.Fatalf("CreateAttachment: %v", err)
	}
	msg, err := ctrl.Store().AppendMessage(store.Message{
		SessionID: sessionID,
		Role:      string(runtime.RoleUser),
		Content:   "read the attached report",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := ctrl.Store().SetAttachmentMessage(att.ID, msg.ID); err != nil {
		t.Fatalf("SetAttachmentMessage: %v", err)
	}

	history := &storeHistory{st: ctrl.Store()}
	messages, err := history.Messages(ctx, sessionID)
	if err != nil {
		t.Fatalf("history.Messages: %v", err)
	}
	var user *runtime.Message
	for i := range messages {
		if messages[i].Role == runtime.RoleUser {
			user = &messages[i]
		}
	}
	if user == nil {
		t.Fatal("history has no user message")
	}
	for _, want := range []string{
		`name="report.pdf"`,
		`path="` + stored + `"`,
		`mime="application/pdf"`,
		`size="9"`,
		"read the attached report",
	} {
		if !strings.Contains(user.Content, want) {
			t.Errorf("replayed attachment content missing %q:\n%s", want, user.Content)
		}
	}
}
