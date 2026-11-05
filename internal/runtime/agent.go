package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/zeokeel/zeokeel/internal/state"
)

// DefaultMaxIterations is the per-turn ceiling when the user has not configured
// one. Zero means no fixed ceiling: like DSH's agent loop (an unbounded
// model/tool loop), the model decides when the turn ends by answering without
// tool calls; the user can always stop it. A positive Config.MaxIterations
// restores a user-chosen safety cap, and reaching that cap ends the turn
// gracefully (see Agent.completeMaxIterations).
const DefaultMaxIterations = 0

// MaxIterationsLimit is the largest explicit per-turn ceiling the Plugins editor
// accepts. The setting is a user opt-in safety bound, not the default.
const MaxIterationsLimit = 1000

// unlimitedEinoIterations is the positive sentinel handed to eino when ZeoKeel
// has no fixed ceiling. eino's react state treats a configured MaxIterations
// <= 0 as "unset" and falls back to its own default 20
// (eino/adk/react.go genReactState/genAgenticReactState), so 0 cannot express
// "unlimited"; the sentinel is far beyond any real turn and still decrements
// safely.
const unlimitedEinoIterations = 1_000_000_000

// Config configures an Agent. Provider is the production path; einoModel is a
// test seam for injecting an eino model directly (see the package tests) and
// is intentionally unexported so eino does not leak into the public API.
type Config struct {
	// Name is the agent name used by eino for tracing and agent-tool routing.
	// Optional; defaults to "zeokeel".
	Name string
	// Instruction is the system prompt for every turn.
	Instruction string
	// InstructionFunc, when set, is called at the start of every turn to
	// recompute the instruction. The dynamic runtime-context sections (working
	// directory, reference time, mode) must follow the session, so the app
	// supplies this instead of freezing the text at agent build time. Its
	// result replaces Instruction and rebuilds the topology only when the text
	// actually changed.
	InstructionFunc func() string
	// ModelName is forwarded to Provider requests (e.g. "deepseek-v4.1-flash").
	ModelName string
	// Provider is the model backend. Required unless einoModel is set.
	Provider Provider
	// Tools is the tool registry exposed to the model. Optional.
	Tools *Registry
	// MaxIterations caps model/tool round trips per turn. Zero or negative means
	// no fixed ceiling: the model ends the turn itself by answering without tool
	// calls (the DSH behavior). A positive value is a user-chosen safety cap;
	// reaching it completes the turn gracefully with StepLimitNotice shown.
	MaxIterations int
	// StepLimitNotice returns the assistant text shown when a turn reaches
	// MaxIterations. The app supplies it so the notice follows the UI language;
	// nil uses a concise English notice. Because reaching the limit completes
	// the turn (history and checkpoint are saved), the text should tell the
	// user how to continue.
	StepLimitNotice func(limit int) string
	// BudgetWarnSteps and BudgetWarnTokens raise soft cost advisories after the
	// given per-turn step/token counts are crossed, as DSH does: the advisory
	// is injected into the tool result the model sees (and surfaced in the
	// footer) but the turn never stops. nil means the defaults (25/50/100 steps
	// and 200K/500K output tokens); a non-nil empty slice disables that
	// dimension.
	BudgetWarnSteps  []int
	BudgetWarnTokens []int
	// BudgetAdvisory renders the model-visible advisory text for a crossing.
	// kind is "steps" or "tokens"; steps and tokens are the turn's totals.
	// nil uses a concise English notice.
	BudgetAdvisory func(kind string, steps, tokens int) string
	// History persists conversation state across turns. Nil means the Agent is
	// stateless and every Run starts from an empty conversation (or from
	// CheckpointStore when that is configured).
	History History
	// Gate approves tool calls before they execute. Nil means tools run
	// without approval.
	Gate Gate
	// ToolReviewer, when set, is consulted before a tool call that the Gate
	// requires approval for. An allow decision runs the tool without publishing
	// an ApprovalRequest; a deny decision asks the user (the published request
	// carries ReviewReason), so the user can override the recommendation; a
	// reviewer error falls back to the ordinary approval prompt. Read-only
	// calls (which the Gate never requires) and full-access sessions (whose
	// Gate requires nothing) never reach it. Nil preserves the pre-review
	// behavior exactly.
	ToolReviewer ToolReviewer
	// ReviewContext, when set, supplies the automatic reviewer's environment
	// for each review: the session's current working directory (the
	// working_directory tool may move it within a turn) and the project
	// constraints (AGENTS.md). It is called per review, so both stay current.
	// Nil leaves the reviewer's environment empty; it only affects
	// ToolReviewer, never an ordinary approval.
	ReviewContext func() (workDir, constraints string)
	// Compactor condenses the prior conversation when its estimated size
	// exceeds ContextBudget. Nil disables compaction. WindowCompactor is the
	// built-in, model-free implementation. The compactor runs in the turn
	// goroutine (not in Run/Submit); its error becomes a turn error event.
	Compactor Compactor
	// ContextBudget is the approximate prompt budget in tokens (estimated as
	// characters/4) checked before each turn. Zero or negative disables
	// compaction.
	ContextBudget int
	// CheckpointStore saves a checkpoint after every successful turn. When
	// History is nil it is also read at the start of a turn to seed the
	// conversation; when History is configured (the production app does this)
	// History is authoritative and the checkpoint is a best-effort mirror that
	// is written but not read. Nil disables checkpointing.
	CheckpointStore CheckpointStore

	// Mode selects the agent preset and its model-visible tool set (see
	// Modes). "" and "standard" expose the full registry. "ptc" adds the
	// pipeline-style "ptc" batch tool, "minimal" exposes only terminal_open,
	// terminal_list, terminal_read, terminal_send, terminal_signal,
	// terminal_close, read_image and working_directory, and "creator" adds
	// settings_get and settings_set. The legacy "deep", "supervisor" and
	// "planexecute" values remain accepted as internal topology aliases.
	//
	// "deep", "supervisor" and "planexecute" are meant to be used with a
	// Provider: their built-in tools are bound through model options, and
	// "planexecute" additionally requires a model.ToolCallingChatModel (the
	// Provider adapter is one; the unexported eino model test seam must
	// implement it too).
	//
	// History, Compactor, ContextBudget, CheckpointStore and Gate keep working
	// in every mode because they are applied around the ADK run. Instruction is
	// used by standard, deep and supervisor; planexecute uses eino's own
	// planner/executor/replanner prompts.
	Mode string
	// Settings backs the Creator mode settings_get/settings_set tools. Nil
	// keeps those tools visible but makes them report that no settings store is
	// configured; the app passes its store.
	Settings SettingsStore
	// SubagentTools is the allow-list of tool names a child agent may use. Empty
	// means every registered tool except the orchestration tools ("subagent",
	// "workflow"). Names that are not registered are ignored.
	SubagentTools []string
	// MaxSubagentDepth caps child-agent nesting. Zero means
	// DefaultMaxSubagentDepth (2).
	MaxSubagentDepth int
	// SubagentModel, when non-empty, is the model name child agents request.
	// Empty means children share the parent's model.
	SubagentModel string

	// einoModel is a white-box test seam: tests inject a fake eino chat model
	// here to script streaming/tool-call behaviour without a Provider.
	einoModel model.BaseModel[*schema.Message]
}

