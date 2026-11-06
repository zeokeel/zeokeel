package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/zeokeel/zeokeel/old/internal/agentteam"
	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// TeamConfig configures the asynchronous subagent control tools.
//
// Manager is the shared async team. When it is nil the tools create a private
// manager from Runner, so a parent may either wire one long-lived manager or
// let the toolset own it. Runner is optional here because runtime.New binds the
// parent Agent through runtime.SubagentRunnerBinder after registration; each
// tool forwards that binding to the shared manager. Store is optional and only
// supplies the model list; subagent state itself is never persisted.
type TeamConfig struct {
	Manager *agentteam.Manager
	Runner  runtime.SubagentRunner
	Store   *store.Store
}

const (
	// teamMaxOutput caps one subagent-control result (64 KiB).
	teamMaxOutput = DefaultMaxOutputBytes
)

const listSubagentModelsDescription = `List the running async subagent team and the models configured for the app.

Call it with an empty JSON object:
  {}

The tool returns {"kind":"subagent_models","agents":[{id,name,status,task,
current_task,output,error,turns,queue_len,...}],"models":[{id,name,model,
default}]}. "status" is one of queued, running, completed, failed or
interrupted. Use the agent id (or a unique agent name) as the "target" of
send_message and interrupt_agent. The model list is empty when no store is
configured.`

const sendMessageDescription = `Send a message to a team subagent.

Call it with a JSON object:
  {"target":"agent-1","message":"Also check the failing test"}

"target" is the agent id returned by the team listing (a unique agent name is
also accepted) and "message" is required.

A running agent receives the message at its next turn boundary; an idle
completed, failed or interrupted agent starts a new turn with it. The call
returns acceptance only:
{"kind":"subagent_message","message_id":...,"target":...,"agent_id":...,
"agent_name":...,"status":"delivered","pending":N}. The agent's reply is never
returned through this tool; list the team to read its latest output.`

const interruptAgentDescription = `Ask a team subagent to stop its current work.

Call it with a JSON object:
  {"target":"agent-1"}

"target" is the agent id (or a unique agent name). The call requests
cancellation and returns immediately:
{"kind":"subagent_interrupt","target":...,"agent_id":...,"status":...,
"accepted":true}. A local agent's queued messages stay parked until a later
send_message; interrupting an idle agent is an accepted no-op. Subagents that
the target started are not interrupted.`

// teamToolBase is embedded by every team tool. Its SetSubagentRunner method is
// promoted, so runtime.New binds the parent Agent even though the tools were
// registered before it existed.
type teamToolBase struct {
	mgr *agentteam.Manager
	st  *store.Store
}

// SetSubagentRunner implements runtime.SubagentRunnerBinder.
func (b *teamToolBase) SetSubagentRunner(runner runtime.SubagentRunner) {
	if b.mgr != nil {
		b.mgr.SetRunner(runner)
	}
}

// NewTeamTools returns list_subagent_models, send_message and interrupt_agent.
func NewTeamTools(cfg TeamConfig) []runtime.Tool {
	mgr := cfg.Manager
	if mgr == nil {
		mgr = agentteam.New(agentteam.Config{Runner: cfg.Runner})
	} else if cfg.Runner != nil {
		mgr.SetRunner(cfg.Runner)
	}
	base := teamToolBase{mgr: mgr, st: cfg.Store}
	return []runtime.Tool{
		&listSubagentModelsTool{teamToolBase: base},
		&sendMessageTool{teamToolBase: base},
		&interruptAgentTool{teamToolBase: base},
	}
}

// subagentModelView is the secret-free projection of a configured model.
type subagentModelView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Model   string `json:"model"`
	Default bool   `json:"default,omitempty"`
}

// subagentModelsResult is the listing result.
type subagentModelsResult struct {
	Kind   string              `json:"kind"`
	Agents []agentteam.Member  `json:"agents"`
	Models []subagentModelView `json:"models"`
	Note   string              `json:"note,omitempty"`
}

