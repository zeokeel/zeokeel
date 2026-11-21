package agent

// Lazy tool discovery: the request carries the core surface, tool_search lists
// the rest and activation reaches the live run (and the following runs of the
// same session).

import (
	"context"
	"strings"
	"sync"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"
	"github.com/zeokeel/zeokeel-rig/tools/lsp"
	"github.com/zeokeel/zeokeel-rig/tools/schedule"
	"github.com/zeokeel/zeokeel-rig/tools/worktree"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// discoveryStubTool is a registry tool with a controllable description.
type discoveryStubTool struct {
	name        string
	description string
}

func (t *discoveryStubTool) Name() string           { return t.name }
func (t *discoveryStubTool) Description() string    { return t.description }
func (t *discoveryStubTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *discoveryStubTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{Content: "ok"}, nil
}

// fakeActivator records activations and reports them as activated.
type fakeActivator struct {
	sessionID string
	activated []string
}

func (f *fakeActivator) ActivateTools(sessionID string, names ...string) ([]string, []string) {
	f.sessionID = sessionID
	f.activated = append(f.activated, names...)
	return names, nil
}

// ActiveTools completes the rig.ToolActivator contract.
func (f *fakeActivator) ActiveTools(string) []string { return append([]string(nil), f.activated...) }

func newDiscoveryService(t *testing.T) (*service, *rigtools.Registry, string) {
	t.Helper()
	registry := rigtools.NewRegistry(
		&discoveryStubTool{name: "read_file", description: "Read one file."},
		&discoveryStubTool{name: "bash", description: "Run a command."},
		&discoveryStubTool{name: "workflow", description: "Run a JavaScript workflow."},
		&discoveryStubTool{name: "lsp_definition", description: "Go to a symbol definition."},
		&discoveryStubTool{name: "custom_tool", description: "A host-registered tool."},
		&discoveryStubTool{name: "enter_worktree", description: "Create a git worktree."},
	)
	settings := newFakeSettings(nil)
	svc := newService(Config{}, registry, contracts.SettingsService(settings), &fakeLogger{}, newFakeStore(), fakeApprovals{})
	if err := svc.registerDiscoveryTool(svc.newDiscoveryTool()); err != nil {
		t.Fatalf("register tool_search: %v", err)
	}
	sessionID, err := svc.NewSession("discovery")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return svc, registry, sessionID
}

// TestDiscoveryInitialActiveKeepsTheCoreSurface pins the starting set: frequent
// coding tools are active, the long tail is not.
func TestDiscoveryInitialActiveKeepsTheCoreSurface(t *testing.T) {
	svc, registry, sessionID := newDiscoveryService(t)
	active := svc.initialActiveTools(sessionID, registry.Tools())
	set := make(map[string]bool, len(active))
	for _, name := range active {
		set[name] = true
	}
	for _, want := range []string{"read_file", "bash", "tool_search", "workflow"} {
		if !set[want] {
			t.Errorf("core tool %q is not initially active", want)
		}
	}
	for _, hidden := range []string{"lsp_definition", "enter_worktree"} {
		if set[hidden] {
			t.Errorf("long-tail tool %q is active without discovery", hidden)
		}
	}
	// A tool the discovery list does not know (custom host/MCP registration)
	// stays active: discovery must never silently hide an unknown tool.
	if !set["custom_tool"] {
		t.Error("unknown custom tool was not active")
	}
}

// TestDiscoveryListsOnlyHiddenTools pins the list: active tools are absent, the
// hidden ones carry a one-line summary.
func TestDiscoveryListsOnlyHiddenTools(t *testing.T) {
	svc, _, sessionID := newDiscoveryService(t)
	items := svc.discoverableToolsFor(sessionID)
	names := make(map[string]string, len(items))
	for _, item := range items {
		names[item.Name] = item.Summary
	}
	if _, ok := names["read_file"]; ok {
		t.Fatal("active tool listed as discoverable")
	}
	if _, ok := names["tool_search"]; ok {
		t.Fatal("tool_search listed as discoverable")
	}
	if names["lsp_definition"] != "Go to a symbol definition" {
		t.Fatalf("lsp_definition summary = %q, want the first sentence", names["lsp_definition"])
	}
	if names["enter_worktree"] == "" {
		t.Fatal("enter_worktree missing from the discoverable list")
	}
}

// TestDiscoveryActivationReachesTheRun pins activation: the live runner is asked,
// the names are remembered for the session, and they disappear from the list.
func TestDiscoveryActivationReachesTheRun(t *testing.T) {
	svc, registry, sessionID := newDiscoveryService(t)
	activator := &fakeActivator{}
	svc.bindSessionRunner(sessionID, activator)

	if runner := svc.sessionRunner(sessionID); runner == nil {
		t.Fatal("sessionRunner is nil right after bind")
	}
	activated, unknown := svc.activateSessionToolsFor(sessionID, []string{"lsp_definition"})
	if len(activated) != 1 || activated[0] != "lsp_definition" || len(unknown) != 0 {
		t.Fatalf("activation = %v/%v, want lsp_definition", activated, unknown)
	}
	if activator.sessionID != sessionID {
		t.Fatalf("activation session = %q, want %q", activator.sessionID, sessionID)
	}
	if active := svc.initialActiveTools(sessionID, registry.Tools()); !discoveryHasName(active, "lsp_definition") {
		t.Fatalf("activation not remembered: %v", active)
	}
	for _, item := range svc.discoverableToolsFor(sessionID) {
		if item.Name == "lsp_definition" {
			t.Fatal("activated tool still listed as discoverable")
		}
	}
}

// TestDiscoveryWithoutLiveRunReportsUnknown pins the outside-a-run case: no
// runner means nothing can be activated (the model gets a readable unknown list).
func TestDiscoveryWithoutLiveRunReportsUnknown(t *testing.T) {
	svc, _, sessionID := newDiscoveryService(t)
	activated, unknown := svc.activateSessionToolsFor(sessionID, []string{"lsp_definition"})
	if len(activated) != 0 || len(unknown) != 1 {
		t.Fatalf("activation outside a run = %v/%v, want unknown", activated, unknown)
	}
}

// TestDiscoveryPromptGuidanceFollowsTheActiveSurface pins that the prompt does
// not describe tools the request does not carry.
func TestDiscoveryPromptGuidanceFollowsTheActiveSurface(t *testing.T) {
	svc, registry, sessionID := newDiscoveryService(t)
	active := svc.activeToolSet(sessionID, registry.Tools())
	tools := activeRunTools(registry.Tools(), active)
	for _, tool := range tools {
		if tool.Name() == "enter_worktree" || tool.Name() == "lsp_definition" {
			t.Fatalf("hidden tool %q kept in the prompt surface", tool.Name())
		}
	}
	names := toolNameSet(tools)
	if names["enter_worktree"] {
		t.Fatal("prompt tool set claims enter_worktree is visible")
	}
	if !names["read_file"] {
		t.Fatal("prompt tool set lost read_file")
	}
}

func discoveryHasName(names []string, want string) bool {
	for _, name := range names {
		if strings.TrimSpace(name) == want {
			return true
		}
	}
	return false
}

// TestDiscoveryTailCoversTheRegisteredFamilies pins the tail against the tools
// the SDK actually registers: a family split across the tail and the active
// surface (exit_worktree was) makes the prompt and the request disagree.
func TestDiscoveryTailCoversTheRegisteredFamilies(t *testing.T) {
	families := [][]rig.Tool{
		worktree.Tools(nil),
		lsp.Tools(nil, nil),
		schedule.Tools(nil),
	}
	for _, tools := range families {
		for _, tool := range tools {
			if tool == nil {
				continue
			}
			if name := tool.Name(); !isDiscoverableTail(name) {
				t.Errorf("%q is registered by an SDK family but missing from discoveryTailTools", name)
			}
		}
	}
	for _, name := range []string{"mcp__server__echo", "mcp__other__search"} {
		if !isDiscoverableTail(name) {
			t.Errorf("MCP tool %q is not part of the tail", name)
		}
	}
	if isDiscoverableTail("custom_tool") {
		t.Error("unknown tool treated as a tail tool")
	}
}

// TestDiscoveryPresetTailToolStaysActive pins the preset case: a preset that
// names a tail tool must still get it active (skip_discovery), and tool_search
// itself survives the allowlist.
func TestDiscoveryPresetTailToolStaysActive(t *testing.T) {
	registry := rigtools.NewRegistry(
		&discoveryStubTool{name: "read_file", description: "Read one file."},
		&discoveryStubTool{name: "lsp_definition", description: "Go to a symbol definition."},
	)
	settings := newFakeSettings(map[string]string{
		contracts.SettingAgentPresets: `[{"id":"tail","name":"Tail","tools":["read_file","lsp_definition"]}]`,
	})
	svc := newService(Config{}, registry, contracts.SettingsService(settings), &fakeLogger{}, newFakeStore(), fakeApprovals{})
	sessionID, err := svc.NewSession("preset")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPreset(sessionID, "tail"); err != nil {
		t.Fatalf("SetSessionPreset: %v", err)
	}
	if err := svc.registerDiscoveryTool(svc.newDiscoveryTool()); err != nil {
		t.Fatalf("register tool_search: %v", err)
	}
	surface := svc.toolsForSession(sessionID)
	active := svc.initialActiveTools(sessionID, surface)
	if !discoveryHasName(active, "lsp_definition") {
		t.Fatalf("preset-named tail tool not active: %v", active)
	}
	if !discoveryHasName(active, "tool_search") {
		t.Fatalf("tool_search dropped by the preset allowlist: %v", active)
	}
}

// discoveryProvider activates lsp_definition through tool_search on its first
// call, then answers.
type discoveryProvider struct {
	mu       sync.Mutex
	calls    int
	requests []rig.Request
}

func (p *discoveryProvider) Name() string { return "discovery" }

func (p *discoveryProvider) Stream(_ context.Context, request rig.Request) (rig.Stream, error) {
	p.mu.Lock()
	p.calls++
	p.requests = append(p.requests, request)
	call := p.calls
	p.mu.Unlock()
	if call == 1 {
		return &scriptedStream{chunks: []rig.Chunk{
			{ToolCalls: []rig.ToolCallDelta{{Index: 0, ID: "call-1", Name: "tool_search",
				Arguments: `{"activate":["lsp_definition"]}`}}},
			{FinishReason: "tool_calls"},
		}}, nil
	}
	return &scriptedStream{chunks: []rig.Chunk{{Delta: "done"}, {FinishReason: "stop"}}}, nil
}

func (p *discoveryProvider) request(t *testing.T, n int) rig.Request {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) < n {
		t.Fatalf("provider requests = %d, want at least %d", len(p.requests), n)
	}
	return p.requests[n-1]
}

