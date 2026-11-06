//go:build e2e

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/prompt"
	"github.com/zeokeel/zeokeel/old/internal/provider/openai"
	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// TestRealModelToolCall proves the production registry is not just registered
// but actually callable by a real model: one live turn must invoke a tool and
// complete it.
func TestRealModelToolCall(t *testing.T) {
	apiKey := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	if apiKey == "" {
		t.Skip("DEEPSEEK_API_KEY not set")
	}
	base := strings.TrimSpace(os.Getenv("ZEOKEEL_PROVIDER_BASE_URL"))
	if base == "" {
		base = openai.DefaultBaseURL
	}
	model := strings.TrimSpace(os.Getenv("ZEOKEEL_PROVIDER_MODEL"))
	if model == "" {
		model = openai.DefaultModel
	}
	prov, err := openai.New(openai.Config{BaseURL: base, APIKey: apiKey, Model: model})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, closeFn, err := registerAllTools(st, dir, func(key, env, def string) string { return def })
	if err != nil {
		t.Fatal(err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	names := make([]string, 0, len(reg.List()))
	for _, tl := range reg.List() {
		names = append(names, tl.Name())
	}
	instruction := prompt.Compose(prompt.Context{
		WorkDir:        dir,
		SandboxPolicy:  "workspace-write",
		ApprovalPolicy: "ask",
		Language:       "en",
		Time:           time.Now(),
		Tools:          names,
	})
	agent, err := runtime.New(runtime.Config{
		Name:        "zeokeel-e2e",
		Instruction: instruction,
		ModelName:   model,
		Provider:    prov,
		Tools:       reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	const sessionID = "e2e-tools"
	ch, err := agent.Run(ctx, sessionID, "Use the todo tool with {\"action\":\"list\"} now. After it returns, reply done.")
	if err != nil {
		t.Fatal(err)
	}
	events := make([]state.Event, 0, 64)
	for ev := range ch {
		events = append(events, ev)
	}
	var started, ended bool
	for _, ev := range events {
		switch e := ev.(type) {
		case state.ToolStart:
			if e.Name == "todo" {
				started = true
			}
		case state.ToolEnd:
			ended = true
		}
	}
	if !started {
		t.Fatalf("model did not call todo; events=%v", kinds(events))
	}
	if !ended {
		t.Fatalf("tool started but never ended; events=%v", kinds(events))
	}
}

func kinds(events []state.Event) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, fmt.Sprintf("%T", ev))
	}
	return out
}
