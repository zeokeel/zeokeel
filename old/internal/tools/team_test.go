package tools

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/agentteam"
	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// teamTestBlockingRunner blocks every child turn until its context is canceled.
type teamTestBlockingRunner struct {
	mu      sync.Mutex
	started chan struct{}
	calls   int
}

func (r *teamTestBlockingRunner) RunSubagent(ctx context.Context, _ runtime.SubagentRequest) (runtime.SubagentResult, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	if r.started != nil {
		select {
		case r.started <- struct{}{}:
		default:
		}
	}
	<-ctx.Done()
	return runtime.SubagentResult{}, ctx.Err()
}

// teamTestImmediateRunner returns a deterministic final text.
type teamTestImmediateRunner struct{}

func (teamTestImmediateRunner) RunSubagent(_ context.Context, req runtime.SubagentRequest) (runtime.SubagentResult, error) {
	return runtime.SubagentResult{Agent: req.Agent, Task: req.Task, Output: "done:" + req.Task}, nil
}

func teamTestTools(t *testing.T, cfg TeamConfig) map[string]runtime.Tool {
	t.Helper()
	out := make(map[string]runtime.Tool)
	for _, tool := range NewTeamTools(cfg) {
		out[tool.Name()] = tool
	}
	for _, name := range []string{"list_subagent_models", "send_message", "interrupt_agent"} {
		if out[name] == nil {
			t.Fatalf("missing tool %q (got %v)", name, out)
		}
		if strings.TrimSpace(out[name].Description()) == "" {
			t.Fatalf("tool %q has an empty description", name)
		}
	}
	if len(out) != 3 {
		t.Fatalf("tools = %v, want three", out)
	}
	return out
}

func teamTestWait(t *testing.T, mgr *agentteam.Manager, id string) agentteam.Member {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	member, err := mgr.Wait(ctx, id)
	if err != nil {
		t.Fatalf("Wait(%s): %v", id, err)
	}
	return member
}

