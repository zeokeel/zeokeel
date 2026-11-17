package agent

// 2026-11 UI review C: a restored attachment chip reads like the hot one (file
// name + size). buildUserInput persists an image's name/size in a companion
// "[附件] name (N bytes)" metadata text part (the same channel a file uses) and
// its bytes in the data URL; userMessageContent merges them back, decodes the
// data URL for size/MIME/thumbnail, and keeps the metadata out of the prompt body.
// Old records without the metadata part fall back to the generic image name and
// still decode their size — no error.

import (
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestUserMessageContentRestoresAttachmentNameAndSize(t *testing.T) {
	img := contracts.Attachment{ID: "a1", Name: "zeokeel.png", MIME: "image/png", Data: make([]byte, 70)}
	file := contracts.Attachment{ID: "a2", Name: "notes.txt", MIME: "text/plain", Data: make([]byte, 100)}
	messages := buildUserInput("你好", []contracts.Attachment{img, file})
	text, attachments := userMessageContent(messages[0])

	// The metadata parts never become prompt body.
	if text != "你好" {
		t.Fatalf("prompt body = %q, want %q (attachment metadata leaked into the body)", text, "你好")
	}
	if strings.Contains(text, "[附件]") {
		t.Fatalf("the attachment metadata rendered as body: %q", text)
	}
	if len(attachments) != 2 {
		t.Fatalf("attachments = %d, want 2 (%+v)", len(attachments), attachments)
	}
	gotImg := attachments[0]
	if gotImg.Name != "zeokeel.png" || gotImg.Size() != 70 || !gotImg.IsImage() || len(gotImg.Data) != 70 {
		t.Fatalf("restored image = %+v, want name zeokeel.png, size 70, decoded data", gotImg)
	}
	gotFile := attachments[1]
	if gotFile.Name != "notes.txt" || gotFile.Size() != 100 {
		t.Fatalf("restored file = %+v, want name notes.txt and size 100", gotFile)
	}
}

func TestUserMessageContentOldImageRecordFallsBack(t *testing.T) {
	// An old record carries only the image part, no companion metadata text part.
	dataURL := contracts.Attachment{MIME: "image/png", Data: make([]byte, 70)}.DataURL()
	msg := rig.Message{Role: rig.RoleUser, Content: []rig.ContentPart{rig.ImagePart(dataURL, "")}}
	text, attachments := userMessageContent(msg)
	if text != "" {
		t.Fatalf("old image record body = %q, want empty", text)
	}
	if len(attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(attachments))
	}
	a := attachments[0]
	if a.Name != "图片" {
		t.Fatalf("old image name = %q, want the generic 图片 fallback", a.Name)
	}
	if a.Size() != 70 || len(a.Data) != 70 || !a.IsImage() {
		t.Fatalf("old image did not decode its size/data: %+v", a)
	}
}

func TestParseAttachmentTextReturnsSize(t *testing.T) {
	name, size, ok := parseAttachmentText("[附件] zeokeel.png (70 bytes)")
	if !ok || name != "zeokeel.png" || size != 70 {
		t.Fatalf("parseAttachmentText = %q/%d/%v, want zeokeel.png/70/true", name, size, ok)
	}
	// A text-file part keeps its content below the header; only the header parses.
	name, size, ok = parseAttachmentText("[附件] notes.txt (100 bytes)\nfile body")
	if !ok || name != "notes.txt" || size != 100 {
		t.Fatalf("parseAttachmentText with content = %q/%d/%v, want notes.txt/100/true", name, size, ok)
	}
	if _, _, ok := parseAttachmentText("普通正文"); ok {
		t.Fatalf("plain prose was mistaken for attachment metadata")
	}
}
