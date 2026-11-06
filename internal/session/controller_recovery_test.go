package session_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// messageOnlyHistory is a minimal runtime.History over the messages table: it
// is enough to assert the next provider request after a crash recovery does not
// duplicate the crashed prompt. Tool reconstruction is covered by the
// production storeHistory adapter in internal/app.
type messageOnlyHistory struct {
	st *store.Store
}

var _ runtime.History = (*messageOnlyHistory)(nil)

func (h *messageOnlyHistory) Messages(ctx context.Context, sessionID string) ([]runtime.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	messages, err := h.st.ListMessages(sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]runtime.Message, 0, len(messages))
	for _, msg := range messages {
		switch runtime.Role(msg.Role) {
		case runtime.RoleUser, runtime.RoleAssistant:
			out = append(out, runtime.Message{Role: runtime.Role(msg.Role), Content: msg.Content})
		}
	}
	return out, nil
}

func (h *messageOnlyHistory) Append(context.Context, string, ...runtime.Message) error { return nil }

// newRecoveryHarness is newHarness with a store-backed History, so a Submit
// after a recovery sees the repaired conversation exactly like production.
func newRecoveryHarness(t *testing.T, provider runtime.Provider) *harness {
	t.Helper()
	history := &messageOnlyHistory{}
	h := newHarnessConfigured(t, provider, func(cfg *runtime.Config) {
		cfg.History = history
	})
	history.st = h.store
	return h
}

// saveCrashCheckpoint stores one non-terminal barrier checkpoint, encoded the
// way the app's storeCheckpoint adapter encodes runtime.Checkpoint.
func saveCrashCheckpoint(t *testing.T, st *store.Store, sessionID, turnID, phase string, history []runtime.Message) {
	t.Helper()
	raw, err := json.Marshal(history)
	if err != nil {
		t.Fatalf("encode checkpoint history: %v", err)
	}
	if err := st.SaveCheckpoint(store.Checkpoint{
		SessionID: sessionID,
		History:   raw,
		TurnID:    turnID,
		Phase:     phase,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}
}

// seedCrashedCheckpoint creates a session that looks like a process died
// mid-turn right after writing a barrier checkpoint: the prompt is persisted,
// the session row is left running, and the checkpoint records the turn.
func seedCrashedCheckpoint(t *testing.T, h *harness, prompt, turnID, phase string, history []runtime.Message) (sessionID, userMsgID string) {
	t.Helper()
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "crashed turn")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	user, err := h.store.AppendMessage(store.Message{
		SessionID: id,
		Role:      string(state.RoleUser),
		Content:   prompt,
		TurnID:    turnID,
	})
	if err != nil {
		t.Fatalf("AppendMessage(user): %v", err)
	}
	saveCrashCheckpoint(t, h.store, id, turnID, phase, history)
	if _, err := h.store.UpdateSessionStatus(id, store.SessionRunning); err != nil {
		t.Fatalf("UpdateSessionStatus(running): %v", err)
	}
	return id, user.ID
}

// seedOpenToolCall records a dispatched tool call the way the controller does
// at tool_start (event + running row), so the recovery has a card to close.
func seedOpenToolCall(t *testing.T, h *harness, sessionID, callID, name string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"call_id": callID, "name": name, "args": map[string]any{}})
	if err != nil {
		t.Fatalf("marshal tool_start: %v", err)
	}
	if _, err := h.store.AppendEvent(sessionID, store.Event{Type: session.EventToolStart, Payload: payload}); err != nil {
		t.Fatalf("AppendEvent(tool_start): %v", err)
	}
	if err := h.store.RecordToolCall(store.ToolCall{
		ID: callID, SessionID: sessionID, Name: name, Args: "{}",
		Status: "running", StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordToolCall: %v", err)
	}
}

// loadCheckpointHistory returns the persisted checkpoint and its decoded
// conversation.
func loadCheckpointHistory(t *testing.T, st *store.Store, sessionID string) (*store.Checkpoint, []runtime.Message) {
	t.Helper()
	checkpoint, err := st.LoadCheckpoint(sessionID)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	var history []runtime.Message
	if err := json.Unmarshal(checkpoint.History, &history); err != nil {
		t.Fatalf("decode checkpoint history: %v", err)
	}
	return checkpoint, history
}

func snapshotTextCount(snapshot *state.Snapshot, text string) int {
	count := 0
	for _, msg := range snapshot.Messages {
		if strings.TrimSpace(msg.Text) == strings.TrimSpace(text) {
			count++
		}
	}
	return count
}

func snapshotTexts(snapshot *state.Snapshot) []string {
	out := make([]string, 0, len(snapshot.Messages))
	for _, msg := range snapshot.Messages {
		out = append(out, string(msg.Role)+":"+msg.Text)
	}
	return out
}

