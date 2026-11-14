package agent

// Tests for context-overflow recovery: one automatic compaction + one retry of
// the same turn, no duplicated user message, no replayed tool side effects, and
// a durable context_recovery audit event.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// scriptStep is one scripted provider response.
type scriptStep struct {
	chunks []rig.Chunk
	err    error
}

// overflowScriptProvider answers normal requests from a script and every
// compaction-summary request with a fixed summary. Context overflow is returned
// as a provider error, exactly like an OpenAI-compatible backend.
type overflowScriptProvider struct {
	mu           sync.Mutex
	steps        []scriptStep
	normalCalls  int
	summaryCalls int
}

func (p *overflowScriptProvider) Name() string { return "overflow-script" }

func (p *overflowScriptProvider) Stream(_ context.Context, request rig.Request) (rig.Stream, error) {
	if isCompactionSummaryRequest(request) {
		p.mu.Lock()
		p.summaryCalls++
		p.mu.Unlock()
		return &scriptedStream{chunks: []rig.Chunk{{Delta: "SUMMARY"}}}, nil
	}
	p.mu.Lock()
	index := p.normalCalls
	p.normalCalls++
	p.mu.Unlock()
	if index >= len(p.steps) {
		return &scriptedStream{chunks: []rig.Chunk{{Delta: "ok"}, {FinishReason: "stop"}}}, nil
	}
	step := p.steps[index]
	if step.err != nil {
		return nil, step.err
	}
	return &scriptedStream{chunks: step.chunks}, nil
}

// isCompactionSummaryRequest detects the LLM compactor's summarize call.
func isCompactionSummaryRequest(request rig.Request) bool {
	for index := len(request.Messages) - 1; index >= 0; index-- {
		if request.Messages[index].Role != rig.RoleUser {
			continue
		}
		return strings.Contains(request.Messages[index].Text(), "Summarize the conversation above")
	}
	return false
}

// calls returns the normal/summary call counts.
func (p *overflowScriptProvider) calls() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.normalCalls, p.summaryCalls
}

func contextOverflowErr() error {
	return errors.New("openai: context_length_exceeded: maximum context length is 128000 tokens")
}

// recoveryEvents decodes the durable context_recovery events of one session.
func recoveryEvents(t *testing.T, store *fakeStore, sessionID string) []compactPayload {
	t.Helper()
	events := eventsOfType(t, store, sessionID, contextRecoveryEventType)
	out := make([]compactPayload, 0, len(events))
	for _, event := range events {
		var payload compactPayload
		decodeEvent(t, event, &payload)
		out = append(out, payload)
	}
	return out
}