// agentSnapshot is the set of mutable build inputs consumed by buildMode. It is
// threaded through the builders explicitly so none of them has to read Agent
// fields (and take agentMu) while a rebuild is in progress.
type agentSnapshot struct {
	mode        string
	instruction string
	modelName   string
	chatModel   model.BaseModel[*schema.Message]
}

// agentState is the immutable topology published after a successful rebuild.
// Readers load the pointer atomically, which keeps Mode/Instruction/ModelName
// and the per-turn topology consistent without taking agentMu. gen changes on
// every publish so cached children can detect a stale build.
type agentState struct {
	snap     agentSnapshot
	adkAgent adk.Agent
	// strict makes the runtime reject tool names missing from the registry.
	// Standard mode keeps that behavior; the prebuilt modes own additional
	// built-in tools (write_todos, task, transfer_to_agent, ...) that are not
	// registered here, so they must be left to eino.
	strict bool
	gen    uint64
}

// Agent is ZeoKeel's agent runtime: one eino agent topology shared by all
// sessions, streaming each turn as state events on a per-call channel.
//
// Agent is safe for concurrent use. Run calls may arrive concurrently, but
// their eino turns are serialized internally: eino's ChatModelAgent keeps
// per-run state on the agent itself, so two simultaneous Runner.Run calls race.
// A queued turn starts as soon as the previous one finishes; each Run call still
// gets its own session id and output channel.
type Agent struct {
	registry      *Registry
	history       History
	compactor     Compactor
	contextBudget int
	checkpoints   CheckpointStore

	name          string
	instrFunc     func() string
	gate          Gate
	reviewer      ToolReviewer
	reviewContext func() (workDir, constraints string)
	maxIterations int
	// stepLimitNotice renders the assistant notice emitted when a turn reaches
	// maxIterations; it is never nil after New.
	stepLimitNotice func(limit int) string
	// Soft per-turn cost advisories (never a hard stop): the thresholds are
	// sorted once in New and the advisory renderer is never nil.
	budgetSteps    []int
	budgetTokens   []int
	budgetAdvisory func(kind string, steps, tokens int) string
	settings       SettingsStore

	subagentTools    []string
	maxSubagentDepth int
	subagentModel    string
	provider         Provider

	// agentMu serializes topology rebuilds so updating the mode/instruction/
	// model and publishing the rebuilt topology is one atomic step. Read paths
	// (Mode, Instruction, ModelName, current) never take it: they load
	// currentState, so a builder that needs the mode cannot self-deadlock.
	agentMu      sync.Mutex
	currentState atomic.Pointer[agentState]

	// runMu serializes eino turns. eino's TypedChatModelAgent stores per-run
	// state (the shared reactConfig and its cancel context) on the agent, so
	// two concurrent Runner.Run calls over the same topology race. The turn
	// goroutine takes this lock, not Run itself, so Submit stays asynchronous:
	// a second session queues its turn instead of blocking the caller.
	runMu sync.Mutex

	subMu     sync.Mutex
	subagents map[string]*subagentEntry

	// External advisories (FP-15): background jobs and async subagents finish
	// outside a turn. Their notices go to the running turn's advisory queue
	// when one is active, otherwise they wait for the next turn's prompt.
	advisoryMu        sync.Mutex
	pendingAdvisories []string
	activeTurn        atomic.Pointer[turnContext]
}