func countRole(history []runtime.Message, role runtime.Role) int {
	count := 0
	for _, msg := range history {
		if msg.Role == role {
			count++
		}
	}
	return count
}

// TestOpenSessionRecoversRequestedCheckpoint is the crash acceptance test for a
// process that died while the model request was in flight: reopening the
// session appends one interrupted assistant anchor (visible in the transcript
// and in the repaired checkpoint), records the interrupted turn mark, and does
// not duplicate the user's prompt.
func TestOpenSessionRecoversRequestedCheckpoint(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	h := newRecoveryHarness(t, provider)
	ctx := context.Background()
	id, userID := seedCrashedCheckpoint(t, h, "finish the migration", "turn-crash",
		runtime.CheckpointPhaseRequested,
		[]runtime.Message{{Role: runtime.RoleUser, Content: "finish the migration"}})

	h.restart()
	snapshot, err := h.ctrl.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession after a requested-phase crash: %v", err)
	}
	if got := snapshotTextCount(snapshot, runtime.InterruptedTurnMarker); got != 1 {
		t.Fatalf("interrupted marker messages = %d, want 1 (messages: %v)", got, snapshotTexts(snapshot))
	}
	if got := snapshot.TurnMarks[userID]; got != state.ReasonInterrupted {
		t.Fatalf("turn mark = %q, want interrupted (%+v)", got, snapshot.TurnMarks)
	}
	if snapshot.Phase != state.PhaseIdle || snapshot.TurnReason != state.ReasonInterrupted {
		t.Fatalf("recovered snapshot = phase %q reason %q, want idle/interrupted", snapshot.Phase, snapshot.TurnReason)
	}
	sess, err := h.store.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Status != store.SessionIdle {
		t.Fatalf("persisted session status = %q, want idle", sess.Status)
	}
	checkpoint, history := loadCheckpointHistory(t, h.store, id)
	if checkpoint.Phase != runtime.CheckpointPhaseCompleted {
		t.Fatalf("repaired checkpoint phase = %q, want completed", checkpoint.Phase)
	}
	if len(history) != 2 {
		t.Fatalf("repaired checkpoint history = %+v, want [user marker]", history)
	}
	if history[0].Role != runtime.RoleUser || history[0].Content != "finish the migration" {
		t.Fatalf("repaired history lost the prompt: %+v", history[0])
	}
	if history[1].Role != runtime.RoleAssistant || history[1].Content != runtime.InterruptedTurnMarker {
		t.Fatalf("repaired history does not end on the marker: %+v", history[1])
	}
	if got := countRole(history, runtime.RoleUser); got != 1 {
		t.Fatalf("repaired history has %d user messages, want 1", got)
	}

	// Idempotent across a second restart: the completed checkpoint is a no-op
	// and the marker row is not appended twice.
	h.restart()
	if _, err := h.ctrl.OpenSession(ctx, id); err != nil {
		t.Fatalf("second OpenSession: %v", err)
	}
	messages, err := h.store.ListMessages(id)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	markers := 0
	for _, msg := range messages {
		if msg.Content == runtime.InterruptedTurnMarker {
			markers++
		}
	}
	if markers != 1 {
		t.Fatalf("marker rows after the second open = %d, want 1", markers)
	}
}