func TestTeamToolsListSendAndInterrupt(t *testing.T) {
	runner := &teamTestBlockingRunner{started: make(chan struct{}, 1)}
	mgr := agentteam.New(agentteam.Config{Runner: runner})
	defer mgr.Close()
	tools := teamTestTools(t, TeamConfig{Manager: mgr})

	out, err := sqInvoke(t, tools["list_subagent_models"], `{}`)
	if err != nil {
		t.Fatalf("list_subagent_models: %v", err)
	}
	var listing struct {
		Kind   string              `json:"kind"`
		Agents []agentteam.Member  `json:"agents"`
		Models []subagentModelView `json:"models"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		t.Fatalf("decode list: %v\n%s", err, out)
	}
	if listing.Kind != "subagent_models" || len(listing.Agents) != 0 || len(listing.Models) != 0 {
		t.Fatalf("initial listing = %+v", listing)
	}

	spawned, err := mgr.Spawn(context.Background(), agentteam.SpawnRequest{Task: "long task", Agent: "worker"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	<-runner.started

	out, err = sqInvoke(t, tools["list_subagent_models"], `{}`)
	if err != nil {
		t.Fatalf("list_subagent_models running: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		t.Fatalf("decode running list: %v", err)
	}
	if len(listing.Agents) != 1 || listing.Agents[0].Status != agentteam.StatusRunning {
		t.Fatalf("running listing = %+v", listing.Agents)
	}

	out, err = sqInvoke(t, tools["send_message"], `{"target":"worker","message":"Also check the tests"}`)
	if err != nil {
		t.Fatalf("send_message: %v", err)
	}
	var delivery struct {
		Kind      string `json:"kind"`
		MessageID string `json:"message_id"`
		AgentID   string `json:"agent_id"`
		AgentName string `json:"agent_name"`
		Status    string `json:"status"`
		Pending   int    `json:"pending"`
	}
	if err := json.Unmarshal([]byte(out), &delivery); err != nil {
		t.Fatalf("decode send_message: %v\n%s", err, out)
	}
	if delivery.Kind != "subagent_message" || delivery.MessageID == "" ||
		delivery.AgentID != spawned.ID || delivery.AgentName != "worker" ||
		delivery.Status != "delivered" || delivery.Pending != 1 {
		t.Fatalf("delivery = %+v", delivery)
	}

	out, err = sqInvoke(t, tools["interrupt_agent"], `{"target":"`+spawned.ID+`"}`)
	if err != nil {
		t.Fatalf("interrupt_agent: %v", err)
	}
	var interrupted struct {
		Kind     string `json:"kind"`
		AgentID  string `json:"agent_id"`
		Accepted bool   `json:"accepted"`
	}
	if err := json.Unmarshal([]byte(out), &interrupted); err != nil {
		t.Fatalf("decode interrupt: %v\n%s", err, out)
	}
	if interrupted.Kind != "subagent_interrupt" || !interrupted.Accepted || interrupted.AgentID != spawned.ID {
		t.Fatalf("interrupt = %+v", interrupted)
	}
	settled := teamTestWait(t, mgr, spawned.ID)
	if settled.Status != agentteam.StatusInterrupted || settled.QueueLen != 1 {
		t.Fatalf("settled = %+v", settled)
	}

	out, err = sqInvoke(t, tools["list_subagent_models"], `{}`)
	if err != nil {
		t.Fatalf("final list: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		t.Fatalf("decode final list: %v", err)
	}
	if len(listing.Agents) != 1 || listing.Agents[0].Status != agentteam.StatusInterrupted || listing.Agents[0].QueueLen != 1 {
		t.Fatalf("final listing = %+v", listing.Agents)
	}
}

func TestTeamToolsListConfiguredModelsWithoutSecrets(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	if _, err := st.CreateModel(store.Model{
		ID:      "primary",
		Name:    "Primary",
		BaseURL: "https://example.invalid",
		APIKey:  "super-secret-key",
		Model:   "deepseek-test",
	}); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	mgr := agentteam.New(agentteam.Config{})
	defer mgr.Close()
	tools := teamTestTools(t, TeamConfig{Manager: mgr, Store: st})

	out, err := sqInvoke(t, tools["list_subagent_models"], `{}`)
	if err != nil {
		t.Fatalf("list_subagent_models: %v", err)
	}
	if strings.Contains(out, "super-secret-key") {
		t.Fatalf("listing leaks an API key: %s", out)
	}
	var listing struct {
		Models []subagentModelView `json:"models"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		t.Fatalf("decode listing: %v\n%s", err, out)
	}
	if len(listing.Models) != 1 || listing.Models[0].ID != "primary" ||
		listing.Models[0].Name != "Primary" || listing.Models[0].Model != "deepseek-test" ||
		!listing.Models[0].Default {
		t.Fatalf("models = %+v", listing.Models)
	}
}

func TestTeamToolsBindRunnerAndValidateTargets(t *testing.T) {
	mgr := agentteam.New(agentteam.Config{})
	defer mgr.Close()
	tools := teamTestTools(t, TeamConfig{Manager: mgr})
	for name, tool := range tools {
		binder, ok := tool.(runtime.SubagentRunnerBinder)
		if !ok {
			t.Fatalf("%s does not implement runtime.SubagentRunnerBinder", name)
		}
		binder.SetSubagentRunner(teamTestImmediateRunner{})
	}

	spawned, err := mgr.Spawn(context.Background(), agentteam.SpawnRequest{Task: "work"})
	if err != nil {
		t.Fatalf("Spawn after binding: %v", err)
	}
	settled := teamTestWait(t, mgr, spawned.ID)
	if settled.Status != agentteam.StatusCompleted || settled.Output != "done:work" {
		t.Fatalf("settled = %+v", settled)
	}

	if _, err := sqInvoke(t, tools["send_message"], `{"target":"missing","message":"hello"}`); err == nil {
		t.Fatal("send_message to an unknown target = nil error")
	}
	if _, err := sqInvoke(t, tools["send_message"], `{"target":"`+spawned.ID+`","message":" "}`); err == nil {
		t.Fatal("send_message with a blank message = nil error")
	}
	if _, err := sqInvoke(t, tools["interrupt_agent"], `{"target":"missing"}`); err == nil {
		t.Fatal("interrupt_agent for an unknown target = nil error")
	}
	if _, err := sqInvoke(t, tools["send_message"], `{`); err == nil {
		t.Fatal("send_message with invalid JSON = nil error")
	}
}