// listSubagentModelsTool implements list_subagent_models.
type listSubagentModelsTool struct {
	teamToolBase
}

func (t *listSubagentModelsTool) Name() string        { return "list_subagent_models" }
func (t *listSubagentModelsTool) Description() string { return listSubagentModelsDescription }

func (t *listSubagentModelsTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if err := teamContext(ctx); err != nil {
		return "", err
	}
	var in struct{}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	if t.mgr == nil {
		return "", errors.New("list_subagent_models: no team manager configured")
	}
	agents := t.mgr.Members()
	if agents == nil {
		agents = []agentteam.Member{}
	}
	models := make([]subagentModelView, 0)
	if t.st != nil {
		list, err := t.st.ListModels()
		if err != nil {
			return "", fmt.Errorf("list_subagent_models: %w", err)
		}
		for _, model := range list {
			models = append(models, subagentModelView{
				ID:      model.ID,
				Name:    model.Name,
				Model:   model.Model,
				Default: model.Default,
			})
		}
	}
	return boundedToolJSON(subagentModelsResult{
		Kind:   "subagent_models",
		Agents: agents,
		Models: models,
	}, teamMaxOutput, t.Name())
}

// sendMessageTool implements send_message.
type sendMessageTool struct {
	teamToolBase
}

func (t *sendMessageTool) Name() string        { return "send_message" }
func (t *sendMessageTool) Description() string { return sendMessageDescription }

func (t *sendMessageTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if err := teamContext(ctx); err != nil {
		return "", err
	}
	var in struct {
		Target  string `json:"target"`
		Message string `json:"message"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	target := strings.TrimSpace(in.Target)
	if target == "" {
		return "", errors.New(`send_message: "target" is required`)
	}
	message := strings.TrimSpace(in.Message)
	if message == "" {
		return "", errors.New(`send_message: "message" is required and must contain non-whitespace text`)
	}
	if t.mgr == nil {
		return "", errors.New("send_message: no team manager configured")
	}
	delivery, err := t.mgr.Send(target, message)
	if err != nil {
		return "", fmt.Errorf("send_message: %w", err)
	}
	out := struct {
		Kind      string `json:"kind"`
		MessageID string `json:"message_id"`
		Target    string `json:"target"`
		AgentID   string `json:"agent_id"`
		AgentName string `json:"agent_name"`
		Status    string `json:"status"`
		Pending   int    `json:"pending"`
	}{
		Kind:      "subagent_message",
		MessageID: delivery.MessageID,
		Target:    target,
		AgentID:   delivery.AgentID,
		AgentName: delivery.AgentName,
		Status:    delivery.Status,
		Pending:   delivery.Pending,
	}
	return boundedToolJSON(out, teamMaxOutput, t.Name())
}

// interruptAgentTool implements interrupt_agent.
type interruptAgentTool struct {
	teamToolBase
}

func (t *interruptAgentTool) Name() string        { return "interrupt_agent" }
func (t *interruptAgentTool) Description() string { return interruptAgentDescription }

func (t *interruptAgentTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if err := teamContext(ctx); err != nil {
		return "", err
	}
	var in struct {
		Target string `json:"target"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	target := strings.TrimSpace(in.Target)
	if target == "" {
		return "", errors.New(`interrupt_agent: "target" is required`)
	}
	if t.mgr == nil {
		return "", errors.New("interrupt_agent: no team manager configured")
	}
	member, err := t.mgr.Interrupt(target)
	if err != nil {
		return "", fmt.Errorf("interrupt_agent: %w", err)
	}
	out := struct {
		Kind     string           `json:"kind"`
		Target   string           `json:"target"`
		AgentID  string           `json:"agent_id"`
		Status   agentteam.Status `json:"status"`
		Accepted bool             `json:"accepted"`
	}{
		Kind:     "subagent_interrupt",
		Target:   target,
		AgentID:  member.ID,
		Status:   member.Status,
		Accepted: true,
	}
	return boundedToolJSON(out, teamMaxOutput, t.Name())
}

// teamContext normalizes and checks a tool context.
func teamContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