// New builds an Agent from cfg.
func New(cfg Config) (*Agent, error) {
	if cfg.Provider == nil && cfg.einoModel == nil {
		return nil, errors.New("runtime: Config.Provider is required")
	}
	if cfg.Provider != nil && cfg.einoModel != nil {
		return nil, errors.New("runtime: Config.Provider and the internal eino model seam are mutually exclusive")
	}

	mode, err := NormalizeMode(cfg.Mode)
	if err != nil {
		return nil, err
	}

	name := cfg.Name
	if name == "" {
		name = "zeokeel"
	}
	maxIterations := cfg.MaxIterations
	if maxIterations <= 0 {
		// Zero is the default "no fixed ceiling"; a negative configuration means
		// the same. Normalize it so MaxIterations() never reports a negative.
		maxIterations = DefaultMaxIterations
	}

	var chatModel model.BaseModel[*schema.Message]
	if cfg.Provider != nil {
		chatModel = newProviderChatModel(cfg.Provider, cfg.ModelName)
	} else {
		chatModel = cfg.einoModel
	}

	registry := cfg.Tools
	if registry == nil {
		registry = NewRegistry()
	}
	registry.seal()

	maxDepth := cfg.MaxSubagentDepth
	if maxDepth <= 0 {
		maxDepth = DefaultMaxSubagentDepth
	}

	stepLimitNotice := cfg.StepLimitNotice
	if stepLimitNotice == nil {
		stepLimitNotice = defaultStepLimitNotice
	}
	budgetSteps := normalizeWarnThresholds(cfg.BudgetWarnSteps, defaultBudgetWarnSteps)
	budgetTokens := normalizeWarnThresholds(cfg.BudgetWarnTokens, defaultBudgetWarnTokens)
	budgetAdvisory := cfg.BudgetAdvisory
	if budgetAdvisory == nil {
		budgetAdvisory = defaultBudgetAdvisory
	}

	a := &Agent{
		registry:         registry,
		history:          cfg.History,
		compactor:        cfg.Compactor,
		contextBudget:    cfg.ContextBudget,
		checkpoints:      cfg.CheckpointStore,
		name:             name,
		instrFunc:        cfg.InstructionFunc,
		gate:             cfg.Gate,
		reviewer:         cfg.ToolReviewer,
		reviewContext:    cfg.ReviewContext,
		maxIterations:    maxIterations,
		stepLimitNotice:  stepLimitNotice,
		budgetSteps:      budgetSteps,
		budgetTokens:     budgetTokens,
		budgetAdvisory:   budgetAdvisory,
		settings:         cfg.Settings,
		subagentTools:    append([]string(nil), cfg.SubagentTools...),
		maxSubagentDepth: maxDepth,
		subagentModel:    strings.TrimSpace(cfg.SubagentModel),
		provider:         cfg.Provider,
		subagents:        make(map[string]*subagentEntry),
	}

	snap := agentSnapshot{
		mode:        mode,
		instruction: cfg.Instruction,
		modelName:   cfg.ModelName,
		chatModel:   chatModel,
	}
	adkAgent, err := a.buildMode(snap)
	if err != nil {
		return nil, err
	}
	a.currentState.Store(&agentState{
		snap:     snap,
		adkAgent: adkAgent,
		strict:   strictMode(mode),
		gen:      1,
	})
	a.bindSubagentRunners()
	a.bindApprovalGates()
	a.bindToolReviewers()
	a.bindAdvisorySinks()
	return a, nil
}

