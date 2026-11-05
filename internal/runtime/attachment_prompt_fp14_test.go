package runtime

import (
	"strings"
	"testing"
)

// TestAttachmentPromptRendersReadOnlyPath pins FP-14: a binary attachment that
// carries no inline text is rendered as a read-only path reference with
// metadata, never silently dropped and never inlined.
func TestAttachmentPromptRendersReadOnlyPath(t *testing.T) {
	prompt := AttachmentPrompt("summarize this", []UserAttachment{{
		Name: "report.pdf",
		Path: "/data/attachments/sess-1/att-1-report.pdf",
		MIME: "application/pdf",
		Size: 2048,
	}})
	if !strings.Contains(prompt, "<attachment name=\"report.pdf\"") {
		t.Fatalf("prompt missing attachment header: %q", prompt)
	}
	if !strings.Contains(prompt, "path=\"/data/attachments/sess-1/att-1-report.pdf\"") {
		t.Fatalf("prompt missing read-only path: %q", prompt)
	}
	if !strings.Contains(prompt, "mime=\"application/pdf\"") || !strings.Contains(prompt, "size=\"2048\"") {
		t.Fatalf("prompt missing attachment metadata: %q", prompt)
	}
	if !strings.Contains(prompt, "read_file") || !strings.Contains(prompt, "read-only copy") {
		t.Fatalf("prompt missing read-on-demand guidance: %q", prompt)
	}
	if strings.Contains(prompt, "</attachment>") {
		t.Fatalf("path attachment was rendered as an inline body: %q", prompt)
	}
}

// TestAttachmentPromptEscapesPathAttributes pins that the attribute values are
// escaped just like the name (paths are user-provided text).
func TestAttachmentPromptEscapesPathAttributes(t *testing.T) {
	prompt := AttachmentPrompt("go", []UserAttachment{{
		Name: `a"b.pdf`,
		Path: `/tmp/a"b&c.pdf`,
	}})
	if strings.Contains(prompt, `name="a"b.pdf"`) || strings.Contains(prompt, `path="/tmp/a"b&c.pdf"`) {
		t.Fatalf("attribute values were not escaped: %q", prompt)
	}
	if !strings.Contains(prompt, "&quot;") || !strings.Contains(prompt, "&amp;") {
		t.Fatalf("expected HTML-ish escaping in the prompt: %q", prompt)
	}
}

// TestAttachmentPromptTextFastPathUnchanged pins that small text attachments
// still ride inline (the fast path stays).
func TestAttachmentPromptTextFastPathUnchanged(t *testing.T) {
	prompt := AttachmentPrompt("read", []UserAttachment{{
		Name: "notes.txt",
		Text: "hello text",
	}})
	if !strings.Contains(prompt, "<attachment name=\"notes.txt\">\nhello text") {
		t.Fatalf("text attachment no longer inlined: %q", prompt)
	}
}
