package agent

// Tests for the synchronous subagent delegation seam: service.Run, the two
// registered tools, provider/model propagation, fork guards and concurrency.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// newSubagentModuleService activates the agent module and returns both the
// concrete service and the context (for KeySubagents).
func newSubagentModuleService(t *testing.T) (*service, cordis.Context) {
	t.Helper()
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	registry := rigtool.NewRegistry()
	settings := newFakeSettings(nil)
	logger := &fakeLogger{}
	store := newFakeStore()
	provide(t, ctx, contracts.KeyToolkit, registry)
	provide(t, ctx, contracts.KeySettings, contracts.SettingsService(settings))
	provide(t, ctx, contracts.KeyLogger, contracts.Logger(logger))
	provide(t, ctx, contracts.KeyStore, contracts.Store(store))
	provide(t, ctx, contracts.KeyApprovals, contracts.Approvals(fakeApprovals{}))
	if err := Module().Factory(ctx, Config{}); err != nil {
		t.Fatalf("activate agent module: %v", err)
	}
	chat, err := ctx.Require(contracts.KeyChat)
	if err != nil {
		t.Fatalf("require KeyChat: %v", err)
	}
	svc, ok := chat.(*service)
	if !ok {
		t.Fatalf("KeyChat = %T, want *service", chat)
	}
	if _, err := ctx.Require(contracts.KeySubagents); err != nil {
		t.Fatalf("require KeySubagents: %v", err)
	}
	return svc, ctx
}