// HasReviewContext reports whether the agent supplies the reviewer's
// environment (working directory and project constraints). It lets the app
// assert its Auto review wiring without exposing the closure.
func (a *Agent) HasReviewContext() bool {
	return a != nil && a.reviewContext != nil
}

// HasToolReviewer reports whether the agent was built with an automatic tool
// reviewer. It lets the app assert its permission wiring without exposing the
// reviewer itself.
func (a *Agent) HasToolReviewer() bool {
	return a != nil && a.reviewer != nil
}

// loadState returns the current immutable topology snapshot. It is safe to call
// from anywhere, including while agentMu is held by a rebuild.
func (a *Agent) loadState() *agentState { return a.currentState.Load() }

// Instruction returns the system prompt the agent runs with.
func (a *Agent) Instruction() string { return a.loadState().snap.instruction }

// ToolNames returns the model-visible tool names for the current mode, sorted.
// The registry may hold tools another mode owns (Creator's settings tools, the
// app-registered "ptc" tool); unlike Registry.List this reflects what the model
// actually sees.
func (a *Agent) ToolNames() []string {
	mode := a.Mode()
	list := a.effectiveTools(mode)
	names := make([]string, 0, len(list))
	for _, t := range list {
		names = append(names, t.Name())
	}
	return names
}

// EffectiveTools returns the model-visible tools for the current mode, sorted.
// It is the inspectable form of ToolNames and includes the tools a mode
// synthesizes ("ptc" or settings_get/settings_set) when they are not in the
// registry.
func (a *Agent) EffectiveTools() []Tool { return a.effectiveTools(a.Mode()) }

// ToolByName returns the model-visible tool named name in the current mode.
func (a *Agent) ToolByName(name string) (Tool, bool) { return a.toolByName(a.Mode(), name) }

// toolByName returns the model-visible tool named name in mode. Unlike
// ToolByName it takes the mode explicitly, so a running turn can resolve tool
// names against the topology it pinned at Run time instead of the live one.
func (a *Agent) toolByName(mode, name string) (Tool, bool) {
	for _, t := range a.effectiveTools(mode) {
		if t.Name() == name {
			return t, true
		}
	}
	return nil, false
}

// ModelName returns the model the next provider request asks for.
func (a *Agent) ModelName() string { return a.loadState().snap.modelName }

// SetModel switches the model used from the next turn on. A provider-backed
// agent rebuilds its chat model and topology; a turn that is already running
// keeps the model it started with. The eino test seam (no Provider) cannot
// change models and reports that as an error.
func (a *Agent) SetModel(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("runtime: empty model name")
	}
	a.agentMu.Lock()
	if a.provider == nil {
		a.agentMu.Unlock()
		return errors.New("runtime: SetModel requires a Provider")
	}
	cur := a.loadState()
	nextSnap := cur.snap
	nextSnap.modelName = name
	nextSnap.chatModel = newProviderChatModel(a.provider, name)
	next, err := a.buildMode(nextSnap)
	if err != nil {
		a.agentMu.Unlock()
		return err
	}
	a.publishLocked(nextSnap, next)
	a.agentMu.Unlock()
	// Child agents cache the model and instruction they were built with. Clear
	// them after releasing agentMu so the subagent path never contends for it;
	// stale entries are also rejected by the generation check in childAgent.
	a.clearSubagents()
	return nil
}

// MaxIterations returns the configured per-turn ceiling; 0 means no fixed
// ceiling (the default: the model decides when to end the turn).
func (a *Agent) MaxIterations() int { return a.maxIterations }

// einoMaxIterations maps ZeoKeel's "no fixed ceiling" (<=0) onto eino's
// positive-only MaxIterations. eino falls back to 20 for a configured value
// <= 0 (eino/adk/react.go genReactState/genAgenticReactState), so unlimited is
// sent as a very large positive sentinel that no real turn reaches.
func (a *Agent) einoMaxIterations() int {
	if a.maxIterations > 0 {
		return a.maxIterations
	}
	return unlimitedEinoIterations
}

// MaxSubagentDepth returns the child-agent nesting cap.
func (a *Agent) MaxSubagentDepth() int { return a.maxSubagentDepth }

// SubagentModel returns the model name child agents request, or "" when they
// share the parent's model.
func (a *Agent) SubagentModel() string { return a.subagentModel }

