package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// saveTestCheckpoint stores cp for sessionID in the in-memory test store.
func saveTestCheckpoint(t *testing.T, store *memoryCheckpointStore, sessionID string, cp *Checkpoint) {
	t.Helper()
	if err := store.Save(context.Background(), sessionID, cp); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
}

func interruptedTestCheckpoint(sessionID, phase string, history []Message) *Checkpoint {
	return &Checkpoint{
		History: history,
		TurnID:  "turn-" + sessionID,
		Phase:   phase,
	}
}

// TestRecoverInterruptedTurnRequestedPhase covers a crash while the model
// request was in flight: the conversation tail is the user prompt, so recovery
// appends only an assistant anchor (never a second user message).
func TestRecoverInterruptedTurnRequestedPhase(t *testing.T) {
	store := newMemoryCheckpointStore()
	original := interruptedTestCheckpoint("s-requested", CheckpointPhaseRequested, []Message{
		{Role: RoleUser, Content: "fix the failing test"},
	})
	saveTestCheckpoint(t, store, "s-requested", original)

	repair, err := RecoverInterruptedTurn(context.Background(), store, "s-requested")
	if err != nil {
		t.Fatalf("RecoverInterruptedTurn: %v", err)
	}
	if repair == nil {
		t.Fatal("requested checkpoint was not repaired")
	}
	if repair.SessionID != "s-requested" || repair.TurnID != "turn-s-requested" || repair.Phase != CheckpointPhaseRequested {
		t.Fatalf("repair identity = %+v", repair)
	}
	// Exactly one appended assistant marker; no user message is ever appended.
	if len(repair.Appended) != 1 {
		t.Fatalf("appended = %+v, want one assistant marker", repair.Appended)
	}
	marker := repair.Appended[0]
	if marker.Role != RoleAssistant || !strings.Contains(strings.ToLower(marker.Content), "interrupt") {
		t.Fatalf("marker = %+v, want an assistant message naming the interruption", marker)
	}
	// The repaired history keeps the prompt exactly once and ends on the marker.
	if got := len(repair.History); got != 2 {
		t.Fatalf("repaired history = %+v, want [user marker]", repair.History)
	}
	userMessages := 0
	for _, msg := range repair.History {
		if msg.Role == RoleUser {
			userMessages++
		}
	}
	if userMessages != 1 {
		t.Fatalf("repaired history has %d user messages, want exactly 1: %+v", userMessages, repair.History)
	}
	if last := repair.History[len(repair.History)-1]; last.Role != RoleAssistant || last.Content == "" {
		t.Fatalf("repaired history does not end on the assistant marker: %+v", repair.History)
	}
	// The input checkpoint is not mutated by recovery.
	if len(original.History) != 1 || original.History[0].Role != RoleUser {
		t.Fatalf("recovery mutated the checkpoint: %+v", original.History)
	}
	// A second read of the same checkpoint is byte-for-byte identical.
	again, err := RecoverInterruptedTurn(context.Background(), store, "s-requested")
	if err != nil {
		t.Fatalf("second RecoverInterruptedTurn: %v", err)
	}
	if !reflect.DeepEqual(repair, again) {
		t.Fatalf("recovery is not deterministic:\nfirst = %+v\nsecond = %+v", repair, again)
	}
}

