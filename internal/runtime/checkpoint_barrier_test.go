package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// countingTool is a fake tool that counts invocations, so a barrier test can
// prove the tool body never ran (or ran exactly once).
type countingTool struct {
	name string
	mu   sync.Mutex
	n    int
}

func (t *countingTool) Name() string        { return t.name }
func (t *countingTool) Description() string { return "Fake side-effecting tool." }
func (t *countingTool) Invoke(context.Context, json.RawMessage) (string, error) {
	t.mu.Lock()
	t.n++
	t.mu.Unlock()
	return "ok", nil
}

func (t *countingTool) calls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.n
}

// newBarrierAgent builds an agent whose model asks for toolName on the first
// call and then answers, with a real checkpoint store attached.
func newBarrierAgent(t *testing.T, store CheckpointStore, toolName string) (*Agent, *countingTool) {
	t.Helper()
	tool := &countingTool{name: toolName}
	reg := NewRegistry()
	if err := reg.Register(tool); err != nil {
		t.Fatalf("register %s: %v", toolName, err)
	}
	model := newScriptModel(func(call int) ([]*schema.Message, error) {
		if call == 0 {
			return toolCall(toolName, "{}"), nil
		}
		return finalText("done"), nil
	})
	agent := newTestAgent(t, Config{
		einoModel:       model,
		Tools:           reg,
		CheckpointStore: store,
		MaxIterations:   4,
	})
	return agent, tool
}

// TestToolBarrierCheckpointRunsBeforeSideEffect pins FP-11's tool barrier: a
// checkpoint with the tool_pending phase is written after the assistant's tool
// call is recorded but before the tool body runs, and the completed checkpoint
// still follows.
func TestToolBarrierCheckpointRunsBeforeSideEffect(t *testing.T) {
	store := newMemoryCheckpointStore()
	agent, tool := newBarrierAgent(t, store, "write_file")
	events := runTurn(t, agent, "session-tool-barrier", "please write the file")
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("turn error: %q", errText)
	}
	if tool.calls() != 1 {
		t.Fatalf("tool calls = %d, want 1", tool.calls())
	}
	phases := store.savedPhases()
	// The scripted eino model bypasses providerChatModel (so no request
	// barrier here); the tool barrier and completed checkpoint still happen.
	if len(phases) != 2 || phases[0] != CheckpointPhaseToolPending || phases[1] != CheckpointPhaseCompleted {
		t.Fatalf("saved phases = %v, want [tool_pending completed]", phases)
	}
	toolCPs := store.checkpointsAt(CheckpointPhaseToolPending)
	if len(toolCPs) != 1 {
		t.Fatalf("tool_pending checkpoints = %d, want 1", len(toolCPs))
	}
	// The barrier snapshot contains the user message and the assistant tool
	// call, but not the tool result (the body had not run yet).
	contents := messageContents(toolCPs[0].History)
	joined := strings.Join(contents, "|")
	if !strings.Contains(joined, "user:please write the file") {
		t.Fatalf("tool barrier history = %v, want the user message", contents)
	}
	if strings.Contains(joined, "tool:") {
		t.Fatalf("tool barrier history unexpectedly contains a tool result: %v", contents)
	}
	if cp := store.get("session-tool-barrier"); cp == nil || cp.Phase != CheckpointPhaseCompleted {
		t.Fatalf("final checkpoint = %+v, want completed", cp)
	}
}

// TestModelBarrierCheckpointRunsBeforeProvider pins the model barrier ordering:
// when the provider is invoked the requested-phase checkpoint is already saved.
func TestModelBarrierCheckpointRunsBeforeProvider(t *testing.T) {
	store := newMemoryCheckpointStore()
	provider := &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
		phases := store.savedPhases()
		if len(phases) == 0 || phases[len(phases)-1] != CheckpointPhaseRequested {
			t.Errorf("provider called before the request checkpoint was saved: %v", phases)
		}
		return []Chunk{{Text: "hi", Done: true}}, nil
	}}
	agent := newTestAgent(t, Config{Provider: provider, CheckpointStore: store})
	events := runTurn(t, agent, "session-model-barrier", "hello")
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("turn error: %q", errText)
	}
	phases := store.savedPhases()
	if len(phases) != 2 || phases[0] != CheckpointPhaseRequested || phases[1] != CheckpointPhaseCompleted {
		t.Fatalf("saved phases = %v, want [requested completed]", phases)
	}
	requested := store.checkpointsAt(CheckpointPhaseRequested)
	if len(requested) != 1 {
		t.Fatalf("requested checkpoints = %d, want 1", len(requested))
	}
	if got := messageContents(requested[0].History); len(got) != 1 || got[0] != "user:hello" {
		t.Fatalf("request barrier history = %v, want the user prompt", got)
	}
}