// Mode returns the current agent topology mode.
func (a *Agent) Mode() string { return a.loadState().snap.mode }

// SetMode rebuilds the eino topology for mode and swaps it in for the next
// turn. A turn that is already running keeps the topology it started with,
// including the tool set its streamed tool calls are validated against.
// Build failures leave the current topology untouched.
//
// The mode also selects a prompt policy, so SetMode recomputes the dynamic
// instruction (when Config.InstructionFunc is set) before publishing the
// rebuilt topology. The caller is expected to have made the new mode current
// before calling SetMode; the app's Controller persists it first and then runs
// the switcher, so Instruction() reflects the selected preset immediately
// instead of lagging until the next Run.
//
// The build runs while agentMu is held so it sees one consistent
// instruction/model pair; buildMode receives the snapshot explicitly and never
// calls back into Mode(), which keeps the lock non-reentrant-safe.
func (a *Agent) SetMode(mode string) error {
	normalized, err := NormalizeMode(mode)
	if err != nil {
		return err
	}
	// InstructionFunc may inspect agent or caller state, so call it before
	// taking agentMu. An empty result keeps the current instruction, matching
	// refreshInstruction.
	instruction := ""
	if a.instrFunc != nil {
		instruction = a.instrFunc()
	}
	a.agentMu.Lock()
	cur := a.loadState()
	nextSnap := cur.snap
	nextSnap.mode = normalized
	if strings.TrimSpace(instruction) != "" {
		nextSnap.instruction = instruction
	}
	next, err := a.buildMode(nextSnap)
	if err != nil {
		a.agentMu.Unlock()
		return err
	}
	a.publishLocked(nextSnap, next)
	a.agentMu.Unlock()
	// Children were built with the previous mode's tool set and instruction;
	// a mode switch must rebuild them, exactly like a model switch.
	a.clearSubagents()
	return nil
}

// publishLocked stores next as the new immutable topology. The caller holds
// agentMu (write side); gen advances so cached children built from an older
// snapshot are rebuilt on their next use.
func (a *Agent) publishLocked(snap agentSnapshot, next adk.Agent) {
	gen := uint64(1)
	if cur := a.loadState(); cur != nil {
		gen = cur.gen + 1
	}
	a.currentState.Store(&agentState{
		snap:     snap,
		adkAgent: next,
		strict:   strictMode(snap.mode),
		gen:      gen,
	})
}

// clearSubagents drops every cached child agent. It is called outside agentMu
// so the lock order is always subMu alone on this path.
func (a *Agent) clearSubagents() {
	a.subMu.Lock()
	a.subagents = make(map[string]*subagentEntry)
	a.subMu.Unlock()
}

// refreshInstruction recomputes the system prompt for the next turn and
// swaps in a rebuilt topology when the text changed. A failure keeps the
// previous topology; the dynamic sections are an improvement to prompt
// accuracy, not a reason to fail a turn.
func (a *Agent) refreshInstruction() {
	if a.instrFunc == nil {
		return
	}
	text := a.instrFunc()
	if text == "" {
		return
	}
	a.agentMu.Lock()
	defer a.agentMu.Unlock()
	cur := a.loadState()
	if text == cur.snap.instruction {
		return
	}
	nextSnap := cur.snap
	nextSnap.instruction = text
	next, err := a.buildMode(nextSnap)
	if err != nil {
		return
	}
	a.publishLocked(nextSnap, next)
}

// pin loads the topology this turn must run. It returns the whole immutable
// agentState, not just the adk.Agent: tool-call validation needs the pinned
// mode and strictness too, because a narrowing mode switch while the turn runs
// must not make the pinned topology's own tools look unknown.
func (a *Agent) pin() (*agentState, error) {
	pinned := a.loadState()
	if pinned == nil || pinned.adkAgent == nil {
		return nil, errors.New("runtime: agent has no published topology")
	}
	return pinned, nil
}