// TestRecoverInterruptedTurnToolPendingAddsResults covers a crash after the
// assistant's tool call was recorded but before the tool returned: the next
// request needs a real result for that call id, and the outcome must be marked
// unknown (never replayed).
func TestRecoverInterruptedTurnToolPendingAddsResults(t *testing.T) {
	store := newMemoryCheckpointStore()
	history := []Message{
		{Role: RoleUser, Content: "delete the temp file"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{
			{ID: "call-1", Name: "bash", Arguments: json.RawMessage(`{"command":"rm -rf /tmp/zeokeel-test"}`)},
		}},
	}
	saveTestCheckpoint(t, store, "s-tool", interruptedTestCheckpoint("s-tool", CheckpointPhaseToolPending, history))

	repair, err := RecoverInterruptedTurn(context.Background(), store, "s-tool")
	if err != nil {
		t.Fatalf("RecoverInterruptedTurn: %v", err)
	}
	if repair == nil {
		t.Fatal("tool_pending checkpoint was not repaired")
	}
	if len(repair.Appended) != 2 {
		t.Fatalf("appended = %+v, want [tool result marker]", repair.Appended)
	}
	toolResult := repair.Appended[0]
	if toolResult.Role != RoleTool || toolResult.ToolCallID != "call-1" || toolResult.ToolName != "bash" {
		t.Fatalf("synthetic tool result = %+v", toolResult)
	}
	if strings.TrimSpace(toolResult.Content) == "" {
		t.Fatal("synthetic tool result is empty; the next provider request would reject a dangling call")
	}
	if !strings.Contains(strings.ToLower(toolResult.Content), "not replayed") {
		t.Fatalf("synthetic tool result does not say the call was not replayed: %q", toolResult.Content)
	}
	if marker := repair.Appended[1]; marker.Role != RoleAssistant || marker.Content == "" {
		t.Fatalf("appended marker = %+v", marker)
	}
	// History is the checkpoint tail plus the closers, in protocol order:
	// user -> assistant(tool call) -> tool(result) -> assistant(marker).
	wantRoles := []Role{RoleUser, RoleAssistant, RoleTool, RoleAssistant}
	if len(repair.History) != len(wantRoles) {
		t.Fatalf("repaired history roles = %v, want %v", messageRoles(repair.History), wantRoles)
	}
	for i, want := range wantRoles {
		if repair.History[i].Role != want {
			t.Fatalf("repaired history roles = %v, want %v", messageRoles(repair.History), wantRoles)
		}
	}
	for _, msg := range repair.Appended {
		if msg.Role == RoleUser {
			t.Fatalf("recovery appended a duplicate user message: %+v", msg)
		}
	}
}

// TestRecoverInterruptedTurnKeepsAnsweredToolResults covers a requested-phase
// barrier written after a completed tool round trip: the existing result is
// preserved and only the assistant anchor is added.
func TestRecoverInterruptedTurnKeepsAnsweredToolResults(t *testing.T) {
	store := newMemoryCheckpointStore()
	history := []Message{
		{Role: RoleUser, Content: "read the file"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.txt"}`)}}},
		{Role: RoleTool, ToolCallID: "call-1", ToolName: "read_file", Content: "file body"},
	}
	saveTestCheckpoint(t, store, "s-answered", interruptedTestCheckpoint("s-answered", CheckpointPhaseRequested, history))

	repair, err := RecoverInterruptedTurn(context.Background(), store, "s-answered")
	if err != nil {
		t.Fatalf("RecoverInterruptedTurn: %v", err)
	}
	if repair == nil {
		t.Fatal("requested checkpoint was not repaired")
	}
	if len(repair.Appended) != 1 || repair.Appended[0].Role != RoleAssistant {
		t.Fatalf("appended = %+v, want only the assistant marker", repair.Appended)
	}
	if len(repair.History) != 4 || repair.History[2].ToolCallID != "call-1" {
		t.Fatalf("repaired history = %+v, want the answered call preserved", repair.History)
	}
}

// TestRecoverInterruptedTurnClosesEveryParallelToolCall covers parallel tool
// calls: a request barrier after two dispatched calls must fill both results.
func TestRecoverInterruptedTurnClosesEveryParallelToolCall(t *testing.T) {
	store := newMemoryCheckpointStore()
	history := []Message{
		{Role: RoleUser, Content: "run both"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{
			{ID: "call-a", Name: "bash", Arguments: json.RawMessage(`{}`)},
			{ID: "call-b", Name: "write_file", Arguments: json.RawMessage(`{}`)},
		}},
	}
	saveTestCheckpoint(t, store, "s-parallel", interruptedTestCheckpoint("s-parallel", CheckpointPhaseToolPending, history))

	repair, err := RecoverInterruptedTurn(context.Background(), store, "s-parallel")
	if err != nil {
		t.Fatalf("RecoverInterruptedTurn: %v", err)
	}
	if repair == nil {
		t.Fatal("tool_pending checkpoint was not repaired")
	}
	var results []string
	for _, msg := range repair.Appended {
		if msg.Role == RoleTool {
			results = append(results, msg.ToolCallID)
		}
	}
	if len(results) != 2 || results[0] != "call-a" || results[1] != "call-b" {
		t.Fatalf("synthetic tool results = %v, want [call-a call-b] in call order", results)
	}
}

// TestRecoverInterruptedTurnNoOp covers the states that need no repair.
func TestRecoverInterruptedTurnNoOp(t *testing.T) {
	cases := []struct {
		name    string
		phase   string
		hasCP   bool
		history []Message
	}{
		{name: "completed", phase: CheckpointPhaseCompleted, hasCP: true, history: []Message{{Role: RoleUser, Content: "done"}}},
		{name: "empty phase", phase: "", hasCP: true, history: []Message{{Role: RoleUser, Content: "x"}}},
		{name: "unknown phase", phase: "weird", hasCP: true, history: []Message{{Role: RoleUser, Content: "x"}}},
		{name: "no checkpoint", hasCP: false},
		{name: "empty history", phase: CheckpointPhaseRequested, hasCP: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemoryCheckpointStore()
			if tc.hasCP {
				saveTestCheckpoint(t, store, "s-noop", interruptedTestCheckpoint("s-noop", tc.phase, tc.history))
			}
			repair, err := RecoverInterruptedTurn(context.Background(), store, "s-noop")
			if err != nil {
				t.Fatalf("RecoverInterruptedTurn: %v", err)
			}
			if repair != nil {
				t.Fatalf("repair = %+v, want a no-op", repair)
			}
		})
	}
}

