package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

// TestForkAttachmentHistoryReplaysInForkTurn proves the production History
// adapter sees a fork's copied attachment rows: a turn continued in the fork
// sends the source's text block and image data URL again, even though the fork
// turn itself has no pending attachments.
func TestForkAttachmentHistoryReplaysInForkTurn(t *testing.T) {
	provider := &wiringProvider{}
	ctrl, _ := newWiredController(t, provider, nil)
	ctx := context.Background()

	srcID, err := ctrl.CreateSession(ctx, "fork attachment replay")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	srcDir := t.TempDir()
	const textBody = "FORK-REPLAY-TEXT-MARKER-1a2b"
	textPath := filepath.Join(srcDir, "note.txt")
	if err := os.WriteFile(textPath, []byte(textBody), 0o600); err != nil {
		t.Fatalf("write text attachment: %v", err)
	}
	pngPath := filepath.Join(srcDir, "pixel.png")
	if err := os.WriteFile(pngPath, []byte("\x89PNG\r\n\x1a\nfork-replay-image"), 0o600); err != nil {
		t.Fatalf("write image attachment: %v", err)
	}
	if _, err := ctrl.AttachFiles(ctx, srcID, []string{textPath, pngPath}); err != nil {
		t.Fatalf("AttachFiles: %v", err)
	}
	if err := ctrl.Submit(ctx, srcID, "first attached turn"); err != nil {
		t.Fatalf("Submit(source): %v", err)
	}
	waitWiringIdle(t, ctrl)
	ctrl.Wait()

	first, ok := provider.requestAt(0)
	if !ok {
		t.Fatal("provider saw no source request")
	}
	if !forkAttachmentRequestHasText(first, textBody) || !forkAttachmentRequestHasImage(first) {
		t.Fatalf("source request did not carry the attachments: %+v", first.Messages)
	}

	forkID, err := ctrl.ForkSession(ctx, srcID)
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if err := ctrl.Submit(ctx, forkID, "second in the fork"); err != nil {
		t.Fatalf("Submit(fork): %v", err)
	}
	waitWiringIdle(t, ctrl)
	ctrl.Wait()

	second, ok := provider.requestAt(1)
	if !ok {
		t.Fatal("provider saw no fork request")
	}
	if !forkAttachmentRequestHasText(second, textBody) {
		t.Fatalf("fork continuation lost the copied text attachment: %+v", second.Messages)
	}
	if !forkAttachmentRequestHasImage(second) {
		t.Fatalf("fork continuation lost the copied image attachment: %+v", second.Messages)
	}
}

// forkAttachmentRequestHasText reports whether any message content in req contains substr.
func forkAttachmentRequestHasText(req runtime.Request, substr string) bool {
	for _, m := range req.Messages {
		if strings.Contains(m.Content, substr) {
			return true
		}
	}
	return false
}

// forkAttachmentRequestHasImage reports whether any message in req carries a data URL image.
func forkAttachmentRequestHasImage(req runtime.Request) bool {
	for _, m := range req.Messages {
		for _, img := range m.Images {
			if strings.HasPrefix(img.URL, "data:image/png;base64,") {
				return true
			}
		}
	}
	return false
}