// TestDiscoveryActivationEndToEnd drives a real run: the first request omits the
// tail, tool_search appends it to the next request of the same run, the
// following run remembers it, and the run releases its activator.
func TestDiscoveryActivationEndToEnd(t *testing.T) {
	svc, registry, sessionID := newDiscoveryService(t)
	provider := &discoveryProvider{}
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	if err := svc.Send(sessionID, "load the lsp tool"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)

	first := provider.request(t, 1)
	if hasToolNamed(first.Tools, "lsp_definition") {
		t.Fatal("first request carried the discoverable tail")
	}
	if !hasToolNamed(first.Tools, "tool_search") {
		t.Fatal("first request lost tool_search")
	}
	if system := systemTextOf(first); strings.Contains(system, "enter_worktree") {
		t.Fatalf("system prompt describes a hidden tool: %q", system)
	}
	second := provider.request(t, 2)
	if !hasToolNamed(second.Tools, "lsp_definition") {
		t.Fatalf("activated tool missing from the next request: %v", toolNamesOf(second.Tools))
	}
	if got := toolNamesOf(second.Tools); len(got) == 0 || got[len(got)-1] != "lsp_definition" {
		t.Fatalf("activation was not appended at the end (prefix cache): %v", got)
	}
	if runner := svc.sessionRunner(sessionID); runner != nil {
		t.Fatal("run released its activator late (runner still bound)")
	}

	if err := svc.Send(sessionID, "again"); err != nil {
		t.Fatalf("Send again: %v", err)
	}
	waitIdle(t, svc, sessionID)
	third := provider.request(t, 3)
	if !hasToolNamed(third.Tools, "lsp_definition") {
		t.Fatalf("activation not remembered for the next run: %v", toolNamesOf(third.Tools))
	}
	_ = registry
	// The context snapshot (tool directory / estimated prompt) must describe the
	// same surface as the request.
	folded, ok := svc.SessionContext(sessionID)
	if !ok {
		t.Fatal("no context snapshot for the session")
	}
	snapshotNames := make([]string, 0, len(folded.Snapshot.Tools))
	for _, tool := range folded.Snapshot.Tools {
		snapshotNames = append(snapshotNames, tool.Name)
	}
	requestNames := toolNamesOf(third.Tools)
	if strings.Join(snapshotNames, ",") != strings.Join(requestNames, ",") {
		t.Fatalf("snapshot tools %v != request tools %v", snapshotNames, requestNames)
	}
}

