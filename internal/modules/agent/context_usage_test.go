package agent

// Tests for MessageUsage.ContextTokens: the prompt-token count of the LAST model
// call of a turn (the real conversation context), versus the accumulated
// RunResult.Usage counters Rig reports for billing.

import (
	"context"
	"sync"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// twoCallUsageProvider runs one tool round: the first model call requests a tool
// and reports first usage, the second call answers and (optionally) reports
// second usage.
type twoCallUsageProvider struct {
	mu     sync.Mutex
	calls  int
	first  rig.Usage
	second *rig.Usage // nil means the second model call reports no usage
}

func (p *twoCallUsageProvider) Name() string { return "two-call-usage" }

func (p *twoCallUsageProvider) Stream(context.Context, rig.Request) (rig.Stream, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	first := p.first
	second := p.second
	p.mu.Unlock()
	if call == 1 {
		return &scriptedStream{chunks: []rig.Chunk{
			{ToolCalls: []rig.ToolCallDelta{{Index: 0, ID: "call-1", Name: "read_file", Arguments: "{}"}}},
			{Usage: &first},
			{FinishReason: "tool_calls"},
		}}, nil
	}
	chunks := []rig.Chunk{{Delta: "done"}}
	if second != nil {
		chunks = append(chunks, rig.Chunk{Usage: second})
	}
	chunks = append(chunks, rig.Chunk{FinishReason: "stop"})
	return &scriptedStream{chunks: chunks}, nil
}

func (p *twoCallUsageProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// newTwoCallUsageService builds the agent service with one readable tool so the
// scripted provider can run a tool round.
func newTwoCallUsageService(t *testing.T, provider rig.Provider) (*service, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	svc := newService(Config{}, rigtools.NewRegistry(&gateTestTool{name: "read_file"}),
		contracts.SettingsService(newFakeSettings(nil)), &fakeLogger{}, store, fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	return svc, store
}

// TestRunContextTokensIsLastModelCall is the audit fix: PromptTokens stays the
// accumulated billing total, ContextTokens is the last model call's prompt count
// (the conversation context the model actually saw).
func TestRunContextTokensIsLastModelCall(t *testing.T) {
	last := rig.Usage{PromptTokens: 340, CompletionTokens: 9, TotalTokens: 349, CachedTokens: 30}
	provider := &twoCallUsageProvider{
		first:  rig.Usage{PromptTokens: 120, CompletionTokens: 5, TotalTokens: 125},
		second: &last,
	}
	svc, store := newTwoCallUsageService(t, provider)
	id, err := svc.NewSession("context usage")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	assistant := lastAssistantMessage(t, svc, id)
	if assistant.Usage == nil {
		t.Fatalf("assistant Usage = nil, want tokens from both model calls")
	}
	got := assistant.Usage
	if got.PromptTokens != 460 || got.CompletionTokens != 14 || got.TotalTokens != 474 || got.CachedTokens != 30 {
		t.Fatalf("accumulated Usage = %+v, want prompt 460/completion 14/total 474/cached 30", got)
	}
	if got.ContextTokens != 340 {
		t.Fatalf("ContextTokens = %d, want the LAST model call's prompt tokens (340), not the accumulated 460", got.ContextTokens)
	}
	if got.Iterations < 2 {
		t.Fatalf("Iterations = %d, want >= 2 model calls", got.Iterations)
	}
	if calls := provider.callCount(); calls != 2 {
		t.Fatalf("provider calls = %d, want 2", calls)
	}

	events := usageEvents(t, store, id)
	if len(events) != 1 {
		t.Fatalf("usage events = %+v, want exactly one for the turn", events)
	}
	if got, ok := events[0]["contextTokens"].(float64); !ok || got != 340 {
		t.Fatalf("usage event contextTokens = %v, want 340: %+v", events[0]["contextTokens"], events[0])
	}
}

// TestRunContextTokensZeroWhenLastCallReportsNoPromptTokens covers the reported
// edge: the per-call value is fresh, so a last call with zero prompt tokens gives
// ContextTokens 0 even though the accumulated PromptTokens stayed non-zero.
func TestRunContextTokensZeroWhenLastCallReportsNoPromptTokens(t *testing.T) {
	last := rig.Usage{PromptTokens: 0, CompletionTokens: 9, TotalTokens: 9}
	provider := &twoCallUsageProvider{
		first:  rig.Usage{PromptTokens: 120, CompletionTokens: 5, TotalTokens: 125},
		second: &last,
	}
	svc, _ := newTwoCallUsageService(t, provider)
	id, err := svc.NewSession("context zero")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	assistant := lastAssistantMessage(t, svc, id)
	if assistant.Usage == nil {
		t.Fatal("assistant Usage = nil, want the accumulated provider tokens")
	}
	if assistant.Usage.PromptTokens != 120 {
		t.Fatalf("accumulated PromptTokens = %d, want 120", assistant.Usage.PromptTokens)
	}
	if assistant.Usage.ContextTokens != 0 {
		t.Fatalf("ContextTokens = %d, want 0 when the last call reports no prompt tokens", assistant.Usage.ContextTokens)
	}
}
