package hosttools

// Unit tests for the product-owned DSH goal tools: model-visible schema/result
// JSON and argument validation. The full behavioral/durable flow lives in
// internal/modules/hosttools (real registry + goal service adapter).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	righosttools "github.com/zeokeel/zeokeel-rig/tools/hosttools"
)

// fakeGoalRevisionStore is an in-memory RevisionGoalStore for tool tests.
type fakeGoalRevisionStore struct {
	current  righosttools.GoalSnapshot
	hasGoal  bool
	created  int
	updated  int
	lastArgs []string
}

func (f *fakeGoalRevisionStore) CurrentGoal(context.Context) (righosttools.GoalSnapshot, bool) {
	return f.current, f.hasGoal
}

func (f *fakeGoalRevisionStore) CreateGoal(_ context.Context, objective string, maxGoalRounds int) (righosttools.GoalSnapshot, error) {
	f.created++
	f.lastArgs = []string{objective}
	f.current = righosttools.GoalSnapshot{
		ID: "goal-1", Revision: 1, Objective: objective,
		Phase: righosttools.GoalPhaseActive, MaxGoalRounds: maxGoalRounds,
		Activation: righosttools.GoalActivationArmed,
	}
	f.hasGoal = true
	return f.current, nil
}

func (f *fakeGoalRevisionStore) UpdateGoal(_ context.Context, ref righosttools.GoalRef, update righosttools.GoalUpdate) (righosttools.GoalSnapshot, error) {
	f.updated++
	f.lastArgs = []string{ref.ID, string(update.Action)}
	f.current.Revision = ref.Revision + 1
	return f.current, nil
}

// toolByName resolves one tool from the product goal trio.
func toolByName(t *testing.T, store RevisionGoalStore, name string) rig.Tool {
	t.Helper()
	for _, tool := range GoalTools(store) {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("goal tools lack %q", name)
	return nil
}

// TestGoalToolsSchema pins the DSH model-visible contract: names, required
// arguments and the result JSON shape.
func TestGoalToolsSchema(t *testing.T) {
	store := &fakeGoalRevisionStore{}
	names := make([]string, 0, 3)
	for _, tool := range GoalTools(store) {
		names = append(names, tool.Name())
	}
	if strings.Join(names, ",") != "get_goal,create_goal,update_goal" {
		t.Fatalf("goal tool names = %v", names)
	}
	create := toolByName(t, store, ToolCreateGoal)
	schema := create.Schema()
	if schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatalf("create_goal schema = %v", schema)
	}
	properties := schema["properties"].(map[string]any)
	for _, key := range []string{"objective", "max_goal_rounds"} {
		if properties[key] == nil {
			t.Fatalf("create_goal schema lacks %q", key)
		}
	}
	required, _ := schema["required"].([]string)
	if len(required) != 1 || required[0] != "objective" {
		t.Fatalf("create_goal required = %v", required)
	}
	update := toolByName(t, store, ToolUpdateGoal)
	updateRequired, _ := update.Schema()["required"].([]string)
	if strings.Join(updateRequired, ",") != "goal_id,revision,action" {
		t.Fatalf("update_goal required = %v", updateRequired)
	}
	if toolByName(t, store, ToolGetGoal) == nil {
		t.Fatal("get_goal tool is nil")
	}
}

// TestGetGoalNullAndValue pins {"goal":null} and the full goal JSON.
func TestGetGoalNullAndValue(t *testing.T) {
	store := &fakeGoalRevisionStore{}
	tool := toolByName(t, store, ToolGetGoal)
	result, err := tool.Invoke(context.Background(), rig.ToolCall{Name: ToolGetGoal, Arguments: `{}`})
	if err != nil || result.IsError {
		t.Fatalf("get_goal empty = %+v, %v", result, err)
	}
	if result.Content != `{"goal":null}` {
		t.Fatalf("get_goal empty = %q, want {\"goal\":null}", result.Content)
	}
	createTool := toolByName(t, store, ToolCreateGoal)
	result, err = createTool.Invoke(context.Background(), rig.ToolCall{Name: ToolCreateGoal,
		Arguments: `{"objective":"ship it","max_goal_rounds":7}`})
	if err != nil || result.IsError {
		t.Fatalf("create_goal = %+v, %v", result, err)
	}
	result, err = tool.Invoke(context.Background(), rig.ToolCall{Name: ToolGetGoal, Arguments: `{}`})
	if err != nil || result.IsError {
		t.Fatalf("get_goal = %+v, %v", result, err)
	}
	var output struct {
		Goal *struct {
			ID            string `json:"id"`
			Revision      int    `json:"revision"`
			Objective     string `json:"objective"`
			Phase         string `json:"phase"`
			MaxGoalRounds int    `json:"maxGoalRounds"`
		} `json:"goal"`
		Activation string `json:"activation"`
	}
	if err := json.Unmarshal([]byte(result.Content), &output); err != nil {
		t.Fatalf("decode get_goal %q: %v", result.Content, err)
	}
	if output.Goal == nil || output.Goal.ID != "goal-1" || output.Goal.Objective != "ship it" ||
		output.Goal.MaxGoalRounds != 7 || output.Activation != string(righosttools.GoalActivationArmed) {
		t.Fatalf("get_goal output = %+v", output)
	}
}

// TestGoalToolValidation pins the argument rules delegated to the adapter.
func TestGoalToolValidation(t *testing.T) {
	store := &fakeGoalRevisionStore{}
	create := toolByName(t, store, ToolCreateGoal)
	for _, arguments := range []string{`{}`, `{"objective":"   "}`, `{"objective":"x","max_goal_rounds":-1}`} {
		result, err := create.Invoke(context.Background(), rig.ToolCall{Name: ToolCreateGoal, Arguments: arguments})
		if err != nil || !result.IsError {
			t.Fatalf("create_goal %s = %+v, %v; want a model-facing error", arguments, result, err)
		}
	}
	if store.created != 0 {
		t.Fatalf("invalid create_goal calls reached the store %d times", store.created)
	}
	update := toolByName(t, store, ToolUpdateGoal)
	cases := map[string]string{
		`{"goal_id":"goal-1","revision":1,"action":"bogus"}`:                 "unknown action",
		`{"goal_id":"goal-1","revision":1,"action":"blocked"}`:               "blocked_reason is required",
		`{"goal_id":"goal-1","revision":1,"action":"pause","objective":"x"}`: "valid only with action edit",
		`{"goal_id":"","revision":1,"action":"pause"}`:                       "goal_id must be non-empty",
		`{"goal_id":"goal-1","revision":0,"action":"pause"}`:                 "revision must be a positive integer",
	}
	for arguments, want := range cases {
		result, err := update.Invoke(context.Background(), rig.ToolCall{Name: ToolUpdateGoal, Arguments: arguments})
		if err != nil || !result.IsError || !strings.Contains(result.Content, want) {
			t.Fatalf("update_goal %s = %+v, %v; want error containing %q", arguments, result, err, want)
		}
	}
	// A valid pause is forwarded to the adapter.
	result, err := update.Invoke(context.Background(), rig.ToolCall{Name: ToolUpdateGoal,
		Arguments: `{"goal_id":"goal-1","revision":1,"action":"pause"}`})
	if err != nil || result.IsError {
		t.Fatalf("valid update_goal = %+v, %v", result, err)
	}
	if store.updated != 1 || strings.Join(store.lastArgs, ",") != "goal-1,pause" {
		t.Fatalf("adapter update calls = %d args %v", store.updated, store.lastArgs)
	}
}
