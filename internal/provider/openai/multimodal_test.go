package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

// captureChatBody runs one streaming request against an httptest server and
// returns the raw request body plus its decoded form.
func captureChatBody(t *testing.T, req runtime.Request) ([]byte, map[string]any) {
	t.Helper()
	type captured struct {
		raw  []byte
		body map[string]any
	}
	got := make(chan captured, 1)
	p, _ := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode request body %q: %v", raw, err)
			return
		}
		got <- captured{raw: raw, body: body}
		writeSSE(t, w, "data: [DONE]\n\n")
	})

	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	collect(t, ch)

	select {
	case c := <-got:
		return c.raw, c.body
	case <-time.After(5 * time.Second):
		t.Fatal("server did not receive the request")
		return nil, nil
	}
}

func firstMessage(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) == 0 {
		t.Fatalf("messages = %v", body["messages"])
	}
	msg, ok := msgs[0].(map[string]any)
	if !ok {
		t.Fatalf("message 0 = %v", msgs[0])
	}
	return msg
}

func TestRequestContentPartsForImageMessage(t *testing.T) {
	imageURL := "data:image/png;base64,iVBORw0KGgo="
	raw, body := captureChatBody(t, runtime.Request{
		Messages: []runtime.Message{{
			Role:    runtime.RoleUser,
			Content: "what is this?",
			Images:  []runtime.ImagePart{{URL: imageURL}},
		}},
	})

	msg := firstMessage(t, body)
	parts, ok := msg["content"].([]any)
	if !ok {
		t.Fatalf("content = %#v, want a part array", msg["content"])
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2: %v", len(parts), parts)
	}
	text, ok := parts[0].(map[string]any)
	if !ok || text["type"] != "text" || text["text"] != "what is this?" {
		t.Fatalf("text part = %v", parts[0])
	}
	image, ok := parts[1].(map[string]any)
	if !ok || image["type"] != "image_url" {
		t.Fatalf("image part = %v", parts[1])
	}
	imageURLPart, ok := image["image_url"].(map[string]any)
	if !ok {
		t.Fatalf("image_url = %v", image["image_url"])
	}
	if imageURLPart["url"] != imageURL || imageURLPart["detail"] != "auto" {
		t.Fatalf("image_url payload = %v", imageURLPart)
	}

	// The wire JSON has exactly the text part followed by the image part.
	want := `"content":[{"type":"text","text":"what is this?"},{"type":"image_url","image_url":{"url":"` + imageURL + `","detail":"auto"}}]`
	if !strings.Contains(string(raw), want) {
		t.Fatalf("request JSON missing expected parts\nwant substring: %s\ngot: %s", want, raw)
	}
}

func TestRequestContentPartsMultipleImagesAndDetail(t *testing.T) {
	_, body := captureChatBody(t, runtime.Request{
		Messages: []runtime.Message{{
			Role:    runtime.RoleAssistant,
			Content: "generated",
			Images: []runtime.ImagePart{
				{URL: "data:image/png;base64,AAAA", Detail: "high"},
				{URL: "data:image/jpeg;base64,BBBB", Detail: ""},
				{URL: ""}, // unusable parts are skipped
			},
		}},
	})

	msg := firstMessage(t, body)
	parts, ok := msg["content"].([]any)
	if !ok {
		t.Fatalf("assistant content = %#v, want an array", msg["content"])
	}
	if len(parts) != 3 {
		t.Fatalf("parts = %d, want text + 2 images: %v", len(parts), parts)
	}
	first := parts[1].(map[string]any)["image_url"].(map[string]any)
	if first["detail"] != "high" {
		t.Fatalf("first detail = %v, want high", first["detail"])
	}
	second := parts[2].(map[string]any)["image_url"].(map[string]any)
	if second["detail"] != "auto" {
		t.Fatalf("second detail = %v, want auto", second["detail"])
	}
}

func TestRequestTextOnlyContentStaysString(t *testing.T) {
	raw, body := captureChatBody(t, runtime.Request{
		Messages: []runtime.Message{
			{Role: runtime.RoleSystem, Content: "be brief"},
			{Role: runtime.RoleUser, Content: "read a.txt"},
			{Role: runtime.RoleAssistant, Content: "sure"},
			{Role: runtime.RoleTool, Content: "contents", ToolCallID: "call_1", ToolName: "read_file"},
		},
	})
	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) != 4 {
		t.Fatalf("messages = %v, want 4", body["messages"])
	}
	for i, want := range []string{"be brief", "read a.txt", "sure", "contents"} {
		msg := msgs[i].(map[string]any)
		got, ok := msg["content"].(string)
		if !ok {
			t.Fatalf("message %d content = %#v, want a plain string", i, msg["content"])
		}
		if got != want {
			t.Fatalf("message %d content = %q, want %q", i, got, want)
		}
	}
	// Text-only requests are byte-identical in shape to the historical form:
	// no content arrays anywhere.
	if strings.Contains(string(raw), `"type":"text"`) || strings.Contains(string(raw), `"type":"image_url"`) {
		t.Fatalf("text-only request contains content parts: %s", raw)
	}
}

func TestRequestToolRoleKeepsStringContentEvenWithImages(t *testing.T) {
	_, body := captureChatBody(t, runtime.Request{
		Messages: []runtime.Message{
			{Role: runtime.RoleTool, Content: "tool output", ToolCallID: "call_1", ToolName: "read_image",
				Images: []runtime.ImagePart{{URL: "data:image/png;base64,AAAA"}}},
			{Role: runtime.RoleSystem, Content: "system", Images: []runtime.ImagePart{{URL: "data:image/png;base64,BBBB"}}},
		},
	})
	msgs := body["messages"].([]any)
	for i, want := range []string{"tool output", "system"} {
		msg := msgs[i].(map[string]any)
		got, ok := msg["content"].(string)
		if !ok || got != want {
			t.Fatalf("message %d content = %#v, want string %q", i, msg["content"], want)
		}
	}
}

func TestRequestImageMessageWithEmptyContentStillHasTextPart(t *testing.T) {
	_, body := captureChatBody(t, runtime.Request{
		Messages: []runtime.Message{{
			Role:   runtime.RoleUser,
			Images: []runtime.ImagePart{{URL: "data:image/png;base64,AAAA"}},
		}},
	})
	msg := firstMessage(t, body)
	parts, ok := msg["content"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("content = %#v, want text + image", msg["content"])
	}
	text := parts[0].(map[string]any)
	if text["type"] != "text" || text["text"] != "" {
		t.Fatalf("empty text part = %v", text)
	}
}
