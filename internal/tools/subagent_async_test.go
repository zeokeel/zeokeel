package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

// decodeSubagentEnvelope decodes one of the async subagent tool JSON replies.
func decodeSubagentEnvelope(t *testing.T, raw string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("reply is not JSON: %v\n%s", err, raw)
	}
	return out
}

// TestSubagentAsyncSpawnStatusListAndNotice pins FP-03: async:true returns a
// child id immediately, the completion is announced through the advisory sink,
// and status/list read the background child.
func TestSubagentAsyncSpawnStatusListAndNotice(t *testing.T) {
	runner := &fakeSubagentRunner{result: runtime.SubagentResult{Output: "async result"}}
	tool := NewSubagentTool(SubagentConfig{Runner: runner})
	notices := make(chan string, 4)
	tool.SetAdvisorySink(func(text string) { notices <- text })

	raw, err := tool.Invoke(context.Background(), json.RawMessage(
		`{"task":"background research","agent":"worker","async":true}`))
	if err != nil {
		t.Fatalf("async Invoke: %v", err)
	}
	spawn := decodeSubagentEnvelope(t, raw)
	if spawn["kind"] != "subagent_async" {
		t.Fatalf("spawn reply = %v, want kind subagent_async", spawn)
	}
	childID, _ := spawn["child_id"].(string)
	if childID == "" {
		t.Fatalf("spawn reply has no child_id: %v", spawn)
	}

	// The child must be visible through list and status immediately.
	listOut, err := tool.Invoke(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	list := decodeSubagentEnvelope(t, listOut)
	if list["kind"] != "subagent_list" {
		t.Fatalf("list reply = %v", list)
	}
	if got, _ := list["count"].(float64); got != 1 {
		t.Fatalf("list count = %v, want 1", list["count"])
	}

	statusArgs, _ := json.Marshal(map[string]string{"action": "status", "id": childID})
	statusOut, err := tool.Invoke(context.Background(), statusArgs)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	status := decodeSubagentEnvelope(t, statusOut)
	member, _ := status["member"].(map[string]any)
	if member == nil || member["id"] != childID {
		t.Fatalf("status reply = %v, want the member", status)
	}

	// The completion notice reaches the parent turn through the advisory sink.
	select {
	case notice := <-notices:
		for _, want := range []string{childID, "finished", "async result"} {
			if !strings.Contains(notice, want) {
				t.Fatalf("completion notice %q missing %q", notice, want)
			}
		}
	case <-time.After(8 * time.Second):
		t.Fatal("async subagent never announced its completion")
	}

	// And the status settles to completed with the output.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		statusOut, err = tool.Invoke(context.Background(), statusArgs)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		status = decodeSubagentEnvelope(t, statusOut)
		member, _ = status["member"].(map[string]any)
		if member != nil && member["status"] == "completed" {
			if member["output"] != "async result" {
				t.Fatalf("completed output = %v, want async result", member["output"])
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("async child never reached completed: %v", status)
}

// TestSubagentAsyncRequiresRunner pins the configuration error path.
func TestSubagentAsyncRequiresRunner(t *testing.T) {
	tool := NewSubagentTool(SubagentConfig{})
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"task":"x","async":true}`)); err == nil {
		t.Fatal("async spawn without a runner = nil error")
	}
}

// TestSubagentSyncUnchanged pins backwards compatibility: without async the
// tool still returns the child's final text synchronously.
func TestSubagentSyncUnchanged(t *testing.T) {
	runner := &fakeSubagentRunner{result: runtime.SubagentResult{Output: "sync result"}}
	tool := NewSubagentTool(SubagentConfig{Runner: runner})
	out, err := tool.Invoke(context.Background(), json.RawMessage(`{"task":"sync task"}`))
	if err != nil {
		t.Fatalf("sync Invoke: %v", err)
	}
	if out != "sync result" {
		t.Fatalf("sync output = %q, want the child text", out)
	}
}

// TestSubagentUnknownActionRejected pins strict action validation.
func TestSubagentUnknownActionRejected(t *testing.T) {
	runner := &fakeSubagentRunner{}
	tool := NewSubagentTool(SubagentConfig{Runner: runner})
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"action":"explode"}`)); err == nil {
		t.Fatal("unknown action = nil error")
	}
}