// TestOpenSessionRecoversToolPendingCheckpoint covers a crash after a
// side-effecting tool was dispatched: the reopened session closes the tool card
// with the explicit unknown-outcome result and the repaired conversation has a
// valid assistant(tool_call) -> tool(result) exchange.
func TestOpenSessionRecoversToolPendingCheckpoint(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	h := newRecoveryHarness(t, provider)
	ctx := context.Background()
	const callID = "call-crashed"
	history := []runtime.Message{
		{Role: runtime.RoleUser, Content: "delete the temp directory"},
		{Role: runtime.RoleAssistant, ToolCalls: []runtime.ToolCall{
			{ID: callID, Name: "echo", Arguments: json.RawMessage(`{"path":"/tmp/x"}`)},
		}},
	}
	id, _ := seedCrashedCheckpoint(t, h, "delete the temp directory", "turn-tool",
		runtime.CheckpointPhaseToolPending, history)
	seedOpenToolCall(t, h, id, callID, "echo")

	h.restart()
	snapshot, err := h.ctrl.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession after a tool_pending crash: %v", err)
	}
	tool, ok := snapshot.Tools[callID]
	if !ok || tool.Status != state.ToolError {
		t.Fatalf("recovered tool = %+v (ok=%v), want a closed error card", tool, ok)
	}
	if tool.Result != runtime.InterruptedToolResult {
		t.Fatalf("recovered tool result = %q, want the unknown-outcome result", tool.Result)
	}
	if got := snapshotTextCount(snapshot, runtime.InterruptedTurnMarker); got != 1 {
		t.Fatalf("interrupted marker messages = %d, want 1 (messages: %v)", got, snapshotTexts(snapshot))
	}
	call, err := h.store.GetToolCall(callID)
	if err != nil {
		t.Fatalf("GetToolCall: %v", err)
	}
	if call.Status != "error" || call.Result != runtime.InterruptedToolResult {
		t.Fatalf("persisted tool row = %+v, want a closed error row", call)
	}
	checkpoint, repaired := loadCheckpointHistory(t, h.store, id)
	if checkpoint.Phase != runtime.CheckpointPhaseCompleted {
		t.Fatalf("repaired checkpoint phase = %q, want completed", checkpoint.Phase)
	}
	wantRoles := []runtime.Role{runtime.RoleUser, runtime.RoleAssistant, runtime.RoleTool, runtime.RoleAssistant}
	if len(repaired) != len(wantRoles) {
		t.Fatalf("repaired history = %+v, want roles %v", repaired, wantRoles)
	}
	for i, want := range wantRoles {
		if repaired[i].Role != want {
			t.Fatalf("repaired history roles = %v, want %v", repaired, wantRoles)
		}
	}
	if repaired[2].ToolCallID != callID || repaired[2].Content != runtime.InterruptedToolResult {
		t.Fatalf("repaired tool result = %+v", repaired[2])
	}
	if repaired[3].Content != runtime.InterruptedTurnMarker {
		t.Fatalf("repaired history does not end on the marker: %+v", repaired[3])
	}
	// The tool call was not replayed: the row is closed exactly once.
	calls, err := h.store.ToolCallsForSession(id)
	if err != nil {
		t.Fatalf("ToolCallsForSession: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("tool rows = %+v, want exactly the one recovered call", calls)
	}
	// Idempotent second open.
	h.restart()
	if _, err := h.ctrl.OpenSession(ctx, id); err != nil {
		t.Fatalf("second OpenSession: %v", err)
	}
	messages, _ := h.store.ListMessages(id)
	markers := 0
	for _, msg := range messages {
		if msg.Content == runtime.InterruptedTurnMarker {
			markers++
		}
	}
	if markers != 1 {
		t.Fatalf("marker rows after the second open = %d, want 1", markers)
	}
}

// TestOpenSessionRecoversCheckpointToolCallWithoutRow covers the narrow crash
// window between the barrier checkpoint and the controller's tool_start write:
// the checkpoint knows the call, the tool_calls table does not, and recovery
// must still persist a closed row so the next request's history is valid.
func TestOpenSessionRecoversCheckpointToolCallWithoutRow(t *testing.T) {
	h := newRecoveryHarness(t, &fakeProvider{respond: echoRespond})
	ctx := context.Background()
	const callID = "call-no-row"
	history := []runtime.Message{
		{Role: runtime.RoleUser, Content: "write the report"},
		{Role: runtime.RoleAssistant, ToolCalls: []runtime.ToolCall{
			{ID: callID, Name: "echo", Arguments: json.RawMessage(`{"text":"hi"}`)},
		}},
	}
	id, _ := seedCrashedCheckpoint(t, h, "write the report", "turn-no-row",
		runtime.CheckpointPhaseToolPending, history)

	h.restart()
	if _, err := h.ctrl.OpenSession(ctx, id); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	call, err := h.store.GetToolCall(callID)
	if err != nil {
		t.Fatalf("recovered tool row missing: %v", err)
	}
	if call.Status != "error" || call.Result != runtime.InterruptedToolResult {
		t.Fatalf("recovered tool row = %+v", call)
	}
	if strings.TrimSpace(call.Args) == "" {
		t.Fatalf("recovered tool row lost the call arguments: %+v", call)
	}
}