// registryTool returns a registered tool by name.
func registryTool(t *testing.T, registry *rigtool.Registry, name string) rig.Tool {
	t.Helper()
	for _, tool := range registry.Tools() {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("tool %q is not registered", name)
	return nil
}

func TestRunSubagentDelegatesSynchronously(t *testing.T) {
	provider := &captureProvider{}
	svc, _ := newAttachmentService(t, provider)
	result, err := svc.Run(context.Background(), contracts.SubagentRequest{Prompt: "  summarize the repo  "})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Text != "ok" {
		t.Fatalf("result text = %q, want the subagent output", result.Text)
	}
	if result.Name == "" || result.AgentID == "" || result.AgentID != result.Name {
		t.Fatalf("result identity = %+v", result)
	}
	if !strings.HasPrefix(result.Name, "sub-") {
		t.Fatalf("auto name = %q, want sub-<n>", result.Name)
	}
	request := provider.lastRequest()
	if request.Model == "" {
		t.Fatal("subagent provider request has an empty model")
	}
	provider.mu.Lock()
	calls := len(provider.requests)
	provider.mu.Unlock()
	if calls != 1 {
		t.Fatalf("provider calls = %d, want exactly one subagent delegation", calls)
	}
}

func TestRunSubagentValidationAndForkGuard(t *testing.T) {
	provider := &captureProvider{}
	svc, _ := newAttachmentService(t, provider)

	if _, err := svc.Run(context.Background(), contracts.SubagentRequest{Prompt: "   "}); err == nil ||
		!strings.Contains(err.Error(), "prompt") {
		t.Fatalf("Run(blank prompt) = %v, want prompt error", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Run(cancelled, contracts.SubagentRequest{Prompt: "work"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(cancelled ctx) = %v, want context.Canceled", err)
	}
	if _, err := svc.Run(context.Background(), contracts.SubagentRequest{Prompt: "work", Fork: true}); err == nil ||
		!strings.Contains(err.Error(), "active session") {
		t.Fatalf("Run(fork without session) = %v, want active-session error", err)
	}
	if _, err := svc.NewSession("parent"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// Fork with an active session (empty history) is allowed.
	if _, err := svc.Run(context.Background(), contracts.SubagentRequest{Prompt: "fork work", Fork: true}); err != nil {
		t.Fatalf("Run(fork with active session): %v", err)
	}
	if _, err := svc.Run(context.Background(), contracts.SubagentRequest{Prompt: "bad budget", MaxIterations: -1}); err == nil {
		t.Fatal("Run(negative MaxIterations) returned nil error")
	}
}

func TestRunSubagentModelOverrideAndConcurrency(t *testing.T) {
	provider := &captureProvider{}
	svc, _ := newAttachmentService(t, provider)

	type outcome struct {
		result contracts.SubagentResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	var wg sync.WaitGroup
	for _, model := range []string{"model-a", "model-b"} {
		model := model
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := svc.Run(context.Background(), contracts.SubagentRequest{Prompt: "work " + model, Model: model})
			outcomes <- outcome{result: result, err: err}
		}()
	}
	wg.Wait()
	close(outcomes)
	names := make(map[string]bool)
	for outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("concurrent Run: %v", outcome.err)
		}
		if outcome.result.Name == "" {
			t.Fatal("concurrent Run returned an empty name")
		}
		names[outcome.result.Name] = true
	}
	if len(names) != 2 {
		t.Fatalf("subagent names = %v, want unique auto names", names)
	}
	provider.mu.Lock()
	var models []string
	for _, request := range provider.requests {
		models = append(models, request.Model)
	}
	provider.mu.Unlock()
	seen := map[string]bool{}
	for _, model := range models {
		seen[model] = true
	}
	for _, want := range []string{"model-a", "model-b"} {
		if !seen[want] {
			t.Fatalf("provider models = %v, want both overrides", models)
		}
	}
}

func TestSubagentToolsRegisteredAndInvokable(t *testing.T) {
	svc, _ := newSubagentModuleService(t)
	provider := &captureProvider{}
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	for _, name := range []string{"subagent", "subagent_fork"} {
		tool := registryTool(t, svc.registry, name)
		if strings.TrimSpace(tool.Description()) == "" {
			t.Fatalf("%s has an empty description", name)
		}
		if tool.Schema()["type"] != "object" {
			t.Fatalf("%s schema type = %v, want object", name, tool.Schema()["type"])
		}
	}

	subagent := registryTool(t, svc.registry, "subagent")
	result, err := subagent.Invoke(context.Background(), rig.ToolCall{
		ID: "call-1", Name: "subagent",
		Arguments: `{"prompt":"do the thing","model":"model-override","max_iterations":3}`,
	})
	if err != nil {
		t.Fatalf("subagent Invoke: %v", err)
	}
	if result.IsError || !strings.Contains(result.Content, "ok") || !strings.Contains(result.Content, "sub-") {
		t.Fatalf("subagent result = %+v", result)
	}
	if request := provider.lastRequest(); request.Model != "model-override" {
		t.Fatalf("subagent model = %q, want override", request.Model)
	}

	fork := registryTool(t, svc.registry, "subagent_fork")
	result, err = fork.Invoke(context.Background(), rig.ToolCall{
		ID: "call-2", Name: "subagent_fork", Arguments: `{"prompt":"fork the thing"}`,
	})
	if err != nil {
		t.Fatalf("subagent_fork Invoke: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "active session") {
		t.Fatalf("fork without active session = %+v, want an error result", result)
	}
	if _, err := svc.NewSession("parent"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	result, err = fork.Invoke(context.Background(), rig.ToolCall{
		ID: "call-3", Name: "subagent_fork", Arguments: `{"prompt":"fork the thing"}`,
	})
	if err != nil || result.IsError {
		t.Fatalf("fork with active session = %+v, %v", result, err)
	}

	if _, err := subagent.Invoke(context.Background(), rig.ToolCall{
		ID: "call-4", Name: "subagent", Arguments: `not-json`,
	}); err == nil {
		t.Fatal("subagent Invoke(invalid JSON) returned nil error")
	}
	if result, err := subagent.Invoke(context.Background(), rig.ToolCall{
		ID: "call-5", Name: "subagent", Arguments: `{"prompt":"   "}`,
	}); err != nil || !result.IsError || !strings.Contains(result.Content, "prompt") {
		t.Fatalf("subagent Invoke(blank prompt) = %+v, %v", result, err)
	}
}

func TestSubagentExcludedTools(t *testing.T) {
	names := subagentExcludedTools()
	want := []string{"subagent", "subagent_fork", "workflow", "team", "ralph", "todo", "spawn_teammate"}
	for _, name := range want {
		found := false
		for _, excluded := range names {
			if excluded == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("excluded tools %v lack %q", names, name)
		}
	}
	// The helper must return a fresh slice: mutating it cannot change the next
	// child's exclusions.
	names[0] = "mutated"
	again := subagentExcludedTools()
	if len(again) == 0 || again[0] == "mutated" {
		t.Fatalf("subagentExcludedTools leaked mutable state: %v", again)
	}
}

func TestSubagentToolsUnregisterOnDispose(t *testing.T) {
	ctx := cordis.Root()
	registry := rigtool.NewRegistry()
	provide(t, ctx, contracts.KeyToolkit, registry)
	provide(t, ctx, contracts.KeySettings, contracts.SettingsService(newFakeSettings(nil)))
	provide(t, ctx, contracts.KeyLogger, contracts.Logger(&fakeLogger{}))
	provide(t, ctx, contracts.KeyStore, contracts.Store(newFakeStore()))
	provide(t, ctx, contracts.KeyApprovals, contracts.Approvals(fakeApprovals{}))
	if err := Module().Factory(ctx, Config{}); err != nil {
		t.Fatalf("activate agent module: %v", err)
	}
	for _, name := range []string{"subagent", "subagent_fork"} {
		registryTool(t, registry, name)
	}
	if err := ctx.DisposeAll(); err != nil {
		t.Fatalf("DisposeAll: %v", err)
	}
	for _, tool := range registry.Tools() {
		if tool.Name() == "subagent" || tool.Name() == "subagent_fork" {
			t.Fatalf("tool %q survived module disposal", tool.Name())
		}
	}
}
