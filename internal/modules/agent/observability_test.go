package agent

// Tests for the context observability data foundation: per-call usage events,
// manual/inferred compaction, spill events, the request-level context snapshot,
// the contracts.ContextInspector folded view (including restart), and the
// model-price cost estimate surfaced by the inspector.

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// eventsOfType returns the durable events of one session/type in order.
func eventsOfType(t *testing.T, store *fakeStore, sessionID, eventType string) []rig.SessionEvent {
	t.Helper()
	events, err := store.Events(context.Background(), sessionID, 0, 0)
	if err != nil {
		t.Fatalf("Events(%s): %v", sessionID, err)
	}
	out := make([]rig.SessionEvent, 0, len(events))
	for _, event := range events {
		if event.Type == eventType {
			out = append(out, event)
		}
	}
	return out
}

// decodeEvent decodes one event payload into target.
func decodeEvent(t *testing.T, event rig.SessionEvent, target any) {
	t.Helper()
	if err := json.Unmarshal(event.Payload, target); err != nil {
		t.Fatalf("decode %s payload: %v", event.Type, err)
	}
}

// TestModelCallEventPerProviderCall covers the central audit fix: every provider
// call produces one durable model_call event with its own token fields, not only
// the last call of the turn.
func TestModelCallEventPerProviderCall(t *testing.T) {
	first := rig.Usage{PromptTokens: 120, CompletionTokens: 5, TotalTokens: 125, CachedTokens: 20}
	second := rig.Usage{PromptTokens: 340, CompletionTokens: 9, TotalTokens: 349, CachedTokens: 30}
	provider := &twoCallUsageProvider{first: first, second: &second}
	svc, store := newTwoCallUsageService(t, provider)
	sessionID, err := svc.NewSession("model calls")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "run a tool"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)

	events := eventsOfType(t, store, sessionID, modelCallEvent)
	if len(events) != 2 {
		t.Fatalf("model_call events = %d, want 2 (one per provider call)", len(events))
	}
	var payloads []modelCallPayload
	for _, event := range events {
		var payload modelCallPayload
		decodeEvent(t, event, &payload)
		payloads = append(payloads, payload)
	}
	if payloads[0].PromptTokens != first.PromptTokens || payloads[0].CachedTokens != first.CachedTokens ||
		payloads[0].CompletionTokens != first.CompletionTokens || payloads[0].ContextTokens != first.PromptTokens {
		t.Fatalf("first model_call payload = %+v, want the first call's usage", payloads[0])
	}
	if payloads[1].PromptTokens != second.PromptTokens || payloads[1].ContextTokens != second.PromptTokens {
		t.Fatalf("second model_call payload = %+v, want the second call's usage", payloads[1])
	}
	if payloads[0].Model == "" || payloads[1].Model == "" {
		t.Fatalf("model_call payloads lack the model: %+v / %+v", payloads[0], payloads[1])
	}
	if payloads[0].Iteration == 0 || payloads[1].Iteration == 0 {
		t.Fatalf("model_call payloads lack the iteration: %+v / %+v", payloads[0], payloads[1])
	}

	// The folded inspector view exposes the same two requests and the last
	// call's context size as the current used anchor.
	folded, ok := svc.SessionContext(sessionID)
	if !ok {
		t.Fatal("SessionContext reported no data after a run")
	}
	if len(folded.Requests) != 2 {
		t.Fatalf("inspector requests = %d, want 2", len(folded.Requests))
	}
	if folded.Used != second.PromptTokens {
		t.Fatalf("inspector used = %d, want the last context token count %d", folded.Used, second.PromptTokens)
	}
	if folded.Revision == 0 {
		t.Fatal("inspector revision is 0 after recorded events")
	}
}

