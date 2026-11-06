package app

import (
	"context"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// TestStoreHistoryEmitsSpillSummary pins the history half of the tool-result
// spill: once the session layer replaced the persisted tool_calls result with
// the spill summary, the next turn's provider history carries exactly that
// summary (the file path and the head), not the huge output.
func TestStoreHistoryEmitsSpillSummary(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	ws, err := st.CreateWorkspace("rooted", dir)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess, err := st.CreateSession(ws.ID, "spill history")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := st.AppendMessage(store.Message{SessionID: sess.ID, Role: string(state.RoleUser), Content: "list it"}); err != nil {
		t.Fatalf("AppendMessage user: %v", err)
	}
	const summary = "[tool result too large: 84017 bytes. Full output saved to the workspace file .zeokeel/tool-results/call-big.txt; only this summary is re-sent to the model.]\n\n目录项 head"
	if err := st.RecordToolCall(store.ToolCall{
		ID:        "call-big",
		SessionID: sess.ID,
		Name:      "big",
		Args:      "{}",
		Status:    "done",
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordToolCall: %v", err)
	}
	if err := st.FinishToolCall("call-big", summary, "done", time.Now().UTC()); err != nil {
		t.Fatalf("FinishToolCall: %v", err)
	}

	msgs, err := (&storeHistory{st: st}).Messages(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	found := false
	for _, msg := range msgs {
		if msg.Role != runtime.RoleTool {
			continue
		}
		found = true
		if msg.Content != summary {
			t.Fatalf("tool message = %.200q, want the spill summary", msg.Content)
		}
	}
	if !found {
		t.Fatalf("history has no tool message: %+v", msgs)
	}
}
