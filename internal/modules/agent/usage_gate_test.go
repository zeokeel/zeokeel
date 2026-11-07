package agent

// Tests for assistant-turn usage accounting and the plan-mode tool gate.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// --- usage ------------------------------------------------------------------

// usageScriptProvider answers each Stream with a text delta, one usage chunk and
// a stop reason.
type usageScriptProvider struct {
	mu       sync.Mutex
	requests []rig.Request
	usage    rig.Usage
}

func (p *usageScriptProvider) Name() string { return "usage-script" }

func (p *usageScriptProvider) Stream(_ context.Context, request rig.Request) (rig.Stream, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	usage := p.usage
	p.mu.Unlock()
	return &scriptedStream{chunks: []rig.Chunk{
		{Delta: "accounted"},
		{Usage: &usage},
		{FinishReason: "stop"},
	}}, nil
}

func (p *usageScriptProvider) lastRequest() rig.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		return rig.Request{}
	}
	return p.requests[len(p.requests)-1]
}

// newUsageService builds the agent service with a fake durable store and the
// supplied provider factory.
func newUsageService(t *testing.T, provider rig.Provider, settings *fakeSettings) (*service, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	if settings == nil {
		settings = newFakeSettings(nil)
	}
	svc := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, store, fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	return svc, store
}

func lastAssistantMessage(t *testing.T, svc *service, sessionID string) contracts.ChatMessage {
	t.Helper()
	session := waitIdle(t, svc, sessionID)
	for index := len(session.Messages) - 1; index >= 0; index-- {
		if session.Messages[index].Role == contracts.ChatRoleAssistant {
			return session.Messages[index]
		}
	}
	t.Fatalf("no assistant message in %+v", session.Messages)
	return contracts.ChatMessage{}
}

func usageEvents(t *testing.T, store *fakeStore, sessionID string) []map[string]any {
	t.Helper()
	events, err := store.Events(context.Background(), sessionID, 0, 0)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	var out []map[string]any
	for _, event := range events {
		if event.Type != "usage" {
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal(event.Payload, &fields); err != nil {
			t.Fatalf("usage payload %s: %v", event.Payload, err)
		}
		out = append(out, fields)
	}
	return out
}

func TestRunRecordsUsageOnAssistantMessage(t *testing.T) {
	usage := rig.Usage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18, CachedTokens: 3}
	provider := &usageScriptProvider{usage: usage}
	svc, store := newUsageService(t, provider, nil)
	id, err := svc.NewSession("usage")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	assistant := lastAssistantMessage(t, svc, id)
	if assistant.Usage == nil {
		t.Fatalf("assistant Usage = nil, want tokens from provider")
	}
	got := assistant.Usage
	if got.PromptTokens != 11 || got.CompletionTokens != 7 || got.TotalTokens != 18 || got.CachedTokens != 3 {
		t.Fatalf("assistant Usage = %+v, want %+v", got, usage)
	}
	if got.DurationMS < 0 {
		t.Fatalf("DurationMS = %d, want >= 0", got.DurationMS)
	}
	if got.Iterations < 1 {
		t.Fatalf("Iterations = %d, want >= 1", got.Iterations)
	}

	events := usageEvents(t, store, id)
	if len(events) != 1 {
		t.Fatalf("usage events = %+v, want one", events)
	}
	event := events[0]
	if event["promptTokens"].(float64) != 11 || event["completionTokens"].(float64) != 7 ||
		event["totalTokens"].(float64) != 18 || event["cachedTokens"].(float64) != 3 {
		t.Fatalf("usage event fields = %+v", event)
	}
	if _, ok := event["durationMs"]; !ok {
		t.Fatalf("usage event has no durationMs: %+v", event)
	}
}

func TestRunWithoutUsageLeavesUsageNil(t *testing.T) {
	provider := &captureProvider{}
	svc, store := newAttachmentService(t, provider)
	id, err := svc.NewSession("no usage")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	assistant := lastAssistantMessage(t, svc, id)
	if assistant.Usage != nil {
		t.Fatalf("assistant Usage = %+v, want nil when provider reports none", assistant.Usage)
	}
	if events := usageEvents(t, store, id); len(events) != 0 {
		t.Fatalf("usage events = %+v, want none", events)
	}
}

func TestRegenerateRecordsUsage(t *testing.T) {
	usage := rig.Usage{PromptTokens: 4, CompletionTokens: 2, TotalTokens: 6}
	provider := &usageScriptProvider{usage: usage}
	svc, _ := newUsageService(t, provider, nil)
	id, err := svc.NewSession("regenerate usage")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	if err := svc.Regenerate(id); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	assistant := lastAssistantMessage(t, svc, id)
	if assistant.Usage == nil || assistant.Usage.PromptTokens != 4 || assistant.Usage.TotalTokens != 6 {
		t.Fatalf("regenerated assistant Usage = %+v", assistant.Usage)
	}
}

// --- plan-mode gate ---------------------------------------------------------

// gateTestTool is a counting fake tool.
type gateTestTool struct {
	name  string
	calls sync.Once
	count int
}

func (t *gateTestTool) Name() string        { return t.name }
func (t *gateTestTool) Description() string { return "test tool " + t.name }
func (t *gateTestTool) Schema() map[string]any {
	return map[string]any{"type": "object"}
}
func (t *gateTestTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	t.calls.Do(func() {})
	t.count++
	return rig.ToolResult{Content: "ran " + t.name}, nil
}

// toolCallScriptProvider requests one tool call on its first Stream, then
// returns a final text answer.
type toolCallScriptProvider struct {
	mu     sync.Mutex
	calls  int
	tool   string
	toolID string
}