// TestContextSnapshotEventFields pins the request-level snapshot payload.
func TestContextSnapshotEventFields(t *testing.T) {
	echo := &echoTool{}
	svc := newTestService(t, Config{}, newFakeSettings(map[string]string{contracts.SettingsChatModel: "snapshot-model"}), nil, echo)
	provider := &captureProvider{}
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	sessionID, err := svc.NewSession("snapshot")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)

	events := eventsOfType(t, svc.store.(*fakeStore), sessionID, contextSnapshotEvent)
	if len(events) != 1 {
		t.Fatalf("context_snapshot events = %d, want 1 per run", len(events))
	}
	var payload contextSnapshotPayload
	decodeEvent(t, events[0], &payload)
	if payload.Model != "snapshot-model" {
		t.Fatalf("snapshot model = %q, want snapshot-model", payload.Model)
	}
	if payload.SystemPromptBytes == 0 || payload.SystemPromptTokens == 0 {
		t.Fatalf("snapshot system prompt size = %d bytes/%d tokens, want non-zero",
			payload.SystemPromptBytes, payload.SystemPromptTokens)
	}
	if payload.ToolCount == 0 || len(payload.Tools) == 0 {
		t.Fatalf("snapshot tools = %d/%d, want the run's tool catalog", payload.ToolCount, len(payload.Tools))
	}
	foundEcho := false
	for _, tool := range payload.Tools {
		if tool.Name == "echo" {
			foundEcho = true
			if tool.Tokens == 0 {
				t.Fatal("snapshot tool entry has no token estimate")
			}
		}
	}
	if !foundEcho {
		t.Fatalf("snapshot tools lack the registered echo tool: %+v", payload.Tools)
	}
	if payload.History.Messages == 0 || payload.History.User == 0 {
		t.Fatalf("snapshot history = %+v, want the run's message counts", payload.History)
	}
	if payload.EstimatedPrompt == 0 {
		t.Fatal("snapshot estimated prompt is 0")
	}
	if payload.Injected.AgentsMD || payload.Injected.Memory || payload.Injected.Skills {
		t.Fatalf("snapshot injection flags = %+v, want none for a bare run", payload.Injected)
	}
	if len(payload.Sections) == 0 {
		t.Fatal("snapshot sections are empty, want the section-ized system prompt")
	}
	sectionIDs := make(map[string]bool, len(payload.Sections))
	for _, section := range payload.Sections {
		sectionIDs[section.ID] = true
		if section.Bytes == 0 || section.Tokens == 0 || section.Hash == "" || section.Source == "" {
			t.Fatalf("snapshot section = %+v, want bytes/tokens/hash/source", section)
		}
	}
	for _, want := range []string{PromptSectionIdentity, PromptSectionEnvironment,
		PromptSectionWorkingDirectory, PromptSectionApproval, PromptSectionToolGuidance} {
		if !sectionIDs[want] {
			t.Fatalf("snapshot sections lack %q: %+v", want, payload.Sections)
		}
	}
}

// TestPromptInjectionSources covers the injected-context flags (AGENTS.md,
// preset, memory, skills) detected from the rendered prompt/preset.
func TestPromptInjectionSources(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:   "m",
		contracts.SettingAgentPresets: `[{"id":"p1","name":"P1","persona":"You are terse.","prompt":"Prefer tables."}]`,
	})
	svc := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
	sessionID, err := svc.NewSession("injections")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPreset(sessionID, "p1"); err != nil {
		t.Fatalf("SetSessionPreset: %v", err)
	}
	prompt := "base\n\n" + workspaceInstructionsHeading + "\n| file\n\n## Memory\n- m\n\n## Skills\n- s"
	injected := svc.promptInjectionSources(sessionID, prompt)
	if !injected.AgentsMD || !injected.Memory || !injected.Skills || !injected.Preset {
		t.Fatalf("injection flags = %+v, want all true", injected)
	}
	bare := svc.promptInjectionSources(sessionID, "base only")
	if bare.AgentsMD || bare.Memory || bare.Skills {
		t.Fatalf("bare prompt injection flags = %+v, want the prompt markers absent", bare)
	}
	if !bare.Preset {
		t.Fatal("preset injection not detected for the selected preset")
	}
}

