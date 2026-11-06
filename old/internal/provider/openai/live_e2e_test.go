//go:build e2e

package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/tools"
)

// liveConfig reads the test endpoint from the environment only: the URL,
// model and key must never be committed to the repository.
func liveConfig(t *testing.T) Config {
	t.Helper()
	base := strings.TrimSpace(os.Getenv("ZEOKEEL_LIVE_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("ZEOKEEL_LIVE_MODEL"))
	key := strings.TrimSpace(os.Getenv("ZEOKEEL_LIVE_API_KEY"))
	if base == "" || model == "" || key == "" {
		t.Skip("ZEOKEEL_LIVE_BASE_URL/MODEL/API_KEY not set")
	}
	return Config{BaseURL: base, APIKey: key, Model: model}
}

func liveProvider(t *testing.T) *Provider {
	t.Helper()
	p, err := New(liveConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func collectLive(t *testing.T, ch <-chan runtime.Chunk) (string, *runtime.Usage) {
	t.Helper()
	var b strings.Builder
	var usage *runtime.Usage
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("stream error: %v", chunk.Err)
		}
		b.WriteString(chunk.Text)
		if chunk.Usage != nil {
			u := *chunk.Usage
			usage = &u
		}
	}
	return b.String(), usage
}

func TestLiveProviderText(t *testing.T) {
	p := liveProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ch, err := p.Stream(ctx, runtime.Request{Messages: []runtime.Message{{Role: runtime.RoleUser, Content: "Reply with the single word: pong"}}})
	if err != nil {
		t.Fatal(err)
	}
	text, usage := collectLive(t, ch)
	if strings.TrimSpace(text) == "" {
		t.Fatal("empty reply")
	}
	t.Logf("live text reply=%q usage=%+v", text, usage)
}

func solidPNG(t *testing.T, c color.Color) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestLiveProviderImage(t *testing.T) {
	p := liveProvider(t)
	url := solidPNG(t, color.RGBA{R: 255, A: 255})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ch, err := p.Stream(ctx, runtime.Request{Messages: []runtime.Message{{
		Role:    runtime.RoleUser,
		Content: "What color is this image? Answer with one word.",
		Images:  []runtime.ImagePart{{URL: url, Detail: "auto"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	text, usage := collectLive(t, ch)
	low := strings.ToLower(text)
	t.Logf("live image reply=%q usage=%+v", strings.TrimSpace(text), usage)
	if strings.TrimSpace(text) == "" {
		t.Fatal("empty image reply")
	}
	if !strings.Contains(low, "red") && !strings.Contains(low, "红") && !strings.Contains(low, "255") {
		t.Fatalf("reply does not look like it saw a red image: %q", text)
	}
}

func TestLiveToolImage(t *testing.T) {
	p := liveProvider(t)
	dir := t.TempDir()
	imgPath := dir + "/red.png"
	{
		img := image.NewRGBA(image.Rect(0, 0, 32, 32))
		for y := 0; y < 32; y++ {
			for x := 0; x < 32; x++ {
				img.Set(x, y, color.RGBA{R: 255, A: 255})
			}
		}
		f, err := os.Create(imgPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	reg := runtime.NewRegistry()
	_ = reg.Register(tools.NewReadImageTool(tools.ImageConfig{WorkspaceRoot: dir}))
	agent, err := runtime.New(runtime.Config{
		Name:        "live-image-e2e",
		Instruction: "Use the read_image tool when asked about an image. Answer in one word.",
		Provider:    p,
		Tools:       reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "live-image-e2e", `Call read_image with {"file_path":"red.png"} and tell me the image color in one word.`)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var used bool
	for ev := range ch {
		switch e := ev.(type) {
		case state.Token:
			text.WriteString(e.Text)
		case state.ToolStart:
			if e.Name == "read_image" {
				used = true
			}
		}
	}
	low := strings.ToLower(text.String())
	t.Logf("tool-image reply=%q", strings.TrimSpace(text.String()))
	if !used {
		t.Fatal("read_image was not called")
	}
	if !strings.Contains(low, "red") && !strings.Contains(low, "红") && !strings.Contains(low, "255") {
		t.Fatalf("model did not see the attached image: %q", text.String())
	}
}

func TestLiveProviderToolCall(t *testing.T) {
	p := liveProvider(t)
	reg := runtime.NewRegistry()
	for _, tl := range tools.InteractiveTools() {
		_ = reg.Register(tl)
	}
	agent, err := runtime.New(runtime.Config{
		Name:        "live-e2e",
		Instruction: "You are a test agent. Follow the user instruction exactly.",
		Provider:    p,
		Tools:       reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ch, err := agent.Run(ctx, "live-e2e", `Call the todo tool with {"action":"list"}, then reply done.`)
	if err != nil {
		t.Fatal(err)
	}
	var started, ended bool
	for ev := range ch {
		switch e := ev.(type) {
		case state.ToolStart:
			if e.Name == "todo" {
				started = true
			}
		case state.ToolEnd:
			ended = true
		}
	}
	if !started || !ended {
		t.Fatalf("tool call not observed: started=%v ended=%v", started, ended)
	}
	_ = tools.DefaultMaxOutputBytes
}
