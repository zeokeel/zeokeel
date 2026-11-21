package hostbundle_test

// P3b dual-assembly baseline: the same product-shaped rig.AgentSpec must build
// through agent.New and through kit.New(FromAgentSpec) into agents with the same
// tool surface and the same event sequence for a turn. This is the gate before
// the runtime switches from the direct spec to kit.Build.

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"testing"

	"github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/kit"
	"github.com/zeokeel/zeokeel-rig/session"

	"github.com/zeokeel/zeokeel/internal/hostbundle"
	"github.com/zeokeel/zeokeel/internal/lib/agentbuild"
)

type parityProvider struct{ requests []rig.Request }

func (p *parityProvider) Name() string { return "parity" }

func (p *parityProvider) Stream(_ context.Context, req rig.Request) (rig.Stream, error) {
	p.requests = append(p.requests, req)
	return &parityStream{}, nil
}

type parityStream struct{ done bool }

func (s *parityStream) Recv() (rig.Chunk, error) {
	if s.done {
		return rig.Chunk{}, io.EOF
	}
	s.done = true
	return rig.Chunk{Delta: "answer", FinishReason: "stop"}, nil
}

type parityTool struct{ name string }

func (t *parityTool) Name() string           { return t.name }
func (t *parityTool) Description() string    { return "parity tool" }
func (t *parityTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *parityTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{Content: "ok"}, nil
}

func productShapedSpec(provider rig.Provider, history rig.History) rig.AgentSpec {
	return rig.AgentSpec{
		Provider:           provider,
		Model:              "m",
		SystemPrompt:       "product prompt",
		Tools:              []rig.Tool{&parityTool{name: "product_tool"}},
		InitialActiveTools: []string{"product_tool"},
		History:            history,
		Spill:              nil,
		Hooks:              []rig.TurnHook{},
	}
}

func eventTypes(events []rig.Event) []rig.EventType {
	types := make([]rig.EventType, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func TestKitParityWithDirectAgentSpec(t *testing.T) {
	history := session.NewMemory()
	directProvider := &parityProvider{}
	direct, err := agentbuild.BuildAgent(productShapedSpec(directProvider, history), agentbuild.ModeDirect)
	if err != nil {
		t.Fatalf("BuildAgent(direct): %v", err)
	}
	kitProvider := &parityProvider{}
	viaKit, err := agentbuild.BuildAgent(productShapedSpec(kitProvider, session.NewMemory()), agentbuild.ModeKit)
	if err != nil {
		t.Fatalf("BuildAgent(kit): %v", err)
	}
	kitAgent, ok := viaKit.(*kit.Agent)
	if !ok {
		t.Fatalf("kit path returned %T", viaKit)
	}
	if names := kitAgent.Tools(); !slices.Equal(names, []string{"product_tool"}) {
		t.Fatalf("kit Tools = %v", names)
	}

	run := func(agent rig.Agent, sessionID string) (rig.RunResult, []rig.Event) {
		t.Helper()
		var events []rig.Event
		result, err := agent.Run(context.Background(), rig.RunRequest{
			SessionID: sessionID,
			Input:     []rig.Message{rig.NewMessage(rig.RoleUser, "hi")},
			Emit:      func(event rig.Event) { events = append(events, event) },
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		return result, events
	}
	directResult, directEvents := run(direct, "s1")
	kitResult, kitEvents := run(viaKit, "s1")
	if len(directResult.Messages) != len(kitResult.Messages) || directResult.Messages[0].Text() != kitResult.Messages[0].Text() {
		t.Fatalf("results differ: %+v vs %+v", directResult.Messages, kitResult.Messages)
	}
	if !slices.Equal(eventTypes(directEvents), eventTypes(kitEvents)) {
		t.Fatalf("event types differ:\n direct=%v\n kit=%v", eventTypes(directEvents), eventTypes(kitEvents))
	}
	if len(directProvider.requests) != 1 || len(kitProvider.requests) != 1 {
		t.Fatalf("provider calls = %d/%d", len(directProvider.requests), len(kitProvider.requests))
	}
	if directProvider.requests[0].Model != kitProvider.requests[0].Model {
		t.Fatalf("model differs: %q vs %q", directProvider.requests[0].Model, kitProvider.requests[0].Model)
	}
}

// TestHostBundleModuleIsUsableFromKitBuild is the composition-level parity: the
// same product Host bundle that this package exposes builds through kit.Build.
func TestHostBundleModuleIsUsableFromKitBuild(t *testing.T) {
	provider := &parityProvider{}
	if err := kit.RegisterProvider("parity", func(_ json.RawMessage) (rig.Provider, error) {
		return provider, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := kit.RegisterModule("zeokeel.host.parity", func(_ json.RawMessage) (kit.Module, error) {
		return hostbundle.New(hostbundle.Options{
			History:        session.NewMemory(),
			PromptSections: []kit.PromptSection{{Name: "product", Body: "product rules"}},
		}), nil
	}); err != nil {
		t.Fatal(err)
	}
	agent, err := kit.Build(context.Background(), kit.Spec{
		Model:    "m",
		Provider: kit.ProviderSpec{Name: "parity"},
		Modules:  []kit.ModuleRef{{Name: "zeokeel.host.parity"}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := agent.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
