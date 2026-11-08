package acp

// Tests for ACP image prompt blocks (decoding, validation, forwarding to the
// attachment-capable chat backend) and for the session/new model field.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// testImageData stands in for the decoded bytes of one prompt image; only the
// base64 decoding and forwarding are under test, not image format validation.
var testImageData = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0x01, 0x02, 0xff}

// attachmentSendCall records one SendWithAttachments invocation.
type attachmentSendCall struct {
	SessionID   string
	Prompt      string
	Attachments []contracts.Attachment
}

// fakeAttachmentChat embeds fakeChat and adds the optional
// contracts.AttachmentSender half.
type fakeAttachmentChat struct {
	*fakeChat

	attachmentMu    sync.Mutex
	attachmentCalls []attachmentSendCall
	attachmentHook  func(*fakeAttachmentChat, string, string, []contracts.Attachment)
}

func newFakeAttachmentChat() *fakeAttachmentChat {
	return &fakeAttachmentChat{fakeChat: newFakeChat()}
}

func (c *fakeAttachmentChat) SendWithAttachments(sessionID, prompt string, attachments []contracts.Attachment) error {
	c.attachmentMu.Lock()
	c.attachmentCalls = append(c.attachmentCalls, attachmentSendCall{
		SessionID:   sessionID,
		Prompt:      prompt,
		Attachments: cloneTestAttachments(attachments),
	})
	hook := c.attachmentHook
	c.attachmentMu.Unlock()
	if hook != nil {
		hook(c, sessionID, prompt, attachments)
		return nil
	}
	c.setAssistantText(sessionID, "attachments received", false)
	return nil
}

func (c *fakeAttachmentChat) attachmentSends() []attachmentSendCall {
	c.attachmentMu.Lock()
	defer c.attachmentMu.Unlock()
	return append([]attachmentSendCall(nil), c.attachmentCalls...)
}

// cloneTestAttachments deep-copies attachments for safe assertions.
func cloneTestAttachments(in []contracts.Attachment) []contracts.Attachment {
	out := make([]contracts.Attachment, len(in))
	for index, attachment := range in {
		attachment.Data = append([]byte(nil), attachment.Data...)
		out[index] = attachment
	}
	return out
}

// textBlockLine renders one ACP text block.
func textBlockLine(text string) string {
	encoded, _ := json.Marshal(map[string]string{"type": "text", "text": text})
	return string(encoded)
}

// imageBlockLine renders one ACP image block.
func imageBlockLine(data, mimeType string) string {
	encoded, _ := json.Marshal(map[string]string{"type": "image", "data": data, "mimeType": mimeType})
	return string(encoded)
}

