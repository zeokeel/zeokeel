package session_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/store"
)

// TestEventsMirrorToTranscriptAndToolCalls proves the documented store
// contract: every event is also mirrored to session.jsonl in production, and
// tool lifecycles land in the tool_calls table (not only the events trace).
func TestEventsMirrorToTranscriptAndToolCalls(t *testing.T) {
	provider := &fakeProvider{respond: func(_ context.Context, _ runtime.Request, call int) []runtime.Chunk {
		if call == 0 {
			return []runtime.Chunk{{
				ToolCalls: []runtime.ToolCall{{
					ID:        "call-persist",
					Name:      "echo",
					Arguments: json.RawMessage(`{"text":"hello"}`),
				}},
				Done: true,
			}}
		}
		return []runtime.Chunk{{Text: "done", Done: true}}
	}}
	h := newHarness(t, provider)
	id, err := h.ctrl.CreateSession(context.Background(), "persist")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(context.Background(), id, "hello"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	records, err := store.ReadTranscript(h.dir, id)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("session.jsonl is empty; production does not mirror events")
	}

	calls, err := h.store.ToolCallsForSession(id)
	if err != nil {
		t.Fatalf("ToolCallsForSession: %v", err)
	}
	if len(calls) == 0 {
		t.Fatal("tool_calls is empty; production does not record tool calls")
	}
	if calls[0].Name != "echo" || calls[0].Status != "done" {
		t.Fatalf("tool call = %+v, want echo/done", calls[0])
	}
}