// systemTextOf joins the request's system messages.
func systemTextOf(request rig.Request) string {
	var parts []string
	for _, message := range request.Messages {
		if message.Role != rig.RoleSystem {
			continue
		}
		for _, chunk := range message.Content {
			parts = append(parts, chunk.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func hasToolNamed(tools []rig.ToolSpec, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func toolNamesOf(tools []rig.ToolSpec) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

// retryDiscoveryProvider scripts a tool_search activation, then a context
// overflow, then (on the retried run) another activation and an answer.
type retryDiscoveryProvider struct {
	mu       sync.Mutex
	calls    int
	requests []rig.Request
}

func (p *retryDiscoveryProvider) Name() string { return "retry-discovery" }

func (p *retryDiscoveryProvider) Stream(_ context.Context, request rig.Request) (rig.Stream, error) {
	if isCompactionSummaryRequest(request) {
		return &scriptedStream{chunks: []rig.Chunk{{Delta: "SUMMARY"}}}, nil
	}
	p.mu.Lock()
	p.calls++
	p.requests = append(p.requests, request)
	call := p.calls
	p.mu.Unlock()
	switch call {
	case 1:
		return &scriptedStream{chunks: []rig.Chunk{
			{ToolCalls: []rig.ToolCallDelta{{Index: 0, ID: "call-1", Name: "tool_search",
				Arguments: `{"activate":["lsp_definition"]}`}}},
			{FinishReason: "tool_calls"},
		}}, nil
	case 2:
		return nil, contextOverflowErr()
	case 3:
		return &scriptedStream{chunks: []rig.Chunk{
			{ToolCalls: []rig.ToolCallDelta{{Index: 0, ID: "call-2", Name: "tool_search",
				Arguments: `{"activate":["schedule_list"]}`}}},
			{FinishReason: "tool_calls"},
		}}, nil
	}
	return &scriptedStream{chunks: []rig.Chunk{{Delta: "done"}, {FinishReason: "stop"}}}, nil
}

func (p *retryDiscoveryProvider) request(t *testing.T, n int) rig.Request {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) < n {
		t.Fatalf("provider requests = %d, want at least %d", len(p.requests), n)
	}
	return p.requests[n-1]
}

// TestDiscoveryOverflowRetryKeepsTheSurface pins the recovery path: the retried
// run carries the activations made before the overflow and can activate again.
func TestDiscoveryOverflowRetryKeepsTheSurface(t *testing.T) {
	provider := &retryDiscoveryProvider{}
	svc, store := newRecoveryService(t, provider)
	if err := svc.registerDiscoveryTool(svc.newDiscoveryTool()); err != nil {
		t.Fatalf("register tool_search: %v", err)
	}
	// The registry from newRecoveryService is empty: replace it with one that
	// carries the tail tools the provider activates.
	svc.registry = rigtools.NewRegistry(
		&discoveryStubTool{name: "read_file", description: "Read one file."},
		&discoveryStubTool{name: "lsp_definition", description: "Go to a symbol definition."},
		&discoveryStubTool{name: "schedule_list", description: "List reminders."},
	)
	if err := svc.registerDiscoveryTool(svc.newDiscoveryTool()); err != nil {
		t.Fatalf("register tool_search after registry swap: %v", err)
	}
	sessionID, err := svc.NewSession("overflow")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	seedRecoveryHistory(t, store, sessionID, 10)
	if err := svc.Send(sessionID, "the question"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)

	retry := provider.request(t, 3)
	if !hasToolNamed(retry.Tools, "lsp_definition") {
		t.Fatalf("retried request lost the pre-overflow activation: %v", toolNamesOf(retry.Tools))
	}
	after := provider.request(t, 4)
	if !hasToolNamed(after.Tools, "schedule_list") {
		t.Fatalf("activation inside the retry did not reach the reborn runner: %v", toolNamesOf(after.Tools))
	}
}

// TestDiscoveryColdResumeChildKeepsItsFullSurface pins the delegated child: it
// excludes tool_search, so InitialActiveTools must stay nil or the tail would be
// hidden with no way to load it (request and prompt must agree).
func TestDiscoveryColdResumeChildKeepsItsFullSurface(t *testing.T) {
	dataDir := t.TempDir()
	store := newFakeStore()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	provider := &captureProvider{}
	svc := newService(Config{}, rigtools.NewRegistry(
		&discoveryStubTool{name: "read_file", description: "Read one file."},
		&discoveryStubTool{name: "lsp_definition", description: "Go to a symbol definition."},
	), contracts.SettingsService(settings), &fakeLogger{}, store, fakeApprovals{})
	svc.dataDir = dataDir
	svc.childSessions = newChildSessionStore(dataDir, &fakeLogger{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	if err := svc.registerDiscoveryTool(svc.newDiscoveryTool()); err != nil {
		t.Fatalf("register tool_search: %v", err)
	}
	parentID, err := svc.NewSession("parent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	record := seedHistoricalChild(t, svc, parentID, contracts.ChildAgentSubagent, "researcher", "summarize", "first")
	children := newChildAgentsService(svc, nil)
	if _, err := children.SendChildAgent(context.Background(), record.ID, "continue"); err != nil {
		t.Fatalf("SendChildAgent: %v", err)
	}
	request := provider.lastRequest()
	if !hasToolNamed(request.Tools, "lsp_definition") {
		t.Fatalf("cold-resumed child lost its tail tools: %v", toolNamesOf(request.Tools))
	}
	if hasToolNamed(request.Tools, "tool_search") {
		t.Fatalf("cold-resumed child received tool_search: %v", toolNamesOf(request.Tools))
	}
}