// TestContextOverflowRecoversByCompactingAndRetrying covers the happy path: the
// first provider call overflows, the backend compacts once and retries the same
// turn successfully — with exactly one user message.
func TestContextOverflowRecoversByCompactingAndRetrying(t *testing.T) {
	provider := &overflowScriptProvider{steps: []scriptStep{
		{err: contextOverflowErr()},
		{chunks: []rig.Chunk{{Delta: "recovered answer"}, {FinishReason: "stop"}}},
	}}
	svc, store := newRecoveryService(t, provider)
	sessionID, err := svc.NewSession("overflow")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// A history long enough for the recovery compaction to summarize (keepLast
	// 6): seed 10 prior messages.
	seedRecoveryHistory(t, store, sessionID, 10)
	if err := svc.Send(sessionID, "the question"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	if users := userMessages(session); len(users) != 1 {
		t.Fatalf("user messages = %d, want exactly the original one (no duplicate on retry)", len(users))
	}
	assistant := session.Messages[len(session.Messages)-1]
	if assistant.Error != "" || !strings.Contains(assistant.Text, "recovered answer") {
		t.Fatalf("assistant = %+v, want the recovered answer without an error", assistant)
	}
	normalCalls, summaryCalls := provider.calls()
	if normalCalls != 2 || summaryCalls != 1 {
		t.Fatalf("provider calls = %d normal/%d summary, want 2/1 (overflow, summary, retry)", normalCalls, summaryCalls)
	}
	events := recoveryEvents(t, store, sessionID)
	if len(events) != 1 {
		t.Fatalf("context_recovery events = %d, want 1", len(events))
	}
	if events[0].Kind != "context-recovery" || events[0].Saved != 1 || events[0].After > events[0].Before {
		t.Fatalf("context_recovery payload = %+v, want a successful single retry", events[0])
	}
	// The recovery also replaced the history, so it must have archived the raw
	// messages first (fail-closed audit, kind=recovery).
	archives := decodeArchiveEvents(t, store, sessionID)
	if len(archives) != 1 || archives[0].Kind != "recovery" || archives[0].MessageCount == 0 {
		t.Fatalf("recovery archives = %+v, want one raw-history archive", archives)
	}
	folded, ok := svc.SessionContext(sessionID)
	if !ok {
		t.Fatal("context inspector has no timeline")
	}
	foundRecovery := false
	for _, event := range folded.Events {
		if event.Kind == contracts.ContextEventRecovery {
			foundRecovery = true
		}
	}
	if !foundRecovery {
		t.Fatalf("inspector timeline lacks the recovery event: %+v", folded.Events)
	}
}

// TestContextOverflowRetryFailsOnce covers a second overflow: no second retry,
// the turn fails with the context error kind and the audit event says failed.
func TestContextOverflowRetryFailsOnce(t *testing.T) {
	provider := &overflowScriptProvider{steps: []scriptStep{
		{err: contextOverflowErr()},
		{err: contextOverflowErr()},
	}}
	svc, store := newRecoveryService(t, provider)
	sessionID, _ := svc.NewSession("overflow twice")
	if err := svc.Send(sessionID, "the question"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	assistant := session.Messages[len(session.Messages)-1]
	if assistant.Error == "" || assistant.ErrorKind != contracts.MessageErrorKindContext {
		t.Fatalf("assistant = %+v, want a context error row", assistant)
	}
	if users := userMessages(session); len(users) != 1 {
		t.Fatalf("user messages = %d, want 1", len(users))
	}
	normalCalls, _ := provider.calls()
	if normalCalls != 2 {
		t.Fatalf("normal provider calls = %d, want 2 (initial + single retry)", normalCalls)
	}
	events := recoveryEvents(t, store, sessionID)
	if len(events) != 1 || !strings.Contains(events[0].Kind, "failed") {
		t.Fatalf("context_recovery events = %+v, want one failed outcome", events)
	}
}

// TestNonContextErrorDoesNotRetry covers the classifier boundary.
func TestNonContextErrorDoesNotRetry(t *testing.T) {
	provider := &overflowScriptProvider{steps: []scriptStep{
		{err: errors.New("provider boom: connection reset by peer")},
	}}
	svc, store := newRecoveryService(t, provider)
	sessionID, _ := svc.NewSession("other error")
	if err := svc.Send(sessionID, "the question"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	if session.Messages[len(session.Messages)-1].Error == "" {
		t.Fatal("two failed runs left no error row")
	}
	if normalCalls, summaryCalls := provider.calls(); normalCalls != 1 || summaryCalls != 0 {
		t.Fatalf("provider calls = %d/%d, want no retry/compaction for a non-context error", normalCalls, summaryCalls)
	}
	if events := recoveryEvents(t, store, sessionID); len(events) != 0 {
		t.Fatalf("context_recovery events = %+v, want none", events)
	}
}

// TestContextOverflowRetryDoesNotReplayTools covers the side-effect rule: a tool
// executed before the overflow is not called again by the retry (its recorded
// result is replayed from the durable history instead).
func TestContextOverflowRetryDoesNotReplayTools(t *testing.T) {
	tool := &echoTool{}
	provider := &overflowScriptProvider{steps: []scriptStep{
		{chunks: []rig.Chunk{
			{ToolCalls: []rig.ToolCallDelta{{Index: 0, ID: "call-1", Name: "echo", Arguments: `{"text":"hi"}`}}},
			{FinishReason: "tool_calls"},
		}},
		{err: contextOverflowErr()},
		{chunks: []rig.Chunk{{Delta: "recovered answer"}, {FinishReason: "stop"}}},
	}}
	store := newFakeStore()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	svc := newRecoveryServiceWithTools(t, store, settings, provider, tool)
	sessionID, _ := svc.NewSession("tool overflow")
	if err := svc.Send(sessionID, "use the tool"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	if got := tool.calls.Load(); got != 1 {
		t.Fatalf("echo tool calls = %d, want 1 (the retry must not replay side effects)", got)
	}
	if users := userMessages(session); len(users) != 1 {
		t.Fatalf("user messages = %d, want 1", len(users))
	}
	if assistant := session.Messages[len(session.Messages)-1]; !strings.Contains(assistant.Text, "recovered answer") {
		t.Fatalf("assistant = %+v, want the recovered answer", assistant)
	}
	// The durable history keeps exactly one tool result for the call.
	history, err := store.History().Load(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	toolMessages := 0
	for _, message := range history {
		if message.Role == rig.RoleTool {
			toolMessages++
		}
	}
	if toolMessages != 1 {
		t.Fatalf("durable tool messages = %d, want 1", toolMessages)
	}
}

// newRecoveryService builds a service with a fake durable store and a model
// configured.
func newRecoveryService(t *testing.T, provider rig.Provider) (*service, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	svc := newRecoveryServiceWithTools(t, store, settings, provider)
	return svc, store
}

func newRecoveryServiceWithTools(t *testing.T, store *fakeStore, settings *fakeSettings,
	provider rig.Provider, tools ...rig.Tool) *service {
	t.Helper()
	if store == nil {
		store = newFakeStore()
	}
	if settings == nil {
		settings = newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	}
	svc := newService(Config{}, rigtools.NewRegistry(tools...),
		contracts.SettingsService(settings), &fakeLogger{}, store, fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	return svc
}

// seedRecoveryHistory writes count alternating prior messages into the durable
// history so the recovery compaction has something to summarize.
func seedRecoveryHistory(t *testing.T, store *fakeStore, sessionID string, count int) {
	t.Helper()
	messages := make([]rig.Message, 0, count)
	for index := 0; index < count; index++ {
		role := rig.RoleUser
		if index%2 == 1 {
			role = rig.RoleAssistant
		}
		messages = append(messages, rig.NewMessage(role, strings.Repeat("x", 200)))
	}
	if err := store.ReplaceHistory(context.Background(), sessionID, messages); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
}