// TestCompactEventOnManualCompaction pins the exact manual compaction audit.
func TestCompactEventOnManualCompaction(t *testing.T) {
	compactor := &fakeCompactor{messages: compactTestMessages(2)}
	svc, store, sessionID := newCompactService(t, Config{ContextBudget: 100}, compactor, nil)
	if err := store.ReplaceHistory(context.Background(), sessionID, compactTestMessages(4)); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	result, err := svc.CompactNow(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	events := eventsOfType(t, store, sessionID, compactEvent)
	if len(events) != 1 {
		t.Fatalf("compact events = %d, want 1", len(events))
	}
	var payload compactPayload
	decodeEvent(t, events[0], &payload)
	if payload.Kind != "manual" || payload.Inferred {
		t.Fatalf("compact payload = %+v, want an exact manual compaction", payload)
	}
	if payload.Before != result.BeforeTokens || payload.After != result.AfterTokens ||
		payload.Replaced != result.Replaced || payload.Saved != result.Saved {
		t.Fatalf("compact payload = %+v, want the CompactResult %+v", payload, result)
	}
	folded, ok := svc.SessionContext(sessionID)
	if !ok {
		t.Fatal("SessionContext reported no timeline")
	}
	// The archive event precedes the compaction event; both are folded, and the
	// compaction one must stay exact.
	var compactEventFound bool
	for _, event := range folded.Events {
		if event.Kind != contracts.ContextEventCompact {
			continue
		}
		compactEventFound = true
		if event.Inferred {
			t.Fatalf("folded compact event = %+v, want an exact event", event)
		}
	}
	if !compactEventFound {
		t.Fatalf("inspector events = %+v, want the compaction event", folded.Events)
	}
}

// TestInferredAutoCompactionHeuristic covers the only observable signal for
// Rig's internal compaction: a sharp prompt-token drop between two calls is
// recorded as inferred, never as an exact result.
func TestInferredAutoCompactionHeuristic(t *testing.T) {
	if autoCompactDropThreshold(1000, 100) {
		t.Fatal("a tiny context drop was treated as a compaction")
	}
	if autoCompactDropThreshold(10000, 9000) {
		t.Fatal("a small 10% drop was treated as a compaction")
	}
	if !autoCompactDropThreshold(10000, 7000) {
		t.Fatal("a 30% drop on a large context was not treated as a compaction")
	}

	first := rig.Usage{PromptTokens: 10000, CompletionTokens: 5, TotalTokens: 10005}
	second := rig.Usage{PromptTokens: 3000, CompletionTokens: 9, TotalTokens: 3009}
	provider := &twoCallUsageProvider{first: first, second: &second}
	svc, store := newTwoCallUsageService(t, provider)
	sessionID, err := svc.NewSession("auto compact")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "run"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)

	events := eventsOfType(t, store, sessionID, compactEvent)
	if len(events) != 1 {
		t.Fatalf("compact events = %d, want the inferred one", len(events))
	}
	var payload compactPayload
	decodeEvent(t, events[0], &payload)
	if !payload.Inferred || payload.Kind != "auto" || payload.Before != 10000 || payload.After != 3000 {
		t.Fatalf("inferred compact payload = %+v, want auto/inferred 10000->3000", payload)
	}
	// The inferred heuristic never replaced durable history, so it archives
	// nothing (the archive only protects an actual replacement).
	if archives := eventsOfType(t, store, sessionID, compactArchiveEventType); len(archives) != 0 {
		t.Fatalf("inferred auto compaction archived %d events, want none", len(archives))
	}
}

// TestSpillEventRecorded covers the structured spill audit of an oversized tool
// result.
func TestSpillEventRecorded(t *testing.T) {
	big := &bigResultTool{name: "read_file", size: spillThresholdBytes + 1024}
	svc := newTestService(t, Config{}, newFakeSettings(map[string]string{contracts.SettingsChatModel: "spill-model"}), nil, big)
	provider := &twoCallUsageProvider{
		first:  rig.Usage{PromptTokens: 100, CompletionTokens: 5, TotalTokens: 105},
		second: &rig.Usage{PromptTokens: 200, CompletionTokens: 9, TotalTokens: 209},
	}
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	sessionID, err := svc.NewSession("spill")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "read the big file"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	assistant := session.Messages[len(session.Messages)-1]
	if len(assistant.Tools) != 1 || !strings.HasPrefix(assistant.Tools[0].Summary, rig.SpillSummaryPrefix) {
		t.Fatalf("tool summary = %+v, want the spill summary prefix", assistant.Tools)
	}

	events := eventsOfType(t, svc.store.(*fakeStore), sessionID, spillEvent)
	if len(events) != 1 {
		t.Fatalf("spill events = %d, want 1", len(events))
	}
	var payload spillPayload
	decodeEvent(t, events[0], &payload)
	if payload.CallID != "call-1" || payload.Tool != "read_file" {
		t.Fatalf("spill payload = %+v, want the call id/name", payload)
	}
	if payload.Path == "" || payload.Bytes <= spillThresholdBytes {
		t.Fatalf("spill payload = %+v, want the path and real byte count", payload)
	}
	folded, ok := svc.SessionContext(sessionID)
	if !ok || len(folded.Events) != 1 || folded.Events[0].Kind != contracts.ContextEventSpill ||
		folded.Events[0].Path != payload.Path || folded.Events[0].Bytes != payload.Bytes {
		t.Fatalf("folded spill = %+v (ok=%v), want path/bytes", folded.Events, ok)
	}
}

// TestParseSpillSummary pins the parser against Rig's stable summary format.
func TestParseSpillSummary(t *testing.T) {
	content := rig.SpillSummaryPrefix + "\npath: /tmp/spill/read_file-1.txt\nbytes: 40000\npreview: hello world"
	parsed, ok := parseSpillSummary(content)
	if !ok {
		t.Fatal("parseSpillSummary rejected a Rig spill summary")
	}
	if parsed.Path != "/tmp/spill/read_file-1.txt" || parsed.Bytes != 40000 || parsed.Preview != "hello world" {
		t.Fatalf("parsed spill = %+v, want path/bytes/preview", parsed)
	}
	if _, ok := parseSpillSummary("plain tool output"); ok {
		t.Fatal("parseSpillSummary accepted a non-spilled result")
	}
}