// TestBarrierCheckpointFailureBlocksModelRequest pins fail-closed: a checkpoint
// failure stops the model request before the provider is called.
func TestBarrierCheckpointFailureBlocksModelRequest(t *testing.T) {
	store := newMemoryCheckpointStore()
	store.saveErr = errors.New("checkpoint disk full")
	calls := 0
	provider := &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
		calls++
		return []Chunk{{Text: "should not run", Done: true}}, nil
	}}
	agent := newTestAgent(t, Config{Provider: provider, CheckpointStore: store})
	events := runTurn(t, agent, "session-barrier-fail", "hello")
	errText := findErrorText(events)
	if !strings.Contains(errText, "save checkpoint before model request") || !strings.Contains(errText, "checkpoint disk full") {
		t.Fatalf("error = %q, want the barrier failure", errText)
	}
	if calls != 0 {
		t.Fatalf("provider calls = %d, want 0 (the request must be blocked)", calls)
	}
}

// TestBarrierCheckpointFailureBlocksToolBody pins fail-closed on the tool
// barrier: the tool body never runs when its pre-execution checkpoint fails.
func TestBarrierCheckpointFailureBlocksToolBody(t *testing.T) {
	store := newMemoryCheckpointStore()
	store.failPhase = CheckpointPhaseToolPending
	agent, tool := newBarrierAgent(t, store, "write_file")
	events := runTurn(t, agent, "session-tool-barrier-fail", "write it")
	errText := findErrorText(events)
	if !strings.Contains(errText, "save checkpoint before tool") {
		t.Fatalf("error = %q, want the tool barrier failure", errText)
	}
	if tool.calls() != 0 {
		t.Fatalf("tool calls = %d, want 0 (the body must not run)", tool.calls())
	}
}

// TestRecoveryFromBarrierCheckpoint pins the recovery path: a checkpoint saved
// by a barrier phase still seeds the next agent (the phase only labels how far
// the interrupted turn got).
func TestRecoveryFromBarrierCheckpoint(t *testing.T) {
	store := newMemoryCheckpointStore()
	store.data["session-recover-barrier"] = &Checkpoint{
		History: []Message{
			{Role: RoleUser, Content: "recover me"},
			{Role: RoleAssistant, Content: "recovered answer"},
		},
		TurnID: "crashed",
		Phase:  CheckpointPhaseRequested,
	}
	provider := &fakeProvider{fn: func(context.Context, Request) ([]Chunk, error) {
		return []Chunk{{Text: "ok", Done: true}}, nil
	}}
	agent := newTestAgent(t, Config{Provider: provider, CheckpointStore: store})
	events := runTurn(t, agent, "session-recover-barrier", "continue")
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("turn error: %q", errText)
	}
	got := providerMessages(t, provider, 0)
	want := []string{"user:recover me", "assistant:recovered answer", "user:continue"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("provider messages = %v, want the barrier checkpoint history seeded", got)
	}
}

// TestReadOnlyToolHasNoToolBarrier pins the minimization: a pure read tool does
// not pay a pre-execution checkpoint (only the model barriers remain).
func TestReadOnlyToolHasNoToolBarrier(t *testing.T) {
	store := newMemoryCheckpointStore()
	agent, tool := newBarrierAgent(t, store, "read_file")
	events := runTurn(t, agent, "session-read-barrier", "read it")
	if errText := findErrorText(events); errText != "" {
		t.Fatalf("turn error: %q", errText)
	}
	if tool.calls() != 1 {
		t.Fatalf("read tool calls = %d, want 1", tool.calls())
	}
	for _, phase := range store.savedPhases() {
		if phase == CheckpointPhaseToolPending {
			t.Fatalf("read-only tool wrote a tool_pending checkpoint: %v", store.savedPhases())
		}
	}
}
