package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

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

// subagentEntry is one cached child agent, the tool names it was given, the
// topology generation it was built for, and whether a run currently owns it.
// eino's ChatModelAgent must not be driven by two goroutines at once, so a busy
// entry is never handed out; a concurrent caller builds its own child instead
// (child construction is cheap relative to a model turn).
type subagentEntry struct {
	agent *adk.ChatModelAgent
	tools []string
	gen   uint64
	busy  bool
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

	child, toolNames, release, err := a.acquireChildAgent(ctx, name)
	if err != nil {
		return SubagentResult{}, err
	}
	defer release()

	payload, err := json.Marshal(map[string]string{"request": task})
	if err != nil {
		return SubagentResult{}, fmt.Errorf("runtime: encode subagent request: %w", err)
	}
	// Delegated runs have no user approval channel (the async Agent Teams
	// runner has no controller turn to attach approval cards to). Mark the
	// context so a gated tool fails with actionable guidance instead of a
	// generic "outside an agent turn" error.
	runCtx := WithSubagentDepth(WithDelegatedApproval(ctx), depth)
	agentTool := adk.NewAgentTool(runCtx, child)
	invokable, ok := agentTool.(tool.InvokableTool)
	if !ok {
		return SubagentResult{}, errors.New("runtime: eino agent tool is not invokable")
	}
	output, err := invokable.InvokableRun(runCtx, string(payload))
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

// acquireChildAgent returns a child agent for name together with a release
// function the caller must call when the run finishes. An idle cached entry is
// reused; a busy entry (a concurrent RunSubagent is driving that eino agent) or
// a missing one gets a freshly built child that is cached only when the slot is
// still free. This keeps parallel team members from driving the same
// ChatModelAgent concurrently, which eino does not support (Agent.runMu exists
// for exactly that reason on the root path).
//
// The parent snapshot is loaded before subMu is taken and passed to the
// builder, so this path never takes agentMu while holding subMu (SetModel takes
// them in the opposite order to clear the cache). Entries carry the topology
// generation: a child built from a snapshot that a concurrent SetMode/SetModel
// has since replaced is ignored and rebuilt instead of surviving the clear.
func (a *Agent) acquireChildAgent(ctx context.Context, name string) (*adk.ChatModelAgent, []string, func(), error) {
	snap := a.loadState()
	a.subMu.Lock()
	if entry, ok := a.subagents[name]; ok && entry.gen == snap.gen && !entry.busy {
		entry.busy = true
		a.subMu.Unlock()
		return entry.agent, entry.tools, func() { a.releaseChildAgent(name, entry) }, nil
	}
	a.subMu.Unlock()

	// Build outside the lock: building is not free and two different calls may
	// legitimately race here (each then gets its own agent).
	child, err := a.buildSubagentAgent(ctx, name, snap.snap)
	if err != nil {
		return nil, nil, nil, err
	}
	tools := a.subagentToolNames(snap.snap.mode)

	a.subMu.Lock()
	if entry, ok := a.subagents[name]; ok && entry.gen == snap.gen && entry.busy {
		// Another run owns the cached entry: this run keeps its private child.
		a.subMu.Unlock()
		return child, tools, func() {}, nil
	}
	entry := &subagentEntry{agent: child, tools: tools, gen: snap.gen, busy: true}
	a.subagents[name] = entry
	a.subMu.Unlock()
	return child, tools, func() { a.releaseChildAgent(name, entry) }, nil
}

// releaseChildAgent marks a cached entry idle again. The entry is only touched
// when it is still the one this run acquired (a SetModel clear or a newer
// build may have replaced it).
func (a *Agent) releaseChildAgent(name string, entry *subagentEntry) {
	a.subMu.Lock()
	defer a.subMu.Unlock()
	if current, ok := a.subagents[name]; ok && current == entry {
		entry.busy = false
	}
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
// hold concrete instances.
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
// on nested calls. New hands them the same Gate the root
// topology uses, so an inner call cannot skip approval just because the outer
// outer call was allowed.
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
// themselves on nested calls. New hands them the same
// reviewer the root topology uses, so an inner mutating call cannot skip review
// just because the outer call was allowed.
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

// delegatedApprovalGuidance is the model-facing recovery instruction when a
// delegated agent hits a tool that requires user approval: it names the
// non-mutating tools it can still use and tells it to report the blocked action
// for the parent to perform. Phrased without "retry", because retrying the same
// gated call cannot succeed in a delegated run.
const delegatedApprovalGuidance = "Use non-mutating tools (read_file, glob, grep, read_image, skill, memory, session_query, todo, present) " +
	"and report the blocked action so the parent agent can perform it; do not retry the same call."

// delegatedApprovalKey marks a context whose run is a delegated agent (subagent
// or Agent Teams member) with no interactive approval channel. Tool calls that
// would need user approval fail closed with actionable guidance instead of
// surfacing a generic "outside an agent turn" runtime error, which the model
// could only retry in a loop.
type delegatedApprovalKey struct{}

// WithDelegatedApproval marks ctx as belonging to a delegated agent run. It is
// applied by RunSubagent; an inherited turn context (if any) still wins, since
// it provides a real approval channel.
func WithDelegatedApproval(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, delegatedApprovalKey{}, true)
}

// delegatedApprovalFromContext reports whether ctx belongs to a delegated run
// without an approval channel.
func delegatedApprovalFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	marked, _ := ctx.Value(delegatedApprovalKey{}).(bool)
	return marked
}