// Run starts one turn for sessionID and streams the turn's events until the
// turn finishes, fails, or ctx is cancelled. The returned channel is closed at
// the end of the turn and must be drained by the caller.
//
// When Config.History is set, prior messages are loaded before the turn starts
// and the ADK turn is seeded with them; the turn's user/assistant/tool messages
// are appended once it completes. A history read failure is a setup error
// (returned here); a history append failure surfaces as a state.Error at the
// end of an otherwise completed turn.
//
// When History is nil but Config.CheckpointStore is set, the prior conversation
// comes from the saved checkpoint instead (missing or unreadable checkpoints
// mean a fresh conversation). After a successful turn the completed
// conversation is saved to the checkpoint store; a save failure surfaces as a
// state.Error.
//
// Compaction (Config.Compactor) runs inside the turn goroutine, not here: a
// model-written summary can take seconds, and Run is called synchronously from
// the controller's Submit path (and therefore from UI handlers). The turn
// publishes PhaseRunning before compacting, so the UI can show progress while
// the summary is written; a compaction failure arrives as a state.Error and a
// failed terminal status instead of a setup error.
//
// Run never returns mid-turn errors through its error result: model/tool
// failures and compaction failures arrive as state.Error events so the UI can
// render them. The error return is reserved for invalid input and setup
// failures (a bad History read, for example).
func (a *Agent) Run(ctx context.Context, sessionID, text string) (<-chan state.Event, error) {
	return a.RunWithAttachments(ctx, sessionID, text, nil)
}

// RunWithAttachments starts one turn with user files attached. Text
// attachments are appended to the prompt the model sees (see
// AttachmentPrompt); image attachments travel as multimodal image parts on
// the user message. An empty attachment slice behaves exactly like Run.
//
// Attachments apply to this turn only. Later turns replay their content from
// History, so a History implementation that persists attachment rows must
// decode them back into Message.Content/Images.
func (a *Agent) RunWithAttachments(ctx context.Context, sessionID, text string, attachments []UserAttachment) (<-chan state.Event, error) {
	if ctx == nil {
		return nil, errors.New("runtime: nil context")
	}
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("runtime: empty prompt")
	}

	// Recompose the dynamic runtime context (working directory, time, mode)
	// for this turn. A cheap text comparison avoids rebuilding the topology
	// when nothing changed.
	a.refreshInstruction()

	// Pin the topology for this turn. A shared agent can be rebuilt for another
	// session while this turn waits for runMu; loading it now keeps the mode,
	// instruction and tool set the caller composed at Run time. The pinned
	// state covers tool-call validation as well: a turn already in flight (or
	// queued behind one) must keep accepting the tools its own topology can
	// execute, even if SetMode has since narrowed the live mode.
	pinned, err := a.pin()
	if err != nil {
		return nil, err
	}

	// Load the prior conversation before returning: Controller.Submit persists
	// the new prompt only after Run returns, so reading History here (and not
	// in the turn goroutine) keeps the prompt out of the seeded conversation.
	// Compaction is deliberately deferred to the turn goroutine; only the
	// cheap History read belongs on the caller's goroutine.
	prior, err := a.loadPriorConversation(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("runtime: load history for session %q: %w", sessionID, err)
	}

	out := make(chan state.Event, 64)
	runCtx, cancel := context.WithCancel(withSessionID(ctx, sessionID))

	go a.run(runCtx, cancel, sessionID, text, attachments, prior, pinned, out)
	return out, nil
}

