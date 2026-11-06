package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// Checkpoint-driven crash recovery.
//
// The runtime writes a non-terminal barrier checkpoint before every model
// request (requested) and before a top-level side-effecting tool dispatch
// (tool_pending). A process that dies mid-turn therefore leaves a checkpoint
// that is the most precise record of the interrupted turn: it carries the
// conversation including the abandoned tail. recoverInterruptedCheckpoint
// consumes it (through runtime.RecoverInterruptedTurn), closes every dispatched
// tool call with an explicit unknown-outcome result, appends an assistant marker
// that anchors the abandoned tail in the next model request, and finally stores
// a completed-phase checkpoint so a second recovery is a no-op.
//
// It is the checkpoint half of the existing recoverInterruptedSession repair:
// the legacy half closes open tool rows and projects the interrupted turn mark
// from the store's status/events; this half closes the checkpoint-recorded tail
// that the store tables may not have (a call dispatched before its tool_calls
// row existed) and repairs the model-visible conversation.

// storeCheckpointLoader adapts the durable checkpoints table to the runtime's
// read-only recovery seam.
type storeCheckpointLoader struct {
	st *store.Store
}

var _ runtime.CheckpointLoader = storeCheckpointLoader{}

// Load implements runtime.CheckpointLoader.
func (l storeCheckpointLoader) Load(ctx context.Context, sessionID string) (*runtime.Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l.st == nil {
		return nil, nil
	}
	checkpoint, err := l.st.LoadCheckpoint(sessionID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var history []runtime.Message
	if len(checkpoint.History) > 0 {
		if err := json.Unmarshal(checkpoint.History, &history); err != nil {
			return nil, fmt.Errorf("session: decode checkpoint history for %q: %w", sessionID, err)
		}
	}
	return &runtime.Checkpoint{
		History:   history,
		TurnID:    checkpoint.TurnID,
		UpdatedAt: checkpoint.UpdatedAt,
		Phase:     checkpoint.Phase,
	}, nil
}

// recoverInterruptedCheckpoint consumes a non-terminal barrier checkpoint for
// sess and applies the interrupted-turn repair. A nil/complete checkpoint is a
// no-op. It is idempotent across retries and restarts: tool rows already closed
// are left alone, the marker message is written once per turn, and the repaired
// completed checkpoint makes the next recovery a no-op.
func (c *Controller) recoverInterruptedCheckpoint(sess *store.Session) error {
	if c == nil || c.store == nil || sess == nil || strings.TrimSpace(sess.ID) == "" {
		return nil
	}
	// Transitional only: a terminal (idle/failed) session row means the turn
	// reached a recorded end (a normal finish, a Stop or a failure), so the
	// checkpoint must not be reinterpreted as a crash.
	if terminalStoreStatus(sess.Status) {
		return nil
	}
	repair, err := runtime.RecoverInterruptedTurn(context.Background(), storeCheckpointLoader{st: c.store}, sess.ID)
	if err != nil {
		// A damaged checkpoint is a recovery hint only: it must not stop the
		// session from opening. The legacy status/tool repair still closes the
		// store tail afterwards.
		return nil
	}
	if repair == nil {
		return nil
	}
	calls := indexRepairCalls(repair.History)
	for _, msg := range repair.Appended {
		if msg.Role != runtime.RoleTool {
			continue
		}
		if err := c.closeRecoveredTool(sess.ID, msg.ToolCallID, msg.ToolName, nil, calls); err != nil {
			return fmt.Errorf("session: recover interrupted checkpoint: close tool %q: %w", msg.ToolCallID, err)
		}
	}
	if err := c.ensureInterruptedMarker(sess.ID, repair.TurnID); err != nil {
		return fmt.Errorf("session: recover interrupted checkpoint: persist marker: %w", err)
	}
	// Store the repaired conversation as a completed checkpoint: the next
	// recovery sees a completed phase and is a no-op, which is what makes the
	// repair idempotent across restarts.
	history, err := json.Marshal(repair.History)
	if err != nil {
		return fmt.Errorf("session: recover interrupted checkpoint: encode history: %w", err)
	}
	if err := c.store.SaveCheckpoint(store.Checkpoint{
		SessionID: sess.ID,
		History:   history,
		TurnID:    repair.TurnID,
		UpdatedAt: time.Now().UTC(),
		Phase:     runtime.CheckpointPhaseCompleted,
	}); err != nil {
		return fmt.Errorf("session: recover interrupted checkpoint: save repaired checkpoint: %w", err)
	}
	return nil
}

// callMetadata indexes the assistant tool calls recorded in a repair history.
type callMetadata struct {
	name      string
	arguments json.RawMessage
}

// indexRepairCalls maps tool-call ids to their name/arguments from the repaired
// conversation, so synthetic results can create a missing tool_calls row with
// the original call metadata.
func indexRepairCalls(history []runtime.Message) map[string]callMetadata {
	calls := make(map[string]callMetadata)
	for _, msg := range history {
		if msg.Role != runtime.RoleAssistant {
			continue
		}
		for _, call := range msg.ToolCalls {
			if call.ID == "" {
				continue
			}
			calls[call.ID] = callMetadata{name: call.Name, arguments: call.Arguments}
		}
	}
	return calls
}

// ensureInterruptedMarker appends the assistant marker message at most once per
// turn. A marker row persisted before a crash (between the append and the
// checkpoint save) must not be duplicated by the retry.
func (c *Controller) ensureInterruptedMarker(sessionID, turnID string) error {
	messages, err := c.store.ListMessages(sessionID)
	if err != nil {
		return err
	}
	for _, msg := range messages {
		if !strings.EqualFold(strings.TrimSpace(msg.Role), string(state.RoleAssistant)) {
			continue
		}
		if msg.Content != runtime.InterruptedTurnMarker {
			continue
		}
		if turnID == "" || msg.TurnID == turnID {
			return nil
		}
	}
	_, err = c.store.AppendMessage(store.Message{
		SessionID: sessionID,
		Role:      string(state.RoleAssistant),
		Content:   runtime.InterruptedTurnMarker,
		TurnID:    turnID,
	})
	return err
}

// closeRecoveredTool closes one checkpoint-recorded tool call with the
// unknown-outcome result. The tool_calls row may be missing (the crash landed
// between the barrier checkpoint and the controller's tool_start write); it is
// created closed so the next provider request reconstructs a valid
// assistant(tool_call) -> tool(result) exchange.
func (c *Controller) closeRecoveredTool(sessionID, callID, name string, arguments json.RawMessage, metadata map[string]callMetadata) error {
	if strings.TrimSpace(callID) == "" {
		return nil
	}
	if meta, ok := metadata[callID]; ok {
		if name == "" {
			name = meta.name
		}
		if len(arguments) == 0 {
			arguments = meta.arguments
		}
	}
	if name == "" {
		name = "unknown"
	}
	now := time.Now().UTC()
	existing, err := c.store.GetToolCall(callID)
	if err == nil && existing != nil {
		if terminalToolCallStatus(existing.Status) {
			return nil
		}
		if err := c.appendEvent(sessionID, state.ToolEnd{
			CallID:   callID,
			Result:   runtime.InterruptedToolResult,
			IsError:  true,
			Duration: time.Since(existing.StartedAt),
		}); err != nil {
			return err
		}
		return c.store.FinishToolCall(callID, runtime.InterruptedToolResult, "error", now)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	argText := strings.TrimSpace(string(arguments))
	if argText == "" {
		argText = "{}"
	}
	if err := c.store.RecordToolCall(store.ToolCall{
		ID:        callID,
		SessionID: sessionID,
		Name:      name,
		Args:      argText,
		Result:    runtime.InterruptedToolResult,
		Status:    "error",
		StartedAt: now,
		EndedAt:   now,
	}); err != nil {
		return err
	}
	if err := c.appendEvent(sessionID, state.ToolStart{CallID: callID, Name: name, Args: arguments}); err != nil {
		return err
	}
	return c.appendEvent(sessionID, state.ToolEnd{
		CallID:  callID,
		Result:  runtime.InterruptedToolResult,
		IsError: true,
	})
}
