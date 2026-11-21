package hostbundle_test

// The host bundle must compile through kit.Build and hand the product services
// to the agent: prompt sections reach the request, business tools are
// registered, and history is persisted through the product's History.

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/kit"
	"github.com/zeokeel/zeokeel-rig/session"

	"github.com/zeokeel/zeokeel/internal/hostbundle"
)

type fakeProvider struct{ requests []rig.Request }

func (p *fakeProvider) Name() string { return "hostbundle-fake" }

func (p *fakeProvider) Stream(_ context.Context, req rig.Request) (rig.Stream, error) {
	p.requests = append(p.requests, req)
	return &fakeStream{}, nil
}

type fakeStream struct{ done bool }

func (s *fakeStream) Recv() (rig.Chunk, error) {
	if s.done {
		return rig.Chunk{}, io.EOF
	}
	s.done = true
	return rig.Chunk{Delta: "ok", FinishReason: "stop"}, nil
}

type fakeTool struct{ name string }

func (t *fakeTool) Name() string           { return t.name }
func (t *fakeTool) Description() string    { return "business tool" }
func (t *fakeTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *fakeTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{Content: "ok"}, nil
}

func TestHostBundleCompilesIntoKit(t *testing.T) {
	provider := &fakeProvider{}
	if err := kit.RegisterProvider("hostbundle-test", func(json.RawMessage) (rig.Provider, error) {
		return provider, nil
	}); err != nil {
		t.Fatal(err)
	}
	history := session.NewMemory()
	if err := kit.RegisterModule("zeokeel.host.test", func(json.RawMessage) (kit.Module, error) {
		return hostbundle.New(hostbundle.Options{
			History:        history,
			PromptSections: []kit.PromptSection{{Name: "product", Body: "product rules"}},
			Tools:          []rig.Tool{&fakeTool{name: "dingtalk_send"}},
		}), nil
	}); err != nil {
		t.Fatal(err)
	}
	agent, err := kit.Build(context.Background(), kit.Spec{
		Model:    "m",
		Provider: kit.ProviderSpec{Name: "hostbundle-test"},
		Modules:  []kit.ModuleRef{{Name: "zeokeel.host.test"}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := agent.Tool("dingtalk_send"); !ok {
		t.Fatal("business tool was not registered")
	}
	if _, err := agent.Run(context.Background(), kit.RunRequest{
		SessionID: "s1",
		Input:     []rig.Message{rig.NewMessage(rig.RoleUser, "hi")},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(provider.requests) == 0 || !strings.Contains(provider.requests[0].Messages[0].Text(), "product rules") {
		t.Fatalf("request = %+v", provider.requests)
	}
	loaded, err := history.Load(context.Background(), "s1")
	if err != nil || len(loaded) != 2 {
		t.Fatalf("history = %d/%v, want the user message and the answer", len(loaded), err)
	}
	if err := agent.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