func (p *toolCallScriptProvider) Name() string { return "tool-call-script" }

func (p *toolCallScriptProvider) Stream(context.Context, rig.Request) (rig.Stream, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if call == 1 {
		return &scriptedStream{chunks: []rig.Chunk{
			{ToolCalls: []rig.ToolCallDelta{{Index: 0, ID: p.toolID, Name: p.tool, Arguments: "{}"}}},
			{FinishReason: "tool_calls"},
		}}, nil
	}
	return &scriptedStream{chunks: []rig.Chunk{
		{Delta: "done"},
		{FinishReason: "stop"},
	}}, nil
}

func planGateService(t *testing.T, mode string, tools ...rig.Tool) *service {
	t.Helper()
	settings := newFakeSettings(map[string]string{contracts.SettingAgentMode: mode})
	registry := rigtools.NewRegistry(tools...)
	return newService(Config{}, registry, contracts.SettingsService(settings), &fakeLogger{}, newFakeStore(), fakeApprovals{})
}

func TestGatePlanModeWhitelist(t *testing.T) {
	svc := planGateService(t, contracts.AgentModePlan)
	gate := svc.gate()
	cases := []struct {
		name string
		want rig.Decision
	}{
		{"read_file", rig.DecisionAllow},
		{"list_dir", rig.DecisionAllow},
		{"glob", rig.DecisionAllow},
		{"grep", rig.DecisionAllow},
		{"ask_user", rig.DecisionAllow},
		{"present", rig.DecisionAllow},
		{"plan_review", rig.DecisionAllow},
		{"exit_plan_mode", rig.DecisionAllow},
		{"goal", rig.DecisionAllow},
		{"team_task_list", rig.DecisionAllow},
		{"team_task_get", rig.DecisionAllow},
		{"memory", rig.DecisionAllow},
		{"skill", rig.DecisionAllow},
		{"skills", rig.DecisionAllow},
		{"web_fetch", rig.DecisionAllow},
		{"write_file", rig.DecisionDeny},
		{"edit_file", rig.DecisionDeny},
		{"bash", rig.DecisionDeny},
		{"pwsh", rig.DecisionDeny},
		{"subagent", rig.DecisionDeny},
		{"unknown_tool", rig.DecisionDeny},
	}
	for _, tc := range cases {
		tool := &gateTestTool{name: tc.name}
		decision, err := gate.Approve(context.Background(), tool, rig.ToolCall{Name: tc.name})
		if err != nil {
			t.Fatalf("Approve(%q): %v", tc.name, err)
		}
		if decision != tc.want {
			t.Fatalf("plan mode %q decision = %q, want %q", tc.name, decision, tc.want)
		}
	}
}

func TestGateModeSwitches(t *testing.T) {
	settings := newFakeSettings(map[string]string{contracts.SettingAgentMode: contracts.AgentModeNormal})
	svc := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
	tool := &gateTestTool{name: "bash"}
	decision, err := svc.gate().Approve(context.Background(), tool, rig.ToolCall{Name: "bash"})
	if err != nil || decision != rig.DecisionAllow {
		t.Fatalf("normal mode bash = %q/%v, want allow through approvals", decision, err)
	}
	if err := settings.Set(contracts.SettingAgentMode, contracts.AgentModePlan); err != nil {
		t.Fatalf("Set plan: %v", err)
	}
	decision, err = svc.gate().Approve(context.Background(), tool, rig.ToolCall{Name: "bash"})
	if err != nil || decision != rig.DecisionDeny {
		t.Fatalf("plan mode bash = %q/%v, want deny", decision, err)
	}
	if err := settings.Set(contracts.SettingAgentMode, contracts.AgentModeNormal); err != nil {
		t.Fatalf("Set normal: %v", err)
	}
	decision, err = svc.gate().Approve(context.Background(), tool, rig.ToolCall{Name: "bash"})
	if err != nil || decision != rig.DecisionAllow {
		t.Fatalf("normal again bash = %q/%v, want allow", decision, err)
	}
}

func TestGateIntegrationPlanModeBlocksWriteTool(t *testing.T) {
	readTool := &gateTestTool{name: "read_file"}
	writeTool := &gateTestTool{name: "write_file"}
	provider := &toolCallScriptProvider{tool: "write_file", toolID: "call-write"}
	settings := newFakeSettings(map[string]string{contracts.SettingAgentMode: contracts.AgentModePlan})
	registry := rigtools.NewRegistry(readTool, writeTool)
	svc := newService(Config{}, registry, contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	id, err := svc.NewSession("plan gate")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "write something"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	if writeTool.count != 0 {
		t.Fatalf("denied write_file executed %d times, want 0", writeTool.count)
	}
	if readTool.count != 0 {
		t.Fatalf("unrequested read_file executed %d times", readTool.count)
	}
}

func TestGateNilApprovalsNormalModeAllows(t *testing.T) {
	settings := newFakeSettings(nil)
	svc := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), nil)
	decision, err := svc.gate().Approve(context.Background(), &gateTestTool{name: "bash"}, rig.ToolCall{Name: "bash"})
	if err != nil || decision != rig.DecisionAllow {
		t.Fatalf("nil approvals normal bash = %q/%v, want allow-all", decision, err)
	}
	if err := settings.Set(contracts.SettingAgentMode, strings.ToUpper(contracts.AgentModePlan)); err != nil {
		t.Fatalf("Set plan: %v", err)
	}
	decision, err = svc.gate().Approve(context.Background(), &gateTestTool{name: "bash"}, rig.ToolCall{Name: "bash"})
	if err != nil || decision != rig.DecisionDeny {
		t.Fatalf("nil approvals plan bash = %q/%v, want deny", decision, err)
	}
}
