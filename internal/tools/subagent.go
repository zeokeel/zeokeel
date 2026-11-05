package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/agentteam"
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

"task" is required for a normal run and must describe a complete piece of work:
the subagent starts with only this text (plus the surrounding conversation) and
a restricted tool subset, so it cannot ask follow-up questions. "agent" is an
optional name for the child; it defaults to "subagent".

By default the tool runs the child agent to completion and returns its final
text. With "async":true it starts the child in the background instead and
returns {"kind":"subagent_async","child_id":"agent-1","status":...}
immediately; the completion is reported automatically as a harness advisory in
a later step, and these actions inspect background children:
  {"action":"status","id":"agent-1"}   current status/output of one child
  {"action":"list"}                    every background child
  {"action":"cancel","id":"agent-1"}   interrupt one child
The id (or a unique agent name) is accepted wherever an id is required.

A depth cap (default 2 levels) rejects a call that would nest deeper instead of
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

	// Async delegation (FP-03): async==true spawns a background member through
	// an internal agentteam.Manager; the member's completion is announced to
	// the parent turn through the runtime advisory sink. sink is installed by
	// runtime.New via AdvisorySinkBinder.
	async   *agentteam.Manager
	asyncOK bool
	sink    runtime.AdvisorySink
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
	if t.async != nil {
		t.async.SetRunner(runner)
	}
	t.mu.Unlock()
}

// SetAdvisorySink implements runtime.AdvisorySinkBinder: the runtime installs
// its external-notice sink so a finished async subagent is reported to the
// parent agent in-turn.
func (t *SubagentTool) SetAdvisorySink(sink runtime.AdvisorySink) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.sink = sink
	t.mu.Unlock()
}

// Calls reports how many subagent runs this tool completed. It is used by
// tests and future telemetry; it never blocks on a run.
func (t *SubagentTool) Calls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls
}

