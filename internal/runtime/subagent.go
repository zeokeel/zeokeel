package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
)

// DefaultMaxSubagentDepth is the default nesting limit for child agents. The
// root agent runs at depth 0; a subagent it spawns runs at depth 1, and a
// subagent that one spawns runs at depth 2. A call that would exceed the limit
// is rejected instead of recursing forever.
const DefaultMaxSubagentDepth = 2

// orchestrationToolNames are the tools that spawn further agents. A child agent
// never receives them by default: that keeps the default subagent tree shallow
// even though the depth counter is the authoritative guard.
var orchestrationToolNames = map[string]bool{
	"subagent": true,
	"workflow": true,
}

// SubagentRequest is one child-agent invocation. Task is the prompt handed to
// the child; Agent optionally names the child (default "subagent") and Depth is
// the 1-based nesting depth of the child, computed by the caller from
// SubagentDepth.
type SubagentRequest struct {
	Task  string `json:"task"`
	Agent string `json:"agent,omitempty"`
	Depth int    `json:"depth,omitempty"`
}

// SubagentResult is the outcome of one child-agent run. Tools is the restricted
// tool subset the child was given, which makes the delegation auditable.
type SubagentResult struct {
	Agent  string   `json:"agent"`
	Task   string   `json:"task"`
	Output string   `json:"output"`
	Depth  int      `json:"depth"`
	Tools  []string `json:"tools,omitempty"`
}

// SubagentRunner runs one child agent and returns its final assistant text. It
// is the seam between the tools package and the eino runtime: tools never see
// eino types.
//
// Agent implements it, so apps only need to hand the runtime.Agent to the
// subagent and workflow tools (runtime.New does that automatically when the
// tool implements SubagentRunnerBinder).
type SubagentRunner interface {
	RunSubagent(ctx context.Context, req SubagentRequest) (SubagentResult, error)
}

// SubagentRunnerBinder is implemented by tools that need the Agent's subagent
// runner. New calls SetSubagentRunner on every registered tool that implements
// this interface, which solves the registry/agent construction cycle: the tool
// can be registered before the Agent exists.
type SubagentRunnerBinder interface {
	SetSubagentRunner(SubagentRunner)
}

// subagentDepthContextKey carries the current nesting depth to nested tools.
type subagentDepthContextKey struct{}

// WithSubagentDepth returns ctx tagged with depth. RunSubagent tags the child
// context itself; the helper is exported so nested runners and tests can model
// a deeper call site.
func WithSubagentDepth(ctx context.Context, depth int) context.Context {
	if depth < 0 {
		depth = 0
	}
	return context.WithValue(ctx, subagentDepthContextKey{}, depth)
}

// SubagentDepth returns the current nesting depth carried by ctx: 0 for the
// root agent, 1 for a child it spawned, and so on. Tools use it to derive the
// depth of the child they are about to run.
func SubagentDepth(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	depth, _ := ctx.Value(subagentDepthContextKey{}).(int)
	if depth < 0 {
		return 0
	}
	return depth
}

// subagentEntry is one cached child agent, the tool names it was given and the
// topology generation it was built for.
type subagentEntry struct {
	agent *adk.ChatModelAgent
	tools []string
	gen   uint64
}

