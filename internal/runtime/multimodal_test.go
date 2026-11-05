package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

// testImageURL builds a valid, small image data URL. The injector validates the
// data URL shape and base64 payload, not that the bytes decode as an image (the
// provider is responsible for the latter).
func testImageURL(seed string) string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("png-"+seed))
}

func toolImageMessage(toolName, url string) *schema.Message {
	content, _ := json.Marshal(map[string]any{
		"kind":           "image",
		"image_data_url": url,
	})
	return schema.ToolMessage(string(content), "call-"+toolName, schema.WithToolName(toolName))
}

func injectedImages(messages []*schema.Message) []string {
	var out []string
	for _, msg := range messages {
		if msg == nil || msg.Role != schema.User {
			continue
		}
		for _, part := range imagePartsFromEino(msg) {
			out = append(out, part.URL)
		}
	}
	return out
}

func TestInjectToolImages(t *testing.T) {
	url := testImageURL("one")

	t.Run("valid", func(t *testing.T) {
		in := []*schema.Message{schema.UserMessage("read the picture"), toolImageMessage("read_image", url)}
		out := injectToolImages(in)
		if len(out) != 3 {
			t.Fatalf("messages = %d, want 3: %#v", len(out), out)
		}
		last := out[2]
		if last.Role != schema.User {
			t.Fatalf("injected role = %q, want user", last.Role)
		}
		if want := fmt.Sprintf(injectedImageText, "read_image"); last.Content != want {
			t.Fatalf("injected content = %q, want %q", last.Content, want)
		}
		images := imagePartsFromEino(last)
		if len(images) != 1 || images[0].URL != url || images[0].Detail != "auto" {
			t.Fatalf("injected images = %+v", images)
		}
		// The input slice must not be mutated.
		if len(in) != 2 {
			t.Fatalf("input slice grew to %d", len(in))
		}
	})

	t.Run("duplicate tool results", func(t *testing.T) {
		in := []*schema.Message{
			schema.UserMessage("read both"),
			toolImageMessage("read_image", url),
			toolImageMessage("read_image", url),
		}
		out := injectToolImages(in)
		if got := injectedImages(out); len(got) != 1 || got[0] != url {
			t.Fatalf("images = %v, want exactly one %q", got, url)
		}
		// A second pass is a no-op.
		again := injectToolImages(out)
		if len(again) != len(out) {
			t.Fatalf("second pass changed length from %d to %d", len(out), len(again))
		}
	})

	t.Run("duplicate already attached", func(t *testing.T) {
		attached := newInjectedImageMessage("read_image", ImagePart{URL: url, Detail: "auto"})
		in := []*schema.Message{
			schema.UserMessage("read"),
			toolImageMessage("read_image", url),
			attached,
		}
		out := injectToolImages(in)
		if len(out) != len(in) {
			t.Fatalf("re-attached an image: length %d -> %d", len(in), len(out))
		}
	})

	t.Run("max four", func(t *testing.T) {
		in := []*schema.Message{schema.UserMessage("many")}
		for i := 0; i < 6; i++ {
			in = append(in, toolImageMessage(fmt.Sprintf("tool%d", i), testImageURL(fmt.Sprintf("%d", i))))
		}
		out := injectToolImages(in)
		if got := injectedImages(out); len(got) != MaxImagesPerTurn {
			t.Fatalf("images = %d, want %d", len(got), MaxImagesPerTurn)
		}
	})

	t.Run("oversized", func(t *testing.T) {
		huge := "data:image/png;base64," + strings.Repeat("A", MaxImageDataURLBytes)
		in := []*schema.Message{schema.UserMessage("big"), toolImageMessage("read_image", huge)}
		out := injectToolImages(in)
		if got := injectedImages(out); len(got) != 0 {
			t.Fatalf("oversized image was attached: %v", got)
		}
	})

	t.Run("malformed and non-image", func(t *testing.T) {
		cases := map[string]string{
			"not json":         "image_data_url: data:image/png;base64,AAAA",
			"bad json":         `{"image_data_url":`,
			"missing field":    `{"kind":"image"}`,
			"empty":            `{"image_data_url":""}`,
			"plain text":       `{"image_data_url":"data:text/plain;base64,AAAA"}`,
			"http url":         `{"image_data_url":"https://example.com/a.png"}`,
			"no base64 marker": `{"image_data_url":"data:image/png,AAAA"}`,
			"bad base64":       `{"image_data_url":"data:image/png;base64,%%%%"}`,
		}
		for name, content := range cases {
			t.Run(name, func(t *testing.T) {
				in := []*schema.Message{
					schema.UserMessage("x"),
					schema.ToolMessage(content, "call-1", schema.WithToolName("read_image")),
				}
				out := injectToolImages(in)
				if len(out) != len(in) {
					t.Fatalf("malformed output injected an image: %#v", out)
				}
			})
		}
	})

	t.Run("non-tool message ignored", func(t *testing.T) {
		in := []*schema.Message{
			schema.UserMessage(`{"image_data_url":"` + url + `"}`),
			schema.AssistantMessage(`{"image_data_url":"`+url+`"}`, nil),
		}
		out := injectToolImages(in)
		if got := injectedImages(out); len(got) != 0 {
			t.Fatalf("non-tool message injected an image: %v", got)
		}
	})
}

