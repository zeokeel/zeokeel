package app

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/store"
)

// TestAttachmentHistoryReplayed proves the app's History adapter rebuilds the
// model-visible attachment content from the persisted rows: a reopened session
// (or a later turn) still sends the text block and the image data URL.
func TestAttachmentHistoryReplayed(t *testing.T) {
	dir := t.TempDir()
	ctrl, cleanup, err := BuildServices(dir, nil)
	if err != nil {
		t.Fatalf("BuildServices: %v", err)
	}
	t.Cleanup(func() { _ = cleanup() })
	ctx := context.Background()
	sessionID, err := ctrl.CreateSession(ctx, "app attachments")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	srcDir := t.TempDir()
	textPath := filepath.Join(srcDir, "history.txt")
	const textBody = "replayed attachment body"
	if err := os.WriteFile(textPath, []byte(textBody), 0o600); err != nil {
		t.Fatalf("write text attachment: %v", err)
	}
	pngPath := filepath.Join(srcDir, "history.png")
	if err := os.WriteFile(pngPath, []byte("\x89PNG\r\n\x1a\nnot-a-real-image"), 0o600); err != nil {
		t.Fatalf("write image attachment: %v", err)
	}
	if _, err := ctrl.AttachFiles(ctx, sessionID, []string{textPath, pngPath}); err != nil {
		t.Fatalf("AttachFiles: %v", err)
	}
	rows, err := ctrl.Store().ListAttachments(sessionID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("ListAttachments = %d, %v; want 2", len(rows), err)
	}
	// The copies live under the app data directory, not next to the source.
	attachmentDir := filepath.Join(dir, "attachments", sessionID)
	for _, row := range rows {
		if filepath.Dir(row.Path) != attachmentDir {
			t.Fatalf("attachment path = %q, want under %q", row.Path, attachmentDir)
		}
	}

	msg, err := ctrl.Store().AppendMessage(store.Message{
		SessionID: sessionID,
		Role:      string(runtime.RoleUser),
		Content:   "use these files",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	for _, row := range rows {
		if err := ctrl.Store().SetAttachmentMessage(row.ID, msg.ID); err != nil {
			t.Fatalf("SetAttachmentMessage(%s): %v", row.ID, err)
		}
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
		t.Fatalf("history has no user message: %+v", messages)
	}
	if !strings.Contains(user.Content, "use these files") || !strings.Contains(user.Content, textBody) {
		t.Fatalf("history user content missing text attachment: %q", user.Content)
	}
	if len(user.Images) != 1 {
		t.Fatalf("history images = %+v, want one image part", user.Images)
	}
	if !strings.HasPrefix(user.Images[0].URL, "data:image/png;base64,") {
		t.Fatalf("history image URL = %q, want image/png data URL", user.Images[0].URL)
	}
	if _, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(user.Images[0].URL, "data:image/png;base64,")); err != nil {
		t.Fatalf("history image data URL is not base64: %v", err)
	}
}