// RunSubagent implements SubagentRunner by running a child eino ChatModelAgent
// through adk.NewAgentTool. The child shares the parent's model, instruction and
// Gate but only sees the restricted tool subset configured by
// Config.SubagentTools (by default every registered tool except "subagent" and
// "workflow"). Child agents are stateless: they do not load or append History,
// compaction or checkpoints.
func (a *Agent) RunSubagent(ctx context.Context, req SubagentRequest) (SubagentResult, error) {
	if ctx == nil {
		return SubagentResult{}, errors.New("runtime: nil context")
	}
	task := strings.TrimSpace(req.Task)
	if task == "" {
		return SubagentResult{}, errors.New("runtime: subagent task is empty")
	}
	if err := ctx.Err(); err != nil {
		return SubagentResult{}, err
	}

	depth := req.Depth
	if depth < 1 {
		depth = 1
	}
	limit := a.maxSubagentDepth
	if limit <= 0 {
		limit = DefaultMaxSubagentDepth
	}
	if depth > limit {
		return SubagentResult{}, fmt.Errorf("runtime: subagent depth %d exceeds limit %d", depth, limit)
	}

	name := strings.TrimSpace(req.Agent)
	if name == "" {
		name = "subagent"
	}

	child, toolNames, err := a.childAgent(ctx, name)
	if err != nil {
		return SubagentResult{}, err
	}

	payload, err := json.Marshal(map[string]string{"request": task})
	if err != nil {
		return SubagentResult{}, fmt.Errorf("runtime: encode subagent request: %w", err)
	}
	agentTool := adk.NewAgentTool(WithSubagentDepth(ctx, depth), child)
	invokable, ok := agentTool.(tool.InvokableTool)
	if !ok {
		return SubagentResult{}, errors.New("runtime: eino agent tool is not invokable")
	}
	output, err := invokable.InvokableRun(WithSubagentDepth(ctx, depth), string(payload))
	if err != nil {
		return SubagentResult{}, fmt.Errorf("runtime: subagent %q: %w", name, err)
	}
	return SubagentResult{
		Agent:  name,
		Task:   task,
		Output: output,
		Depth:  depth,
		Tools:  toolNames,
	}, nil
}

// childAgent returns the cached (or newly built) child agent for name. Child
// agents are shared by every session, exactly like the root agent, and are safe
// for concurrent use.
//
// The parent snapshot is loaded before subMu is taken and passed to the
// builder, so this path never takes agentMu while holding subMu (SetModel takes
// them in the opposite order to clear the cache). Entries carry the topology
// generation: a child built from a snapshot that a concurrent SetMode/SetModel
// has since replaced is ignored and rebuilt instead of surviving the clear.
func (a *Agent) childAgent(ctx context.Context, name string) (*adk.ChatModelAgent, []string, error) {
	snap := a.loadState()
	a.subMu.Lock()
	defer a.subMu.Unlock()
	if entry, ok := a.subagents[name]; ok && entry.gen == snap.gen {
		return entry.agent, entry.tools, nil
	}
	child, err := a.buildSubagentAgent(ctx, name, snap.snap)
	if err != nil {
		return nil, nil, err
	}
	entry := &subagentEntry{agent: child, tools: a.subagentToolNames(snap.snap.mode), gen: snap.gen}
	a.subagents[name] = entry
	return entry.agent, entry.tools, nil
}

// buildSubagentAgent builds one child ChatModelAgent with the restricted tool
// subset for snap.mode. It is also what supervisor mode uses for its default
// subagent.
func (a *Agent) buildSubagentAgent(ctx context.Context, name string, snap agentSnapshot) (*adk.ChatModelAgent, error) {
	if strings.TrimSpace(name) == "" {
		name = "subagent"
	}
	instruction := snap.instruction
	if instruction != "" {
		instruction += "\n\n"
	}
	// A PTC parent's instruction advertises the "ptc" batch tool, but
	// subagentToolNames is deliberately root-only (the batch tool resolves
	// nested calls against the unfiltered registry). Tell the child so its
	// prompt and its bound tool set cannot disagree.
	if snap.mode == ModePTC {
		instruction += "PTC batch is root-only; use individual tools instead of the ptc tool.\n\n"
	}
	instruction += "You are a focused subagent. Complete the single task you are given" +
		" with the tools available to you and reply with your final result."

	childModel := snap.chatModel
	if a.subagentModel != "" && a.provider != nil {
		childModel = newProviderChatModel(a.provider, a.subagentModel)
	}
	child, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          name,
		Description:   "A focused ZeoKeel subagent that completes one delegated task.",
		Instruction:   instruction,
		Model:         childModel,
		MaxIterations: a.einoMaxIterations(),
		Handlers:      a.modelHandlers(),
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: a.restrictedEinoTools(snap.mode)},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("runtime: build subagent %q: %w", name, err)
	}
	return child, nil
}

