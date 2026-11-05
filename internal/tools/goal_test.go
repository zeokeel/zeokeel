package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

func decodeGoalResult(t *testing.T, raw string) GoalResult {
	t.Helper()
	var out GoalResult
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("goal result is not JSON: %v\n%s", err, raw)
	}
	return out
}

func TestGoalToolSetGetComplete(t *testing.T) {
	tool := NewGoalTool()
	if tool.Name() != "goal" || strings.TrimSpace(tool.Description()) == "" {
		t.Fatalf("tool metadata = %q / %q", tool.Name(), tool.Description())
	}

	raw, err := tool.Invoke(context.Background(), json.RawMessage(
		`{"action":"set","objective":"Ship the release","steps":["test","notes"],"progress":40}`))
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	out := decodeGoalResult(t, raw)
	if out.Kind != "goal" || out.Action != "set" || out.Goal == nil {
		t.Fatalf("result = %+v", out)
	}
	if out.Goal.Objective != "Ship the release" || out.Goal.Progress != 40 ||
		out.Goal.Status != GoalActive || len(out.Goal.Steps) != 2 {
		t.Fatalf("goal = %+v", *out.Goal)
	}
	if out.Goal.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt is zero")
	}

	raw, err = tool.Invoke(context.Background(), json.RawMessage(`{"action":"get"}`))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got := decodeGoalResult(t, raw); got.Goal == nil || got.Goal.Objective != "Ship the release" {
		t.Fatalf("get = %+v", got)
	}

	raw, err = tool.Invoke(context.Background(), json.RawMessage(`{"action":"complete"}`))
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	out = decodeGoalResult(t, raw)
	if out.Goal == nil || out.Goal.Status != GoalCompleted || out.Goal.Progress != 100 {
		t.Fatalf("complete = %+v", out.Goal)
	}
	if tool.Current() == nil || tool.Current().Status != GoalCompleted {
		t.Fatalf("Current() = %+v", tool.Current())
	}
}

func TestGoalToolSetKeepsFieldsAndClearsSteps(t *testing.T) {
	tool := NewGoalTool()
	if _, err := tool.Invoke(context.Background(), json.RawMessage(
		`{"action":"set","objective":"Objective A","steps":["one"],"progress":10}`)); err != nil {
		t.Fatalf("set: %v", err)
	}

	// No objective and no steps: keep both, only update progress.
	raw, err := tool.Invoke(context.Background(), json.RawMessage(`{"action":"set","progress":55}`))
	if err != nil {
		t.Fatalf("set progress: %v", err)
	}
	goal := decodeGoalResult(t, raw).Goal
	if goal == nil || goal.Objective != "Objective A" || goal.Progress != 55 || len(goal.Steps) != 1 {
		t.Fatalf("goal = %+v", goal)
	}

	// An explicit empty steps array clears the list.
	raw, err = tool.Invoke(context.Background(), json.RawMessage(`{"action":"set","steps":[]}`))
	if err != nil {
		t.Fatalf("set clear: %v", err)
	}
	if goal := decodeGoalResult(t, raw).Goal; goal == nil || len(goal.Steps) != 0 {
		t.Fatalf("goal = %+v", goal)
	}
}

func TestGoalToolAbandon(t *testing.T) {
	tool := NewGoalTool()
	if _, err := tool.Invoke(context.Background(), json.RawMessage(
		`{"action":"set","objective":"Doomed","progress":70}`)); err != nil {
		t.Fatalf("set: %v", err)
	}
	raw, err := tool.Invoke(context.Background(), json.RawMessage(`{"action":"abandon","message":"blocked"}`))
	if err != nil {
		t.Fatalf("abandon: %v", err)
	}
	out := decodeGoalResult(t, raw)
	if out.Goal == nil || out.Goal.Status != GoalAbandoned {
		t.Fatalf("goal = %+v", out.Goal)
	}
	if out.Goal.Progress != 70 {
		t.Fatalf("abandon changed progress to %d", out.Goal.Progress)
	}
	if out.Message != "blocked" {
		t.Fatalf("message = %q", out.Message)
	}
}

func TestGoalToolEmptyAndValidation(t *testing.T) {
	tool := NewGoalTool()

	raw, err := tool.Invoke(context.Background(), json.RawMessage(`{"action":"get"}`))
	if err != nil {
		t.Fatalf("get empty: %v", err)
	}
	if got := decodeGoalResult(t, raw); got.Goal != nil {
		t.Fatalf("empty get = %+v, want no goal", got)
	}

	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("missing action = nil error")
	}
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"action":"explode"}`)); err == nil {
		t.Fatal("unknown action = nil error")
	}
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"action":"set"}`)); err == nil {
		t.Fatal("set without objective = nil error")
	}
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"action":"complete"}`)); err == nil {
		t.Fatal("complete without a goal = nil error")
	}
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"action":"abandon"}`)); err == nil {
		t.Fatal("abandon without a goal = nil error")
	}
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"action":"set","objective":"x","progress":101}`)); err == nil {
		t.Fatal("progress 101 = nil error")
	}
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid JSON = nil error")
	}
}

func TestGoalToolCurrentReturnsCopy(t *testing.T) {
	tool := NewGoalTool()
	if _, err := tool.Invoke(context.Background(), json.RawMessage(
		`{"action":"set","objective":"Keep me","steps":["a"]}`)); err != nil {
		t.Fatalf("set: %v", err)
	}
	current := tool.Current()
	if current == nil {
		t.Fatal("Current() = nil")
	}
	current.Objective = "mutated"
	current.Steps[0] = "mutated"
	if got := tool.Current(); got.Objective != "Keep me" || got.Steps[0] != "a" {
		t.Fatalf("Current() leaked tool state: %+v", got)
	}
}

// TestGoalToolIsolatesSessions proves the shared goal tool keeps one goal per
// session; session B must not see session A's objective after a restart-like
// switch on the same agent.
func TestGoalToolIsolatesSessions(t *testing.T) {
	tool := NewGoalTool()
	ctxA := runtime.WithSessionID(context.Background(), "session-a")
	ctxB := runtime.WithSessionID(context.Background(), "session-b")

	if _, err := tool.Invoke(ctxA, json.RawMessage(`{"action":"set","objective":"Ship A"}`)); err != nil {
		t.Fatalf("set A: %v", err)
	}
	raw, err := tool.Invoke(ctxB, json.RawMessage(`{"action":"get"}`))
	if err != nil {
		t.Fatalf("get B: %v", err)
	}
	if got := decodeGoalResult(t, raw); got.Goal != nil {
		t.Fatalf("session B saw session A's goal: %+v", got.Goal)
	}
	if got := tool.GoalFor("session-a"); got == nil || got.Objective != "Ship A" {
		t.Fatalf("GoalFor(A) = %+v", got)
	}
	if got := tool.GoalFor("session-b"); got != nil {
		t.Fatalf("GoalFor(B) = %+v, want nil", got)
	}
}