// subagentArgs is the decoded subagent argument object. The action field
// defaults to running a task; status/list/cancel inspect async children.
type subagentArgs struct {
	Task   string `json:"task"`
	Agent  string `json:"agent,omitempty"`
	Async  bool   `json:"async,omitempty"`
	Action string `json:"action,omitempty"`
	ID     string `json:"id,omitempty"`
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

	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "", "run":
	case "status":
		return t.asyncStatus(in.ID)
	case "list":
		return t.asyncList()
	case "cancel":
		return t.asyncCancel(in.ID)
	default:
		return "", fmt.Errorf("subagent: unknown action %q (want \"run\", \"status\", \"list\" or \"cancel\" or omit it)", in.Action)
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

	if in.Async {
		return t.spawnAsync(ctx, task, agent, depth)
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

// spawnAsync starts a background subagent and returns its child id. The member
// keeps running on the manager's context after this tool call returns and its
// completion is announced to the parent agent (FP-03).
func (t *SubagentTool) spawnAsync(ctx context.Context, task, agent string, depth int) (string, error) {
	t.mu.Lock()
	mgr, err := t.asyncManagerLocked()
	t.mu.Unlock()
	if err != nil {
		return "", err
	}
	member, err := mgr.Spawn(ctx, agentteam.SpawnRequest{Task: task, Agent: agent, Depth: depth})
	if err != nil {
		return "", fmt.Errorf("subagent: start async task: %w", err)
	}
	go t.notifyAsyncCompletion(member.ID)
	out, err := json.Marshal(struct {
		Kind    string `json:"kind"`
		ChildID string `json:"child_id"`
		Agent   string `json:"agent"`
		Status  string `json:"status"`
		Message string `json:"message"`
	}{
		Kind:    "subagent_async",
		ChildID: member.ID,
		Agent:   member.Name,
		Status:  string(member.Status),
		Message: "Async subagent started; it keeps running in the background. Its completion will be reported automatically; use action \"status\" with this child_id to read the current output.",
	})
	if err != nil {
		return "", fmt.Errorf("subagent: encode async result: %w", err)
	}
	return string(out), nil
}

// asyncManagerLocked returns the internal async manager, creating it on first
// use. t.mu must be held.
func (t *SubagentTool) asyncManagerLocked() (*agentteam.Manager, error) {
	if t.runner == nil {
		return nil, errors.New("subagent: no agent runner configured")
	}
	if t.async == nil {
		t.async = agentteam.New(agentteam.Config{Runner: t.runner})
		t.asyncOK = true
	}
	return t.async, nil
}

// notifyAsyncCompletion waits for one async member to settle and posts a
// model-visible advisory with its outcome. The wait uses a background context so
// it never inherits the spawning turn's cancellation.
func (t *SubagentTool) notifyAsyncCompletion(childID string) {
	t.mu.Lock()
	mgr := t.async
	t.mu.Unlock()
	if mgr == nil {
		return
	}
	member, err := mgr.Wait(context.Background(), childID)
	t.mu.Lock()
	sink := t.sink
	t.mu.Unlock()
	if sink == nil {
		return
	}
	if err != nil {
		sink(fmt.Sprintf("Async subagent %s could not be observed: %v. Use subagent {\"action\":\"status\",\"id\":%q}.", childID, err, childID))
		return
	}
	summary := strings.TrimSpace(member.Output)
	if summary == "" {
		summary = "(no final text)"
	}
	if len(summary) > 400 {
		summary = summary[:400] + "…"
	}
	outcome := string(member.Status)
	if member.Error != "" {
		outcome += ": " + member.Error
	}
	sink(fmt.Sprintf(
		"Async subagent %s (%s) finished (%s). Result: %s\nRead the full state with subagent {\"action\":\"status\",\"id\":%q}; do not spawn the same task again.",
		childID, member.Name, outcome, summary, childID))
}

// findAsyncMember resolves a child id or a unique agent name.
func (t *SubagentTool) findAsyncMemberLocked(target string) (agentteam.Member, error) {
	mgr, err := t.asyncManagerLocked()
	if err != nil {
		return agentteam.Member{}, err
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return agentteam.Member{}, errors.New(`subagent: "id" is required for this action`)
	}
	members := mgr.Members()
	for _, member := range members {
		if member.ID == target {
			return member, nil
		}
	}
	var matches []agentteam.Member
	for _, member := range members {
		if member.Name == target {
			matches = append(matches, member)
		}
	}
	switch len(matches) {
	case 0:
		return agentteam.Member{}, fmt.Errorf("subagent: unknown async child %q (use action \"list\")", target)
	case 1:
		return matches[0], nil
	default:
		return agentteam.Member{}, fmt.Errorf("subagent: %q is ambiguous (%d async children share that name)", target, len(matches))
	}
}

// asyncStatus returns one async child's snapshot as JSON.
func (t *SubagentTool) asyncStatus(target string) (string, error) {
	t.mu.Lock()
	member, err := t.findAsyncMemberLocked(target)
	t.mu.Unlock()
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(struct {
		Kind   string           `json:"kind"`
		Member agentteam.Member `json:"member"`
	}{Kind: "subagent_status", Member: member})
	if err != nil {
		return "", fmt.Errorf("subagent: encode status: %w", err)
	}
	return string(out), nil
}

// asyncList returns every async child of this tool.
func (t *SubagentTool) asyncList() (string, error) {
	t.mu.Lock()
	mgr, err := t.asyncManagerLocked()
	t.mu.Unlock()
	if err != nil {
		return "", err
	}
	members := mgr.Members()
	out, err := json.Marshal(struct {
		Kind    string             `json:"kind"`
		Count   int                `json:"count"`
		Members []agentteam.Member `json:"members"`
	}{Kind: "subagent_list", Count: len(members), Members: members})
	if err != nil {
		return "", fmt.Errorf("subagent: encode list: %w", err)
	}
	return string(out), nil
}

// asyncCancel interrupts one async child.
func (t *SubagentTool) asyncCancel(target string) (string, error) {
	t.mu.Lock()
	mgr, err := t.asyncManagerLocked()
	t.mu.Unlock()
	if err != nil {
		return "", err
	}
	member, err := mgr.Interrupt(strings.TrimSpace(target))
	if err != nil {
		return "", fmt.Errorf("subagent: cancel async child: %w", err)
	}
	out, err := json.Marshal(struct {
		Kind   string           `json:"kind"`
		Member agentteam.Member `json:"member"`
	}{Kind: "subagent_cancel", Member: member})
	if err != nil {
		return "", fmt.Errorf("subagent: encode cancel: %w", err)
	}
	return string(out), nil
}