// TestRunToolImageReachesNextModelRequest drives the real eino ReAct loop with
// the white-box fake model: the first call requests read_image, the tool returns
// an image JSON, and the second model call must carry a synthetic user message
// with the image as a multimodal part.
func TestRunToolImageReachesNextModelRequest(t *testing.T) {
	imageURL := testImageURL("loop")
	toolResult, err := json.Marshal(map[string]any{
		"kind":           "image",
		"path":           "pic.png",
		"format":         "png",
		"mime":           "image/png",
		"width":          3,
		"height":         2,
		"bytes":          12,
		"image_data_url": imageURL,
	})
	if err != nil {
		t.Fatalf("encode tool result: %v", err)
	}

	imageTool := &fakeClock{result: string(toolResult)}
	reg := NewRegistry()
	if err := reg.Register(imageTool); err != nil {
		t.Fatal(err)
	}

	m := &fakeChatModel{script: func(_ context.Context, call int, _ []*schema.Message) ([]*schema.Message, error) {
		switch call {
		case 0:
			return []*schema.Message{{
				Role: schema.Assistant,
				ToolCalls: []schema.ToolCall{{
					ID:       "call-image",
					Type:     "function",
					Function: schema.FunctionCall{Name: "clock", Arguments: `{}`},
				}},
			}}, nil
		default:
			return []*schema.Message{schema.AssistantMessage("I can see it.", nil)}, nil
		}
	}}
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-image", "what is in pic.png?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("turn failed: %s", errText)
	}
	if m.callCount() != 2 {
		t.Fatalf("model calls = %d, want 2", m.callCount())
	}

	// The first model call cannot see the image yet.
	for _, msg := range m.inputAt(0) {
		if msg != nil && imagePartsFromEino(msg) != nil {
			t.Fatalf("first model call already had an image: %#v", msg)
		}
	}

	second := m.inputAt(1)
	var injected *schema.Message
	for _, msg := range second {
		if msg == nil || msg.Role != schema.User {
			continue
		}
		if parts := imagePartsFromEino(msg); len(parts) > 0 {
			injected = msg
			break
		}
	}
	if injected == nil {
		t.Fatalf("second model call has no image user message: %#v", second)
	}
	if want := fmt.Sprintf(injectedImageText, "clock"); injected.Content != want {
		t.Fatalf("injected content = %q, want %q", injected.Content, want)
	}
	parts := imagePartsFromEino(injected)
	if len(parts) != 1 || parts[0].URL != imageURL || parts[0].Detail != "auto" {
		t.Fatalf("injected image parts = %+v", parts)
	}
	if got := strings.Join(tokenTexts(events), ""); got != "I can see it." {
		t.Fatalf("final text = %q", got)
	}
}