// run owns the turn. It is the only goroutine closing out; tool goroutines may
// publish approval requests through the turn's eventSink, which serializes them
// against this goroutine's sends. It waits for the global turn lock before
// entering eino and holds it until the turn (and any subagent it spawned)
// finishes, so the shared topology is never driven by two turns at once.
//
// Compaction happens here, before the turn lock, so a slow summarizer neither
// blocks Run/Submit nor serializes unrelated sessions. Failures are published
// as state events because Run has already returned a live event channel.
func (a *Agent) run(ctx context.Context, cancel context.CancelFunc, sessionID, text string, attachments []UserAttachment, prior []Message, pinned *agentState, out chan<- state.Event) {
	sink := newEventSink(out)
	defer sink.close()
	defer cancel()

	msgID := uuid.NewString()
	emit := func(ev state.Event) bool {
		return sink.send(ctx, ev)
	}
	// terminal emits happen after the turn has already decided to stop, so a
	// cancelled consumer must not leak this goroutine.
	emitTerminal := sink.trySend

	if !emit(state.Status{Phase: state.PhaseRunning}) {
		return
	}

	usage := newTurnUsage()
	pending := make(map[string]time.Time) // CallID -> start time
	started := make(map[string]bool)      // CallID -> ToolStart emitted

	fail := func(err error) {
		// A cancelled turn is not a failure: close the calls it left open (so no
		// card keeps spinning and the next turn's history has no dangling
		// call), mark the turn stopped and leave it quiet otherwise.
		if ctx.Err() != nil {
			finishPendingTools(pending, ctx.Err(), true, func(ev state.Event) bool {
				emitTerminal(ev)
				return true
			})
			emitTerminal(state.Status{Phase: state.PhaseIdle, Reason: state.ReasonStopped})
			return
		}
		finishPendingTools(pending, err, false, emit)
		emit(state.Status{Phase: state.PhaseFailed, Reason: state.ReasonFailed})
		emit(state.Error{MsgID: msgID, Err: err})
	}

	// Compact the prior conversation inside the turn (see the Run doc): the
	// summary call is bounded by the Compactor's own timeout and its failure is
	// a turn error, not a setup error.
	prior, err := a.compactPrior(ctx, sessionID, prior)
	if err != nil {
		fail(fmt.Errorf("runtime: compact conversation for session %q: %w", sessionID, err))
		return
	}

	pendingAdvisories := a.takePendingAdvisories()
	inputText := AttachmentPrompt(text, attachments)
	if len(pendingAdvisories) > 0 {
		inputText = appendAdvisoriesToPrompt(inputText, pendingAdvisories)
	}
	input := make([]*schema.Message, 0, len(prior)+1)
	input = append(input, messagesToEino(prior)...)
	input = append(input, userMessageWithAttachments(inputText, AttachmentImages(attachments)))

	// Serialize the eino turn only after compaction: the shared topology must
	// not be driven by two turns at once, but compacting sessions need not
	// queue behind an unrelated turn.
	a.runMu.Lock()
	defer a.runMu.Unlock()

	record := newTurnRecord(inputText)
	// Tool invocations receive a context derived from this one, so they can
	// publish approval requests and (indirectly) contribute to the record.
	turnCtx := &turnContext{
		sink:           sink,
		record:         record,
		userRequest:    text,
		reviewContext:  a.reviewContext,
		budgetSteps:    a.budgetSteps,
		budgetTokens:   a.budgetTokens,
		renderAdvisory: a.budgetAdvisory,
	}
	ctx = withTurnContext(ctx, turnCtx)
	// External notices that arrive while this turn runs are delivered with the
	// next provider request; the active pointer is cleared when the turn ends so
	// late notices wait for the next turn instead of vanishing.
	a.activeTurn.Store(turnCtx)
	defer a.activeTurn.CompareAndSwap(turnCtx, nil)
	// Barrier checkpoints (FP-11): the model adapter flushes the request before
	// it is sent and the tool wrapper flushes before a side-effecting tool body
	// runs, so a crash can lose neither. A Save failure fails the barrier
	// closed instead of continuing without a recovery record.
	ctx = withTurnCheckpoint(ctx, &turnCheckpointContext{
		store:     a.checkpoints,
		prior:     prior,
		sessionID: sessionID,
		turnID:    msgID,
	})

	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           pinned.adkAgent,
		EnableStreaming: true,
		SessionID:       sessionID,
	})
	iter := runner.Run(ctx, input)

	for {
		if ctx.Err() != nil {
			finishPendingTools(pending, ctx.Err(), true, func(ev state.Event) bool {
				emitTerminal(ev)
				return true
			})
			emitTerminal(state.Status{Phase: state.PhaseIdle, Reason: state.ReasonStopped})
			return
		}

		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			// Reaching the per-turn step ceiling is a budget boundary, not a
			// failure: the turn already did real work and the user can simply
			// send another message. Finish it as completed with a notice
			// instead of surfacing eino's NodeRunError. Real model/tool errors,
			// timeouts and cancellation still fail below.
			if errors.Is(ev.Err, adk.ErrExceedMaxIterations) {
				if err := a.completeMaxIterations(ctx, sessionID, msgID, record, prior, pending, emit); err != nil {
					fail(err)
				}
				return
			}
			fail(ev.Err)
			return
		}
		if ev.Output == nil || ev.Output.MessageOutput == nil {
			continue
		}

		err := a.consumeMessage(ctx, sessionID, msgID, pinned, ev.Output.MessageOutput, emit, usage, pending, started)
		if err != nil {
			fail(err)
			return
		}
	}

	// Persist only completed turns: a failed or cancelled turn is retried from
	// the last committed conversation state instead of leaving a partial one.
	turnMessages := record.messages()
	if a.history != nil {
		if err := a.history.Append(ctx, sessionID, turnMessages...); err != nil {
			fail(fmt.Errorf("runtime: append history for session %q: %w", sessionID, err))
			return
		}
	}
	if a.checkpoints != nil {
		cp := checkpointFor(prior, turnMessages, msgID, CheckpointPhaseCompleted, time.Now())
		if err := a.checkpoints.Save(ctx, sessionID, cp); err != nil {
			fail(fmt.Errorf("runtime: save checkpoint for session %q: %w", sessionID, err))
			return
		}
	}

	// Usage was published as deltas after each model call; the turn completes
	// normally (ReasonNone) and nothing is left to publish.
	if !emit(state.Status{Phase: state.PhaseIdle}) {
		return
	}
}

