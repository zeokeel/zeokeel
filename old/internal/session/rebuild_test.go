package session_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/store"
	"github.com/zeokeel/zeokeel/old/internal/tools"
)

// TestModelSwitchAppliesToNextRequest proves a selected model reaches the
// provider on the next turn: the controller fires the rebuild hook, the app's
// rebuild swaps in an agent built with the new model, and the next request
// carries it.
func TestModelSwitchAppliesToNextRequest(t *testing.T) {
	provider := &fakeProvider{respond: echoRespond}
	h := newHarness(t, provider)
	// The harness agent was built without a model name; give it one so the
	// switch is observable.

	model, err := h.ctrl.Models().Create(store.Model{Name: "Model Two", Model: "model-two"})
	if err != nil {
		t.Fatalf("Create model: %v", err)
	}
	var mu sync.Mutex
	var reasons []string
	h.ctrl.SetAgentRebuilder(func(reason string) error {
		mu.Lock()
		reasons = append(reasons, reason)
		mu.Unlock()
		return nil
	})
	if err := h.ctrl.Models().SetDefault(model.ID); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	mu.Lock()
	saw := len(reasons) == 1 && reasons[0] == "model"
	mu.Unlock()
	if !saw {
		t.Fatalf("rebuild reasons = %v, want [model]", reasons)
	}
	if got := h.agent.ModelName(); got != "model-two" {
		t.Fatalf("running agent model = %q, want model-two before the next turn", got)
	}

	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "model switch")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.ctrl.Submit(ctx, id, "hello"); err != nil {
		t.Fatal(err)
	}
	waitIdleAndDrain(t, h.ctrl)
	req, ok := provider.requestAt(0)
	if !ok {
		t.Fatal("provider got no request")
	}
	if req.Model != "model-two" {
		t.Fatalf("provider request model = %q, want the newly selected model", req.Model)
	}
}

// TestPluginToggleRebuildsNextTurn proves the plugin hook is not just a log:
// SetPluginEnabled asks the app to rebuild the agent.
func TestPluginToggleRebuildsNextTurn(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	var reasons []string
	h.ctrl.SetAgentRebuilder(func(reason string) error {
		reasons = append(reasons, reason)
		return nil
	})
	if err := h.ctrl.SetPluginEnabled("shell", false); err != nil {
		t.Fatalf("SetPluginEnabled: %v", err)
	}
	if len(reasons) != 1 || reasons[0] != "plugin" {
		t.Fatalf("rebuild reasons = %v, want [plugin]", reasons)
	}
}

// TestWorkspaceChangeRebuildsAgent proves selecting a workspace asks the app
// to rebuild the tools against the new root.
func TestWorkspaceChangeRebuildsAgent(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	var reasons []string
	h.ctrl.SetAgentRebuilder(func(reason string) error {
		reasons = append(reasons, reason)
		return nil
	})
	ws, err := h.ctrl.CreateWorkspace(context.Background(), "other", t.TempDir())
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if len(reasons) == 0 || reasons[len(reasons)-1] != "workspace" {
		t.Fatalf("rebuild reasons after create = %v", reasons)
	}
	reasons = nil
	if err := h.ctrl.SelectWorkspace(context.Background(), ws.ID); err != nil {
		t.Fatalf("SelectWorkspace: %v", err)
	}
	if len(reasons) != 1 || reasons[0] != "workspace" {
		t.Fatalf("rebuild reasons after select = %v", reasons)
	}
}

// TestHistoryReplayRestoresToolState reopens a controller over the same store
// and checks the todo, goal, artifacts and pending question survive, because
// the panels read the controller maps rather than the replayed snapshot.
func TestHistoryReplayRestoresToolState(t *testing.T) {
	h := newHarnessWithTools(t)
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "persisted tool state")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.ctrl.Submit(ctx, id, "run tools"); err != nil {
		t.Fatal(err)
	}
	waitIdleAndDrain(t, h.ctrl)

	if got := h.ctrl.SessionTodos(id); len(got) == 0 || got[0].Task != "persisted task" {
		t.Fatalf("live todos = %+v", got)
	}
	if got := h.ctrl.SessionGoal(id); got == nil || got.Objective != "persisted goal" {
		t.Fatalf("live goal = %+v", got)
	}
	if got := h.ctrl.Artifacts(); len(got) == 0 || got[0].Title != "Persisted artifact" {
		t.Fatalf("live artifacts = %+v", got)
	}
	if got := h.ctrl.Questions(); len(got) == 0 || got[0].Question != "Continue?" {
		t.Fatalf("live questions = %+v", got)
	}

	// Restart over the same store: the maps must be folded back from events.
	h.restart()
	if _, err := h.ctrl.OpenSession(ctx, id); err != nil {
		t.Fatalf("OpenSession after restart: %v", err)
	}
	if got := h.ctrl.SessionTodos(id); len(got) == 0 || got[0].Task != "persisted task" {
		t.Fatalf("replayed todos = %+v", got)
	}
	if got := h.ctrl.SessionGoal(id); got == nil || got.Objective != "persisted goal" {
		t.Fatalf("replayed goal = %+v", got)
	}
	if got := h.ctrl.Artifacts(); len(got) == 0 || got[0].Title != "Persisted artifact" {
		t.Fatalf("replayed artifacts = %+v", got)
	}
	if got := h.ctrl.Questions(); len(got) == 0 || got[0].Question != "Continue?" {
		t.Fatalf("replayed questions = %+v", got)
	}
}

