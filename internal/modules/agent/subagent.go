package agent

// Synchronous subagent delegation: the agent service exposes
// contracts.Subagents (used by the subagent/subagent_fork tools it registers in
// the shared toolkit) and drives Rig's in-process SubagentRunner.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"
	rigagent "github.com/zeokeel/zeokeel-rig/agent"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// subagentToolName is the synchronous delegation tool.
	subagentToolName = "subagent"
	// subagentForkToolName delegates with a copy of the parent history.
	subagentForkToolName = "subagent_fork"

	// subagentMaxDepth keeps delegation to one level: children never receive
	// orchestration tools, so they cannot start grandchildren.
	subagentMaxDepth = 1
	// subagentMaxConcurrent bounds the live children of one delegation.
	subagentMaxConcurrent = 2
)

// subagentExcludedTools are removed from every child's tool set. The Rig
// defaults already exclude subagent/workflow/team; the product names this
// module and hosttools register are appended so a child can never recurse.
func subagentExcludedTools() []string {
	names := make([]string, 0, len(rigagent.DefaultExcludedTools)+8)
	seen := make(map[string]bool, len(rigagent.DefaultExcludedTools)+8)
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}
	for _, name := range rigagent.DefaultExcludedTools {
		add(name)
	}
	for _, name := range []string{
		subagentToolName,
		subagentForkToolName,
		"ralph",
		"todo",
		"todo_write",
	} {
		add(name)
	}
	// The team tools are registered later in the same module; children must
	// never receive them either.
	for _, name := range teamToolNames() {
		add(name)
	}
	return names
}

// subagentParent resolves the provider config and active session for one
// delegation. The session's effective model wins (per-session override), so a
// delegated child follows the same model as its parent conversation.
func (s *service) subagentParent() (contracts.ChatConfig, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	active := s.active
	cfg := s.configLocked()
	if active != "" {
		cfg = s.configForSessionLocked(active)
	}
	return cfg, active
}

// Run implements contracts.Subagents: it starts one in-process subagent, waits
// for its final answer and stops it. Fork copies the active session's history
// into the child; without an active session the fork request is rejected.
func (s *service) Run(ctx context.Context, req contracts.SubagentRequest) (contracts.SubagentResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return contracts.SubagentResult{}, errors.New("agent: subagent prompt is required")
	}
	if err := ctx.Err(); err != nil {
		return contracts.SubagentResult{}, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = fmt.Sprintf("sub-%d", s.nextSubagentSeq.Add(1))
	}

	cfg, active := s.subagentParent()
	provider, err := s.newProvider(cfg, s.sessionEffort(active))
	if err != nil {
		return contracts.SubagentResult{}, fmt.Errorf("agent: subagent %q: create provider: %w", name, err)
	}
	parent := rig.AgentSpec{
		Provider:      provider,
		Model:         cfg.Model,
		SystemPrompt:  cfg.SystemPrompt,
		MaxIterations: cfg.MaxIterations,
		Tools:         s.registry.Tools(),
		Gate:          s.gate(),
		History:       s.historyView(),
		Logger:        s.logger,
	}
	options := rigagent.RunnerOptions{
		MaxDepth:      subagentMaxDepth,
		MaxConcurrent: subagentMaxConcurrent,
		ExcludedTools: subagentExcludedTools(),
	}
	if req.Fork {
		if active == "" {
			return contracts.SubagentResult{}, errors.New("agent: fork subagent requires an active session")
		}
		options.ParentSessionID = active
		options.ParentHistory = s.historyView()
	}
	runner, err := rigagent.NewRunner(parent, options)
	if err != nil {
		return contracts.SubagentResult{}, fmt.Errorf("agent: subagent %q: create runner: %w", name, err)
	}
	sub, err := runner.Start(ctx, rig.SubagentSpec{
		Name:          name,
		Prompt:        prompt,
		Model:         strings.TrimSpace(req.Model),
		MaxIterations: req.MaxIterations,
		Fork:          req.Fork,
	})
	if err != nil {
		return contracts.SubagentResult{}, fmt.Errorf("agent: start subagent %q: %w", name, err)
	}
	defer func() {
		if stopErr := sub.Stop(); stopErr != nil {
			s.logger.Printf("[agent] stop subagent %s: %v", name, stopErr)
		}
	}()
	result, err := sub.Wait(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return contracts.SubagentResult{}, ctxErr
		}
		return contracts.SubagentResult{}, fmt.Errorf("agent: subagent %q: %w", name, err)
	}
	return contracts.SubagentResult{
		AgentID: name,
		Name:    name,
		Text:    result.Output,
	}, nil
}

// newSubagentTools builds the two delegation tools registered by Module.
func newSubagentTools(svc *service) []rig.Tool {
	return []rig.Tool{
		&subagentTool{service: svc},
		&subagentTool{service: svc, fork: true},
	}
}

// subagentTool is the model-facing synchronous delegation tool.
type subagentTool struct {
	service *service
	fork    bool
}

// Name implements rig.Tool.
func (t *subagentTool) Name() string {
	if t.fork {
		return subagentForkToolName
	}
	return subagentToolName
}

// Description implements rig.Tool.
func (t *subagentTool) Description() string {
	if t.fork {
		return "Delegate a self-contained task to a synchronous in-process subagent that starts from a copy of " +
			"this conversation's history. Use it for independent work that needs the current context. " +
			"Returns the subagent's final answer; the subagent cannot start further subagents."
	}
	return "Delegate a self-contained task to a synchronous in-process subagent with fresh context. " +
		"Use it for independent work such as research or review. Returns the subagent's final answer; " +
		"the subagent cannot start further subagents."
}

// Schema implements rig.Tool.
func (t *subagentTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": map[string]any{
				"type":        "string",
				"description": "The complete, self-contained task for the subagent.",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Optional display name (default: sub-<n>).",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Optional model override for the subagent (default: this session's model).",
			},
			"max_iterations": map[string]any{
				"type":        "integer",
				"description": "Optional model/tool round cap for the subagent (0 uses the default).",
			},
		},
		"required":             []string{"prompt"},
		"additionalProperties": false,
	}
}

// Invoke implements rig.Tool.
func (t *subagentTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	var args struct {
		Prompt        string `json:"prompt"`
		Name          string `json:"name"`
		Model         string `json:"model"`
		MaxIterations int    `json:"max_iterations"`
	}
	arguments := strings.TrimSpace(call.Arguments)
	if arguments != "" && arguments != "null" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return rig.ToolResult{}, fmt.Errorf("%s: invalid arguments: %w", t.Name(), err)
		}
	}
	result, err := t.service.Run(ctx, contracts.SubagentRequest{
		Prompt:        args.Prompt,
		Name:          args.Name,
		Model:         args.Model,
		MaxIterations: args.MaxIterations,
		Fork:          t.fork,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return rig.ToolResult{}, ctxErr
		}
		return rig.ToolResult{Content: t.Name() + ": " + err.Error(), IsError: true}, nil
	}
	text := strings.TrimSpace(result.Text)
	if text == "" {
		text = "(no output)"
	}
	return rig.ToolResult{Content: fmt.Sprintf(
		"subagent %s finished (agent %s):\n%s", result.Name, result.AgentID, text)}, nil
}
