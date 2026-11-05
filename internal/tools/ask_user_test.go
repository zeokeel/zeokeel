package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func decodeQuestionResult(t *testing.T, raw string) QuestionResult {
	t.Helper()
	var out QuestionResult
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("ask_user result is not JSON: %v\n%s", err, raw)
	}
	return out
}

func TestAskUserToolRecordsQuestion(t *testing.T) {
	tool := NewAskUserTool()
	if got := tool.Name(); got != "ask_user" {
		t.Fatalf("Name = %q, want ask_user", got)
	}

	out := decodeQuestionResult(t, mustInvoke(t, tool, map[string]any{
		"question": "Which database?",
		"options":  []string{"sqlite", " sqlite ", "", "postgres"},
		"context":  "The schema differs.",
	}))
	if out.Kind != "question" || out.Question != "Which database?" || out.Status != "pending" {
		t.Fatalf("result = %+v", out)
	}
	if len(out.Options) != 2 || out.Options[0] != "sqlite" || out.Options[1] != "postgres" {
		t.Fatalf("options = %v, want cleaned [sqlite postgres]", out.Options)
	}

	pending := tool.Pending()
	if len(pending) != 1 || pending[0].Question != "Which database?" || pending[0].Context != "The schema differs." {
		t.Fatalf("Pending() = %+v", pending)
	}
	// Pending returns copies; mutating one must not change the tool's record.
	pending[0].Options[0] = "mutated"
	if got := tool.Pending()[0].Options[0]; got != "sqlite" {
		t.Fatalf("tool options changed through Pending(): %q", got)
	}
}

func TestAskUserToolValidatesArguments(t *testing.T) {
	tool := NewAskUserTool()
	if out, err := invokeTool(t, tool, map[string]any{"question": "   "}); err == nil {
		t.Fatalf("blank question: err = nil, out = %q", out)
	}
	if out, err := invokeTool(t, tool, map[string]any{}); err == nil {
		t.Fatalf("missing question: err = nil, out = %q", out)
	}
	if out, err := tool.Invoke(context.Background(), json.RawMessage("not-json")); err == nil {
		t.Fatalf("invalid JSON: err = nil, out = %q", out)
	}
	if got := len(tool.Pending()); got != 0 {
		t.Fatalf("Pending() after failures = %d, want 0", got)
	}
}

func TestAskUserToolDoesNotBlock(t *testing.T) {
	// The answer is the session's next user turn, so Invoke must return even
	// though nobody has answered yet; blocking here would deadlock the turn.
	tool := NewAskUserTool()
	args := mustArgs(t, map[string]any{"question": "Continue?"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := tool.Invoke(context.Background(), args); err != nil {
			t.Errorf("Invoke: %v", err)
		}
	}()
	<-done
}