// completeMaxIterations ends a turn that reached MaxIterations as a completed
// turn rather than a failure. The model/tool work already streamed stays on
// screen and in history, the user gets an assistant notice that names the
// ceiling and says how to continue, and the session returns to idle so the next
// message resumes from the committed conversation. It only returns an error
// when persisting the completed turn fails, which is a real failure.
//
// This is the graceful half of the step-ceiling contract: the ceiling is a
// safety budget, not an error condition (eino would otherwise wrap it in a
// NodeRunError that the UI renders as a red failed turn).
func (a *Agent) completeMaxIterations(
	ctx context.Context,
	sessionID, msgID string,
	record *turnRecord,
	prior []Message,
	pending map[string]time.Time,
	emit func(state.Event) bool,
) error {
	// A tool call can only still be pending when the ceiling interrupted a tool
	// round trip; close it so its card stops spinning.
	finishPendingTools(pending, errors.New("stopped at the per-turn step limit"), false, emit)

	notice := a.stepLimitNotice(a.maxIterations)
	if !emit(state.Token{SessionID: sessionID, MsgID: msgID, Text: notice}) {
		// The consumer is gone (cancellation): there is nobody to persist for.
		return nil
	}
	record.add(Message{Role: RoleAssistant, Content: notice})

	// Save the capped turn exactly like a successful one: the next message must
	// see the tools and results that already ran, otherwise "continue" would
	// restart the work from scratch.
	turnMessages := record.messages()
	if a.history != nil {
		if err := a.history.Append(ctx, sessionID, turnMessages...); err != nil {
			return fmt.Errorf("runtime: append history for session %q: %w", sessionID, err)
		}
	}
	if a.checkpoints != nil {
		cp := checkpointFor(prior, turnMessages, msgID, CheckpointPhaseCompleted, time.Now())
		if err := a.checkpoints.Save(ctx, sessionID, cp); err != nil {
			return fmt.Errorf("runtime: save checkpoint for session %q: %w", sessionID, err)
		}
	}

	// Usage deltas were published per model call; the capped turn ends idle with
	// no stop reason so it reads as a completed turn, exactly like a normal end.
	if !emit(state.Status{Phase: state.PhaseIdle}) {
		return nil
	}
	return nil
}

// defaultStepLimitNotice is the English step-limit notice used when
// Config.StepLimitNotice is nil (tests and headless callers). The app supplies
// localized copy so a Chinese UI shows Chinese text.
func defaultStepLimitNotice(limit int) string {
	return fmt.Sprintf(
		"Reached this turn's step limit (%d steps). The work and results so far are saved; send another message and I'll continue from here.",
		limit)
}

// defaultBudgetWarnSteps / defaultBudgetWarnTokens are the soft per-turn cost
// advisories DSH-style long runs should raise (never a hard stop).
var (
	defaultBudgetWarnSteps  = []int{25, 50, 100}
	defaultBudgetWarnTokens = []int{200_000, 500_000}
)

// normalizeWarnThresholds copies, drops non-positive values and sorts a
// threshold list. A nil list means the defaults; a non-nil empty list disables
// the dimension.
func normalizeWarnThresholds(given, defaults []int) []int {
	if given == nil {
		given = defaults
	}
	out := make([]int, 0, len(given))
	last := 0
	for _, v := range given {
		if v <= 0 || v <= last {
			continue
		}
		out = append(out, v)
		last = v
	}
	sort.Ints(out)
	return out
}

// defaultBudgetAdvisory is the English advisory injected into the model's next
// tool result when a soft budget threshold is crossed.
func defaultBudgetAdvisory(kind string, steps, tokens int) string {
	return fmt.Sprintf(
		"Cost advisory (not a hard stop): this turn has run %d model steps and %d output tokens. Keep going if the work is still useful, but consider wrapping up the current step and reporting progress; the user can stop the turn if the cost is not worth it.",
		steps, tokens)
}

// finishPendingTools closes tool calls that never produced a result, so the UI
// does not spin forever and the next turn's history has no unanswered call.
// cancelled marks the calls a turn ending closed (Stop, cancellation, the step
// ceiling); the snapshot then shows ToolCancelled instead of a red error.
func finishPendingTools(pending map[string]time.Time, cause error, cancelled bool, emit func(state.Event) bool) {
	for callID, startedAt := range pending {
		emit(state.ToolEnd{
			CallID:    callID,
			Result:    cause.Error(),
			IsError:   !cancelled,
			Duration:  time.Since(startedAt),
			Cancelled: cancelled,
		})
		delete(pending, callID)
	}
}