// TestRecoverInterruptedTurnTolerantAndIdempotent covers the error and
// idempotency contract: an unreadable checkpoint is tolerated (no repair, no
// error), a cancelled context is reported, and persisting RepairedCheckpoint
// makes the next recovery a no-op.
func TestRecoverInterruptedTurnTolerantAndIdempotent(t *testing.T) {
	t.Run("unreadable checkpoint tolerated", func(t *testing.T) {
		store := newMemoryCheckpointStore()
		store.loadErr = errors.New("corrupted checkpoint payload")
		repair, err := RecoverInterruptedTurn(context.Background(), store, "s-corrupt")
		if err != nil {
			t.Fatalf("corrupt checkpoint must not fail recovery: %v", err)
		}
		if repair != nil {
			t.Fatalf("repair = %+v, want no-op for a corrupt checkpoint", repair)
		}
	})
	t.Run("nil store", func(t *testing.T) {
		repair, err := RecoverInterruptedTurn(context.Background(), nil, "s-nil")
		if err != nil || repair != nil {
			t.Fatalf("nil store = (%+v, %v), want (nil, nil)", repair, err)
		}
	})
	t.Run("canceled context", func(t *testing.T) {
		store := newMemoryCheckpointStore()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := RecoverInterruptedTurn(ctx, store, "s-canceled"); err == nil {
			t.Fatal("cancelled recovery returned no error")
		}
	})
	t.Run("persisted repair is a no-op", func(t *testing.T) {
		store := newMemoryCheckpointStore()
		history := []Message{
			{Role: RoleUser, Content: "do it"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call-1", Name: "bash", Arguments: json.RawMessage(`{}`)}}},
		}
		saveTestCheckpoint(t, store, "s-idem", interruptedTestCheckpoint("s-idem", CheckpointPhaseToolPending, history))
		repair, err := RecoverInterruptedTurn(context.Background(), store, "s-idem")
		if err != nil || repair == nil {
			t.Fatalf("first recovery = (%+v, %v)", repair, err)
		}
		repaired := repair.RepairedCheckpoint()
		if repaired == nil || repaired.Phase != CheckpointPhaseCompleted || !reflect.DeepEqual(repaired.History, repair.History) {
			t.Fatalf("RepairedCheckpoint = %+v", repaired)
		}
		if repaired.TurnID != "turn-s-idem" {
			t.Fatalf("RepairedCheckpoint turn id = %q", repaired.TurnID)
		}
		if err := store.Save(context.Background(), "s-idem", repaired); err != nil {
			t.Fatalf("save repaired checkpoint: %v", err)
		}
		again, err := RecoverInterruptedTurn(context.Background(), store, "s-idem")
		if err != nil {
			t.Fatalf("recovery after repair: %v", err)
		}
		if again != nil {
			t.Fatalf("second recovery = %+v, want a no-op after the completed checkpoint was persisted", again)
		}
	})
}

func messageRoles(msgs []Message) []Role {
	roles := make([]Role, len(msgs))
	for i, msg := range msgs {
		roles[i] = msg.Role
	}
	return roles
}