// TestContextInspectorSurvivesRestart covers the durable fold: a restart over the
// same store rebuilds the snapshot/requests/events timeline.
func TestContextInspectorSurvivesRestart(t *testing.T) {
	store := newFakeStore()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "restart-model"})
	first := newQueueService(t, store, settings, &usageOnlyProvider{usage: rig.Usage{
		PromptTokens: 100, CompletionTokens: 7, TotalTokens: 107,
	}})
	sessionID, err := first.NewSession("restart context")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := first.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, first, sessionID)

	second := newQueueService(t, store, settings, &captureProvider{})
	second.restore(context.Background())
	folded, ok := second.SessionContext(sessionID)
	if !ok {
		t.Fatal("SessionContext after restart reported no data")
	}
	if folded.Snapshot.Model != "restart-model" {
		t.Fatalf("restored snapshot model = %q, want restart-model", folded.Snapshot.Model)
	}
	if len(folded.Requests) != 1 {
		t.Fatalf("restored requests = %d, want 1", len(folded.Requests))
	}
	if folded.Requests[0].ContextTokens != 100 || folded.Requests[0].Completion != 7 {
		t.Fatalf("restored request = %+v, want the captured usage", folded.Requests[0])
	}
	if folded.Used != 100 || folded.Window != 0 {
		t.Fatalf("restored used/window = %d/%d, want 100/0", folded.Used, folded.Window)
	}
}

// TestContextInspectorCost pins the price-based cost estimate exposed by the
// inspector and the model-catalog price fields.
func TestContextInspectorCost(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel: "priced-model",
		contracts.SettingModelCatalogModels: `[{"id":"priced-model","contextWindow":128000,` +
			`"inputPricePerMillion":2,"cachedInputPricePerMillion":1,"outputPricePerMillion":8}]`,
	})
	store := newFakeStore()
	svc := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, store, fakeApprovals{})
	provider := &twoCallUsageProvider{
		first: rig.Usage{PromptTokens: 100000, CachedTokens: 10000, CompletionTokens: 1000, TotalTokens: 101000},
	}
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	sessionID, err := svc.NewSession("cost")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "cost"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)

	folded, ok := svc.SessionContext(sessionID)
	if !ok || len(folded.Requests) == 0 {
		t.Fatalf("inspector requests = %+v (ok=%v)", folded.Requests, ok)
	}
	request := folded.Requests[0]
	if !request.CostKnown {
		t.Fatal("request cost is unknown for a priced model")
	}
	// uncached 90000 * 2/1M + cached 10000 * 1/1M + completion 1000 * 8/1M.
	want := 0.18 + 0.01 + 0.008
	if math.Abs(request.CostUSD-want) > 1e-9 {
		t.Fatalf("request cost = %v, want %v", request.CostUSD, want)
	}
	if !folded.CostKnown || math.Abs(folded.TotalCostUSD-want) > 1e-9 {
		t.Fatalf("total cost = %v (known=%v), want %v", folded.TotalCostUSD, folded.CostKnown, want)
	}
	if folded.Window != 128000 {
		t.Fatalf("inspector window = %d, want 128000", folded.Window)
	}
}

// TestContextInspectorUnknownAndDeletedSession covers the empty state.
func TestContextInspectorUnknownAndDeletedSession(t *testing.T) {
	svc := newQueueService(t, newFakeStore(), nil, &captureProvider{})
	if _, ok := svc.SessionContext("sess-missing"); ok {
		t.Fatal("SessionContext for an unknown session reported data")
	}
	sessionID, err := svc.NewSession("deleted context")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)
	if _, ok := svc.SessionContext(sessionID); !ok {
		t.Fatal("SessionContext before delete reported no data")
	}
	if err := svc.DeleteSession(sessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, ok := svc.SessionContext(sessionID); ok {
		t.Fatal("SessionContext still reports data for a deleted session")
	}
}

// usageOnlyProvider answers one call and reports usage.
type usageOnlyProvider struct{ usage rig.Usage }

func (p *usageOnlyProvider) Name() string { return "usage-only" }

func (p *usageOnlyProvider) Stream(context.Context, rig.Request) (rig.Stream, error) {
	usage := p.usage
	return &scriptedStream{chunks: []rig.Chunk{{Delta: "ok"}, {Usage: &usage}, {FinishReason: "stop"}}}, nil
}

// bigResultTool returns a large deterministic payload for the spill test.
type bigResultTool struct {
	name string
	size int
}

func (t *bigResultTool) Name() string           { return t.name }
func (t *bigResultTool) Description() string    { return "Return a large result." }
func (t *bigResultTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *bigResultTool) Invoke(_ context.Context, _ rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{Content: strings.Repeat("x", t.size)}, nil
}