// blocksPromptLine renders one session/prompt frame carrying blocks.
func blocksPromptLine(id any, sessionID string, blocks ...string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"method":"session/prompt","params":{"sessionId":%q,"prompt":[%s]}}`,
		jsonValue(id), sessionID, strings.Join(blocks, ","))
}

// promptResponse finds the response frame of a request id.
func promptResponse(t *testing.T, frames []wireFrame, id any) wireFrame {
	t.Helper()
	want := jsonValue(id)
	for _, frame := range frames {
		if string(frame.ID) == want && frame.Method == "" {
			return frame
		}
	}
	t.Fatalf("no response frame for id %s in %+v", want, frames)
	return wireFrame{}
}

func TestSessionNewReturnsConfiguredModel(t *testing.T) {
	chat := newFakeChat()
	chat.model = "fake-model-7"
	service := newService(Config{}, chat, &fakeLogger{})
	frames := serveLines(t, service, sessionNewLine("new"))
	if len(frames) != 1 {
		t.Fatalf("frames = %+v, want one session/new response", frames)
	}
	var result struct {
		SessionID string `json:"sessionId"`
		Model     string `json:"model"`
	}
	if err := json.Unmarshal(frames[0].Result, &result); err != nil {
		t.Fatalf("decode session/new result: %v", err)
	}
	if result.SessionID != "sess-1" || result.Model != "fake-model-7" {
		t.Fatalf("session/new result = %+v, want sess-1/fake-model-7", result)
	}
}

func TestPromptTextAndImageForwardedToAttachmentSender(t *testing.T) {
	chat := newFakeAttachmentChat()
	service := newService(Config{}, chat, &fakeLogger{})
	encoded := base64.StdEncoding.EncodeToString(testImageData)
	frames := serveLines(t, service,
		sessionNewLine("new"),
		blocksPromptLine("prompt", "sess-1", textBlockLine("describe this image"), imageBlockLine(encoded, "image/png")),
	)
	if len(chat.sendCalls) != 0 {
		t.Fatalf("plain Send calls = %v, want none for an image prompt", chat.sendCalls)
	}
	calls := chat.attachmentSends()
	if len(calls) != 1 {
		t.Fatalf("SendWithAttachments calls = %d, want 1", len(calls))
	}
	call := calls[0]
	if call.SessionID != "sess-1" || call.Prompt != "describe this image" {
		t.Fatalf("SendWithAttachments call = %+v", call)
	}
	if len(call.Attachments) != 1 {
		t.Fatalf("attachments = %+v, want one image", call.Attachments)
	}
	attachment := call.Attachments[0]
	if attachment.ID == "" || attachment.Name != "image-1.png" || attachment.MIME != "image/png" {
		t.Fatalf("attachment metadata = %+v", attachment)
	}
	if !bytes.Equal(attachment.Data, testImageData) {
		t.Fatalf("attachment data = %v, want decoded %v", attachment.Data, testImageData)
	}
	response := promptResponse(t, frames, "prompt")
	if response.Error != nil || !strings.Contains(string(response.Result), StopReasonEndTurn) {
		t.Fatalf("prompt response = %+v", response)
	}
	// The existing subscription streaming path still runs: the fake chat reports
	// its assistant text through a session/update chunk.
	chunks := 0
	for _, frame := range frames {
		if frame.Method == MethodSessionUpdate {
			chunks++
		}
	}
	if chunks != 1 {
		t.Fatalf("session/update chunks = %d, want 1", chunks)
	}
}

func TestPromptImageOnlyAccepted(t *testing.T) {
	chat := newFakeAttachmentChat()
	service := newService(Config{}, chat, &fakeLogger{})
	encoded := base64.RawStdEncoding.EncodeToString(testImageData)
	frames := serveLines(t, service,
		blocksPromptLine(1, "sess-1", imageBlockLine(encoded, "image/jpeg")),
	)
	calls := chat.attachmentSends()
	if len(calls) != 1 {
		t.Fatalf("SendWithAttachments calls = %d, want 1", len(calls))
	}
	if calls[0].Prompt != "" || len(calls[0].Attachments) != 1 {
		t.Fatalf("image-only call = %+v", calls[0])
	}
	attachment := calls[0].Attachments[0]
	if attachment.Name != "image-1.jpeg" || attachment.MIME != "image/jpeg" ||
		!bytes.Equal(attachment.Data, testImageData) {
		t.Fatalf("image-only attachment = %+v", attachment)
	}
	response := promptResponse(t, frames, 1)
	if response.Error != nil || !strings.Contains(string(response.Result), StopReasonEndTurn) {
		t.Fatalf("prompt response = %+v", response)
	}
}

func TestPromptImageValidationErrors(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString(testImageData)
	cases := []struct {
		name  string
		block string
		want  string
	}{
		{"malformed base64", imageBlockLine("!!!not-base64!!!", "image/png"), "base64"},
		{"missing data", `{"type":"image","mimeType":"image/png"}`, "data"},
		{"missing mimeType", imageBlockLine(valid, ""), "mimeType"},
		{"non-image mimeType", imageBlockLine(valid, "text/plain"), "image/*"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// A plain fakeChat does not implement AttachmentSender: validation
			// must win with -32602 before the unsupported-backend path.
			chat := newFakeChat()
			service := newService(Config{}, chat, &fakeLogger{})
			frames := serveLines(t, service, blocksPromptLine(1, "sess-1", testCase.block))
			if len(frames) != 1 {
				t.Fatalf("frames = %+v, want one error response", frames)
			}
			frame := frames[0]
			if frame.Error == nil || frame.Error.Code != CodeInvalidParams {
				t.Fatalf("frame = %+v, want invalid params", frame)
			}
			if !strings.Contains(frame.Error.Message, testCase.want) {
				t.Fatalf("error message = %q, want it to mention %q", frame.Error.Message, testCase.want)
			}
			if len(chat.sendCalls) != 0 {
				t.Fatalf("invalid image still sent: %+v", chat.sendCalls)
			}
		})
	}
}

func TestPromptImageWithoutAttachmentSender(t *testing.T) {
	// Plain fakeChat does not implement contracts.AttachmentSender.
	chat := newFakeChat()
	service := newService(Config{}, chat, &fakeLogger{})
	encoded := base64.StdEncoding.EncodeToString(testImageData)
	frames := serveLines(t, service, blocksPromptLine(1, "sess-1", imageBlockLine(encoded, "image/png")))
	if len(frames) != 1 {
		t.Fatalf("frames = %+v, want one error response", frames)
	}
	frame := frames[0]
	if frame.Error == nil || frame.Error.Code != CodeInternalError {
		t.Fatalf("frame = %+v, want internal error -32603", frame)
	}
	if !strings.Contains(frame.Error.Message, "image attachments") {
		t.Fatalf("error message = %q, want a readable attachment-support message", frame.Error.Message)
	}
	if len(chat.sendCalls) != 0 {
		t.Fatalf("Send calls = %v, want none", chat.sendCalls)
	}
}

func TestPromptImageSizeLimit(t *testing.T) {
	oversized := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x7f}, maxPromptImageBytes+1))
	attachments, rpcErr := promptImages(promptParams{Prompt: []contentBlock{
		{Type: "image", Data: oversized, MimeType: "image/png"},
	}})
	if rpcErr == nil || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("promptImages(oversized) = %+v/%+v, want -32602", attachments, rpcErr)
	}
	if !strings.Contains(rpcErr.Message, "10 MiB") || !strings.Contains(rpcErr.Message, "limit") {
		t.Fatalf("size-limit message = %q", rpcErr.Message)
	}
}