// TestOpenSessionLeavesNormalTurnsAlone pins the crash-versus-normal-end
// distinction: a completed checkpoint and a turn that ended normally (idle
// session row, for example after a user Stop) are never reinterpreted as a
// crash.
func TestOpenSessionLeavesNormalTurnsAlone(t *testing.T) {
	cases := []struct {
		name   string
		phase  string
		status store.SessionStatus
	}{
		{name: "completed checkpoint", phase: runtime.CheckpointPhaseCompleted, status: store.SessionIdle},
		{name: "requested checkpoint of a stopped turn", phase: runtime.CheckpointPhaseRequested, status: store.SessionIdle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRecoveryHarness(t, &fakeProvider{respond: echoRespond})
			ctx := context.Background()
			id, userID := seedCrashedCheckpoint(t, h, "stopped prompt", "turn-normal", tc.phase,
				[]runtime.Message{{Role: runtime.RoleUser, Content: "stopped prompt"}})
			if _, err := h.store.UpdateSessionStatus(id, tc.status); err != nil {
				t.Fatalf("UpdateSessionStatus: %v", err)
			}
			h.restart()
			snapshot, err := h.ctrl.OpenSession(ctx, id)
			if err != nil {
				t.Fatalf("OpenSession: %v", err)
			}
			if got := snapshotTextCount(snapshot, runtime.InterruptedTurnMarker); got != 0 {
				t.Fatalf("normal turn gained %d interrupted markers", got)
			}
			if _, marked := snapshot.TurnMarks[userID]; marked {
				t.Fatalf("normal turn gained an interrupted mark: %+v", snapshot.TurnMarks)
			}
			checkpoint, err := h.store.LoadCheckpoint(id)
			if err != nil {
				t.Fatalf("LoadCheckpoint: %v", err)
			}
			if checkpoint.Phase != tc.phase {
				t.Fatalf("normal turn checkpoint was rewritten: phase = %q, want %q", checkpoint.Phase, tc.phase)
			}
		})
	}
}

// TestSubmitRecoversRequestedCheckpointBeforeNewTurn covers the first-Submit
// path: a session this controller has never opened is repaired before the new
// turn starts, so the provider request contains the crashed prompt once, the
// interrupted assistant anchor, and the new prompt — no duplicate user message.
func TestSubmitRecoversRequestedCheckpointBeforeNewTurn(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	h := newRecoveryHarness(t, provider)
	ctx := context.Background()
	id, _ := seedCrashedCheckpoint(t, h, "finish the migration", "turn-submit",
		runtime.CheckpointPhaseRequested,
		[]runtime.Message{{Role: runtime.RoleUser, Content: "finish the migration"}})
	h.restart()

	if err := h.ctrl.Submit(ctx, id, "continue the migration"); err != nil {
		t.Fatalf("Submit after a crash: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	request, ok := provider.requestAt(0)
	if !ok {
		t.Fatal("provider saw no request")
	}
	crashed := 0
	markers := 0
	lastUser := ""
	for _, msg := range request.Messages {
		switch msg.Role {
		case runtime.RoleUser:
			lastUser = msg.Content
			if msg.Content == "finish the migration" {
				crashed++
			}
		case runtime.RoleAssistant:
			if msg.Content == runtime.InterruptedTurnMarker {
				markers++
			}
		}
	}
	if crashed != 1 {
		t.Fatalf("provider request has %d copies of the crashed prompt, want 1: %+v", crashed, request.Messages)
	}
	if markers != 1 {
		t.Fatalf("provider request has %d interrupted markers, want 1: %+v", markers, request.Messages)
	}
	if lastUser != "continue the migration" {
		t.Fatalf("last user message = %q, want the new prompt", lastUser)
	}
	snapshot := h.ctrl.Snapshot()
	if got := snapshotTextCount(snapshot, runtime.InterruptedTurnMarker); got != 1 {
		t.Fatalf("snapshot marker messages = %d, want 1", got)
	}
}

// TestOpenSessionToleratesCorruptCheckpoint pins the fail-open contract: a
// checkpoint whose history cannot be decoded must not stop the session from
// opening, and the legacy repair still closes it.
func TestOpenSessionToleratesCorruptCheckpoint(t *testing.T) {
	h := newRecoveryHarness(t, &fakeProvider{respond: echoRespond})
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "corrupt")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := h.store.AppendMessage(store.Message{SessionID: id, Role: string(state.RoleUser), Content: "corrupt prompt"}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := h.store.SaveCheckpoint(store.Checkpoint{
		SessionID: id,
		History:   json.RawMessage(`{not json`),
		TurnID:    "turn-corrupt",
		Phase:     runtime.CheckpointPhaseRequested,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}
	if _, err := h.store.UpdateSessionStatus(id, store.SessionRunning); err != nil {
		t.Fatalf("UpdateSessionStatus: %v", err)
	}
	h.restart()
	snapshot, err := h.ctrl.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession with a corrupt checkpoint: %v", err)
	}
	if got := snapshotTextCount(snapshot, runtime.InterruptedTurnMarker); got != 0 {
		t.Fatalf("corrupt checkpoint appended %d markers, want none", got)
	}
	if snapshot.Phase != state.PhaseIdle {
		t.Fatalf("corrupt checkpoint left the session in phase %q, want idle", snapshot.Phase)
	}
}
