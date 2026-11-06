package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/store"
)

// Startup crash recovery is wired through the session Controller, so every app
// start path that builds services through BuildServicesWith — the desktop
// startServices, RunHeadless and NewHeadlessRuntime — gets it. These tests pin
// that shared startup contract with the production storeHistory and
// storeCheckpoint adapters: a session left running by a dead process is
// repaired when it is reopened (desktop) or first submitted to (headless), the
// next provider request has no duplicated user message, a dispatched tool is
// closed with an explicit unknown-outcome result and never replayed.

// countingStartupTool proves a recovered tool call is not executed again.
type countingStartupTool struct {
	mu    sync.Mutex
	calls int
}

func (t *countingStartupTool) Name() string        { return "side_effect" }
func (t *countingStartupTool) Description() string { return "Side-effecting fake tool." }
func (t *countingStartupTool) Invoke(context.Context, json.RawMessage) (string, error) {
	t.mu.Lock()
	t.calls++
	t.mu.Unlock()
	return "executed", nil
}

func (t *countingStartupTool) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls
}

// seedCrashedStore writes a session that looks like a process died mid-turn:
// the prompt is persisted, the session row is left running, and the checkpoint
// records the interrupted phase. It returns the session and user message ids.
func seedCrashedStore(t *testing.T, dir, prompt, turnID, phase string, history []runtime.Message, toolCalls ...store.ToolCall) (string, string) {
	t.Helper()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	workspace, err := st.CreateWorkspace("test", dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	crashed, err := st.CreateSession(workspace.ID, "crashed session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	user, err := st.AppendMessage(store.Message{
		SessionID: crashed.ID,
		Role:      "user",
		Content:   prompt,
		TurnID:    turnID,
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	raw, err := json.Marshal(history)
	if err != nil {
		t.Fatalf("encode checkpoint history: %v", err)
	}
	if err := st.SaveCheckpoint(store.Checkpoint{
		SessionID: crashed.ID,
		History:   raw,
		TurnID:    turnID,
		Phase:     phase,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}
	for _, call := range toolCalls {
		if err := st.RecordToolCall(call); err != nil {
			t.Fatalf("RecordToolCall: %v", err)
		}
	}
	if _, err := st.UpdateSessionStatus(crashed.ID, store.SessionRunning); err != nil {
		t.Fatalf("UpdateSessionStatus: %v", err)
	}
	return crashed.ID, user.ID
}

// newStartupRecoveryController builds services exactly like the app start
// paths do, with the production history/checkpoint adapters and a scripted
// provider instead of a network model.
func newStartupRecoveryController(t *testing.T, dir string, provider runtime.Provider, extraTool runtime.Tool) *session.Controller {
	t.Helper()
	ctrl, cleanup, err := BuildServicesWith(dir, func(st *store.Store) (*AgentBundle, error) {
		registry := runtime.NewRegistry()
		if extraTool != nil {
			if err := registry.Register(extraTool); err != nil {
				return nil, err
			}
		}
		agent, err := runtime.New(runtime.Config{
			Provider:        provider,
			Tools:           registry,
			Instruction:     "startup recovery test",
			History:         &storeHistory{st: st},
			CheckpointStore: &storeCheckpoint{st: st},
		})
		if err != nil {
			return nil, err
		}
		return &AgentBundle{Agent: agent}, nil
	})
	if err != nil {
		t.Fatalf("BuildServicesWith: %v", err)
	}
	t.Cleanup(func() { _ = cleanup() })
	return ctrl
}

// TestDesktopStartupRecoversInterruptedCheckpoint covers the desktop path: the
// app starts services (BuildServicesWith) and opens the last session, which was
// interrupted while the model request was in flight.
func TestDesktopStartupRecoversInterruptedCheckpoint(t *testing.T) {
	dir := t.TempDir()
	const prompt = "finish the migration"
	id, userMsgID := seedCrashedStore(t, dir, prompt, "turn-crash",
		runtime.CheckpointPhaseRequested,
		[]runtime.Message{{Role: runtime.RoleUser, Content: prompt}})
	provider := &resumeProvider{reply: "resumed"}
	ctrl := newStartupRecoveryController(t, dir, provider, nil)
	ctx := context.Background()

	snapshot, err := ctrl.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession at desktop startup: %v", err)
	}
	markers := 0
	for _, msg := range snapshot.Messages {
		if msg.Text == runtime.InterruptedTurnMarker {
			markers++
		}
	}
	if markers != 1 {
		t.Fatalf("recovered snapshot has %d interrupted markers, want 1", markers)
	}
	if got := snapshot.TurnMarks[userMsgID]; got != "interrupted" {
		t.Fatalf("turn mark = %q, want interrupted (%+v)", got, snapshot.TurnMarks)
	}
	checkpoint, err := ctrl.Store().LoadCheckpoint(id)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if checkpoint.Phase != runtime.CheckpointPhaseCompleted {
		t.Fatalf("repaired checkpoint phase = %q, want completed", checkpoint.Phase)
	}

	if err := ctrl.Submit(ctx, id, "continue the migration"); err != nil {
		t.Fatalf("Submit after recovery: %v", err)
	}
	waitWiringIdle(t, ctrl)
	request, ok := provider.requestAt(0)
	if !ok {
		t.Fatal("provider saw no request")
	}
	got := conversationMessages(request)
	want := []string{
		"user:" + prompt,
		"assistant:" + runtime.InterruptedTurnMarker,
		"user:continue the migration",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("resumed provider messages = %v, want %v", got, want)
	}
}

// TestHeadlessStartupRecoversInterruptedCheckpoint covers the headless path:
// RunHeadless/NewHeadlessRuntime build services with BuildServicesWith and then
// submit to a session directly (no UI open), so the first Submit must consume
// the interrupted checkpoint before the new turn starts.
func TestHeadlessStartupRecoversInterruptedCheckpoint(t *testing.T) {
	dir := t.TempDir()
	const prompt = "finish the migration"
	id, _ := seedCrashedStore(t, dir, prompt, "turn-headless",
		runtime.CheckpointPhaseRequested,
		[]runtime.Message{{Role: runtime.RoleUser, Content: prompt}})
	provider := &resumeProvider{reply: "resumed"}
	ctrl := newStartupRecoveryController(t, dir, provider, nil)
	ctx := context.Background()

	// No OpenSession first: this is exactly headlessRuntime.Submit on a session
	// a previous process left running.
	if err := ctrl.Submit(ctx, id, "continue the migration"); err != nil {
		t.Fatalf("Submit after a headless restart: %v", err)
	}
	waitWiringIdle(t, ctrl)
	request, ok := provider.requestAt(0)
	if !ok {
		t.Fatal("provider saw no request")
	}
	got := conversationMessages(request)
	want := []string{
		"user:" + prompt,
		"assistant:" + runtime.InterruptedTurnMarker,
		"user:continue the migration",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("headless provider messages = %v, want %v", got, want)
	}
	snapshot := ctrl.Snapshot()
	markers := 0
	for _, msg := range snapshot.Messages {
		if msg.Text == runtime.InterruptedTurnMarker {
			markers++
		}
	}
	if markers != 1 {
		t.Fatalf("snapshot has %d interrupted markers, want 1", markers)
	}
}

// TestStartupRecoveryClosesPendingToolWithoutReplay covers the tool_pending
// phase through the production history reconstruction: the next provider
// request contains a real (unknown-outcome) tool result for the dispatched
// call, and the tool body is not executed again.
func TestStartupRecoveryClosesPendingToolWithoutReplay(t *testing.T) {
	dir := t.TempDir()
	const (
		prompt = "run the side effect"
		callID = "call-crashed"
	)
	history := []runtime.Message{
		{Role: runtime.RoleUser, Content: prompt},
		{Role: runtime.RoleAssistant, ToolCalls: []runtime.ToolCall{
			{ID: callID, Name: "side_effect", Arguments: json.RawMessage(`{"value":1}`)},
		}},
	}
	id, _ := seedCrashedStore(t, dir, prompt, "turn-tool",
		runtime.CheckpointPhaseToolPending, history)
	provider := &resumeProvider{reply: "resumed"}
	tool := &countingStartupTool{}
	ctrl := newStartupRecoveryController(t, dir, provider, tool)
	ctx := context.Background()

	if err := ctrl.Submit(ctx, id, "continue"); err != nil {
		t.Fatalf("Submit after a tool-phase crash: %v", err)
	}
	waitWiringIdle(t, ctrl)
	if calls := tool.count(); calls != 0 {
		t.Fatalf("recovered tool call was replayed %d time(s), want 0", calls)
	}
	request, ok := provider.requestAt(0)
	if !ok {
		t.Fatal("provider saw no request")
	}
	toolResults := 0
	for _, msg := range request.Messages {
		if msg.Role != runtime.RoleTool {
			continue
		}
		if msg.ToolCallID != callID {
			continue
		}
		toolResults++
		if msg.Content != runtime.InterruptedToolResult {
			t.Fatalf("recovered tool result = %q, want the unknown-outcome result", msg.Content)
		}
	}
	if toolResults != 1 {
		t.Fatalf("provider request has %d results for %q, want 1: %+v", toolResults, callID, request.Messages)
	}
	markers := 0
	for _, msg := range request.Messages {
		if msg.Role == runtime.RoleAssistant && msg.Content == runtime.InterruptedTurnMarker {
			markers++
		}
	}
	if markers != 1 {
		t.Fatalf("provider request has %d interrupted markers, want 1", markers)
	}
	call, err := ctrl.Store().GetToolCall(callID)
	if err != nil {
		t.Fatalf("recovered tool row missing: %v", err)
	}
	if call.Status != "error" || call.Result != runtime.InterruptedToolResult {
		t.Fatalf("recovered tool row = %+v, want a closed error row", call)
	}
}
