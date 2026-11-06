package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// advisoryBinderTool is a fake external event source: it records the sink the
// runtime installs and can emit one notice from inside Invoke.
type advisoryBinderTool struct {
	name string

	mu   sync.Mutex
	sink AdvisorySink
}

func (t *advisoryBinderTool) Name() string        { return t.name }
func (t *advisoryBinderTool) Description() string { return "Fake external event source." }
func (t *advisoryBinderTool) Invoke(context.Context, json.RawMessage) (string, error) {
	t.emit("external event finished")
	return "ok", nil
}

func (t *advisoryBinderTool) SetAdvisorySink(sink AdvisorySink) {
	t.mu.Lock()
	t.sink = sink
	t.mu.Unlock()
}

func (t *advisoryBinderTool) emit(text string) {
	t.mu.Lock()
	sink := t.sink
	t.mu.Unlock()
	if sink != nil {
		sink(text)
	}
}

func (t *advisoryBinderTool) bound() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sink != nil
}

// TestAdvisorySinkBindsRegisteredTools pins the binding half of FP-15: New
// hands the Agent's external-notice sink to every registered binder tool.
func TestAdvisorySinkBindsRegisteredTools(t *testing.T) {
	tool := &advisoryBinderTool{name: "notifier"}
	reg := NewRegistry()
	if err := reg.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	agent := newTestAgent(t, Config{
		einoModel: newScriptModel(func(int) ([]*schema.Message, error) { return finalText("ok"), nil }),
		Tools:     reg,
	})
	if !tool.bound() {
		t.Fatal("registered advisory-binder tool never received the Agent sink")
	}
	// The sink routes to the same Agent (a notice enqueued now waits for a turn).
	tool.emit("before turn")
	if got := agent.takePendingAdvisories(); len(got) != 1 || got[0] != "before turn" {
		t.Fatalf("pending advisories = %v, want the emitted notice", got)
	}
}

// TestExternalAdvisoryQueuedForNextTurn pins that a notice produced while no
// turn is running reaches the next turn's prompt.
func TestExternalAdvisoryQueuedForNextTurn(t *testing.T) {
	provider := &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
		return []Chunk{{Text: "ok", Done: true}}, nil
	}}
	agent := newTestAgent(t, Config{Provider: provider})
	agent.enqueueExternalAdvisory("Background job job-7 finished: done")
	events := runTurn(t, agent, "session-pending-advisory", "hello")
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("turn error: %q", errText)
	}
	msgs, ok := provider.requestAt(0)
	if !ok {
		t.Fatal("provider received no request")
	}
	if !strings.Contains(msgs.Messages[len(msgs.Messages)-1].Content, "[harness advisory] Background job job-7 finished") {
		t.Fatalf("first request prompt = %+v, want the queued advisory appended", messageContents(msgs.Messages))
	}
	if got := agent.takePendingAdvisories(); len(got) != 0 {
		t.Fatalf("pending advisories were not drained: %v", got)
	}
}

// TestExternalAdvisoryInjectedIntoActiveTurn pins the in-turn path: a notice
// emitted while a turn is running rides the next provider request.
func TestExternalAdvisoryInjectedIntoActiveTurn(t *testing.T) {
	tool := &advisoryBinderTool{name: "notifier"}
	reg := NewRegistry()
	if err := reg.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	p := &fakeProvider{fn: func(_ context.Context, req Request) ([]Chunk, error) {
		// First request asks for the tool; the second sees its result.
		if len(req.Messages) <= 2 {
			return toolCallChunk("call-1", "notifier"), nil
		}
		return []Chunk{{Text: "done", Done: true}}, nil
	}}
	agent := newTestAgent(t, Config{Provider: p, Tools: reg})
	events := runTurn(t, agent, "session-active-advisory", "run the notifier")
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("turn error: %q", errText)
	}
	second, ok := p.requestAt(1)
	if !ok {
		t.Fatal("provider received no follow-up request")
	}
	joined := strings.Join(messageContents(second.Messages), "|")
	if !strings.Contains(joined, "[harness advisory] external event finished") {
		t.Fatalf("follow-up request = %s, want the in-turn advisory", joined)
	}
}

// TestActiveTurnClearedAfterTurn pins that a notice arriving after the turn
// ended is queued for the next turn instead of being dropped or touching a
// closed turn.
func TestActiveTurnClearedAfterTurn(t *testing.T) {
	agent := newTestAgent(t, Config{Provider: &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
		return []Chunk{{Text: "ok", Done: true}}, nil
	}}})
	runTurn(t, agent, "session-clear-active", "hello")
	if agent.activeTurn.Load() != nil {
		t.Fatal("activeTurn was not cleared at the end of the turn")
	}
	agent.enqueueExternalAdvisory("late notice")
	if got := agent.takePendingAdvisories(); len(got) != 1 || got[0] != "late notice" {
		t.Fatalf("late notice pending = %v, want it queued for the next turn", got)
	}
}