// TestRunToolImageReachesProviderRequest covers the production adapter path: the
// synthetic user message produced by the eino middleware must reach
// runtime.Provider as a Message with Images populated.
func TestRunToolImageReachesProviderRequest(t *testing.T) {
	imageURL := testImageURL("provider")
	toolResult, err := json.Marshal(map[string]any{
		"kind":           "image",
		"image_data_url": imageURL,
	})
	if err != nil {
		t.Fatalf("encode tool result: %v", err)
	}
	reg := NewRegistry()
	if err := reg.Register(&fakeClock{result: string(toolResult)}); err != nil {
		t.Fatal(err)
	}

	call := 0
	p := &fakeProvider{fn: func(_ context.Context, req Request) ([]Chunk, error) {
		call++
		if call == 1 {
			return []Chunk{{
				ToolCalls: []ToolCall{{
					ID:        "call-provider-image",
					Name:      "clock",
					Arguments: json.RawMessage(`{}`),
				}},
			}}, nil
		}
		return []Chunk{{Text: "ok", Done: true}}, nil
	}}
	agent := newTestAgent(t, Config{Provider: p, Tools: reg})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-provider-image", "look")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	collectEvents(t, ch)

	req, ok := p.requestAt(1)
	if !ok {
		t.Fatal("provider got no second request")
	}
	var found *Message
	for i := range req.Messages {
		if req.Messages[i].Role == RoleUser && len(req.Messages[i].Images) > 0 {
			found = &req.Messages[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("second provider request has no image user message: %+v", req.Messages)
	}
	if want := fmt.Sprintf(injectedImageText, "clock"); found.Content != want {
		t.Fatalf("injected content = %q, want %q", found.Content, want)
	}
	if len(found.Images) != 1 || found.Images[0].URL != imageURL || found.Images[0].Detail != "auto" {
		t.Fatalf("injected images = %+v", found.Images)
	}
}

// imageToolJSON is the JSON object read_image returns, carrying one data URL.
func imageToolJSON(url string) string {
	content, _ := json.Marshal(map[string]any{
		"kind":           "image",
		"image_data_url": url,
	})
	return string(content)
}

// ptcBatchMessage builds the tool message the ptc tool returns for one batch.
// Each output is the nested tool's raw result text, exactly as PTCResult.Output
// encodes it (a JSON string escaped inside the batch object).
func ptcBatchMessage(t *testing.T, outputs ...string) *schema.Message {
	t.Helper()
	response := PTCResponse{Results: make([]PTCResult, 0, len(outputs))}
	for i, output := range outputs {
		response.Results = append(response.Results, PTCResult{
			Index: i, Tool: "read_image", OK: true, Output: output,
		})
	}
	content, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("encode PTC response: %v", err)
	}
	return schema.ToolMessage(string(content), "call-ptc", schema.WithToolName(ptcToolName))
}

// TestInjectPTCBatchImages proves a PTC batch result is scanned like a direct
// tool result: images nested in results[].output are attached with the same
// per-turn cap and dedupe, instead of reaching the model as escaped text only.
func TestInjectPTCBatchImages(t *testing.T) {
	t.Run("single image", func(t *testing.T) {
		url := testImageURL("ptc-one")
		in := []*schema.Message{schema.UserMessage("look"), ptcBatchMessage(t, imageToolJSON(url))}
		out := injectToolImages(in)
		if got := injectedImages(out); len(got) != 1 || got[0] != url {
			t.Fatalf("images = %v, want exactly one %q", got, url)
		}
		var injected *schema.Message
		for _, msg := range out {
			if msg != nil && msg.Role == schema.User && len(imagePartsFromEino(msg)) > 0 {
				injected = msg
				break
			}
		}
		if injected == nil {
			t.Fatalf("no image user message injected: %#v", out)
		}
		if want := fmt.Sprintf(injectedImageText, ptcToolName); injected.Content != want {
			t.Fatalf("injected content = %q, want %q", injected.Content, want)
		}
		if len(out) != len(in)+1 || len(in) != 2 {
			t.Fatalf("messages = %d, want %d", len(out), len(in)+1)
		}
		// A second pass is a no-op.
		if again := injectToolImages(out); len(again) != len(out) {
			t.Fatalf("second pass changed length from %d to %d", len(out), len(again))
		}
	})

	t.Run("several images and dedupe", func(t *testing.T) {
		first := testImageURL("ptc-a")
		second := testImageURL("ptc-b")
		in := []*schema.Message{
			schema.UserMessage("scan"),
			ptcBatchMessage(t, imageToolJSON(first), "plain text", imageToolJSON(second), imageToolJSON(first)),
		}
		out := injectToolImages(in)
		got := injectedImages(out)
		if len(got) != 2 || got[0] != first || got[1] != second {
			t.Fatalf("images = %v, want [%q %q]", got, first, second)
		}
	})

	t.Run("dedupes a direct tool image", func(t *testing.T) {
		url := testImageURL("ptc-dup")
		in := []*schema.Message{
			schema.UserMessage("read"),
			toolImageMessage("read_image", url),
			ptcBatchMessage(t, imageToolJSON(url)),
		}
		out := injectToolImages(in)
		if got := injectedImages(out); len(got) != 1 || got[0] != url {
			t.Fatalf("images = %v, want exactly one %q", got, url)
		}
	})

	t.Run("per-turn cap", func(t *testing.T) {
		outputs := make([]string, 0, 6)
		for i := 0; i < 6; i++ {
			outputs = append(outputs, imageToolJSON(testImageURL(fmt.Sprintf("ptc-cap-%d", i))))
		}
		in := []*schema.Message{schema.UserMessage("many"), ptcBatchMessage(t, outputs...)}
		out := injectToolImages(in)
		if got := injectedImages(out); len(got) != MaxImagesPerTurn {
			t.Fatalf("images = %d, want %d: %v", len(got), MaxImagesPerTurn, got)
		}
	})

	t.Run("malformed batch ignored", func(t *testing.T) {
		cases := map[string]string{
			"not json":         `{"results":[{"output":"image_data_url: data:image/png;base64,AAAA"}]}`,
			"bad output json":  `{"results":[{"output":"{\"image_data_url\":"}]}`,
			"non-image output": `{"results":[{"output":"{\"image_data_url\":\"https://example.com/a.png\"}"}]}`,
		}
		for name, content := range cases {
			t.Run(name, func(t *testing.T) {
				in := []*schema.Message{
					schema.UserMessage("x"),
					schema.ToolMessage(content, "call-ptc", schema.WithToolName(ptcToolName)),
				}
				out := injectToolImages(in)
				if len(out) != len(in) {
					t.Fatalf("malformed PTC output injected an image: %#v", out)
				}
			})
		}
	})
}

// TestPTCBatchImageReachesNextModelRequest drives the real ptc tool over a
// registry image tool: the nested image must reach the next model call as a
// multimodal part, matching TestRunToolImageReachesNextModelRequest's direct
// call contract.
func TestPTCBatchImageReachesNextModelRequest(t *testing.T) {
	imageURL := testImageURL("ptc-loop")
	reg := NewRegistry()
	if err := reg.Register(&stubTool{name: "pic", invoke: func(context.Context, json.RawMessage) (string, error) {
		return imageToolJSON(imageURL), nil
	}}); err != nil {
		t.Fatal(err)
	}

	m := &fakeChatModel{script: func(_ context.Context, call int, _ []*schema.Message) ([]*schema.Message, error) {
		if call == 0 {
			return []*schema.Message{{
				Role: schema.Assistant,
				ToolCalls: []schema.ToolCall{{
					ID:       "call-ptc-image",
					Type:     "function",
					Function: schema.FunctionCall{Name: ptcToolName, Arguments: `{"calls":[{"tool":"pic"}]}`},
				}},
			}}, nil
		}
		return []*schema.Message{schema.AssistantMessage("I can see it.", nil)}, nil
	}}
	agent := newTestAgent(t, Config{einoModel: m, Tools: reg, Mode: ModePTC})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "session-ptc-image", "look at the batch image")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := collectEvents(t, ch)
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("turn failed: %s", errText)
	}
	if m.callCount() != 2 {
		t.Fatalf("model calls = %d, want 2", m.callCount())
	}

	var injected *schema.Message
	for _, msg := range m.inputAt(1) {
		if msg != nil && msg.Role == schema.User && len(imagePartsFromEino(msg)) > 0 {
			injected = msg
			break
		}
	}
	if injected == nil {
		t.Fatalf("second model call has no image user message: %#v", m.inputAt(1))
	}
	if want := fmt.Sprintf(injectedImageText, ptcToolName); injected.Content != want {
		t.Fatalf("injected content = %q, want %q", injected.Content, want)
	}
	parts := imagePartsFromEino(injected)
	if len(parts) != 1 || parts[0].URL != imageURL || parts[0].Detail != "auto" {
		t.Fatalf("injected image parts = %+v", parts)
	}
}
