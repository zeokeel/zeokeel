package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

const (
	// DefaultSubagentMaxDepth is the default child-agent nesting cap enforced
	// by the subagent and workflow tools. The runtime enforces its own cap too
	// (runtime.DefaultMaxSubagentDepth); the tighter of the two wins.
	DefaultSubagentMaxDepth = 2

	// SubagentStatusCompleted is the status the controller records for a
	// subagent tool call that returned successfully. Failures surface as tool
	// errors instead and are not added to the team panel.
	SubagentStatusCompleted = "completed"
)

// Teammate is one subagent the session spawned. The controller builds these
// from the subagent tool call's arguments and returned text, so the team panel
// can list the agents that worked on the session without any global state.
type Teammate struct {
	// CallID is the runtime tool call id of the subagent invocation.
	CallID string `json:"call_id,omitempty"`
	// Name is the child agent name ("subagent" by default).
	Name string `json:"name"`
	// Task is the request the child was given.
	Task string `json:"task,omitempty"`
	// Status is "completed" for a finished run.
	Status string `json:"status,omitempty"`
	// Output is the child's final text, kept for the trajectory.
	Output string `json:"output,omitempty"`
	// UpdatedAt is when the controller recorded the teammate.
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// subagentDescription documents the tool and its JSON argument schema.
const subagentDescription = `Delegate one self-contained task to a focused subagent.

Call it with a JSON object:
  {"task":"Read cmd/zeokeel/main.go and summarize the startup flow"}
  {"task":"Run the failing test and explain it","agent":"test-reader"}

"task" is required and must describe a complete piece of work: the subagent
starts with only this text (plus the surrounding conversation) and a restricted
tool subset, so it cannot ask follow-up questions. "agent" is an optional name
for the child; it defaults to "subagent".

The tool runs the child agent to completion and returns its final text. A
depth cap (default 2 levels) rejects a call that would nest deeper instead of
recursing. The controller records the invocation (agent name and task) in the
team panel from the tool call.`

// SubagentConfig configures a SubagentTool.
type SubagentConfig struct {
	// Runner executes the child agent. It is required at call time; the
	// runtime.Agent auto-binds itself when the tool is registered with
	// runtime.New, which solves the registry/agent construction cycle.
	Runner runtime.SubagentRunner
	// MaxDepth caps nesting. Zero means DefaultSubagentMaxDepth.
	MaxDepth int
}

// SubagentTool is the "subagent" tool. All state lives in this instance,
// guarded by mu.
type SubagentTool struct {
	mu       sync.Mutex
	runner   runtime.SubagentRunner
	maxDepth int
	calls    int
}

// NewSubagentTool returns a subagent tool. When cfg.Runner is nil the tool
// reports a clear error until a runner is bound (runtime.New binds itself).
func NewSubagentTool(cfg SubagentConfig) *SubagentTool {
	return &SubagentTool{runner: cfg.Runner, maxDepth: cfg.MaxDepth}
}

// Name implements runtime.Tool.
func (t *SubagentTool) Name() string { return "subagent" }

// Description implements runtime.Tool.
func (t *SubagentTool) Description() string { return subagentDescription }

// SetSubagentRunner implements runtime.SubagentRunnerBinder. runtime.New calls
// it for every registered tool after the Agent exists.
func (t *SubagentTool) SetSubagentRunner(runner runtime.SubagentRunner) {
	t.mu.Lock()
	t.runner = runner
	t.mu.Unlock()
}

// Calls reports how many subagent runs this tool completed. It is used by
// tests and future telemetry; it never blocks on a run.
func (t *SubagentTool) Calls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls
}

// subagentArgs is the decoded subagent argument object.
type subagentArgs struct {
	Task  string `json:"task"`
	Agent string `json:"agent,omitempty"`
}

// Invoke implements runtime.Tool.
func (t *SubagentTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var in subagentArgs
	if len(strings.TrimSpace(string(args))) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", fmt.Errorf("subagent: invalid arguments: %w", err)
		}
	}
	task := strings.TrimSpace(in.Task)
	if task == "" {
		return "", errors.New(`subagent: "task" is required`)
	}
	agent := strings.TrimSpace(in.Agent)

	t.mu.Lock()
	runner := t.runner
	maxDepth := t.maxDepth
	t.mu.Unlock()
	if runner == nil {
		return "", errors.New("subagent: no agent runner configured")
	}
	if maxDepth <= 0 {
		maxDepth = DefaultSubagentMaxDepth
	}

	// The runtime carries the current nesting depth on the tool context, so a
	// child that itself has this tool cannot recurse past the cap.
	depth := runtime.SubagentDepth(ctx) + 1
	if depth > maxDepth {
		return "", fmt.Errorf("subagent: depth limit %d reached", maxDepth)
	}

	result, err := runner.RunSubagent(ctx, runtime.SubagentRequest{
		Task:  task,
		Agent: agent,
		Depth: depth,
	})
	if err != nil {
		return "", fmt.Errorf("subagent: %w", err)
	}

	t.mu.Lock()
	t.calls++
	t.mu.Unlock()
	return result.Output, nil
}