// subagentToolNames returns the names of the tools a child agent is allowed to
// use in mode, sorted. Config.SubagentTools is an allow-list when non-empty;
// otherwise the parent's model-visible mode tool set minus the orchestration
// tools is allowed.
//
// "Model-visible" means this includes the tools a mode synthesizes, so a
// Creator child gets settings_get/settings_set even when the registry does not
// hold concrete instances. The PTC batch tool is deliberately root-only: it
// resolves nested calls against the registry instead of the child's restricted
// set, so delegating it would let a child reach orchestration tools the filter
// hides.
func (a *Agent) subagentToolNames(mode string) []string {
	allowed := make(map[string]bool, len(a.subagentTools))
	for _, name := range a.subagentTools {
		name = strings.TrimSpace(name)
		if name != "" {
			allowed[name] = true
		}
	}
	names := make([]string, 0, len(allowed))
	for _, t := range a.effectiveTools(mode) {
		name := t.Name()
		if name == ptcToolName {
			continue
		}
		if len(allowed) > 0 {
			if allowed[name] {
				names = append(names, name)
			}
			continue
		}
		if orchestrationToolNames[name] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// restrictedEinoTools wraps the child tool subset for mode, preserving the Gate
// and reviewer so a child tool call still asks for approval and is reviewed
// automatically exactly like a root call. It binds the same effective tools the
// parent sees (including a mode's synthesized settings tools), so the child's
// advertised and bound tool sets cannot disagree.
func (a *Agent) restrictedEinoTools(mode string) []tool.BaseTool {
	wanted := make(map[string]bool)
	for _, name := range a.subagentToolNames(mode) {
		wanted[name] = true
	}
	out := make([]tool.BaseTool, 0, len(wanted))
	for _, t := range a.effectiveTools(mode) {
		if !wanted[t.Name()] {
			continue
		}
		out = append(out, newEinoTool(t, a.gate, a.reviewer))
	}
	return out
}

// bindSubagentRunners hands the Agent's runner to every registered tool that
// wants it. New calls it once after construction; the registry itself stays
// sealed and immutable.
func (a *Agent) bindSubagentRunners() {
	for _, t := range a.registry.List() {
		if binder, ok := t.(SubagentRunnerBinder); ok {
			binder.SetSubagentRunner(a)
		}
	}
}

// ApprovalGateBinder is implemented by tools that enforce approval themselves
// on nested calls (the PTC batch tool). New hands them the same Gate the root
// topology uses, so an inner call cannot skip approval just because the outer
// "ptc" call was allowed.
type ApprovalGateBinder interface {
	SetApprovalGate(Gate)
}

// bindApprovalGates passes a.gate to every registered tool that implements
// ApprovalGateBinder. It runs once from New, before the agent is used.
func (a *Agent) bindApprovalGates() {
	if a.gate == nil {
		return
	}
	for _, t := range a.registry.List() {
		if binder, ok := t.(ApprovalGateBinder); ok {
			binder.SetApprovalGate(a.gate)
		}
	}
}

// ToolReviewBinder is implemented by tools that enforce automatic review
// themselves on nested calls (the PTC batch tool). New hands them the same
// reviewer the root topology uses, so an inner mutating call cannot skip review
// just because the outer "ptc" call was allowed.
type ToolReviewBinder interface {
	SetToolReviewer(ToolReviewer)
}

// bindToolReviewers passes a.reviewer to every registered tool that implements
// ToolReviewBinder. A nil reviewer means the agent uses the ordinary approval
// prompt only. It runs once from New, before the agent is used.
func (a *Agent) bindToolReviewers() {
	if a.reviewer == nil {
		return
	}
	for _, t := range a.registry.List() {
		if binder, ok := t.(ToolReviewBinder); ok {
			binder.SetToolReviewer(a.reviewer)
		}
	}
}