// turnApprovalContext combines two contexts: the turn state (including the
// approval channel) comes from parent, while cancellation, deadline and the
// child's own values come from child. It lets an async Agent Teams member keep
// the spawning turn's approval channel (so gated tools can still ask the user
// through the parent session) without being cancelled when that turn ends or
// the parent tool context is released.
type turnApprovalContext struct {
	parent context.Context
	child  context.Context
}

func (c *turnApprovalContext) Deadline() (time.Time, bool) { return c.child.Deadline() }
func (c *turnApprovalContext) Done() <-chan struct{}       { return c.child.Done() }
func (c *turnApprovalContext) Err() error                  { return c.child.Err() }

// Value prefers the child's own values and falls back to the parent's. The
// parent's turnContext is what carries the event sink and reviewer context;
// the child never sets one, so the fallback is the approval channel.
func (c *turnApprovalContext) Value(key any) any {
	if value := c.child.Value(key); value != nil {
		return value
	}
	return c.parent.Value(key)
}

// WithTurnApprovalFrom returns child extended with parent's turn approval
// channel (its event sink, user request and review context) while keeping
// child's cancellation, deadline and values. It is used by the Agent Teams
// runner so an asynchronous member can still surface a tool approval request in
// the session that spawned it.
//
// The child's own values win; the parent's are the fallback, so a context value
// placed on the spawning tool context (including the turn context) remains
// visible while cancellation stays with the child. A parent without a turn
// context simply contributes no approval channel: a gated tool in the delegated
// run then fails closed with actionable guidance (see
// delegatedApprovalGuidance), and HasTurnApproval reports false. After the
// parent turn has ended its sink is closed, so a later gated call also fails
// closed instead of hanging.
func WithTurnApprovalFrom(parent, child context.Context) context.Context {
	if child == nil {
		child = context.Background()
	}
	if parent == nil || parent == child {
		return child
	}
	return &turnApprovalContext{parent: parent, child: child}
}

// HasTurnApproval reports whether ctx carries a live turn approval channel,
// either the running turn's own context or one inherited through
// WithTurnApprovalFrom. Callers use it to decide whether a gated tool may ask
// the user instead of failing closed.
func HasTurnApproval(ctx context.Context) bool {
	tc := turnContextFromContext(ctx)
	return tc != nil && tc.sink != nil
}