// newHarnessWithTools builds a harness whose agent registers the interactive
// tools and goal, with a provider that calls each of them on the first model
// request and answers with text afterwards.
func newHarnessWithTools(t *testing.T) *harness {
	t.Helper()
	registry := runtime.NewRegistry()
	for _, tl := range tools.InteractiveTools() {
		if err := registry.Register(tl); err != nil {
			t.Fatalf("register interactive: %v", err)
		}
	}
	if err := registry.Register(tools.NewGoalTool()); err != nil {
		t.Fatalf("register goal: %v", err)
	}
	provider := &fakeProvider{respond: func(_ context.Context, _ runtime.Request, call int) []runtime.Chunk {
		if call > 0 {
			return []runtime.Chunk{{Text: "done", Done: true}}
		}
		return []runtime.Chunk{{
			ToolCalls: []runtime.ToolCall{
				{ID: "todo-1", Name: "todo", Arguments: json.RawMessage(`{"action":"add","task":"persisted task"}`)},
				{ID: "goal-1", Name: "goal", Arguments: json.RawMessage(`{"action":"set","objective":"persisted goal"}`)},
				{ID: "present-1", Name: "present", Arguments: json.RawMessage(`{"title":"Persisted artifact","markdown":"body"}`)},
				{ID: "ask-1", Name: "ask_user", Arguments: json.RawMessage(`{"question":"Continue?"}`)},
			},
			Done: true,
		}}
	}}
	agent, err := runtime.New(runtime.Config{
		Provider:    provider,
		Tools:       registry,
		Instruction: "tool agent",
	})
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}
	h := newHarness(t, provider)
	h.agent = agent
	h.ctrl.Close()
	h.newController()
	return h
}

// TestAnsweredQuestionDoesNotReappearAfterRestart proves the answer event is
// persisted: replaying the session no longer resurrects the question card.
func TestAnsweredQuestionDoesNotReappearAfterRestart(t *testing.T) {
	h := newHarnessWithTools(t)
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "answer persistence")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.ctrl.Submit(ctx, id, "run tools"); err != nil {
		t.Fatal(err)
	}
	waitIdleAndDrain(t, h.ctrl)
	questions := h.ctrl.Questions()
	if len(questions) != 1 || questions[0].CallID == "" {
		t.Fatalf("live questions = %+v", questions)
	}
	if err := h.ctrl.AnswerQuestion(ctx, questions[0].CallID, "yes"); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)
	if got := h.ctrl.Questions(); len(got) != 0 {
		t.Fatalf("questions after answering = %+v", got)
	}

	h.restart()
	if _, err := h.ctrl.OpenSession(ctx, id); err != nil {
		t.Fatalf("OpenSession after restart: %v", err)
	}
	if got := h.ctrl.Questions(); len(got) != 0 {
		t.Fatalf("answered question reappeared after restart: %+v", got)
	}
}

// TestModelCRUDRebuildsAgent covers the Settings > Models path: creating,
// editing and deleting a model must apply to the running agent and fire the
// rebuild hook exactly as SetDefault does.
func TestModelCRUDRebuildsAgent(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	var reasons []string
	h.ctrl.SetAgentRebuilder(func(reason string) error {
		reasons = append(reasons, reason)
		return nil
	})
	model, err := h.ctrl.Models().Create(store.Model{Name: "First", Model: "first-model"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(reasons) != 1 || reasons[0] != "model" {
		t.Fatalf("rebuild reasons after Create = %v, want [model]", reasons)
	}
	if got := h.agent.ModelName(); got != "first-model" {
		t.Fatalf("agent model after Create = %q, want first-model", got)
	}

	model.Model = "second-model"
	if _, err := h.ctrl.Models().Update(*model); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(reasons) != 2 || reasons[1] != "model" {
		t.Fatalf("rebuild reasons after Update = %v", reasons)
	}
	if got := h.agent.ModelName(); got != "second-model" {
		t.Fatalf("agent model after Update = %q, want second-model", got)
	}

	if err := h.ctrl.Models().Delete(model.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(reasons) != 3 || reasons[2] != "model" {
		t.Fatalf("rebuild reasons after Delete = %v", reasons)
	}
}

// TestPerSessionPermissionTriggersRebuild proves switching to a session with a
// different access mode asks the app to rebuild the tool set, instead of
// leaving SessionPermissionOrDefault unused.
func TestPerSessionPermissionTriggersRebuild(t *testing.T) {
	h := newHarness(t, &fakeProvider{respond: echoRespond})
	ctx := context.Background()
	a, err := h.ctrl.CreateSession(ctx, "write session")
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.ctrl.CreateSession(ctx, "read session")
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	h.ctrl.SetAgentRebuilder(func(reason string) error {
		reasons = append(reasons, reason)
		return nil
	})
	// The running agent was built for the default mode.
	if _, err := h.ctrl.OpenSession(ctx, a); err != nil {
		t.Fatal(err)
	}
	h.ctrl.SetBuiltPermission(session.PermissionWorkspace)
	h.ctrl.SetSessionPermission(a, session.PermissionAsk)
	if len(reasons) != 1 || reasons[0] != "permission" {
		t.Fatalf("reasons after mode change = %v, want [permission]", reasons)
	}
	h.ctrl.SetBuiltPermission(session.PermissionAsk)
	// b's mode is set while it is not current, so no rebuild yet; opening it
	// is what must notice the difference.
	h.ctrl.SetSessionPermission(b, session.PermissionFull)
	if len(reasons) != 1 {
		t.Fatalf("setting a non-current session rebuilt: %v", reasons)
	}
	if _, err := h.ctrl.OpenSession(ctx, b); err != nil {
		t.Fatal(err)
	}
	if len(reasons) != 2 || reasons[1] != "permission" {
		t.Fatalf("reasons after opening b = %v, want [permission permission]", reasons)
	}
}
