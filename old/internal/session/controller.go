package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/modelcatalog"
	"github.com/zeokeel/zeokeel/old/internal/presets"
	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
	"github.com/zeokeel/zeokeel/old/internal/tools"
	"github.com/zeokeel/zeokeel/old/internal/workspacechanges"
)

// Sentinel errors returned by Controller. Callers classify failures with
// errors.Is.
var (
	// ErrInvalidConfig reports a missing required dependency.
	ErrInvalidConfig = errors.New("session: invalid config")
	// ErrInvalidArgument reports an empty id or prompt.
	ErrInvalidArgument = errors.New("session: invalid argument")
	// ErrEmptyPrompt reports Submit with blank text.
	ErrEmptyPrompt = errors.New("session: empty prompt")
	// ErrNoAgent reports Submit before an agent was configured.
	ErrNoAgent = errors.New("session: no agent configured")
	// ErrTurnRunning reports a second Submit for a session whose turn is
	// still in flight.
	ErrTurnRunning = errors.New("session: turn already running")
	// ErrClosed reports use of a Controller after Close.
	ErrClosed = errors.New("session: controller closed")

	// errGoalContinuationStopped reports an automatic goal continuation
	// suppressed by a user Stop. It never escapes the controller.
	errGoalContinuationStopped = errors.New("session: goal continuation stopped")
)

// Config is everything a Controller needs. Store, Broker and Agent are owned by
// the caller; Controller only borrows them. Agent may be nil for a
// read-only/static UI; Submit then reports ErrNoAgent. Gate may be nil, in
// which case approval requests are surfaced to the UI but cannot be decided
// through Approve or Deny.
type Config struct {
	Store  *store.Store
	Broker *state.Broker
	// Projector is deprecated (AR-05 convergence): the UI reads the
	// controller's own per-session snapshots, so the raw broker projector is not
	// a second source of truth. When supplied, New closes it immediately to stop
	// the duplicate fold; keep passing nil (or omit the field) once the app
	// wiring is removed.
	Projector   *state.Projector
	Agent       *runtime.Agent
	Gate        *runtime.ApprovalQueue
	WorkspaceID string
	// TitleGenerator writes the automatic title for a session's first
	// completed turn. Nil is allowed: the controller then uses the
	// truncated-first-message fallback.
	TitleGenerator TitleGenerator
	// GoalDriver, when non-nil, automatically submits a continuation turn for
	// an active session goal after every terminal turn. Nil disables the
	// feature; SetGoalDriver can install it later.
	GoalDriver *GoalRoundDriver
	// GoalRounds is a convenience switch for the default driver: a positive
	// value starts a GoalRoundDriver with that budget when GoalDriver is nil.
	// Zero leaves automatic continuation disabled. GoalDriver wins when both
	// are set.
	GoalRounds int
	// AgentFactory, when non-nil, builds one runtime.Agent per session instead
	// of sharing Config.Agent. Per-session agents let turns of different
	// sessions run in parallel (each agent has its own runMu) and let an
	// approval wait block only its own session (RT-04). A nil factory keeps the
	// legacy shared agent; SetAgentFactory can install one later.
	AgentFactory func(sessionID string) (*runtime.Agent, error)
	// AgentDiscard, when non-nil, receives an agent the controller stops using
	// (invalidation, Close or a lost build race) so the app can close the
	// bundle that owns it. It is called without c.mu held.
	AgentDiscard func(sessionID string, agent *runtime.Agent)
	// AgentGateFactory, when non-nil, returns the approval gate that belongs to
	// the factory-built agent of sessionID (each per-session bundle owns its
	// own queue). The controller uses it to resolve approvals and allow-always
	// rules for the right session.
	AgentGateFactory func(sessionID string) *runtime.ApprovalQueue
	// PresetKnownTools, when non-nil, validates user preset tool allowlists
	// during the initial preset load: an unknown tool name fails the load
	// (fail closed) instead of silently mismatching at build time.
	PresetKnownTools []string
}

// Controller wires one live turn end to end: it persists the prompt, runs the
// runtime Agent, forwards every state.Event to the Broker and the event store,
// keeps an immutable state.Snapshot per session, and persists the assistant
// messages and terminal session status.
//
// Submit is asynchronous: it returns once the turn has been accepted (the user
// message is persisted and the session snapshot is PhaseRunning). Turn
// completion is observed through OnUpdate/Snapshot (PhaseIdle or PhaseFailed)
// or by calling Wait. Stop cancels the current session's turn without waiting
// for its goroutine to finish.
//
// Controller is safe for concurrent use. Submissions for independent sessions
// may be in flight at the same time; the runtime queues their shared-agent
// turns because eino's topology cannot drive two turns at once. A second Submit
// for a session that is already running returns ErrTurnRunning. Snapshot and
// OnUpdate describe the session most recently opened, created or submitted to
// (the "current" session).
type Controller struct {
	store  *store.Store
	broker *state.Broker
	agent  *runtime.Agent
	gate   *runtime.ApprovalQueue

	wsMu        sync.Mutex
	workspaceID string

	// bindMu serializes the workspace binding of CreateSession/OpenSession and
	// Submit. The app has one shared runtime agent built for the selected
	// workspace's root, so the "select the session's workspace, then start the
	// turn" sequence must not interleave with another submit switching the
	// root between the rebuild and Agent.Run. UI-driven SelectWorkspace calls
	// do not take it: a user switching workspace mid-turn changes the agent for
	// the next turn, as before.
	bindMu sync.Mutex

	mu        sync.Mutex
	views     map[string]*state.Snapshot
	turns     map[string]*turn
	pending   map[string]map[string]state.ApprovalRequest // session id -> call id
	perms     map[string]string                           // session id -> permission mode at creation
	currentID string
	// onSessionActivated, when non-nil, observes a session becoming current
	// through create/open. It runs without mu held, so the app can reset or
	// restore that session's working directory (the runtime agent shares one
	// working-directory state across sessions in a workspace). Guarded by mu.
	onSessionActivated func(previousSessionID, sessionID string)
	// attachMu serializes attachment admission/removal so the per-turn cap
	// cannot be exceeded by two concurrent picks. It is held across file I/O
	// and store writes, never while holding mu.
	attachMu sync.Mutex
	// builtPermission is the access mode the shared agent/tool set was built
	// for; per-session modes trigger a rebuild when it differs.
	builtPermission string

	// Per-session agent mode (RT-04). agentFactory/agentDiscard are installed
	// by the app through Config or SetAgentFactory; agents caches each
	// session's built agent, builtPermissions records the access mode it was
	// built for, and agentsGeneration invalidates factory builds that were in
	// flight when the app asked for a rebuild. All guarded by mu.
	agentFactory func(sessionID string) (*runtime.Agent, error)
	agentDiscard func(sessionID string, agent *runtime.Agent)
	// modelConfigured reports whether a model is configured/usable (a saved
	// provider key or a keyless local provider, PD-02). HasConfiguredModel
	// exposes it to the UI: unlike HasAgent it must be false on a fresh
	// install, where the per-session AgentFactory is installed but no model can
	// run yet. Guarded by mu.
	modelConfigured  func() bool
	agents           map[string]*runtime.Agent
	agentGates       map[string]*runtime.ApprovalQueue
	agentGateFactory func(sessionID string) *runtime.ApprovalQueue
	builtPermissions map[string]string
	agentsGeneration uint64
	// sessionEventSubs fans one session's semantic events out to streaming
	// subscribers (the headless/ACP runtime). Guarded by mu.
	sessionEventSubs map[string]map[chan state.Event]struct{}

	// presets is the declarative agent-preset registry (FP-06). It is loaded
	// from the data directory at New and replaced by ReloadPresets; nil means
	// the built-ins only (keep Presets() nil-safe).
	presets *presets.Registry
	// questionWaiters, when non-empty, holds the ask_user calls that are
	// blocking inside a turn (FP-16): call id -> waiter. Guarded by mu.
	questionWaiters map[string]*questionWaiter
	// presetKnownTools is the registered tool-name list used to validate user
	// preset allowlists at load time (P1 fail-closed). presetWarnings holds
	// per-session build warnings (for example a allowlist that matched no
	// tool) so the UI can show them; lastPresetLoadErr keeps the latest failed
	// reload error for the settings surface. Guarded by mu.
	presetKnownTools  []string
	presetWarnings    map[string][]string
	lastPresetLoadErr error
	listeners         []func(*state.Snapshot)
	// goalStore, when non-nil, persists each session's goal so a restart keeps
	// it and the /goal API's changes survive. The app wires the store-backed
	// tool state; nil keeps goals in memory only. Guarded by mu.
	goalStore tools.GoalStateStore
	// onTurnFinished, when non-nil, observes terminal turns of every session,
	// including sessions that are not current (OnUpdate publishes only the
	// current session). It runs once per turn from the turn goroutine. Guarded
	// by mu.
	onTurnFinished TurnFinishedHook
	// onApprovalRequested, when non-nil, observes an approval request that just
	// became pending for any session, including sessions that are not current.
	// It runs once per call id from the turn goroutine. Guarded by mu.
	onApprovalRequested ApprovalRequestedHook
	// teamStopper, when non-nil, cancels the asynchronous team members a
	// session spawned. The shell calls the same hook after its own Stop; this
	// one makes a non-UI Stop (API, automation, programmatic cancellation) stop
	// the team too. Guarded by mu; called without it held.
	teamStopper func(sessionID string)
	closed      bool

	// Automatic session titles. titleGen is the configured generator (nil
	// disables the feature), autoTitles marks sessions this Controller already
	// considered, and initialTitles remembers the title each session was
	// created with so a title changed outside RenameSession is never replaced.
	// titleCtx is cancelled by Close so an in-flight provider request cannot
	// outlive the store. These fields are guarded by mu.
	titleGen      TitleGenerator
	titleCtx      context.Context
	titleCancel   context.CancelFunc
	autoTitles    map[string]bool
	initialTitles map[string]string

	// titleWriteMu serializes the final automatic-title write against a manual
	// rename. Both paths hold it across their check-then-write store calls, so
	// a rename can no longer interleave between the eligibility check and the
	// title update and get overwritten. Lock order is titleWriteMu before mu;
	// never acquire titleWriteMu while holding mu.
	titleWriteMu sync.Mutex

	// Tool-derived session state. The runtime hides the session id from tool
	// implementations, so tools report their state in their structured result
	// and the Controller folds that result into the session that ran the call.
	// All four maps are guarded by mu.
	todos     map[string][]tools.Todo               // session id -> tasks
	toolCalls map[string]map[string]string          // session id -> call id -> tool name
	toolArgs  map[string]map[string]json.RawMessage // session id -> call id -> args
	questions map[string]map[string]tools.Question  // session id -> call id -> pending question
	artifacts map[string][]tools.Artifact           // session id -> presented artifacts
	goals     map[string]*tools.Goal                // session id -> current goal
	team      map[string][]tools.Teammate           // session id -> subagents spawned

	// Automatic goal continuation. goalDriver is nil when the feature is
	// disabled. stopRequested records a Stop for the session even after its
	// turn reached the terminal phase, so the completion callback cannot slip
	// one more automatic turn in; a manual Submit clears it.
	goalDriver    *GoalRoundDriver
	stopRequested map[string]bool

	// Per-session agent mode. The mode itself lives in the settings table
	// (session.<id>.mode); modes caches the last value read or written. The
	// switcher is the app's hook for rebuilding/switching the runtime Agent.
	modes        map[string]string
	modeSwitcher func(mode string) error

	// Per-session plan mode. The flag itself lives in the settings table
	// (session.<id>.plan_mode); planModes caches the last value read or
	// written ("true"/"false"). The app's prompt composer reads it through
	// its PlanModeFunc hook at the start of each turn, so no agent rebuild is
	// needed to toggle it.
	planModes map[string]string

	wg sync.WaitGroup

	// PluginChangeHook, when set, runs after SetPluginEnabled persists a
	// toggle. The app may rebuild or gate the agent's tools here; the shell
	// only promises that the value is durable when the hook runs.
	PluginChangeHook func(name string, enabled bool)

	// agentRebuilder is the app-owned hook that rebuilds the runtime agent
	// after a setting the agent reads at build time changed (plugin toggle,
	// workspace root, selected model, access mode). It is called outside
	// c.mu so the rebuild can read controller state.
	agentRebuilder func(reason string) error

	// RunNow is the Automation screen's "Run now" callback. The app owns
	// execution; the shell invokes it through RunAutomationNow.
	RunNow func(id string)
}

// turn is the state of one in-flight Submit. Everything except the atomic
// flags and the write-once done/cancel fields is owned by the drain goroutine
// (see forward/runTurn); the flags are read by Stop, Close and Submit.
type turn struct {
	id        string
	sessionID string
	cancel    context.CancelFunc
	// ctx is the parent-derived turn context. Err() is non-nil when the caller
	// cancelled it (Stop also sets stopped, but an external context cancel only
	// shows up here); the goal driver treats that as a stop request.
	ctx  context.Context
	done chan struct{}

	stopped         atomic.Bool
	failed          atomic.Bool
	terminalApplied atomic.Bool

	// Guarded by Controller.mu while a turn is live.
	phase state.Phase

	// Owned by the drain goroutine.
	drafts          map[string]*draft
	order           []string
	statusPersisted bool
	// toolStarted records that the turn started at least one tool call, so a
	// tool-only turn still counts as producing output for TurnOutcome.
	toolStarted bool
	// userMsgID is the persisted user message that opened this turn; the
	// per-turn TurnEnd marker and the transcript anchor use it. Set before the
	// drain goroutine starts, read only by finishTurn afterwards.
	userMsgID string
	// errText is the first failure message the turn received. Owned by the
	// drain goroutine (applyLocked/finishTurn), like the other unexported
	// per-turn fields above.
	errText string

	// workspaceTurn carries the pre-turn baseline and the file tools'
	// pre-edit content used to report files this turn changed with inline
	// diffs (FP-05 workspace/changes). Nil when diffing is skipped (Read
	// Only, no workspace root, capture failure).
	workspaceTurn *workspacechanges.Turn
}

type draft struct {
	msgID     string
	text      strings.Builder
	reasoning strings.Builder
	saved     bool
}

// New validates cfg and returns a Controller. It does not touch the store.
func New(cfg Config) (*Controller, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("%w: nil store", ErrInvalidConfig)
	}
	if cfg.Broker == nil {
		return nil, fmt.Errorf("%w: nil broker", ErrInvalidConfig)
	}

	goalDriver := cfg.GoalDriver
	if goalDriver == nil && cfg.GoalRounds > 0 {
		goalDriver = NewGoalRoundDriver(GoalRoundDriverConfig{MaxRounds: cfg.GoalRounds})
	}

	c := &Controller{
		store:         cfg.Store,
		broker:        cfg.Broker,
		agent:         cfg.Agent,
		gate:          cfg.Gate,
		workspaceID:   cfg.WorkspaceID,
		views:         make(map[string]*state.Snapshot),
		turns:         make(map[string]*turn),
		pending:       make(map[string]map[string]state.ApprovalRequest),
		perms:         make(map[string]string),
		todos:         make(map[string][]tools.Todo),
		toolCalls:     make(map[string]map[string]string),
		toolArgs:      make(map[string]map[string]json.RawMessage),
		questions:     make(map[string]map[string]tools.Question),
		artifacts:     make(map[string][]tools.Artifact),
		goals:         make(map[string]*tools.Goal),
		team:          make(map[string][]tools.Teammate),
		goalDriver:    goalDriver,
		stopRequested: make(map[string]bool),
		modes:         make(map[string]string),
		planModes:     make(map[string]string),
		titleGen:      cfg.TitleGenerator,
		autoTitles:    make(map[string]bool),
		initialTitles: make(map[string]string),
	}
	if cfg.AgentFactory != nil {
		c.agentFactory = cfg.AgentFactory
		c.agentDiscard = cfg.AgentDiscard
		c.agentGateFactory = cfg.AgentGateFactory
		c.agents = make(map[string]*runtime.Agent)
		c.agentGates = make(map[string]*runtime.ApprovalQueue)
		c.builtPermissions = make(map[string]string)
	}
	c.presetKnownTools = normalizePresetToolNames(cfg.PresetKnownTools)
	if reg, err := presets.Load(presets.Options{
		UserDir:    cfg.Store.Dir(),
		KnownTools: c.presetKnownTools,
	}); err == nil {
		c.presets = reg
	} else {
		// Fail closed on a broken user preset file: built-ins keep the app
		// usable and the app surfaces the reload error in the settings UI.
		c.lastPresetLoadErr = err
		if fallback, ferr := presets.Load(presets.Options{}); ferr == nil {
			c.presets = fallback
		}
	}
	c.titleCtx, c.titleCancel = context.WithCancel(context.Background())
	// AR-05 convergence: the controller folds events into its own per-session
	// views (the UI's source of truth), so a broker-wide projector is no longer
	// consumed. Close one supplied by an older app wiring immediately so the
	// duplicate fold stops; this is idempotent with the owner's later Close.
	if cfg.Projector != nil {
		cfg.Projector.Close()
	}
	return c, nil
}

// CreateSession persists a new idle session and seeds its snapshot. The
// configured workspace is created on first use (its name is WorkspaceID) when
// it does not exist, so a headless caller can pass a stable label such as
// "default".
func (c *Controller) CreateSession(ctx context.Context, title string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return "", ErrClosed
	}

	// Serialize with a Submit's workspace binding: creating a session also
	// selects its workspace and rebuilds the shared agent, so a concurrent
	// submit must not observe a half-switched selection.
	c.bindMu.Lock()
	defer c.bindMu.Unlock()

	workspaceID, err := c.ensureWorkspace()
	if err != nil {
		return "", err
	}
	return c.createSessionInWorkspaceLocked(ctx, workspaceID, title)
}

// CreateSessionInWorkspace creates a session inside an explicit workspace and
// makes it current; it never falls back to another workspace.
func (c *Controller) CreateSessionInWorkspace(ctx context.Context, workspaceID, title string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(workspaceID) == "" {
		return "", fmt.Errorf("%w: create session: empty workspace id", ErrInvalidArgument)
	}
	if _, err := c.store.GetWorkspace(workspaceID); err != nil {
		return "", fmt.Errorf("session: create in workspace %q: %w", workspaceID, err)
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return "", ErrClosed
	}
	c.bindMu.Lock()
	defer c.bindMu.Unlock()
	return c.createSessionInWorkspaceLocked(ctx, workspaceID, title)
}

// CurrentSession returns the current session's stored row.
func (c *Controller) CurrentSession() (*store.Session, error) {
	id := c.CurrentSessionID()
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: no current session", ErrInvalidArgument)
	}
	return c.store.GetSession(id)
}

func (c *Controller) createSessionInWorkspaceLocked(ctx context.Context, workspaceID, title string) (string, error) {
	if strings.TrimSpace(title) == "" {
		title = "Untitled"
	}

	// A session created for an explicit workspace must select that workspace
	// (and rebuild the shared agent for its root) before it becomes current.
	// Without this, CreateSessionInWorkspace(B) while A is selected created a
	// session whose first turn ran with A's tools and prompt WorkDir.
	if err := c.bindWorkspace(ctx, workspaceID); err != nil {
		return "", fmt.Errorf("session: create in workspace %q: %w", workspaceID, err)
	}

	sess, err := c.store.CreateSession(workspaceID, title)
	if err != nil {
		return "", fmt.Errorf("session: create: %w", err)
	}

	snap := state.NewSnapshot()
	snap.SessionID = sess.ID

	// A new session inherits the persisted permission default and stores it as
	// its own effective mode, so a later change to the default only affects
	// sessions created after it.
	permission := ""
	if settings, err := c.Settings().Load(); err == nil {
		permission = settings.Permission
	}
	if permission == "" {
		permission = PermissionWorkspace
	}
	_ = c.store.SetSetting(SessionPermissionKey(sess.ID), permission)

	// Plan mode is off for every new session. Persisting the explicit default
	// (instead of leaving the key absent) means a future global default cannot
	// silently turn on plan review for an existing session after a restart.
	_ = c.store.SetSetting(SessionPlanModeKey(sess.ID), "false")

	// Apply the persisted default preset (Settings > Agent presets) here, in
	// the one creation path every caller shares. Shell new-session buttons and
	// automation's CreateSession must start in the preset the settings screen
	// advertises instead of always Standard.
	startMode := c.defaultAgentPreset()
	if startMode != "" {
		_ = c.store.SetSetting(SessionModeKey(sess.ID), startMode)
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return "", ErrClosed
	}
	previousID := c.currentID
	c.views[sess.ID] = snap
	c.perms[sess.ID] = permission
	c.planModes[sess.ID] = "false"
	c.initialTitles[sess.ID] = sess.Title
	if startMode != "" {
		c.modes[sess.ID] = startMode
	}
	c.currentID = sess.ID
	c.mu.Unlock()

	// Point the shared agent at the new session's mode before the first turn.
	if err := c.applySessionMode(sess.ID); err != nil {
		return "", fmt.Errorf("session: create %q: apply agent mode: %w", sess.ID, err)
	}

	// The new session takes over the shared working-directory state: let the
	// app start it at the workspace root (or its own saved directory) instead
	// of inheriting whatever subdirectory the previous session had entered.
	c.notifySessionActivated(previousID, sess.ID)

	c.notify(sess.ID, snap)
	return sess.ID, nil
}

// defaultAgentPreset returns the normalized agent.preset setting, or "" when
// none is stored (or the stored value is not a supported mode). The controller
// is the shared creation path, so reading the preset here makes shell sessions
// and automation sessions behave the same.
func (c *Controller) defaultAgentPreset() string {
	if c.store == nil {
		return ""
	}
	raw, err := c.store.GetSetting("agent.preset")
	if err != nil || strings.TrimSpace(raw) == "" {
		return ""
	}
	mode, err := runtime.NormalizeMode(raw)
	if err != nil {
		return ""
	}
	return mode
}

// OpenSession makes id the current session and returns its snapshot. History
// is loaded from the store the first time a session is opened; later calls
// return the live snapshot that already contains the session's turns.
func (c *Controller) OpenSession(ctx context.Context, id string) (*state.Snapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: open session: empty id", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Serialize the workspace switch with Submit's binding: both move the
	// shared agent to a different workspace root, and a submit must not start
	// its turn between this switch and the current-session update below.
	c.bindMu.Lock()
	defer c.bindMu.Unlock()

	sess, err := c.store.GetSession(id)
	if err != nil {
		return nil, fmt.Errorf("session: open: %w", err)
	}

	// Cross-workspace sessions are reachable from "In one list" and
	// "Workspace Tree". The agent's tools, Files tab, prompt workdir and
	// session_search scope all follow the selected workspace, so opening a
	// session from another workspace must select that workspace first
	// instead of leaving the UI and the agent on the old root.
	if ws := strings.TrimSpace(sess.WorkspaceID); ws != "" && ws != c.CurrentWorkspaceID() {
		if err := c.SelectWorkspace(ctx, ws); err != nil {
			return nil, fmt.Errorf("session: open: select workspace %q: %w", ws, err)
		}
	}

	c.mu.Lock()
	snap := c.views[id]
	c.mu.Unlock()

	if snap == nil {
		// A previous process may have died mid-turn: close the open tail before
		// replaying so the reopened session is not stuck "running" with a
		// spinning tool card.
		if err := c.recoverInterruptedSession(sess, nil); err != nil {
			return nil, err
		}
		if refreshed, err := c.store.GetSession(id); err == nil {
			sess = refreshed
		}
		loaded, err := c.buildSnapshot(sess)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		if existing := c.views[id]; existing != nil {
			snap = existing
		} else {
			c.views[id] = loaded
			snap = loaded
		}
		c.mu.Unlock()
	}

	// The agent is shared across sessions: make the target session current
	// before pointing it at this session's persisted mode. The app's ModeFunc
	// resolves the preset through Controller.Mode, so composing the mode
	// switch from the previous session left Agent.Instruction() describing the
	// old preset until the next turn. A failed rebuild restores the previous
	// session id, so the previous session stays current.
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	previousID := c.currentID
	c.currentID = id
	c.mu.Unlock()

	if err := c.applySessionMode(id); err != nil {
		c.mu.Lock()
		if !c.closed && c.currentID == id {
			c.currentID = previousID
		}
		c.mu.Unlock()
		return nil, fmt.Errorf("session: open %q: apply agent mode: %w", id, err)
	}

	// Hydrate the session's persisted access mode before deciding whether the
	// agent must be rebuilt. Without this the mode was only in memory, so a
	// restart silently adopted whatever default (or another session's mode)
	// happened to be current.
	c.hydrateSessionPermission(id)

	// Hydrate the per-session plan flag the app's prompt composer reads
	// through its PlanModeFunc hook. A session that predates the feature
	// reads as off.
	c.hydrateSessionPlanMode(id)

	// Each session can have its own access mode. The agent's tools and gate
	// are built for one mode, so switching to a session with a different mode
	// asks the app to rebuild before the next turn.
	_ = c.rebuildForPermission(id)
	// Point the shared agent at this session's selected model before the next
	// request. The choice is per session and never rewrites the global default.
	_ = c.applySessionModel(id)
	// The reopened session takes over the shared working-directory state: the
	// app starts it at the workspace root or its own last directory instead of
	// inheriting the previous session's cwd.
	c.notifySessionActivated(previousID, id)
	c.notify(id, snap)
	return snap, nil
}

// hydrateSessionPermission loads sessionID's persisted access mode into the
// in-memory cache. A session that predates per-session persistence adopts the
// current default once and stores it, so later changes to the default do not
// rewrite its history.
func (c *Controller) hydrateSessionPermission(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" {
		return ""
	}
	c.mu.Lock()
	mode := c.perms[sessionID]
	c.mu.Unlock()
	if mode != "" {
		return mode
	}
	if raw, err := c.store.GetSetting(SessionPermissionKey(sessionID)); err == nil {
		switch strings.TrimSpace(raw) {
		case PermissionFull, PermissionWorkspace, PermissionAsk, PermissionAutoReview:
			mode = strings.TrimSpace(raw)
		}
	}
	if mode == "" {
		mode = PermissionWorkspace
		if settings, err := c.Settings().Load(); err == nil && settings.Permission != "" {
			mode = settings.Permission
		}
		_ = c.store.SetSetting(SessionPermissionKey(sessionID), mode)
	}
	c.mu.Lock()
	if c.perms != nil {
		c.perms[sessionID] = mode
	}
	c.mu.Unlock()
	return mode
}

// Submit starts one turn for sessionID and returns once the turn has been
// accepted. It persists the user message (and a "message" event), seeds the
// snapshot to PhaseRunning, starts runtime.Agent.Run, and drains the returned
// event channel on a goroutine.
//
// A second Submit for a session whose turn is still in flight returns an error
// wrapping ErrTurnRunning. A Submit that arrives after the terminal status was
// observed but before the turn goroutine has fully unwound waits for that turn
// instead of racing it.
func (c *Controller) Submit(ctx context.Context, sessionID, text string) error {
	return c.submit(ctx, sessionID, text, false)
}

// submit is Submit with the automatic flag. Manual submissions clear a prior
// Stop marker; automatic goal continuations respect it and never retry.
func (c *Controller) submit(ctx context.Context, sessionID, text string, automatic bool) error {
	if ctx == nil {
		return fmt.Errorf("%w: submit: nil context", ErrInvalidArgument)
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("%w: submit: empty session id", ErrInvalidArgument)
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("%w: submit: empty prompt", ErrEmptyPrompt)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Reject a controller without an agent before reserving a turn slot,
	// preserving Submit's existing error contract.
	c.mu.Lock()
	hasAgent := c.agent != nil || c.agentFactory != nil
	c.mu.Unlock()
	if !hasAgent {
		return ErrNoAgent
	}

	// Session references (DSH @deepseek-ai/dsh-session-reference parity):
	// resolve the canonical @[label](dsh-session:…) mentions this prompt cites
	// before reserving the turn, so a malformed, self-referential, over-limit
	// or unreadable reference is a plain Submit error the shell can present
	// instead of a silently dropped request. The prompt itself is not changed.
	referenceContext, refErr := c.prepareSessionReferenceContext(ctx, sessionID, text)
	if refErr != nil {
		return fmt.Errorf("session: submit: %w", refErr)
	}

	turnCtx, t, err := c.reserve(ctx, sessionID, automatic)
	if err != nil {
		return err
	}
	launched := false
	// The wait group owns the whole Submit until the drain goroutine takes
	// over, so Close cannot return (and the caller cannot close the store)
	// while this function is still writing.
	defer func() {
		if !launched {
			c.wg.Done()
		}
	}()

	// Read the session after reserving the turn slot (the original order): the
	// goal driver submits its continuation from the finishing turn's goroutine,
	// and a wait-idle observer must see that continuation as an in-flight turn,
	// not as a window between its round counter and its reservation.
	sess, err := c.store.GetSession(sessionID)
	if err != nil {
		c.abortTurn(t)
		return fmt.Errorf("session: submit: %w", err)
	}

	// A session this controller has never loaded may be the tail of a process
	// that died mid-turn. Repair it now (the just-reserved turn is allowed as
	// the only active turn) so the view and the new turn's history see a closed
	// tail, without adding work to the pre-reserve window the goal driver relies
	// on to hand off turns quickly.
	c.mu.Lock()
	hasView := c.views[sessionID] != nil
	c.mu.Unlock()
	if !hasView {
		if err := c.recoverInterruptedSession(sess, t); err != nil {
			c.abortTurn(t)
			return fmt.Errorf("session: submit: recover interrupted turn: %w", err)
		}
		if refreshed, rerr := c.store.GetSession(sessionID); rerr == nil {
			sess = refreshed
		}
	}
	if err := c.ensureView(sess); err != nil {
		c.abortTurn(t)
		return err
	}

	// Snapshot the pending attachments for this turn before starting the run.
	// They are injected into the model-visible prompt (text) and user message
	// (images), then linked to the persisted user message once it is written.
	attachmentRows, attachmentInputs := c.turnAttachmentInputs(sessionID)

	// Start the workspace baseline for the turn's changed-file summary with
	// inline diffs (FP-05). Read Only turns cannot modify files, and a
	// session without a workspace root has nothing to diff. The capture runs
	// before the turn starts and outside bindMu so a slow git call cannot
	// block another binding; the baseline content that a later comparison
	// needs is retained there, and the file tools' target paths are
	// attributed from the turn's ToolStart events (see forward).
	if c.SessionPermissionOrDefault(sessionID) != PermissionAsk {
		if root := strings.TrimSpace(c.workspaceRootForSession(sessionID)); root != "" {
			captureCtx, cancelCapture := context.WithTimeout(context.Background(), workspaceChangesCaptureTimeout)
			workspaceTurn, werr := workspacechanges.StartTurn(captureCtx, root)
			cancelCapture()
			if werr == nil {
				t.workspaceTurn = workspaceTurn
			}
		}
	}

	// Bind the session's workspace and start the turn as one critical section.
	// reserve may have waited for a previous turn, and the user (or another
	// background submit) may have switched the workspace since; Agent.Run pins
	// its topology from the current agent, which is built for the selected
	// workspace's root. bindMu is NOT held across reserve: this controller's
	// goal driver submits the continuation turn from the finishing turn's
	// goroutine, so a Submit blocked in reserve while holding bindMu could
	// deadlock against that continuation. Here nothing blocks on a turn.
	c.bindMu.Lock()
	// reserve switched currentID to the target session, but a concurrent
	// OpenSession/CreateSession that held bindMu while this Submit waited can
	// have switched it again. Re-pin the target before the rebuilds below: the
	// app builds the shared agent for the controller's current session, so the
	// wrong current id would build the wrong permission set (RT-02).
	c.mu.Lock()
	c.currentID = sessionID
	c.mu.Unlock()
	if err := c.bindWorkspace(ctx, sess.WorkspaceID); err != nil {
		c.bindMu.Unlock()
		c.abortTurn(t)
		return fmt.Errorf("session: submit: %w", err)
	}
	// Access mode is captured when the agent is built (tool set and approval
	// gate), and neither CreateSession nor reserve rebuilds. A Submit that
	// targets a session other than the one the agent was built for (automation
	// runs, queued sends, goal continuations) would therefore run with the
	// previous session's permission: Full access would leak into a default
	// Workspace session, and a Read Only session would silently lose its write
	// tools. Rebuild for the target's own mode before the turn starts; a
	// failed rebuild would run the turn under the wrong permission, so refuse
	// the turn instead.
	if rerr := c.rebuildForPermission(sessionID); rerr != nil {
		c.bindMu.Unlock()
		c.abortTurn(t)
		return fmt.Errorf("session: submit: rebuild agent for permission: %w", rerr)
	}
	agent, agentErr := c.agentFor(sessionID)
	if agentErr != nil {
		c.bindMu.Unlock()
		c.abortTurn(t)
		return fmt.Errorf("session: submit: build agent: %w", agentErr)
	}
	if agent == nil {
		c.bindMu.Unlock()
		c.abortTurn(t)
		return ErrNoAgent
	}

	// Point the shared agent at this session's persisted mode before the turn
	// starts. reserve already made sessionID current, so the app's
	// InstructionFunc (Controller.Mode) composes the target preset. Submit can
	// target a session other than the one the user is looking at (queued
	// sends, automation), which previously left the agent running the previous
	// session's topology while Ctrl.Mode reported the target's.
	if err := c.applySessionMode(sessionID); err != nil {
		c.bindMu.Unlock()
		c.abortTurn(t)
		return fmt.Errorf("session: submit: apply agent mode: %w", err)
	}

	// Apply the session's own model to the shared agent for this request. A
	// failed switch keeps the current model rather than dropping the turn.
	_ = c.applySessionModel(sessionID)

	// Queue the referenced-session snapshot as the turn's second user message:
	// the runtime appends it right after the prompt in the provider request, so
	// the user's own message stays byte-identical and the snapshot stays
	// untrusted background.
	if qerr := c.queueSessionReferenceContext(agent, sessionID, referenceContext); qerr != nil {
		c.bindMu.Unlock()
		c.abortTurn(t)
		return fmt.Errorf("session: submit: queue session reference context: %w", qerr)
	}

	// Load the prior conversation before persisting this prompt. Agent.Run
	// reads History synchronously, so doing it first keeps the just-typed
	// prompt out of the seeded conversation; persisting it first made the
	// provider receive the same user message twice and let every turn's
	// history compound.
	events, runErr := agent.RunWithAttachments(turnCtx, sessionID, text, attachmentInputs)
	// RunWithAttachments pins the topology before it returns; the binding is
	// no longer needed. Release bindMu before the error path below, which may
	// call finishTurn and (via the goal driver) submit the continuation turn.
	c.bindMu.Unlock()
	if runErr != nil {
		// The turn never started; the queued snapshot must not leak into a
		// later turn.
		agent.ClearTurnContext(sessionID)
		// Keep the prompt that was typed so the failed attempt is still
		// visible and stored.
		if perr := c.persistUserMessage(t, sessionID, text, attachmentRows); perr != nil {
			c.abortTurn(t)
			return fmt.Errorf("session: submit: persist user message: %w", perr)
		}
		c.forward(t, state.Status{Phase: state.PhaseFailed})
		c.forward(t, state.Error{MsgID: t.id, Err: runErr})
		c.finishTurn(t)
		return fmt.Errorf("session: submit: agent run: %w", runErr)
	}

	if err := c.persistUserMessage(t, sessionID, text, attachmentRows); err != nil {
		c.abortTurn(t)
		return fmt.Errorf("session: submit: persist user message: %w", err)
	}
	// Persist the injected snapshot as its own user row right after the prompt
	// so history and the next provider request carry it in model order and a
	// reopened transcript replays it.
	if referenceContext != "" {
		if err := c.persistSessionReferenceContext(t, sessionID, referenceContext); err != nil {
			c.abortTurn(t)
			return fmt.Errorf("session: submit: persist session reference context: %w", err)
		}
	}

	// The reservation already added this turn to the wait group; the drain
	// goroutine now owns that count (the deferred Done above is skipped).
	go func() {
		defer c.wg.Done()
		c.runTurn(t, events)
	}()
	launched = true
	return nil
}

// persistUserMessage writes the prompt row and its event, updates the session
// row for crash recovery, and folds the message into the live snapshot. The
// trace row is not fatal: the turn's terminal status is written regardless.
func (c *Controller) persistUserMessage(t *turn, sessionID, text string, attachments []store.Attachment) error {
	msg, err := c.store.AppendMessage(store.Message{
		SessionID: sessionID,
		Role:      string(state.RoleUser),
		Content:   text,
		TurnID:    t.id,
	})
	if err != nil {
		return err
	}
	if err := c.appendMessageEvent(msg); err != nil {
		return fmt.Errorf("persist user event: %w", err)
	}
	t.userMsgID = msg.ID
	chips := c.linkAttachments(attachments, msg.ID)
	c.addUserMessage(t, msg, chips)
	_, _ = c.store.UpdateSessionStatus(sessionID, store.SessionRunning)
	return nil
}

// reserve allocates the turn slot for sessionID. It blocks (rather than
// failing) when the previous turn is only waiting to unwind after its terminal
// status, which keeps a Submit issued from an OnUpdate idle callback from
// observing a spurious ErrTurnRunning.
//
// An automatic continuation is different: its caller (the goal driver) runs on
// the finishing turn's goroutine before that turn releases the slot, so reserve
// performs an atomic handoff (delete the terminal turn + register the
// continuation under one lock) instead of waiting for done. That keeps the
// session's turn slot continuously occupied, which wait-idle observers rely on.
//
// automatic turns are the goal driver's continuations: a Stop marker for the
// session suppresses them even when it landed after the finished turn's
// terminal status. Manual turns clear the marker once they own the slot.
func (c *Controller) reserve(ctx context.Context, sessionID string, automatic bool) (context.Context, *turn, error) {
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil, nil, ErrClosed
		}
		if automatic && c.stopRequested[sessionID] {
			c.mu.Unlock()
			return nil, nil, errGoalContinuationStopped
		}
		if existing := c.turns[sessionID]; existing != nil {
			if existing.terminalApplied.Load() {
				if automatic {
					// Atomic slot handoff for the goal driver's continuation.
					// The finishing turn's goroutine calls turnFinished before
					// releasing the slot or closing done, so replacing the
					// finished slot here (under the same lock) guarantees that
					// a wait-idle observer never sees this session without a
					// registered turn: otherwise it could return in the window
					// between the finished turn's deletion and this
					// continuation's reservation.
					delete(c.turns, sessionID)
				} else {
					done := existing.done
					c.mu.Unlock()
					select {
					case <-done:
						continue
					case <-ctx.Done():
						return nil, nil, ctx.Err()
					}
				}
			} else {
				c.mu.Unlock()
				return nil, nil, fmt.Errorf("%w: session %q", ErrTurnRunning, sessionID)
			}
		}

		turnCtx, cancel := context.WithCancel(ctx)
		t := &turn{
			id:        newID(),
			sessionID: sessionID,
			cancel:    cancel,
			ctx:       turnCtx,
			done:      make(chan struct{}),
			phase:     state.PhaseIdle,
			drafts:    make(map[string]*draft),
		}
		c.turns[sessionID] = t
		c.currentID = sessionID
		if !automatic {
			delete(c.stopRequested, sessionID)
		}
		// Add while holding mu: Close sets closed under the same lock and
		// then waits, so it either sees this turn or this branch never ran.
		c.wg.Add(1)
		c.mu.Unlock()
		return turnCtx, t, nil
	}
}

// addUserMessage inserts the persisted prompt at the end of the live snapshot
// and moves it to PhaseRunning before Submit returns, so a caller that waits
// for idle cannot accidentally observe the pre-turn idle snapshot. The
// attachments sent with the message move from the composer's pending chips
// onto the message.
func (c *Controller) addUserMessage(t *turn, msg *store.Message, attachments []state.Attachment) {
	c.mu.Lock()
	snap := c.views[t.sessionID]
	if snap == nil {
		snap = state.NewSnapshot()
		snap.SessionID = t.sessionID
	}
	next := *snap
	next.SessionID = t.sessionID
	next.Seq = snap.Seq + 1
	if t.stopped.Load() {
		next.Phase = state.PhaseIdle
	} else {
		next.Phase = state.PhaseRunning
	}
	// Remove only the attachments that this turn consumed; a file picked
	// while the turn was starting stays staged for the next turn.
	if len(attachments) > 0 && len(next.Attachments) > 0 {
		consumed := make(map[string]bool, len(attachments))
		for _, a := range attachments {
			consumed[a.ID] = true
		}
		pending := make([]state.Attachment, 0, len(next.Attachments))
		for _, a := range next.Attachments {
			if !consumed[a.ID] {
				pending = append(pending, a)
			}
		}
		next.Attachments = pending
	}
	msgs := make([]state.Message, len(snap.Messages), len(snap.Messages)+1)
	copy(msgs, snap.Messages)
	next.Messages = append(msgs, state.Message{
		MsgID: msg.ID,
		// Seq is the Seq this fold assigns the user message, so the message
		// and the tools that turn starts share one snapshot Seq axis. LastSeq
		// equals Seq: the user message is its own visible row and no token
		// event extends it.
		Seq:         next.Seq,
		LastSeq:     next.Seq,
		Role:        state.RoleUser,
		Text:        msg.Content,
		Attachments: append([]state.Attachment(nil), attachments...),
	})
	c.views[t.sessionID] = &next
	current := c.currentID == t.sessionID
	c.mu.Unlock()

	if current {
		c.notify(t.sessionID, &next)
	}
}

// runTurn is the drain goroutine for one accepted turn.
func (c *Controller) runTurn(t *turn, events <-chan state.Event) {
	for ev := range events {
		c.forward(t, ev)
	}
	c.finishTurn(t)
}

// recordToolCall mirrors one tool lifecycle event into the tool_calls table.
// The events table remains the ordered trace; this table is the queryable
// per-call record the store documentation promises. Persistence is best
// effort: a tracing failure must not fail the turn.
func (c *Controller) recordToolCall(sessionID string, ev state.Event) {
	switch e := ev.(type) {
	case state.ToolStart:
		_ = c.store.RecordToolCall(store.ToolCall{
			ID:        e.CallID,
			SessionID: sessionID,
			Name:      e.Name,
			Args:      string(e.Args),
			Status:    "running",
			StartedAt: time.Now().UTC(),
		})
	case state.ToolEnd:
		status := "done"
		switch {
		case e.Cancelled:
			status = "cancelled"
		case e.IsError:
			status = "error"
		}
		_ = c.store.FinishToolCall(e.CallID, e.Result, status, time.Now().UTC())
	}
}

// spillToolResult replaces an oversized tool result with a summary that points
// at the file holding the full output. A missing workspace or a write error
// keeps the original result: spilling is an optimization, never a reason to
// lose the tool output.
func (c *Controller) spillToolResult(sessionID string, te state.ToolEnd) state.ToolEnd {
	if len(te.Result) <= toolResultSpillBytes {
		return te
	}
	root := c.workspaceRootForSession(sessionID)
	if root == "" {
		return te
	}
	summary, ok, err := spillToolResultFile(root, te.CallID, te.Result)
	if err != nil {
		// Record the failure for diagnostics; the turn keeps the full result.
		fmt.Fprintf(os.Stderr, "session: spill tool result %q: %v\n", te.CallID, err)
		return te
	}
	if !ok {
		return te
	}
	te.Result = summary
	return te
}

// workspaceRootForSession resolves the session's workspace root, which is where
// spilled tool results live. Empty means the workspace cannot be resolved.
func (c *Controller) workspaceRootForSession(sessionID string) string {
	if c.store == nil {
		return ""
	}
	sess, err := c.store.GetSession(sessionID)
	if err != nil || sess == nil || sess.WorkspaceID == "" {
		return ""
	}
	ws, err := c.store.GetWorkspace(sess.WorkspaceID)
	if err != nil || ws == nil {
		return ""
	}
	return ws.Root
}

// forward performs the per-event work: accumulate assistant text, persist the
// event, fold it into the session snapshot, fan it out through the Broker and
// notify OnUpdate listeners. Persistence failures do not stop the stream; the
// UI stays live and the terminal status is retried by finishTurn.
func (c *Controller) forward(t *turn, ev state.Event) {
	// Streaming subscribers (headless/ACP) receive the semantic event stream
	// of this session in order.
	c.publishSessionEvent(t.sessionID, ev)
	// Large tool results are persisted as a workspace file plus a short
	// summary, so the next turn's history does not re-send the whole output
	// (the current turn's model already saw the full result).
	if te, ok := ev.(state.ToolEnd); ok {
		ev = c.spillToolResult(t.sessionID, te)
	}
	switch e := ev.(type) {
	case state.Token:
		d := t.draft(e.MsgID)
		d.text.WriteString(e.Text)
	case state.Reasoning:
		d := t.draft(e.MsgID)
		d.reasoning.WriteString(e.Text)
	case state.ToolStart:
		t.toolStarted = true
		// Attribute a file tool's target to the turn's changed-file report
		// (FP-05). The event may be handled after the tool already wrote the
		// file, so this only classifies the path; the comparison's before
		// side comes from the start-of-turn baseline.
		if t.workspaceTurn != nil {
			t.workspaceTurn.NoteToolStart(e.Name, e.Args)
		}
	}

	// A terminal status is the point after which the assistant text is final.
	// Persist it (and the session status) before the snapshot becomes idle so
	// a caller woken by OnUpdate can immediately read a consistent store.
	if st, ok := ev.(state.Status); ok && isTerminalPhase(st.Phase) {
		c.persistDrafts(t)
		c.persistSessionStatus(t, st.Phase)
	}

	_ = c.appendEvent(t.sessionID, ev)
	c.recordToolCall(t.sessionID, ev)

	c.mu.Lock()
	snap, requestedApproval := c.applyLocked(t, ev)
	approvalHook := c.onApprovalRequested
	c.mu.Unlock()

	c.broker.Publish(ev)
	c.notify(t.sessionID, snap)

	// Optional observer for newly pending approvals, including background
	// sessions that OnUpdate never publishes. Deduplicated by call id in
	// applyLocked, so a repeated event cannot notify twice.
	if requestedApproval != nil && approvalHook != nil {
		approvalHook(t.sessionID, *requestedApproval)
	}
}

// applyLocked folds one event into the session snapshot and its bookkeeping and
// returns the snapshot plus the approval request that this event newly made
// pending (nil for every other event and for a repeated call id).
func (c *Controller) applyLocked(t *turn, ev state.Event) (*state.Snapshot, *state.ApprovalRequest) {
	var newApproval *state.ApprovalRequest
	snap := c.views[t.sessionID]
	if snap == nil {
		snap = state.NewSnapshot()
		snap.SessionID = t.sessionID
	}
	next := state.Apply(snap, ev)
	if next.SessionID == "" {
		cp := *next
		cp.SessionID = t.sessionID
		next = &cp
	}

	switch e := ev.(type) {
	case state.ApprovalRequest:
		// A request that raced with Stop belongs to a turn whose context is
		// already cancelled; keep it out of the UI and the decision API.
		if t.stopped.Load() || t.terminalApplied.Load() {
			break
		}
		if c.pending == nil {
			c.pending = make(map[string]map[string]state.ApprovalRequest)
		}
		requests := c.pending[t.sessionID]
		if requests == nil {
			requests = make(map[string]state.ApprovalRequest)
			c.pending[t.sessionID] = requests
		}
		if _, existed := requests[e.CallID]; !existed {
			request := e
			newApproval = &request
		}
		requests[e.CallID] = e
	case state.ToolStart:
		if c.toolCalls == nil {
			c.toolCalls = make(map[string]map[string]string)
		}
		calls := c.toolCalls[t.sessionID]
		if calls == nil {
			calls = make(map[string]string)
			c.toolCalls[t.sessionID] = calls
		}
		calls[e.CallID] = e.Name
		if e.Name == "subagent" {
			if c.toolArgs == nil {
				c.toolArgs = make(map[string]map[string]json.RawMessage)
			}
			args := c.toolArgs[t.sessionID]
			if args == nil {
				args = make(map[string]json.RawMessage)
				c.toolArgs[t.sessionID] = args
			}
			args[e.CallID] = append(json.RawMessage(nil), e.Args...)
		}
	case state.ToolEnd:
		// The name lives on ToolStart; remember it until the call ends so a
		// tool result can be interpreted without parsing the result blindly.
		name := ""
		if calls := c.toolCalls[t.sessionID]; calls != nil {
			name = calls[e.CallID]
			delete(calls, e.CallID)
			if len(calls) == 0 {
				delete(c.toolCalls, t.sessionID)
			}
		}
		// The subagent tool returns its child's final text, so the team entry
		// is assembled from the call's arguments and that text instead of a
		// structured result document.
		args := c.toolArgs[t.sessionID][e.CallID]
		if callArgs := c.toolArgs[t.sessionID]; callArgs != nil {
			delete(callArgs, e.CallID)
			if len(callArgs) == 0 {
				delete(c.toolArgs, t.sessionID)
			}
		}
		if name == "subagent" && !e.IsError {
			c.recordTeammateLocked(t.sessionID, e.CallID, args, e.Result)
		}
		if name != "" && !e.IsError {
			c.applyToolResultLocked(t.sessionID, e.CallID, name, e.Result)
		}
	case state.Status:
		t.phase = e.Phase
		if e.Phase == state.PhaseFailed {
			t.failed.Store(true)
		}
		if isTerminalPhase(e.Phase) {
			t.terminalApplied.Store(true)
			// A terminal phase ends the turn, so no pending decision can still
			// reach a running tool.
			delete(c.pending, t.sessionID)
			delete(c.toolArgs, t.sessionID)
		}
	case state.Error:
		t.failed.Store(true)
		if t.errText == "" && e.Err != nil {
			t.errText = e.Err.Error()
		}
	}

	// Stop is an optimistic state change: late events (for example tokens
	// already buffered by the runtime) must not make a stopped turn look live
	// again.
	if t.stopped.Load() && next.Phase != state.PhaseIdle {
		cp := *next
		cp.Phase = state.PhaseIdle
		next = &cp
	}

	c.views[t.sessionID] = next
	return next, newApproval
}

// applyToolResultLocked folds a successful tool result into the session state
// the UI reads. Tools cannot see the session id (the runtime keeps it private),
// so the structured result document is the transport. Malformed or foreign
// results are ignored: the raw event is still in the trajectory either way.
//
// c.mu must be held.
func (c *Controller) applyToolResultLocked(sessionID, callID, name, result string) {
	c.applyToolResultAtLocked(sessionID, callID, name, result, time.Now().UTC())
}

// applyToolResultAtLocked is applyToolResultLocked with the recording time,
// used when replaying persisted events so a reopened session keeps the
// original presented/asked timestamps.
//
// c.mu must be held.
func (c *Controller) applyToolResultAtLocked(sessionID, callID, name, result string, at time.Time) {
	switch name {
	case "todo":
		var in tools.TodoResult
		if err := json.Unmarshal([]byte(result), &in); err != nil || in.Kind != "todo" {
			return
		}
		c.todos[sessionID] = append([]tools.Todo(nil), in.Tasks...)
	case "ask_user":
		// The runtime's question-gate result carries the answer and marks the
		// status answered (FP-16); the card is already resolved by
		// AnswerQuestion, so only record pending documents here.
		var in struct {
			Kind     string   `json:"kind"`
			Question string   `json:"question"`
			Options  []string `json:"options,omitempty"`
			Context  string   `json:"context,omitempty"`
			Status   string   `json:"status"`
			Answer   string   `json:"answer,omitempty"`
		}
		if err := json.Unmarshal([]byte(result), &in); err != nil || in.Kind != "question" {
			return
		}
		if strings.EqualFold(in.Status, "answered") || strings.TrimSpace(in.Answer) != "" {
			if questions := c.questions[sessionID]; questions != nil {
				delete(questions, callID)
				if len(questions) == 0 {
					delete(c.questions, sessionID)
				}
			}
			return
		}
		question := strings.TrimSpace(in.Question)
		if question == "" {
			return
		}
		if c.questions == nil {
			c.questions = make(map[string]map[string]tools.Question)
		}
		questions := c.questions[sessionID]
		if questions == nil {
			questions = make(map[string]tools.Question)
			c.questions[sessionID] = questions
		}
		questions[callID] = tools.Question{
			CallID:   callID,
			Question: question,
			Options:  append([]string(nil), in.Options...),
			Context:  in.Context,
			Status:   "pending",
			AskedAt:  at,
		}
	case "present":
		var in tools.ArtifactResult
		if err := json.Unmarshal([]byte(result), &in); err != nil || in.Kind != "artifact" {
			return
		}
		title := strings.TrimSpace(in.Title)
		if title == "" {
			return
		}
		c.artifacts[sessionID] = append(c.artifacts[sessionID], tools.Artifact{
			CallID:      callID,
			Title:       title,
			Path:        in.Path,
			Markdown:    in.Markdown,
			Kind:        in.Type,
			PresentedAt: at,
		})
	case "goal":
		var in tools.GoalResult
		if err := json.Unmarshal([]byte(result), &in); err != nil || in.Kind != "goal" {
			return
		}
		if in.Goal == nil {
			if in.Action != "clear" {
				// get on an empty session reports no goal; leave any stored
				// goal alone rather than clearing it.
				return
			}
			// clear removed the goal (the tool persisted the removal too).
			previous := c.goals[sessionID]
			delete(c.goals, sessionID)
			c.goalDriver.goalChanged(sessionID, previous, nil)
			return
		}
		goal := *in.Goal
		goal.Steps = append([]string(nil), in.Goal.Steps...)
		previous := c.goals[sessionID]
		c.goals[sessionID] = &goal
		// A brand-new objective starts a fresh automatic-continuation budget;
		// progress updates for the same active objective must not reset it.
		// Paused/blocked/terminal goals are not continuable, which the driver
		// re-checks in turnFinished; passing the goal (not nil) here keeps the
		// per-session round counter for the transcript/UI (FP-04).
		c.goalDriver.goalChanged(sessionID, previous, &goal)
	}
}

// recordTeammateLocked folds one successful subagent tool call into the
// session's team. args is the call's JSON argument object; output is the child's
// final text. Malformed arguments still record the teammate under the default
// name, because the panel is informative rather than authoritative.
//
// c.mu must be held.
func (c *Controller) recordTeammateLocked(sessionID, callID string, args json.RawMessage, output string) {
	c.recordTeammateAtLocked(sessionID, callID, args, output, time.Now().UTC())
}

// recordTeammateAtLocked is recordTeammateLocked with the recording time.
//
// c.mu must be held.
func (c *Controller) recordTeammateAtLocked(sessionID, callID string, args json.RawMessage, output string, at time.Time) {
	var in struct {
		Task  string `json:"task"`
		Agent string `json:"agent"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &in)
	}
	name := strings.TrimSpace(in.Agent)
	if name == "" {
		name = "subagent"
	}
	c.team[sessionID] = append(c.team[sessionID], tools.Teammate{
		CallID:    callID,
		Name:      name,
		Task:      strings.TrimSpace(in.Task),
		Status:    tools.SubagentStatusCompleted,
		Output:    output,
		UpdatedAt: at,
	})
}

// Events returns the persisted event trace for sessionID, ordered oldest
// first. It is the trajectory tab's data source; it reads the store directly
// so a tab opened after history was loaded still sees every event.
func (c *Controller) Events(ctx context.Context, sessionID string) ([]store.Event, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("%w: events: empty session id", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.store.ListEvents(sessionID)
}

// Todos returns a copy of the current session's todo list, oldest first. It
// returns nil when no todo tool result has been recorded.
func (c *Controller) Todos() []tools.Todo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]tools.Todo(nil), c.todos[c.currentID]...)
}

// SessionTodos returns a copy of sessionID's todo list, oldest first.
func (c *Controller) SessionTodos(sessionID string) []tools.Todo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]tools.Todo(nil), c.todos[sessionID]...)
}

// SetTodos replaces the current session's todo list. The live session calls it
// through the todo panel's data source; tests use it to seed a list without
// running an agent. The slice is copied.
func (c *Controller) SetTodos(todos []tools.Todo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.currentID == "" {
		return
	}
	c.todos[c.currentID] = append([]tools.Todo(nil), todos...)
}

// AddTodo appends one pending task to the current session's list and returns
// it. The task IDs are allocated per session, starting at 1.
func (c *Controller) AddTodo(task string) (tools.Todo, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return tools.Todo{}, fmt.Errorf("%w: add todo: empty task", ErrInvalidArgument)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return tools.Todo{}, ErrClosed
	}
	if c.currentID == "" {
		return tools.Todo{}, fmt.Errorf("%w: add todo: no session", ErrInvalidArgument)
	}
	next := 1
	for _, todo := range c.todos[c.currentID] {
		if todo.ID >= next {
			next = todo.ID + 1
		}
	}
	todo := tools.Todo{ID: next, Task: task, Status: tools.TodoPending}
	c.todos[c.currentID] = append(c.todos[c.currentID], todo)
	return todo, nil
}

// Questions returns the current session's pending user questions, oldest
// first. The live session renders one answer card per entry.
func (c *Controller) Questions() []tools.Question {
	c.mu.Lock()
	defer c.mu.Unlock()
	questions := c.questions[c.currentID]
	if len(questions) == 0 {
		return nil
	}
	out := make([]tools.Question, 0, len(questions))
	for _, question := range questions {
		question.Options = append([]string(nil), question.Options...)
		out = append(out, question)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].AskedAt.Equal(out[j].AskedAt) {
			return out[i].AskedAt.Before(out[j].AskedAt)
		}
		return out[i].CallID < out[j].CallID
	})
	return out
}

// AnswerQuestion answers a pending ask_user question by submitting the answer
// as the session's next user turn. The question is removed once the turn is
// accepted; if Submit fails (for example while another turn is still
// unwinding) the question is restored so the card stays actionable.
func (c *Controller) AnswerQuestion(ctx context.Context, callID, answer string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	callID = strings.TrimSpace(callID)
	answer = strings.TrimSpace(answer)
	if callID == "" || answer == "" {
		return fmt.Errorf("%w: answer question: empty call id or answer", ErrInvalidArgument)
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	// FP-16: a blocking question (AskQuestion) is answered in place, so the
	// turn continues with the answer instead of a new user turn.
	if waiter := c.questionWaiters[callID]; waiter != nil {
		delete(c.questionWaiters, callID)
		questions := c.questions[waiter.sessionID]
		delete(questions, callID)
		if len(questions) == 0 {
			delete(c.questions, waiter.sessionID)
		}
		sessionID := waiter.sessionID
		snap := c.views[sessionID]
		c.mu.Unlock()
		waiter.ch <- questionAnswer{text: answer}
		c.notify(sessionID, snap)
		return c.appendQuestionAnswered(sessionID, callID)
	}
	sessionID := c.currentID
	questions := c.questions[sessionID]
	question, ok := questions[callID]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("%w: answer question: unknown call id %q", ErrInvalidArgument, callID)
	}
	delete(questions, callID)
	if len(questions) == 0 {
		delete(c.questions, sessionID)
	}
	c.mu.Unlock()

	if err := c.Submit(ctx, sessionID, answer); err != nil {
		c.mu.Lock()
		if !c.closed {
			if c.questions[sessionID] == nil {
				c.questions[sessionID] = make(map[string]tools.Question)
			}
			c.questions[sessionID][callID] = question
		}
		c.mu.Unlock()
		return err
	}
	// Persist the resolution after the answer turn was accepted. Without it
	// the next OpenSession replays the original tool_end and puts the
	// question back on screen.
	return c.appendQuestionAnswered(sessionID, callID)
}

// Artifacts returns the current session's presented artifacts, oldest first.
// They are metadata only; no file was copied or opened.
func (c *Controller) Artifacts() []tools.Artifact {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]tools.Artifact(nil), c.artifacts[c.currentID]...)
}

// persistDrafts appends one assistant message per MsgID as soon as that
// message's text is final. The skipped flag makes it safe to call more than
// once without duplicating rows.
func (c *Controller) persistDrafts(t *turn) {
	for _, id := range t.order {
		d := t.drafts[id]
		if d == nil || d.saved {
			continue
		}
		text := d.text.String()
		// A draft without visible text is a leftover from a stopped or failed
		// turn that only produced reasoning. Persisting it used to leave an
		// empty assistant row that polluted the export and the next turn's
		// history; the reasoning stays visible in the Trajectory from its
		// events, so the row is skipped.
		if strings.TrimSpace(text) == "" {
			d.saved = true
			continue
		}
		reasoning := d.reasoning.String()
		msgID := d.msgID
		if msgID == "" {
			msgID = newID()
			d.msgID = msgID
		}
		if _, err := c.store.AppendMessage(store.Message{
			ID:        msgID,
			SessionID: t.sessionID,
			Role:      string(state.RoleAssistant),
			Content:   text,
			Reasoning: reasoning,
			TurnID:    t.id,
		}); err != nil {
			// Keep the unsaved flag; finishTurn retries once.
			continue
		}
		d.saved = true
	}
}

// persistSessionStatus writes the terminal session status once per turn.
func (c *Controller) persistSessionStatus(t *turn, phase state.Phase) {
	if t.statusPersisted {
		return
	}
	status := phaseToStore(phase, t.failed.Load())
	if t.stopped.Load() {
		status = store.SessionIdle
	}
	if _, err := c.store.UpdateSessionStatus(t.sessionID, status); err != nil {
		return
	}
	t.statusPersisted = true
}

// workspaceChangesCaptureTimeout bounds one workspace snapshot (before and
// after) so a very large non-Git workspace cannot stall turn completion.
const workspaceChangesCaptureTimeout = 5 * time.Second

// emitWorkspaceChanges diffs the post-turn workspace against the baseline
// captured in submit and publishes state.WorkspaceChanges for the UI/telemetry.
// Each change carries its unified diff (bounded by the package's per-turn
// budget) so the UI and a replayed session show the inline comparison.
// Capture/diff failures stay silent: the summary is display data, never a
// reason to fail or delay a turn beyond the bounded timeout.
func (c *Controller) emitWorkspaceChanges(t *turn) {
	if c == nil || t == nil || t.workspaceTurn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), workspaceChangesCaptureTimeout)
	defer cancel()
	changes := t.workspaceTurn.Finish(ctx)
	if len(changes) == 0 {
		return
	}
	out := make([]state.WorkspaceChange, 0, len(changes))
	for _, change := range changes {
		out = append(out, state.WorkspaceChange{
			Path:      change.Path,
			Status:    change.Status,
			Diff:      change.Diff,
			Added:     change.Added,
			Removed:   change.Removed,
			Binary:    change.Binary,
			Oversized: change.Oversized,
			Coarse:    change.Coarse,
			Truncated: change.Truncated,
		})
	}
	c.forward(t, state.WorkspaceChanges{TurnID: t.id, Changes: out})
}

// finishTurn closes out a turn: it guarantees a terminal status and persisted
// assistant messages, then unregisters the turn and wakes any Submit waiting
// on it. It runs exactly once per turn.
func (c *Controller) finishTurn(t *turn) {
	reason := state.ReasonNone
	stopped := t.stopped.Load() || (t.ctx != nil && t.ctx.Err() != nil)
	switch {
	case !stopped && t.failed.Load():
		reason = state.ReasonFailed
	case stopped:
		reason = state.ReasonStopped
	}
	if !t.terminalApplied.Load() {
		phase := state.PhaseIdle
		if reason == state.ReasonFailed {
			phase = state.PhaseFailed
		}
		c.forward(t, state.Status{Phase: phase, Reason: reason})
	}
	// Persist the per-turn marker for the transcript: a later reopen must show
	// which historical turns were stopped/failed, not only the latest one.
	// Completed turns keep no mark.
	if reason != state.ReasonNone && t.userMsgID != "" {
		c.forward(t, state.TurnEnd{TurnID: t.id, UserMsgID: t.userMsgID, Reason: reason})
	}
	c.persistDrafts(t)
	if !t.statusPersisted {
		c.persistSessionStatus(t, t.phase)
	}
	// Report the files the turn changed (FP-05). The capture+diff runs on the
	// turn goroutine after the terminal status, before done closes, so the
	// summary belongs to this turn's persisted history.
	c.emitWorkspaceChanges(t)

	// Optional terminal-turn observer: OnUpdate only publishes the current
	// session, so the desktop shell needs this hook to notify about background
	// sessions. It runs once per turn, after the terminal status is persisted,
	// on the turn's goroutine.
	c.mu.Lock()
	onTurnFinished := c.onTurnFinished
	c.mu.Unlock()
	if onTurnFinished != nil {
		stopped := t.stopped.Load() || (t.ctx != nil && t.ctx.Err() != nil)
		onTurnFinished(TurnOutcome{
			SessionID: t.sessionID,
			TurnID:    t.id,
			Failed:    t.failed.Load() && !stopped,
			Stopped:   stopped,
			HasOutput: len(t.order) > 0 || t.toolStarted,
			Error:     t.errText,
		})
	}

	// The first successfully completed turn is also the point where a session
	// earns its automatic title. maybeAutoTitle persists and publishes the
	// title off the turn goroutine; it never delays turn completion.
	c.maybeAutoTitle(t)

	// Automatic goal continuation. This is the only trigger: it runs exactly
	// once per terminal turn, while this turn still owns the slot, so the
	// driver's reserve hands the slot over atomically (delete the finished
	// turn and register the continuation under one lock). Doing this before
	// the slot is released closes the window where a wait-idle observer could
	// see Idle with no turn registered and miss the continuation entirely.
	// A user Stop leaves t.stopped set; a caller-cancelled turn context leaves
	// t.ctx.Err() set. Both suppress the continuation.
	c.mu.Lock()
	driver := c.goalDriver
	c.mu.Unlock()
	if driver != nil {
		stopped := t.stopped.Load() || (t.ctx != nil && t.ctx.Err() != nil)
		driver.turnFinished(c, t.sessionID, stopped)
	}

	// Release the slot. A continuation that took the slot over already
	// replaced it in reserve, so this delete is then a no-op; done is closed
	// last so a waiter that was waiting on this turn only proceeds once any
	// continuation is registered and running.
	c.mu.Lock()
	if c.turns[t.sessionID] == t {
		delete(c.turns, t.sessionID)
	}
	c.mu.Unlock()

	close(t.done)
}

// abortTurn unwinds a reserved turn that never started draining.
func (c *Controller) abortTurn(t *turn) {
	t.cancel()
	c.mu.Lock()
	if c.turns[t.sessionID] == t {
		delete(c.turns, t.sessionID)
	}
	c.mu.Unlock()
	close(t.done)
}

// Stop cancels the current session's turn. See StopSession for the
// per-session semantics used by the headless/ACP runtime.
func (c *Controller) Stop() {
	c.mu.Lock()
	sessionID := c.currentID
	c.mu.Unlock()
	c.StopSession(sessionID)
}

// StopSession cancels sessionID's turn. It is idempotent and does not block
// waiting for the runtime to unwind; the snapshot is moved to PhaseIdle and
// the session row to idle immediately. A later Submit for the same session
// waits for the cancelled turn to finish unwinding. An empty or unknown session
// id is a no-op, so the ACP/headless runtime can cancel defensively.
func (c *Controller) StopSession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	// A session's asynchronous team outlives the turn slot alone (the members
	// run in the team manager, not as tools of this turn), so every Stop
	// request stops them too. It runs off the goroutine and without mu held
	// because stopping members waits for them to settle; the shell's Stop path
	// calls the same hook, and the manager tolerates a repeated StopSession.
	stopper := c.teamStopper
	if stopper != nil {
		go stopper(sessionID)
	}
	// Remember the Stop even when the turn slot is already empty or terminal:
	// the goal driver may be submitting its continuation right now, and the
	// automatic reserve path checks this marker before accepting it (a Stop
	// that lands after the handoff cancels the registered continuation below).
	c.stopRequested[sessionID] = true
	t := c.turns[sessionID]
	if t == nil {
		c.mu.Unlock()
		return
	}
	// The turn already reached a terminal phase; its terminal status is
	// authoritative and must not be rewritten to idle by a late Stop.
	if t.terminalApplied.Load() {
		c.mu.Unlock()
		return
	}
	if !t.stopped.CompareAndSwap(false, true) {
		c.mu.Unlock()
		return
	}
	// Cancelling the turn makes any outstanding decision moot: drop the
	// request so the composer stops showing the card at once.
	delete(c.pending, t.sessionID)
	cancel := t.cancel
	snap := c.views[t.sessionID]
	var next *state.Snapshot
	if snap != nil {
		applied := state.Apply(snap, state.Status{Phase: state.PhaseIdle, Reason: state.ReasonStopped})
		if applied.SessionID == "" {
			cp := *applied
			cp.SessionID = t.sessionID
			applied = &cp
		}
		c.views[t.sessionID] = applied
		next = applied
	}
	c.mu.Unlock()

	cancel()
	_, _ = c.store.UpdateSessionStatus(t.sessionID, store.SessionIdle)
	if next != nil {
		c.notify(t.sessionID, next)
	}
}

// Wait blocks until every turn currently in flight has finished unwinding.
// It is intended for headless tests and shutdown paths; the UI normally waits
// for the terminal Phase through OnUpdate instead.
func (c *Controller) Wait() {
	c.mu.Lock()
	turns := make([]*turn, 0, len(c.turns))
	for _, t := range c.turns {
		turns = append(turns, t)
	}
	c.mu.Unlock()
	for _, t := range turns {
		<-t.done
	}
}

// Snapshot returns the current session's immutable snapshot. It never returns
// nil; before any session exists it returns an empty idle snapshot. The snapshot
// comes from the controller's own per-session fold (views), never from a raw
// broker projector: the projector is session-unaware and cannot represent user
// messages or a session switch (AR-05).
func (c *Controller) Snapshot() *state.Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if snap := c.views[c.currentID]; snap != nil {
		return snap
	}
	if c.currentID != "" {
		snap := state.NewSnapshot()
		snap.SessionID = c.currentID
		return snap
	}
	return state.NewSnapshot()
}

// OnUpdate installs fn as an update listener. Listeners are additive and are
// called with the immutable snapshot of the current session after each applied
// event and after a session is created, opened or stopped. Passing nil removes
// every listener.
//
// Callbacks may run on a turn goroutine, so they must be cheap and must not
// call Close or Wait (they would wait for the goroutine that is invoking
// them). Calling Snapshot, OpenSession, Submit or Stop is safe.
func (c *Controller) OnUpdate(fn func(*state.Snapshot)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if fn == nil {
		c.listeners = nil
		return
	}
	c.listeners = append(c.listeners, fn)
}

// TurnOutcome describes one terminal turn for TurnFinishedHook observers.
type TurnOutcome struct {
	SessionID string
	// TurnID identifies the turn. A hook may see the same turn only once, but
	// observers that keep history use it to deduplicate defensively.
	TurnID string
	// Failed reports an error end. A stopped turn is not a failure.
	Failed bool
	// Stopped reports that the user (or shutdown) cancelled the turn.
	Stopped bool
	// HasOutput reports that the turn produced something the user could read:
	// assistant text/reasoning or a tool call. A turn stopped before anything
	// streamed has none.
	HasOutput bool
	// Error is the turn's failure text (the first state.Error message), empty
	// for completed or stopped turns. Observers that persist an execution
	// history (automation runs) record it for diagnostics.
	Error string
}

// TurnFinishedHook observes a terminal turn of any session.
//
// OnUpdate only publishes the current session, so a background session's turn
// is invisible to the desktop shell without this hook. It is called exactly once
// per turn, on the turn's goroutine, after the terminal status is persisted;
// keep it cheap and non-blocking (the built-in desktop notification path only
// does a map lookup and may spawn the notification goroutine).
type TurnFinishedHook func(TurnOutcome)

// SetOnTurnFinished registers an observer for terminal turns of every session,
// current or background. Passing nil removes it. The hook is optional: with
// none installed the Controller behaves exactly as before.
func (c *Controller) SetOnTurnFinished(hook TurnFinishedHook) {
	c.mu.Lock()
	c.onTurnFinished = hook
	c.mu.Unlock()
}

// ApprovalRequestedHook observes an approval request that just became pending.
//
// OnUpdate only publishes the current session, so a background session waiting
// for approval is invisible to the desktop shell without this hook. It is called
// once per call id, on the turn's goroutine, after the request is recorded and
// the current-session snapshot is published. The request carries the tool name
// and arguments: observers should not render them outside the app.
type ApprovalRequestedHook func(sessionID string, request state.ApprovalRequest)

// SetOnApprovalRequested registers an observer for newly pending approvals of
// every session, current or background. Repeated ApprovalRequest events for the
// same call id are not reported again; decisions and clearing do not re-report.
// Passing nil removes the hook. The hook is optional: with none installed the
// Controller behaves exactly as before.
func (c *Controller) SetOnApprovalRequested(hook ApprovalRequestedHook) {
	c.mu.Lock()
	c.onApprovalRequested = hook
	c.mu.Unlock()
}

// Close cancels every in-flight turn and automatic-title request, waits for
// them to unwind and persists their terminal state. A Projector supplied through
// Config was already closed by New (AR-05); Close never owns one.
func (c *Controller) Close() error {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		for _, t := range c.turns {
			t.stopped.Store(true)
			t.cancel()
		}
		if c.titleCancel != nil {
			c.titleCancel()
		}
		clear(c.pending)
		for callID, waiter := range c.questionWaiters {
			waiter.ch <- questionAnswer{err: ErrClosed}
			delete(c.questionWaiters, callID)
		}
	}
	c.mu.Unlock()

	c.wg.Wait()
	// Drop every cached per-session agent; the app closes the owned bundles
	// through the discard callback.
	c.mu.Lock()
	agents := c.agents
	c.agents = make(map[string]*runtime.Agent)
	c.agentGates = make(map[string]*runtime.ApprovalQueue)
	c.builtPermissions = make(map[string]string)
	discard := c.agentDiscard
	c.mu.Unlock()
	for sessionID, agent := range agents {
		if discard != nil && agent != nil {
			discard(sessionID, agent)
		}
	}
	return nil
}

// ensureWorkspace resolves the store workspace used for new sessions. A
// configured WorkspaceID that does not exist is created with that name (the
// store generates ids); an empty WorkspaceID reuses the first workspace or
// creates "default".
func (c *Controller) ensureWorkspace() (string, error) {
	c.wsMu.Lock()
	defer c.wsMu.Unlock()

	workspaces, err := c.store.ListWorkspaces()
	if err != nil {
		return "", fmt.Errorf("session: resolve workspace: %w", err)
	}
	if c.workspaceID != "" {
		for _, w := range workspaces {
			if w.ID == c.workspaceID {
				_ = c.store.SetSetting(SettingWorkspace, w.ID)
				return w.ID, nil
			}
		}
		w, err := c.store.CreateWorkspace(c.workspaceID, "")
		if err != nil {
			return "", fmt.Errorf("session: create workspace %q: %w", c.workspaceID, err)
		}
		c.workspaceID = w.ID
		_ = c.store.SetSetting(SettingWorkspace, w.ID)
		return w.ID, nil
	}
	if len(workspaces) > 0 {
		c.workspaceID = workspaces[0].ID
		_ = c.store.SetSetting(SettingWorkspace, c.workspaceID)
		return c.workspaceID, nil
	}
	w, err := c.store.CreateWorkspace("default", "")
	if err != nil {
		return "", fmt.Errorf("session: create default workspace: %w", err)
	}
	c.workspaceID = w.ID
	_ = c.store.SetSetting(SettingWorkspace, w.ID)
	return w.ID, nil
}

// lastUserMessageID returns the newest persisted user message id of sessionID,
// or "" when the session has none. Crash recovery uses it to anchor the
// interrupted-turn marker in the transcript.
func (c *Controller) lastUserMessageID(sessionID string) string {
	msgs, err := c.store.ListMessages(sessionID)
	if err != nil {
		return ""
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(msgs[i].Role), string(state.RoleUser)) {
			return msgs[i].ID
		}
	}
	return ""
}

// terminalStoreStatus reports whether a persisted session status already closes
// the latest turn.
func terminalStoreStatus(status store.SessionStatus) bool {
	switch status {
	case store.SessionIdle, store.SessionFailed:
		return true
	default:
		return false
	}
}

// terminalToolCallStatus reports whether a persisted tool call already has an
// end row.
func terminalToolCallStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "done", "error", "cancelled", "denied":
		return true
	default:
		return false
	}
}

// recoveredToolResult is the model-visible result written for a call that a
// stopped or crashed process left open. It mirrors DSH's conservative
// TOOL_OUTCOME_UNKNOWN wording: verify side effects before retrying.
const recoveredToolResult = "Interrupted before the tool call finished; its outcome is unknown. Retry only if the operation is read-only or idempotent; verify external state first if it may have side effects."

// recoverInterruptedSession repairs the tail of a session whose latest turn was
// left open by a stopped or crashed process. It is the ZeoKeel counterpart of
// DSH's interrupted-turn closers: every open tool call gets an error result (so
// no card spins forever and the next turn's history has no dangling call) and
// the turn is closed idle with ReasonInterrupted.
//
// A no-op when a live turn owns the session. Cold loads (OpenSession/ensureView)
// and new submissions call it, so a reopened crashed session never shows a
// phantom "running" phase, a spinning tool card, or an unanswered call.
// mine is the turn the caller just reserved for this session, or nil when
// called on a cold open. Any other active turn means the session is live and
// must not be repaired.
func (c *Controller) recoverInterruptedSession(sess *store.Session, mine *turn) error {
	if sess == nil || strings.TrimSpace(sess.ID) == "" {
		return nil
	}
	c.mu.Lock()
	active := c.turns[sess.ID]
	c.mu.Unlock()
	if active != nil && active != mine {
		return nil
	}

	// A process that died mid-turn leaves a non-terminal barrier checkpoint
	// (requested/tool_pending). Repair that tail from the checkpoint before the
	// store-status repair below: the conversation gains an interrupted
	// assistant anchor and every dispatched-but-unaccounted tool call gets an
	// explicit unknown-outcome result, so the next request has no dangling
	// call and no duplicated user message. Completed checkpoints and normally
	// ended turns are no-ops here.
	if err := c.recoverInterruptedCheckpoint(sess); err != nil {
		return fmt.Errorf("session: recover interrupted turn: %w", err)
	}

	calls, err := c.store.ToolCallsForSession(sess.ID)
	if err != nil {
		return fmt.Errorf("session: recover interrupted turn: %w", err)
	}
	drained := false
	for _, call := range calls {
		if terminalToolCallStatus(call.Status) {
			continue
		}
		drained = true
		if err := c.appendEvent(sess.ID, state.ToolEnd{
			CallID:   call.ID,
			Result:   recoveredToolResult,
			IsError:  true,
			Duration: time.Since(call.StartedAt),
		}); err != nil {
			return fmt.Errorf("session: recover interrupted turn: close tool %q: %w", call.ID, err)
		}
		_ = c.store.FinishToolCall(call.ID, recoveredToolResult, "error", time.Now().UTC())
	}
	if !drained && terminalStoreStatus(sess.Status) {
		return nil
	}

	phase := state.PhaseIdle
	reason := state.ReasonInterrupted
	if sess.Status == store.SessionFailed {
		// Preserve a recorded failure; only the dangling calls needed repair.
		phase = state.PhaseFailed
		reason = state.ReasonFailed
	}
	// Anchor the repaired tail to its last user message so the per-turn
	// transcript marker survives the reopen like a live Stop's would.
	if userMsgID := c.lastUserMessageID(sess.ID); userMsgID != "" {
		if err := c.appendEvent(sess.ID, state.TurnEnd{UserMsgID: userMsgID, Reason: reason}); err != nil {
			return fmt.Errorf("session: recover interrupted turn: append turn end: %w", err)
		}
	}
	if err := c.appendEvent(sess.ID, state.Status{Phase: phase, Reason: reason}); err != nil {
		return fmt.Errorf("session: recover interrupted turn: append status: %w", err)
	}
	if phase == state.PhaseIdle {
		if _, err := c.store.UpdateSessionStatus(sess.ID, store.SessionIdle); err != nil {
			return fmt.Errorf("session: recover interrupted turn: update session status: %w", err)
		}
	}
	return nil
}

// ensureView loads a session's view if this Controller has not seen it yet.
func (c *Controller) ensureView(sess *store.Session) error {
	c.mu.Lock()
	snap := c.views[sess.ID]
	c.mu.Unlock()
	if snap != nil {
		return nil
	}
	loaded, err := c.buildSnapshot(sess)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.views[sess.ID] == nil {
		c.views[sess.ID] = loaded
	}
	c.mu.Unlock()
	return nil
}

// notify invokes the listeners registered for the current session.
func (c *Controller) notify(sessionID string, snap *state.Snapshot) {
	if snap == nil {
		return
	}
	c.mu.Lock()
	if c.currentID != sessionID {
		c.mu.Unlock()
		return
	}
	listeners := make([]func(*state.Snapshot), len(c.listeners))
	copy(listeners, c.listeners)
	c.mu.Unlock()

	for _, fn := range listeners {
		fn(snap)
	}
}

// notifyAny invokes every listener with snap regardless of the current
// session. It is the repaint signal for state the snapshot does not carry
// (session titles live in the sessions table): a background session's title
// still has to refresh the global sidebar.
func (c *Controller) notifyAny(snap *state.Snapshot) {
	if snap == nil {
		return
	}
	c.mu.Lock()
	listeners := make([]func(*state.Snapshot), len(c.listeners))
	copy(listeners, c.listeners)
	c.mu.Unlock()

	for _, fn := range listeners {
		fn(snap)
	}
}

// draft returns the assistant draft for msgID, creating it in first-seen
// order. Only the turn's drain goroutine calls this.
func (t *turn) draft(msgID string) *draft {
	if d := t.drafts[msgID]; d != nil {
		return d
	}
	d := &draft{msgID: msgID}
	t.drafts[msgID] = d
	t.order = append(t.order, msgID)
	return d
}

func isTerminalPhase(phase state.Phase) bool {
	return phase == state.PhaseIdle || phase == state.PhaseFailed
}

// newID returns a random id used for turn grouping. It is deliberately not a
// session/message id: the store assigns those.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("turn-%d-%d", time.Now().UnixNano(), fallbackID.Add(1))
}

var fallbackID atomic.Uint64

// ListSessions returns the sessions of the controller's workspace, most
// recently updated first. It is the sidebar's data source.
func (c *Controller) ListSessions(ctx context.Context) ([]store.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ws, err := c.ensureWorkspace()
	if err != nil {
		return nil, err
	}
	return c.store.ListSessions(ws)
}

// CurrentSessionID is the session most recently opened, created or submitted
// to ("" before any).
func (c *Controller) CurrentSessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.currentID
}

// PendingApprovals returns the approval requests awaiting a decision for the
// current session, ordered by call id. It returns nil when nothing is pending.
func (c *Controller) PendingApprovals() []state.ApprovalRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	requests := c.pending[c.currentID]
	if len(requests) == 0 {
		return nil
	}
	out := make([]state.ApprovalRequest, 0, len(requests))
	for _, request := range requests {
		out = append(out, request)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CallID != out[j].CallID {
			return out[i].CallID < out[j].CallID
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Approve allows the pending tool call callID. It reports whether the call id
// was a pending approval of this Controller; false means the id is unknown, the
// Controller is closed, or no approval queue was configured.
func (c *Controller) Approve(callID string) bool {
	return c.decideApproval(callID, true)
}

// Deny rejects the pending tool call callID. Its return value is Approve's.
func (c *Controller) Deny(callID string) bool {
	return c.decideApproval(callID, false)
}

// ApproveAlways allows the pending tool call callID and records that its tool
// may run without future approval prompts in this Controller's approval queue.
// It reports whether callID was a pending approval of this Controller; false
// means the id is unknown, the Controller is closed, or no approval queue was
// configured.
//
// The always-allow rule is in-memory only: it disappears when the agent (and
// its queue) is rebuilt or ClearApprovalRules is called.
func (c *Controller) ApproveAlways(callID string) bool {
	name, ok := c.pendingToolName(callID)
	if !ok {
		return false
	}
	c.AllowAlways(name)
	return c.Approve(callID)
}

// AllowAlways records that toolName may run without future approval prompts in
// this Controller's approval queue. It is a no-op in static mode or after
// Close. The rule lives only in memory; see ApproveAlways.
func (c *Controller) AllowAlways(toolName string) {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	if c.gate != nil {
		c.gate.AllowAlways(toolName)
	}
	for _, gate := range c.agentGates {
		gate.AllowAlways(toolName)
	}
}

// ClearApprovalRules forgets every Always-allow rule, restoring the approval
// gate's normal predicate. Pending requests are untouched and still need an
// explicit decision. It is a no-op in static mode or after Close.
func (c *Controller) ClearApprovalRules() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	if c.gate != nil {
		c.gate.ClearRules()
	}
	for _, gate := range c.agentGates {
		gate.ClearRules()
	}
}

// pendingToolName returns the tool name of the pending request callID,
// preferring the current session the same way decideApproval does.
func (c *Controller) pendingToolName(callID string) (string, bool) {
	if strings.TrimSpace(callID) == "" {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return "", false
	}
	// Per-session agents own their own approval gate; the shared gate may be
	// nil in a pure per-session configuration (RT-04 seam consistency).
	if request, ok := c.pending[c.currentID][callID]; ok {
		return request.Name, c.gateForSessionLocked(c.currentID) != nil
	}
	for sessionID, requests := range c.pending {
		if sessionID == c.currentID {
			continue
		}
		if request, ok := requests[callID]; ok {
			return request.Name, c.gateForSessionLocked(sessionID) != nil
		}
	}
	return "", false
}

func (c *Controller) decideApproval(callID string, allow bool) bool {
	if strings.TrimSpace(callID) == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	// Prefer the current session: that is the only pending set the UI shows,
	// and it keeps decisions deterministic when sessions reuse a call id.
	owner := c.currentID
	found := c.removePendingLocked(owner, callID)
	if !found {
		for sessionID := range c.pending {
			if sessionID == c.currentID {
				continue
			}
			if c.removePendingLocked(sessionID, callID) {
				owner = sessionID
				found = true
				break
			}
		}
	}
	if !found {
		return false
	}
	// Resolve the decision against the queue that owns the request: with
	// per-session agents every session has its own approval gate (RT-04).
	queue := c.gateForSessionLocked(owner)
	if queue == nil {
		return false
	}
	// A decision may land before the tool reaches Wait; the queue buffers it,
	// so the caller only needs to know that the request was ours to decide.
	if allow {
		queue.Approve(callID)
	} else {
		queue.Deny(callID)
	}
	return true
}

// removePendingLocked deletes callID from sessionID's pending set and reports
// whether it was there. c.mu must be held.
func (c *Controller) removePendingLocked(sessionID, callID string) bool {
	requests := c.pending[sessionID]
	if _, ok := requests[callID]; !ok {
		return false
	}
	delete(requests, callID)
	if len(requests) == 0 {
		delete(c.pending, sessionID)
	}
	return true
}

// WorkspaceRoot returns the filesystem root of the controller's workspace, or
// "" when no workspace exists yet or the store cannot be read. The UI uses it
// to resolve "@" file mentions relative to the project.
func (c *Controller) WorkspaceRoot() string {
	c.wsMu.Lock()
	defer c.wsMu.Unlock()

	workspaces, err := c.store.ListWorkspaces()
	if err != nil {
		return ""
	}
	id := c.workspaceID
	if id == "" {
		if len(workspaces) == 0 {
			return ""
		}
		id = workspaces[0].ID
		c.workspaceID = id
	}
	for _, workspace := range workspaces {
		if workspace.ID == id {
			return workspace.Root
		}
	}
	return ""
}

// Settings keys persisted in the store's settings table. The UI owns these
// values; the runtime and other packages do not read them.
const (
	SettingPermission         = "ui.permission"
	SettingLanguage           = "ui.language"
	SettingTheme              = "ui.theme"
	SettingFontSize           = "ui.font_size"
	SettingWorkDetails        = "ui.work_details"
	SettingShowCodingView     = "ui.show_coding_view"
	SettingCollapseWorkDetail = "ui.collapse_work_details"
	SettingSendBehavior       = "ui.send_behavior"
	SettingPerformanceUsage   = "ui.performance_usage"
	SettingUploadSessionLog   = "ui.upload_session_log"
	// Auto-memory scopes (docs/design/auto-memory.md): the workspace scope
	// defaults to auto (the agent may write project notes directly) while the
	// global user scope defaults to suggest (candidates wait for review).
	SettingMemoryWorkspaceMode = "ui.memory_workspace_mode"
	SettingMemoryGlobalMode    = "ui.memory_global_mode"
	// SettingMemoryExtractor enables the optional background extractor and
	// SettingMemoryExtractionInterval is how many successful turns it waits
	// before running for a session.
	SettingMemoryExtractor          = "ui.memory_extractor"
	SettingMemoryExtractionInterval = "ui.memory_extraction_interval"
	// SettingWorkspace is the id of the workspace the sidebar last selected.
	SettingWorkspace = "ui.workspace"
)

// Per-session settings keys: session.<id>.mode and session.<id>.permission.
const (
	// SessionModePrefix starts a per-session settings key.
	SessionModePrefix = "session."
	// SessionModeSuffix ends a per-session mode setting key.
	SessionModeSuffix = ".mode"
	// SessionPermissionSuffix ends a per-session access-mode setting key.
	SessionPermissionSuffix = ".permission"
	// SessionModelSuffix ends a per-session selected-model setting key.
	SessionModelSuffix = ".model"
	// SessionReasoningEffortSuffix ends a per-session reasoning-effort
	// setting key. It stores the composer's explicit choice; an empty value
	// means the selected model's own configured effort applies.
	SessionReasoningEffortSuffix = ".reasoning_effort"
	// SessionPlanModeSuffix ends a per-session plan-mode setting key.
	SessionPlanModeSuffix = ".plan_mode"
)

// SessionModeKey renders the settings key that stores one session's agent
// mode. sessionID is used verbatim: it is a store-assigned id, never user
// input.
func SessionModeKey(sessionID string) string {
	return SessionModePrefix + sessionID + SessionModeSuffix
}

// SessionPlanModeKey renders the settings key that stores one session's plan
// mode ("true"/"false"). Plan mode is a session-level review gate, not an
// agent preset: it persists across restarts and is independent of Mode.
func SessionPlanModeKey(sessionID string) string {
	return SessionModePrefix + sessionID + SessionPlanModeSuffix
}

// SessionPermissionKey renders the settings key that stores one session's
// effective access mode. Persisting it per session is what lets a restart keep
// the mode the user chose instead of falling back to the global default (or to
// another session's choice).
func SessionPermissionKey(sessionID string) string {
	return SessionModePrefix + sessionID + SessionPermissionSuffix
}

// SessionModelKey renders the settings key that stores one session's selected
// model id. The composer picker writes it; Settings > Models keeps the global
// default used by new sessions and by sessions without a choice.
func SessionModelKey(sessionID string) string {
	return SessionModePrefix + sessionID + SessionModelSuffix
}

// SessionReasoningEffortKey renders the settings key that stores one
// session's explicit reasoning-effort choice. An empty value means "use the
// selected model's configured effort", which keeps a model switch honest.
func SessionReasoningEffortKey(sessionID string) string {
	return SessionModePrefix + sessionID + SessionReasoningEffortSuffix
}

// Permission defaults a new session may inherit.
const (
	PermissionFull      = "full"      // Full access
	PermissionWorkspace = "workspace" // Workspace Write
	PermissionAsk       = "ask"       // Ask
	// PermissionAutoReview is the experimental access mode in which a model
	// reviews each tool call that would otherwise prompt the user and decides
	// to allow or deny it automatically. A reviewer failure falls back to the
	// ordinary approval prompt.
	PermissionAutoReview = "auto_review"
)

// Language values for ui.language.
const (
	LanguageEnglish = "en"
	LanguageChinese = "zh"
)

// Appearance values for ui.theme.
const (
	ThemeLight  = "light"
	ThemeDark   = "dark"
	ThemeSystem = "system"
)

// Font size values for ui.font_size.
const (
	FontSizeSmall = 14
	FontSizeLarge = 16
)

// Work detail values for ui.work_details.
const (
	WorkDetailsStandard = "standard"
	WorkDetailsDetailed = "detailed"
)

// Collapse timing values for ui.collapse_work_details.
const (
	CollapseOnCompletion = "on_completion"
	CollapseImmediately  = "immediately"
)

// Send behavior values for ui.send_behavior.
const (
	SendQueue     = "queue"
	SendInterrupt = "interrupt"
)

// Performance detail values for ui.performance_usage.
const (
	PerformanceStandard = "standard"
	PerformanceDetailed = "detailed"
)

// Settings is the persisted UI configuration. It is deliberately small: one
// value per key in the settings table.
// Auto-memory write modes (docs/design/auto-memory.md §2): off disables the
// scope, suggest stores candidates as pending review items, auto applies them
// immediately.
const (
	MemoryModeOff     = "off"
	MemoryModeSuggest = "suggest"
	MemoryModeAuto    = "auto"
)

// MemoryExtractionDefaultInterval is the default number of successful turns
// between two background extractor runs.
const MemoryExtractionDefaultInterval = 6

// MemoryExtractionMaxInterval bounds the user setting so the extractor cannot
// be configured to run on every single turn without bound.
const MemoryExtractionMaxInterval = 50

type Settings struct {
	Permission         string `json:"permission"`
	Language           string `json:"language"`
	Theme              string `json:"theme"`
	FontSize           int    `json:"font_size"`
	WorkDetails        string `json:"work_details"`
	ShowCodingView     bool   `json:"show_coding_view"`
	CollapseWorkDetail string `json:"collapse_work_details"`
	SendBehavior       string `json:"send_behavior"`
	PerformanceUsage   string `json:"performance_usage"`
	UploadSessionLog   bool   `json:"upload_session_log"`
	// Auto-memory modes and the optional background extractor. The workspace
	// scope defaults to auto, the global user scope to suggest (review-first).
	MemoryWorkspaceMode      string `json:"memory_workspace_mode"`
	MemoryGlobalMode         string `json:"memory_global_mode"`
	MemoryExtractor          bool   `json:"memory_extractor"`
	MemoryExtractionInterval int    `json:"memory_extraction_interval"`
}

// DefaultSettings returns the values a fresh store uses: Workspace Write,
// Chinese (the live shell's product default), the desktop appearance, 14px
// conversation text and detailed work. The language must match the shell's
// unresolved live default, or a fresh install would display one language
// while Settings reported the other and the first save would flip it.
func DefaultSettings() Settings {
	return Settings{
		Permission:               PermissionWorkspace,
		Language:                 LanguageChinese,
		Theme:                    ThemeSystem,
		FontSize:                 FontSizeSmall,
		WorkDetails:              WorkDetailsDetailed,
		ShowCodingView:           true,
		CollapseWorkDetail:       CollapseOnCompletion,
		SendBehavior:             SendQueue,
		PerformanceUsage:         PerformanceDetailed,
		UploadSessionLog:         false,
		MemoryWorkspaceMode:      MemoryModeAuto,
		MemoryGlobalMode:         MemoryModeSuggest,
		MemoryExtractor:          false,
		MemoryExtractionInterval: MemoryExtractionDefaultInterval,
	}
}

// NormalizeSettings replaces unknown values with the defaults, so a corrupt
// or older settings row cannot put the UI in an unreachable state.
func NormalizeSettings(s Settings) Settings {
	def := DefaultSettings()
	switch s.Permission {
	case PermissionFull, PermissionWorkspace, PermissionAsk, PermissionAutoReview:
	default:
		s.Permission = def.Permission
	}
	switch s.Language {
	case LanguageEnglish, LanguageChinese:
	default:
		s.Language = def.Language
	}
	switch s.Theme {
	case ThemeLight, ThemeDark, ThemeSystem:
	default:
		s.Theme = def.Theme
	}
	switch s.FontSize {
	case FontSizeSmall, FontSizeLarge:
	default:
		s.FontSize = def.FontSize
	}
	switch s.WorkDetails {
	case WorkDetailsStandard, WorkDetailsDetailed:
	default:
		s.WorkDetails = def.WorkDetails
	}
	switch s.CollapseWorkDetail {
	case CollapseOnCompletion, CollapseImmediately:
	default:
		s.CollapseWorkDetail = def.CollapseWorkDetail
	}
	switch s.SendBehavior {
	case SendQueue, SendInterrupt:
	default:
		s.SendBehavior = def.SendBehavior
	}
	switch s.PerformanceUsage {
	case PerformanceStandard, PerformanceDetailed:
	default:
		s.PerformanceUsage = def.PerformanceUsage
	}
	switch s.MemoryWorkspaceMode {
	case MemoryModeOff, MemoryModeSuggest, MemoryModeAuto:
	default:
		s.MemoryWorkspaceMode = def.MemoryWorkspaceMode
	}
	switch s.MemoryGlobalMode {
	case MemoryModeOff, MemoryModeSuggest, MemoryModeAuto:
	default:
		s.MemoryGlobalMode = def.MemoryGlobalMode
	}
	if s.MemoryExtractionInterval < 1 || s.MemoryExtractionInterval > MemoryExtractionMaxInterval {
		s.MemoryExtractionInterval = def.MemoryExtractionInterval
	}
	return s
}

// SettingsStore is the small read/write API over the persisted UI settings.
// The shell talks to it through an interface, which keeps the shell testable
// with the same temporary store the controller tests use.
type SettingsStore struct {
	get     func(key string) (string, error)
	set     func(key, value string) error
	changed func(reason string) error
}

// Settings returns the controller's settings accessor.
func (c *Controller) Settings() *SettingsStore {
	return &SettingsStore{get: c.store.GetSetting, set: c.store.SetSetting, changed: c.RebuildAgent}
}

// Load reads every UI setting. Keys that were never written take their
// default value; a missing key is not an error.
func (s *SettingsStore) Load() (Settings, error) {
	cfg := DefaultSettings()
	read := func(key string, dst *string) error {
		v, err := s.get(key)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		*dst = v
		return nil
	}
	if err := read(SettingPermission, &cfg.Permission); err != nil {
		return cfg, fmt.Errorf("settings: load permission: %w", err)
	}
	if err := read(SettingLanguage, &cfg.Language); err != nil {
		return cfg, fmt.Errorf("settings: load language: %w", err)
	}
	if err := read(SettingTheme, &cfg.Theme); err != nil {
		return cfg, fmt.Errorf("settings: load theme: %w", err)
	}
	var fontSize string
	if err := read(SettingFontSize, &fontSize); err != nil {
		return cfg, fmt.Errorf("settings: load font size: %w", err)
	}
	if fontSize != "" {
		if n, err := strconv.Atoi(fontSize); err == nil {
			cfg.FontSize = n
		}
	}
	if err := read(SettingWorkDetails, &cfg.WorkDetails); err != nil {
		return cfg, fmt.Errorf("settings: load work details: %w", err)
	}
	var showCodingView string
	if err := read(SettingShowCodingView, &showCodingView); err != nil {
		return cfg, fmt.Errorf("settings: load show coding view: %w", err)
	}
	if showCodingView != "" {
		if b, err := strconv.ParseBool(showCodingView); err == nil {
			cfg.ShowCodingView = b
		}
	}
	if err := read(SettingCollapseWorkDetail, &cfg.CollapseWorkDetail); err != nil {
		return cfg, fmt.Errorf("settings: load collapse work details: %w", err)
	}
	if err := read(SettingSendBehavior, &cfg.SendBehavior); err != nil {
		return cfg, fmt.Errorf("settings: load send behavior: %w", err)
	}
	if err := read(SettingPerformanceUsage, &cfg.PerformanceUsage); err != nil {
		return cfg, fmt.Errorf("settings: load performance usage: %w", err)
	}
	var uploadLog string
	if err := read(SettingUploadSessionLog, &uploadLog); err != nil {
		return cfg, fmt.Errorf("settings: load upload session log: %w", err)
	}
	if uploadLog != "" {
		if b, err := strconv.ParseBool(uploadLog); err == nil {
			cfg.UploadSessionLog = b
		}
	}
	if err := read(SettingMemoryWorkspaceMode, &cfg.MemoryWorkspaceMode); err != nil {
		return cfg, fmt.Errorf("settings: load memory workspace mode: %w", err)
	}
	if err := read(SettingMemoryGlobalMode, &cfg.MemoryGlobalMode); err != nil {
		return cfg, fmt.Errorf("settings: load memory global mode: %w", err)
	}
	var memoryExtractor string
	if err := read(SettingMemoryExtractor, &memoryExtractor); err != nil {
		return cfg, fmt.Errorf("settings: load memory extractor: %w", err)
	}
	if memoryExtractor != "" {
		if b, err := strconv.ParseBool(memoryExtractor); err == nil {
			cfg.MemoryExtractor = b
		}
	}
	var memoryInterval string
	if err := read(SettingMemoryExtractionInterval, &memoryInterval); err != nil {
		return cfg, fmt.Errorf("settings: load memory extraction interval: %w", err)
	}
	if memoryInterval != "" {
		if n, err := strconv.Atoi(memoryInterval); err == nil {
			cfg.MemoryExtractionInterval = n
		}
	}
	return NormalizeSettings(cfg), nil
}

// Save normalizes and persists every UI setting. A failure leaves the keys
// written before it authoritative; the shell reports the error and keeps its
// in-memory value.
func (s *SettingsStore) Save(cfg Settings) error { return s.save(cfg, true) }

// SavePreservingLanguage is Save for a first-run settings change: when no
// ui.language row exists yet, it leaves the key absent so an unrelated change
// (the theme, the font size) cannot record the resolved default as an
// explicit language choice. The live shell calls this from updateSettings.
func (s *SettingsStore) SavePreservingLanguage(cfg Settings) error { return s.save(cfg, false) }

func (s *SettingsStore) save(cfg Settings, writeDefaultLanguage bool) error {
	cfg = NormalizeSettings(cfg)
	previous := ""
	if s.get != nil {
		if v, err := s.get(SettingPermission); err == nil {
			previous = v
		}
	}
	languageAbsent := false
	if !writeDefaultLanguage && s.get != nil {
		if _, err := s.get(SettingLanguage); errors.Is(err, store.ErrNotFound) {
			languageAbsent = true
		}
	}
	values := []struct {
		key, value string
	}{
		{SettingPermission, cfg.Permission},
		{SettingLanguage, cfg.Language},
		{SettingTheme, cfg.Theme},
		{SettingFontSize, strconv.Itoa(cfg.FontSize)},
		{SettingWorkDetails, cfg.WorkDetails},
		{SettingShowCodingView, strconv.FormatBool(cfg.ShowCodingView)},
		{SettingCollapseWorkDetail, cfg.CollapseWorkDetail},
		{SettingSendBehavior, cfg.SendBehavior},
		{SettingPerformanceUsage, cfg.PerformanceUsage},
		{SettingUploadSessionLog, strconv.FormatBool(cfg.UploadSessionLog)},
		{SettingMemoryWorkspaceMode, cfg.MemoryWorkspaceMode},
		{SettingMemoryGlobalMode, cfg.MemoryGlobalMode},
		{SettingMemoryExtractor, strconv.FormatBool(cfg.MemoryExtractor)},
		{SettingMemoryExtractionInterval, strconv.Itoa(cfg.MemoryExtractionInterval)},
	}
	for _, v := range values {
		if v.key == SettingLanguage && languageAbsent {
			continue
		}
		if err := s.set(v.key, v.value); err != nil {
			return fmt.Errorf("settings: save %s: %w", v.key, err)
		}
	}
	if s.changed != nil && strings.TrimSpace(previous) != cfg.Permission {
		if err := s.changed("permission"); err != nil {
			return fmt.Errorf("settings: apply permission: %w", err)
		}
	}
	return nil
}

// SessionPermission returns the permission mode a session was created with,
// or "" for an unknown session. Changing the default does not affect sessions
// that already exist.
func (c *Controller) SessionPermission(sessionID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.perms[sessionID]
}

// Mode returns the current session's agent mode (one of runtime.Modes). A
// session that never stored a mode reports runtime.ModeStandard. The value is
// persisted as session.<id>.mode and cached after the first read.
func (c *Controller) Mode() string {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return runtime.ModeStandard
	}
	sessionID := c.currentID
	c.mu.Unlock()
	if strings.TrimSpace(sessionID) == "" {
		return runtime.ModeStandard
	}
	return c.sessionModeFor(sessionID)
}

// sessionModeFor reads one session's canonical mode (its persisted
// session.<id>.mode, else Standard) and caches it.
func (c *Controller) sessionModeFor(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" {
		return runtime.ModeStandard
	}
	c.mu.Lock()
	mode := c.modes[sessionID]
	c.mu.Unlock()
	if strings.TrimSpace(mode) != "" {
		return mode
	}
	stored, err := c.store.GetSetting(SessionModeKey(sessionID))
	mode = runtime.ModeStandard
	if err == nil && strings.TrimSpace(stored) != "" {
		if normalized, nerr := runtime.NormalizeMode(stored); nerr == nil {
			mode = normalized
		}
	}
	c.mu.Lock()
	if c.modes != nil && !c.closed {
		c.modes[sessionID] = mode
	}
	c.mu.Unlock()
	return mode
}

// PlanMode reports whether sessionID is in plan mode. A session that never
// stored a value reports off, the default for every new session. The value is
// persisted as session.<id>.plan_mode and cached after the first read. The
// app's prompt composer resolves the flag through its PlanModeFunc hook at the
// start of each turn, so toggling it does not rebuild the agent.
func (c *Controller) PlanMode(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || c.store == nil {
		return false
	}
	c.mu.Lock()
	raw, ok := c.planModes[sessionID]
	closed := c.closed
	c.mu.Unlock()
	if ok {
		return raw == "true"
	}
	if closed {
		return false
	}
	return c.hydrateSessionPlanMode(sessionID)
}

// hydrateSessionPlanMode loads sessionID's persisted plan flag into the
// in-memory cache. A missing or unreadable value means off; the cache keeps the
// last written value so opening a session from another one cannot leak plan
// mode between sessions.
func (c *Controller) hydrateSessionPlanMode(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || c.store == nil {
		return false
	}
	value := "false"
	if stored, err := c.store.GetSetting(SessionPlanModeKey(sessionID)); err == nil {
		switch strings.ToLower(strings.TrimSpace(stored)) {
		case "true", "1", "on", "yes":
			value = "true"
		}
	}
	c.mu.Lock()
	if c.planModes != nil && !c.closed {
		// Re-check the cache: SetPlanMode may have written a newer value while
		// the store read above was in flight. The explicit write wins, so a
		// stale hydration can never resurrect plan mode after the user turned
		// it off (or suppress it after the user turned it on).
		if existing, ok := c.planModes[sessionID]; ok {
			c.mu.Unlock()
			return existing == "true"
		}
		c.planModes[sessionID] = value
	}
	c.mu.Unlock()
	return value == "true"
}

// SetPlanMode persists one session's plan mode and updates the cache the
// app's prompt composer reads. It never touches the agent topology: the
// plan:policy prompt section is recomposed at the start of the next turn, so
// the flag only guides the model (soft guidance; tool use still follows the
// permission/sandbox policy).
func (c *Controller) SetPlanMode(sessionID string, on bool) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("%w: set plan mode: empty session id", ErrInvalidArgument)
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}
	value := "false"
	if on {
		value = "true"
	}
	if err := c.store.SetSetting(SessionPlanModeKey(sessionID), value); err != nil {
		return fmt.Errorf("session: set plan mode: %w", err)
	}
	c.mu.Lock()
	if !c.closed {
		if c.planModes == nil {
			c.planModes = make(map[string]string)
		}
		c.planModes[sessionID] = value
	}
	c.mu.Unlock()
	return nil
}

// applySessionMode makes the shared runtime agent match sessionID's persisted
// mode. Opening a Deep session from a Standard one used to leave the chip and
// the agent's topology disagreeing because only SetMode called the switcher.
func (c *Controller) applySessionMode(sessionID string) error {
	c.mu.Lock()
	switcher := c.modeSwitcher
	shared := c.agent
	factory := c.agentFactory
	c.mu.Unlock()
	agent := shared
	if factory != nil {
		built, err := c.agentFor(sessionID)
		if err != nil {
			return err
		}
		agent = built
	}
	want := c.sessionModeFor(sessionID)
	if strings.TrimSpace(want) == "" {
		want = runtime.ModeStandard
	}
	if agent != nil && agent.Mode() == want {
		return nil
	}
	// The app's switcher targets the shared agent; with per-session agents the
	// controller switches the session's own agent directly.
	if factory == nil && switcher != nil {
		return switcher(want)
	}
	// A controller wired with an agent but no explicit switcher still owns
	// the mode contract; runtime.Agent.SetMode is the same hook the app uses.
	if agent != nil {
		return agent.SetMode(want)
	}
	return nil
}

// SetMode persists the current session's agent mode and then calls the
// app-wired mode switcher (see SetAgentModeSwitcher), if any. The value is
// durable even when the switcher fails, so a restart still opens the session in
// the chosen mode; the switcher error is returned for the UI to show.
//
// The mode belongs to the session, but the runtime Agent is shared: an app that
// wires a switcher is responsible for rebuilding/switching it
// (runtime.Agent.SetMode implements this).
func (c *Controller) SetMode(mode string) error {
	normalized, err := runtime.NormalizeMode(mode)
	if err != nil {
		return fmt.Errorf("%w: set mode: %v", ErrInvalidArgument, err)
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	sessionID := c.currentID
	c.mu.Unlock()
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("%w: set mode: no current session", ErrInvalidArgument)
	}

	if err := c.store.SetSetting(SessionModeKey(sessionID), normalized); err != nil {
		return fmt.Errorf("session: set mode: %w", err)
	}

	c.mu.Lock()
	if c.modes == nil {
		c.modes = make(map[string]string)
	}
	c.modes[sessionID] = normalized
	switcher := c.modeSwitcher
	c.mu.Unlock()

	if switcher != nil {
		if err := switcher(normalized); err != nil {
			return fmt.Errorf("session: switch agent mode: %w", err)
		}
	}
	return nil
}

// SetAgentModeSwitcher wires the hook SetMode calls after persisting a mode.
// The app owns agent construction, so it points this at whatever rebuild or
// runtime.Agent.SetMode call implements the switch. It is safe to call before
// or after the controller is in use.
func (c *Controller) SetAgentModeSwitcher(fn func(mode string) error) {
	c.mu.Lock()
	c.modeSwitcher = fn
	c.mu.Unlock()
}

// SetAgent swaps the runtime agent and approval queue for the next turn. A
// turn already in flight keeps the agent it started with. The app calls this
// after rebuilding tools for a new workspace root, plugin set, access mode or
// selected model, so the "changes apply to the next turn" promise holds.
func (c *Controller) SetAgent(agent *runtime.Agent, gate *runtime.ApprovalQueue) {
	c.mu.Lock()
	c.agent, c.gate = agent, gate
	c.mu.Unlock()
}

// SetAgentFactory installs the per-session agent mode (RT-04): factory builds
// one runtime.Agent per session, discard (optional) receives agents the
// controller stops using so the app can close their bundles. Existing cached
// agents are discarded and in-flight factory builds from before the call are
// ignored. Passing a nil factory restores the legacy shared-agent mode.
func (c *Controller) SetAgentFactory(factory func(sessionID string) (*runtime.Agent, error), discard func(sessionID string, agent *runtime.Agent)) {
	c.mu.Lock()
	c.agentFactory = factory
	c.agentDiscard = discard
	c.agentsGeneration++
	old := c.agents
	c.agents = make(map[string]*runtime.Agent)
	c.agentGates = make(map[string]*runtime.ApprovalQueue)
	c.builtPermissions = make(map[string]string)
	c.mu.Unlock()
	for sessionID, agent := range old {
		if discard != nil && agent != nil {
			discard(sessionID, agent)
		}
	}
}

// SetAgentGateFactory installs the lookup for per-session approval gates. It is
// called by the app together with SetAgentFactory (each per-session bundle owns
// its own queue, so decisions must resolve against the right one).
func (c *Controller) SetAgentGateFactory(fn func(sessionID string) *runtime.ApprovalQueue) {
	c.mu.Lock()
	c.agentGateFactory = fn
	c.mu.Unlock()
}

// gateForSessionLocked returns the approval queue that owns sessionID's
// approvals: the per-session queue in factory mode, else the shared queue. The
// caller holds c.mu.
func (c *Controller) gateForSessionLocked(sessionID string) *runtime.ApprovalQueue {
	if c.agentFactory != nil {
		if gate := c.agentGates[sessionID]; gate != nil {
			return gate
		}
	}
	return c.gate
}

// InvalidateAgents drops every cached per-session agent so the next turn of any
// session rebuilds it (settings, plugin or model changes). It returns
// immediately; bundles are closed through the discard callback.
func (c *Controller) InvalidateAgents() {
	c.mu.Lock()
	c.agentsGeneration++
	old := c.agents
	c.agents = make(map[string]*runtime.Agent)
	c.agentGates = make(map[string]*runtime.ApprovalQueue)
	c.builtPermissions = make(map[string]string)
	discard := c.agentDiscard
	c.mu.Unlock()
	for sessionID, agent := range old {
		if discard != nil && agent != nil {
			discard(sessionID, agent)
		}
	}
}

// invalidateAgent drops sessionID's cached agent (permission change, stale
// build). It is a no-op in shared-agent mode and when nothing is cached.
func (c *Controller) invalidateAgent(sessionID string) {
	c.mu.Lock()
	agent := c.agents[sessionID]
	delete(c.agents, sessionID)
	delete(c.agentGates, sessionID)
	delete(c.builtPermissions, sessionID)
	discard := c.agentDiscard
	c.mu.Unlock()
	if agent != nil && discard != nil {
		discard(sessionID, agent)
	}
}

// agentFor returns the runtime.Agent that should run sessionID's next turn.
// Without a factory it is the shared agent (legacy behavior). With a factory
// the session's agent is built lazily and cached; a build racing an Invalidate
// is retried so a stale agent never runs a turn.
func (c *Controller) agentFor(sessionID string) (*runtime.Agent, error) {
	for attempt := 0; attempt < 4; attempt++ {
		c.mu.Lock()
		factory := c.agentFactory
		if factory == nil {
			agent := c.agent
			c.mu.Unlock()
			return agent, nil
		}
		if agent := c.agents[sessionID]; agent != nil {
			c.mu.Unlock()
			return agent, nil
		}
		generation := c.agentsGeneration
		discard := c.agentDiscard
		c.mu.Unlock()

		agent, err := factory(sessionID)
		if err != nil {
			return nil, err
		}
		if agent == nil {
			return nil, nil
		}
		// Resolve the effective permission before taking mu (the resolver may
		// hydrate from the store and must not run while c.mu is held).
		builtPermission := c.SessionPermissionOrDefault(sessionID)
		gateFactory := func() func(string) *runtime.ApprovalQueue {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.agentGateFactory
		}()
		var sessionGate *runtime.ApprovalQueue
		if gateFactory != nil {
			sessionGate = gateFactory(sessionID)
		}

		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			if discard != nil {
				discard(sessionID, agent)
			}
			return nil, ErrClosed
		}
		if generation != c.agentsGeneration {
			// The app invalidated agents while this one was building; retry
			// with the current generation instead of caching a stale agent.
			c.mu.Unlock()
			if discard != nil {
				discard(sessionID, agent)
			}
			continue
		}
		if existing := c.agents[sessionID]; existing != nil {
			c.mu.Unlock()
			if discard != nil {
				discard(sessionID, agent)
			}
			return existing, nil
		}
		if c.agents == nil {
			c.agents = make(map[string]*runtime.Agent)
		}
		c.agents[sessionID] = agent
		if c.builtPermissions == nil {
			c.builtPermissions = make(map[string]string)
		}
		c.builtPermissions[sessionID] = builtPermission
		if sessionGate != nil {
			if c.agentGates == nil {
				c.agentGates = make(map[string]*runtime.ApprovalQueue)
			}
			c.agentGates[sessionID] = sessionGate
		}
		c.mu.Unlock()
		return agent, nil
	}
	return nil, fmt.Errorf("%w: agent for session %q kept being invalidated", ErrNoAgent, sessionID)
}

// SubscribeSessionEvents streams one session's semantic events (tokens,
// reasoning, tool lifecycle, usage, terminal status) to a buffered channel.
// The returned unsubscribe function stops delivery and closes the channel; it
// is idempotent. Slow subscribers drop events instead of stalling the turn.
func (c *Controller) SubscribeSessionEvents(sessionID string) (<-chan state.Event, func(), error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, nil, fmt.Errorf("%w: subscribe session events: empty session id", ErrInvalidArgument)
	}
	ch := make(chan state.Event, 1024)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, nil, ErrClosed
	}
	if c.sessionEventSubs == nil {
		c.sessionEventSubs = make(map[string]map[chan state.Event]struct{})
	}
	if c.sessionEventSubs[sessionID] == nil {
		c.sessionEventSubs[sessionID] = make(map[chan state.Event]struct{})
	}
	c.sessionEventSubs[sessionID][ch] = struct{}{}
	c.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			c.mu.Lock()
			if subs := c.sessionEventSubs[sessionID]; subs != nil {
				delete(subs, ch)
				if len(subs) == 0 {
					delete(c.sessionEventSubs, sessionID)
				}
			}
			c.mu.Unlock()
			close(ch)
		})
	}
	return ch, unsubscribe, nil
}

// publishSessionEvent fans ev out to sessionID's streaming subscribers. It is
// non-blocking: a subscriber that cannot keep up loses events (never the turn).
func (c *Controller) publishSessionEvent(sessionID string, ev state.Event) {
	c.mu.Lock()
	subs := c.sessionEventSubs[sessionID]
	if len(subs) == 0 {
		c.mu.Unlock()
		return
	}
	targets := make([]chan state.Event, 0, len(subs))
	for ch := range subs {
		targets = append(targets, ch)
	}
	c.mu.Unlock()
	for _, ch := range targets {
		select {
		case ch <- ev:
		default:
		}
	}
}

// HasAgent reports whether a runtime agent is configured. The shell uses it
// to explain a disabled Send / ErrNoAgent instead of silently dropping a
// submit.
func (c *Controller) HasAgent() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.agent != nil || c.agentFactory != nil
}

// SetModelConfiguredFunc installs the app-owned predicate that answers whether
// a model is configured and usable. The app wires it once services are ready,
// because provider settings (key/base URL env fallbacks) live in the app's
// configuration layer; a nil predicate keeps the legacy agent-based fallback.
func (c *Controller) SetModelConfiguredFunc(fn func() bool) {
	c.mu.Lock()
	c.modelConfigured = fn
	c.mu.Unlock()
}

// HasConfiguredModel reports whether the app can actually run a model: a saved
// or environment API key, or a keyless local provider (PD-02). The first-run
// setup card and the empty-session "add a model" hint must use this instead of
// HasAgent: HasAgent is the turn-execution predicate and is true as soon as the
// per-session AgentFactory is installed (RT-04), which happens on startup even
// on a fresh install with no model configured.
//
// Without an app predicate (unit tests, headless edge) it falls back to the
// legacy "a shared agent exists" check.
func (c *Controller) HasConfiguredModel() bool {
	c.mu.Lock()
	fn := c.modelConfigured
	hasAgent := c.agent != nil
	c.mu.Unlock()
	if fn != nil {
		return fn()
	}
	return hasAgent
}

// SetAgentRebuilder wires the app hook that rebuilds the runtime agent after
// a build-time setting changed. The hook returns an error only for reporting;
// the persisted setting stays authoritative either way.
func (c *Controller) SetAgentRebuilder(fn func(reason string) error) {
	c.mu.Lock()
	c.agentRebuilder = fn
	c.mu.Unlock()
}

// RebuildAgent asks the app to rebuild the agent for reason, if a builder is
// wired. It is called without c.mu held.
func (c *Controller) RebuildAgent(reason string) error {
	c.mu.Lock()
	rebuild := c.agentRebuilder
	c.mu.Unlock()
	if rebuild == nil {
		return nil
	}
	return rebuild(reason)
}

// SetOnSessionActivated registers fn, called after a session becomes current
// through CreateSession/CreateSessionInWorkspace/OpenSession. previous is the
// previously current session id ("" when there was none). It is called without
// c.mu held, so listeners may call back into the controller. The app uses it to
// reset the shared working-directory state to each session's own directory: one
// session's "cd .agents" must not leak into the next session's prompt WorkDir.
func (c *Controller) SetOnSessionActivated(fn func(previousSessionID, sessionID string)) {
	c.mu.Lock()
	c.onSessionActivated = fn
	c.mu.Unlock()
}

// notifySessionActivated runs the activation hook without c.mu held.
func (c *Controller) notifySessionActivated(previous, sessionID string) {
	if strings.TrimSpace(sessionID) == "" || previous == sessionID {
		return
	}
	c.mu.Lock()
	hook := c.onSessionActivated
	c.mu.Unlock()
	if hook != nil {
		hook(previous, sessionID)
	}
}

// bindWorkspace makes workspaceID the selected workspace and rebuilds the
// shared agent for its root when the selection differs. Submit and session
// creation call it because both can target a session outside the workspace the
// user is looking at (queued sends, goal continuations, automation, a
// create-in-workspace call): without it the turn would run with the tools,
// prompt WorkDir and file root of the wrong workspace. A rebuild failure is
// returned so the caller can refuse to start a turn on the wrong root.
func (c *Controller) bindWorkspace(ctx context.Context, workspaceID string) error {
	id := strings.TrimSpace(workspaceID)
	previous := c.CurrentWorkspaceID()
	if id == "" || id == previous {
		return nil
	}
	if _, err := c.store.GetWorkspace(id); err != nil {
		return fmt.Errorf("session: bind workspace %q: %w", id, err)
	}
	c.setWorkspace(id)
	if err := c.store.SetSetting(SettingWorkspace, id); err != nil {
		return fmt.Errorf("session: bind workspace %q: persist: %w", id, err)
	}
	if err := c.RebuildAgent("workspace"); err != nil {
		// Roll the selection back: the agent still works on the previous root,
		// and leaving the store/db pointing at a root the agent was never built
		// for would make the next turn even more surprising.
		c.setWorkspace(previous)
		_ = c.store.SetSetting(SettingWorkspace, previous)
		return fmt.Errorf("session: bind workspace %q: rebuild agent: %w", id, err)
	}
	return nil
}

// SetSessionPermission updates one session's access mode and persists it as
// session.<id>.permission. The global default is not touched; it only applies
// to sessions created after this point.
func (c *Controller) SetSessionPermission(sessionID, mode string) {
	switch mode {
	case PermissionFull, PermissionWorkspace, PermissionAsk, PermissionAutoReview:
	default:
		return
	}
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	// Persist first: a rebuild triggered below must see the stored mode when
	// it resolves the session's effective permission.
	_ = c.store.SetSetting(SessionPermissionKey(sessionID), mode)
	c.mu.Lock()
	if c.perms == nil {
		c.mu.Unlock()
		return
	}
	c.perms[sessionID] = mode
	current := c.currentID
	built := c.builtPermission
	factory := c.agentFactory
	builtForSession := c.builtPermissions[sessionID]
	c.mu.Unlock()
	if factory != nil {
		// Per-session agents capture the mode when built: drop this session's
		// agent so the next turn rebuilds it with the new permission (RT-04).
		if sessionID == current && builtForSession != "" && builtForSession != mode {
			c.invalidateAgent(sessionID)
		}
		return
	}
	if sessionID == current && built != "" && built != mode {
		_ = c.RebuildAgent("permission")
	}
}

// SetBuiltPermission records the access mode the current agent was built for.
// The app calls it after every rebuild so OpenSession and SetSessionPermission
// can tell when a per-session mode needs a fresh tool set.
func (c *Controller) SetBuiltPermission(mode string) {
	c.mu.Lock()
	c.builtPermission = mode
	c.mu.Unlock()
}

// rebuildForPermission rebuilds when sessionID's effective mode differs from
// the mode the current agent was built for. A missing built value (startup)
// leaves the app's own startup rebuild in charge. The rebuild is requested
// without c.mu held; the app reads the target session through the controller
// (currentID must already point at sessionID), which is why callers hold
// bindMu across the currentID switch and this call.
func (c *Controller) rebuildForPermission(sessionID string) error {
	c.mu.Lock()
	built := c.builtPermission
	factory := c.agentFactory
	builtForSession := c.builtPermissions[sessionID]
	c.mu.Unlock()
	want := c.SessionPermissionOrDefault(sessionID)
	if factory != nil {
		// The factory builds each agent for the session's effective mode, so a
		// mismatch only means the cached agent is stale: drop it and let the
		// next turn rebuild. Nothing to ask the app for. An empty built value
		// means this session has no cached agent yet.
		if builtForSession == "" || want == "" || builtForSession == want {
			return nil
		}
		c.invalidateAgent(sessionID)
		return nil
	}
	if built == "" || want == "" || built == want {
		return nil
	}
	return c.RebuildAgent("permission")
}

// SessionPermissionOrDefault returns the session's access mode. A session the
// controller has not cached is hydrated from session.<id>.permission first;
// only a truly unknown session falls back to the persisted default.
func (c *Controller) SessionPermissionOrDefault(sessionID string) string {
	c.mu.Lock()
	mode := c.perms[sessionID]
	closed := c.closed
	c.mu.Unlock()
	if mode != "" {
		return mode
	}
	if strings.TrimSpace(sessionID) != "" && !closed {
		if hydrated := c.hydrateSessionPermission(sessionID); hydrated != "" {
			return hydrated
		}
	}
	if settings, err := c.Settings().Load(); err == nil {
		return settings.Permission
	}
	return PermissionWorkspace
}

// SessionModelID returns the model id a session explicitly selected, or "".
func (c *Controller) SessionModelID(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" || c.store == nil {
		return ""
	}
	raw, err := c.store.GetSetting(SessionModelKey(sessionID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(raw)
}

// SessionModelOrDefault returns the session's selected model id, falling back
// to the global default so the composer shows a usable model before a choice
// is made.
func (c *Controller) SessionModelOrDefault(sessionID string) string {
	if id := c.SessionModelID(sessionID); id != "" {
		return id
	}
	if id, err := c.store.DefaultModelID(); err == nil {
		return strings.TrimSpace(id)
	}
	return ""
}

// SetSessionModel persists one session's model choice and applies it to the
// shared agent when that session is current. The global default is untouched;
// Settings > Models still owns the default new sessions start with.
func (c *Controller) SetSessionModel(sessionID, modelID string) error {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" || strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("%w: set session model: empty id", ErrInvalidArgument)
	}
	if _, err := c.store.GetModel(modelID); err != nil {
		return fmt.Errorf("session: set session model: %w", err)
	}
	if err := c.store.SetSetting(SessionModelKey(sessionID), modelID); err != nil {
		return fmt.Errorf("session: set session model: %w", err)
	}
	// A model switch adopts the new model's own configured effort; the old
	// explicit level may not exist on the new model, so it is cleared rather
	// than carried over.
	_ = c.store.SetSetting(SessionReasoningEffortKey(sessionID), "")
	c.mu.Lock()
	current := c.currentID
	c.mu.Unlock()
	if current == sessionID {
		return c.applySessionModel(sessionID)
	}
	return nil
}

// SessionReasoningEffort returns the reasoning effort sessionID explicitly
// selected from the composer, or "" when the selected model's configured
// effort applies.
func (c *Controller) SessionReasoningEffort(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" || c.store == nil {
		return ""
	}
	raw, err := c.store.GetSetting(SessionReasoningEffortKey(sessionID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(raw)
}

// SetSessionReasoningEffort persists one session's reasoning-effort choice and
// applies it to the running agent when that session is current. An empty
// effort clears the explicit choice so the selected model's configured effort
// applies again. The model configuration in Settings > Models is untouched.
func (c *Controller) SetSessionReasoningEffort(sessionID, effort string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("%w: set session reasoning effort: empty session", ErrInvalidArgument)
	}
	effort = strings.TrimSpace(effort)
	if effort != "" && !modelcatalog.ValidEffort(effort) {
		return fmt.Errorf("%w: set session reasoning effort: unknown effort %q", ErrInvalidArgument, effort)
	}
	if err := c.store.SetSetting(SessionReasoningEffortKey(sessionID), effort); err != nil {
		return fmt.Errorf("session: set session reasoning effort: %w", err)
	}
	c.mu.Lock()
	current := c.currentID
	c.mu.Unlock()
	if current == sessionID {
		return c.applySessionModel(sessionID)
	}
	return nil
}

// applySessionModel points the running agent at sessionID's selected model, its
// reasoning effort and its context budget for the next request. A session
// without its own choice falls back to the global default, so switching from a
// session that picked a model back to one that did not cannot leave the agent
// on the previous session's model.
func (c *Controller) applySessionModel(sessionID string) error {
	record, name := c.selectedSessionModel(sessionID)
	if name == "" {
		return nil
	}
	c.mu.Lock()
	agent := c.agent
	factory := c.agentFactory
	c.mu.Unlock()
	if factory != nil {
		built, err := c.agentFor(sessionID)
		if err != nil {
			return err
		}
		agent = built
	}
	if agent == nil {
		return nil
	}
	if err := agent.SetModel(name); err != nil {
		return err
	}
	// The composer's explicit reasoning strength wins over the model's
	// configured default; an empty value still reaches the provider so a
	// session that falls back to a model without an effort cannot inherit the
	// previous model's level.
	effort := c.SessionReasoningEffort(sessionID)
	if effort == "" && record != nil {
		effort = strings.TrimSpace(record.ReasoningEffort)
	}
	_ = agent.SetReasoningEffort(effort)
	// A model with a known context window sizes this session's compaction
	// budget, so the runtime uses the model's real capacity instead of the
	// global fallback. A model without one restores the build-time budget.
	if record != nil {
		agent.SetContextBudget(ContextBudgetForWindow(record.ContextWindow))
	}
	return nil
}

// selectedSessionModel resolves the model record a session runs and the wire
// model name to send. An explicit per-session model wins; otherwise the
// selected preset's default model applies (FP-06); otherwise the global
// default. The record is nil when only a wire name is available (a preset that
// names a provider model without a stored row).
func (c *Controller) selectedSessionModel(sessionID string) (*store.Model, string) {
	if id := c.SessionModelID(sessionID); id != "" {
		if model, err := c.store.GetModel(id); err == nil {
			return model, modelWireName(*model)
		}
	}
	if presetModel := c.SessionPresetModel(sessionID); presetModel != "" {
		if model, err := c.store.GetModel(presetModel); err == nil {
			return model, modelWireName(*model)
		}
		// Not a stored model row: treat the preset value as a wire model name
		// (common for user presets naming provider models).
		return nil, presetModel
	}
	if id := c.SessionModelOrDefault(sessionID); id != "" {
		if model, err := c.store.GetModel(id); err == nil {
			return model, modelWireName(*model)
		}
	}
	return nil, ""
}

// modelWireName is the model id to send for a stored model row.
func modelWireName(model store.Model) string {
	if name := strings.TrimSpace(model.Model); name != "" {
		return name
	}
	return strings.TrimSpace(model.Name)
}

// ContextBudgetForWindow converts a model's combined context window into the
// prompt budget compaction uses. A fifth of the window is reserved for the
// model's own output and the system prompt, mirroring the framework's
// compaction headroom instead of letting a 1M-token window fill with history
// alone. A non-positive window yields 0 (keep the configured fallback).
func ContextBudgetForWindow(window int) int {
	if window <= 0 {
		return 0
	}
	return window * 4 / 5
}

// Goal returns a copy of the current session's goal, or nil when the goal tool
// has not run. The goal is folded from the tool's structured result, so it
// survives across turns without any package-level state.
func (c *Controller) Goal() *tools.Goal {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneGoal(c.goals[c.currentID])
}

// SessionGoal returns a copy of sessionID's goal, or nil.
func (c *Controller) SessionGoal(sessionID string) *tools.Goal {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneGoal(c.goals[sessionID])
}

// SetGoal replaces the current session's goal. Nil clears it. The shell uses it
// only for tests and previews; the tool result path is authoritative. Setting a
// new objective (or reopening a completed/abandoned goal) resets the automatic
// continuation round count; a progress update for the same active objective
// does not.
func (c *Controller) SetGoal(goal *tools.Goal) {
	c.mu.Lock()
	sessionID := c.currentID
	if sessionID == "" {
		c.mu.Unlock()
		return
	}
	previous := c.goals[sessionID]
	var next *tools.Goal
	if goal != nil {
		next = cloneGoal(goal)
	}
	if next == nil {
		delete(c.goals, sessionID)
	} else {
		c.goals[sessionID] = next
	}
	// Report the goal itself (not nil) so a terminal/paused goal keeps its
	// continuation counter; the driver re-checks continuability per turn.
	c.goalDriver.goalChanged(sessionID, previous, next)
	store := c.goalStore
	c.mu.Unlock()
	if store != nil {
		_ = store.SaveGoal(sessionID, cloneGoal(next))
	}
}

// SetGoalStore wires the persistence seam /goal changes are saved through. The
// app passes the store-backed tool state so a goal survives a restart; nil
// keeps goals in memory only (tests, previews).
func (c *Controller) SetGoalStore(store tools.GoalStateStore) {
	c.mu.Lock()
	c.goalStore = store
	c.mu.Unlock()
}

// GoalAction applies one /goal command to the current session's goal and
// persists the result. It is the non-model entry point for the shell's /goal
// command: action is set (with objective), edit (with the replacement
// objective — same goal id/phase, revision bumped), pause, resume, block,
// unblock, complete, abandon, clear or get. steps/progress are optional set
// fields. The returned goal is nil for clear and for get when no goal is set.
func (c *Controller) GoalAction(action, objective string, steps []string, progress *int) (*tools.Goal, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		return nil, fmt.Errorf("%w: goal action is required", ErrInvalidArgument)
	}
	c.mu.Lock()
	sessionID := c.currentID
	if sessionID == "" {
		c.mu.Unlock()
		return nil, fmt.Errorf("%w: goal: no current session", ErrInvalidArgument)
	}
	previous := c.goals[sessionID]
	next, _, err := tools.ApplyGoalAction(previous, action, objective, steps, progress, "")
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	mutated := action != "get"
	if next == nil {
		delete(c.goals, sessionID)
	} else {
		c.goals[sessionID] = next
	}
	if mutated {
		// Report the goal itself (not nil) so pausing/completing keeps the
		// continuation counter; turnFinished re-checks goalContinuable.
		c.goalDriver.goalChanged(sessionID, previous, next)
	}
	store := c.goalStore
	result := cloneGoal(next)
	c.mu.Unlock()
	if mutated && store != nil {
		_ = store.SaveGoal(sessionID, cloneGoal(next))
	}
	return result, nil
}

// GoalRounds returns how many automatic continuation turns the goal driver has
// submitted for sessionID. It is 0 when the feature is disabled or no goal has
// needed a continuation.
func (c *Controller) GoalRounds(sessionID string) int {
	c.mu.Lock()
	driver := c.goalDriver
	c.mu.Unlock()
	if driver == nil {
		return 0
	}
	return driver.Rounds(sessionID)
}

// SetGoalDriver installs d as the automatic goal-continuation driver. Passing
// nil disables the feature. The driver is shared by every session this
// controller owns; its counters are keyed by session id.
func (c *Controller) SetGoalDriver(d *GoalRoundDriver) {
	c.mu.Lock()
	c.goalDriver = d
	c.mu.Unlock()
}

// SetTeamStopper registers the hook that cancels a session's asynchronous team
// members (spawn_team) when Stop is requested, including stops that come from
// outside the UI (API, automation, programmatic cancellation). Nil disables the
// hook. The app wires it to the agent-team manager's StopSession.
func (c *Controller) SetTeamStopper(fn func(sessionID string)) {
	c.mu.Lock()
	c.teamStopper = fn
	c.mu.Unlock()
}

// Team returns the subagents the current session spawned, oldest first. Each
// entry comes from a subagent tool result; the team panel renders them.
func (c *Controller) Team() []tools.Teammate {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]tools.Teammate(nil), c.team[c.currentID]...)
}

// SessionTeam returns sessionID's subagents, oldest first.
func (c *Controller) SessionTeam(sessionID string) []tools.Teammate {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]tools.Teammate(nil), c.team[sessionID]...)
}

// SetTeam replaces the current session's subagent list. The slice is copied.
// It exists for tests and for an app that wants to seed a preview.
func (c *Controller) SetTeam(team []tools.Teammate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.currentID == "" {
		return
	}
	if len(team) == 0 {
		delete(c.team, c.currentID)
		return
	}
	c.team[c.currentID] = append([]tools.Teammate(nil), team...)
}

// cloneGoal copies a goal so callers cannot mutate controller-owned state.
func cloneGoal(goal *tools.Goal) *tools.Goal {
	if goal == nil {
		return nil
	}
	out := *goal
	out.Steps = append([]string(nil), goal.Steps...)
	return &out
}

// RenameSession changes a session's title and returns the updated row.
func (c *Controller) RenameSession(ctx context.Context, id, title string) (*store.Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: rename session: empty id", ErrInvalidArgument)
	}
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("%w: rename session: empty title", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}

	// The title update and the manual-title marker form one critical section
	// with writeAutoTitle's final check-and-write. Without it, an automatic
	// title that already passed its eligibility check could commit after this
	// rename and silently replace the user's choice.
	c.titleWriteMu.Lock()
	sess, err := c.store.UpdateSessionTitle(id, title)
	if err != nil {
		c.titleWriteMu.Unlock()
		return nil, fmt.Errorf("session: rename: %w", err)
	}
	// Remember that the user chose this title: automatic titles must never
	// overwrite a manual rename, not even after another turn or a restart.
	_ = c.store.SetSetting(manualTitleKey(id), "1")
	_ = c.store.SetSetting(autoTitleRetryKey(id), "")
	c.titleWriteMu.Unlock()
	return sess, nil
}

// DeleteSession removes a session and everything stored under it. It refuses
// to delete a session whose turn is still running, and clears the current
// session when that was the one deleted.
func (c *Controller) DeleteSession(ctx context.Context, id string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: delete session: empty id", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	if c.turns[id] != nil {
		c.mu.Unlock()
		return fmt.Errorf("%w: delete session %q", ErrTurnRunning, id)
	}
	c.mu.Unlock()

	if err := c.store.DeleteSession(id); err != nil {
		return fmt.Errorf("session: delete: %w", err)
	}
	// Drop the per-session preset selection with the session (FP-06).
	_ = c.store.SetSetting(presetSettingKeyFor(id), "")
	_ = c.store.SetSetting(SessionModeKey(id), "")

	c.mu.Lock()
	delete(c.views, id)
	delete(c.pending, id)
	delete(c.perms, id)
	delete(c.todos, id)
	delete(c.toolCalls, id)
	delete(c.toolArgs, id)
	delete(c.questions, id)
	delete(c.artifacts, id)
	delete(c.goals, id)
	delete(c.team, id)
	delete(c.modes, id)
	delete(c.planModes, id)
	delete(c.autoTitles, id)
	delete(c.initialTitles, id)
	delete(c.stopRequested, id)
	c.goalDriver.Reset(id)
	if c.currentID == id {
		c.currentID = ""
	}
	c.mu.Unlock()
	// Clear the per-session settings rows this controller owns; the store
	// cannot know which setting prefixes belong to a deleted session.
	_ = c.store.SetSetting(SessionPermissionKey(id), "")
	_ = c.store.SetSetting(SessionModeKey(id), "")
	_ = c.store.SetSetting(SessionModelKey(id), "")
	_ = c.store.SetSetting(SessionReasoningEffortKey(id), "")
	_ = c.store.SetSetting(SessionPlanModeKey(id), "")
	_ = c.store.SetSetting(SessionGoalRoundsKey(id), "")
	_ = c.store.SetSetting(autoTitleKey(id), "")
	_ = c.store.SetSetting(manualTitleKey(id), "")
	_ = c.store.SetSetting(autoTitleRetryKey(id), "")
	return nil
}

// ListWorkspaces returns every registered workspace, oldest first. It is the
// sidebar's workspace data source.
func (c *Controller) ListWorkspaces(ctx context.Context) ([]store.Workspace, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	list, err := c.store.ListWorkspaces()
	if err != nil {
		return nil, fmt.Errorf("session: list workspaces: %w", err)
	}
	return list, nil
}

// CreateWorkspace registers a workspace and selects it. The new workspace
// starts empty; creating a session in it does not affect sessions elsewhere.
func (c *Controller) CreateWorkspace(ctx context.Context, name, root string) (*store.Workspace, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	ws, err := c.store.CreateWorkspace(strings.TrimSpace(name), strings.TrimSpace(root))
	if err != nil {
		return nil, fmt.Errorf("session: create workspace: %w", err)
	}
	c.setWorkspace(ws.ID)
	if err := c.store.SetSetting(SettingWorkspace, ws.ID); err != nil {
		return ws, fmt.Errorf("session: persist workspace: %w", err)
	}
	if err := c.RebuildAgent("workspace"); err != nil {
		return ws, fmt.Errorf("session: rebuild for workspace: %w", err)
	}
	return ws, nil
}

// SelectWorkspace makes id current and persists it as ui.workspace.
func (c *Controller) SelectWorkspace(ctx context.Context, id string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: select workspace: empty id", ErrInvalidArgument)
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}
	if _, err := c.store.GetWorkspace(id); err != nil {
		return fmt.Errorf("session: select workspace: %w", err)
	}
	c.setWorkspace(id)
	if err := c.store.SetSetting(SettingWorkspace, id); err != nil {
		return fmt.Errorf("session: persist workspace: %w", err)
	}
	return c.RebuildAgent("workspace")
}

// RenameWorkspace changes a workspace's name.
func (c *Controller) RenameWorkspace(ctx context.Context, id, name string) (*store.Workspace, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	ws, err := c.store.RenameWorkspace(id, name)
	if err != nil {
		return nil, fmt.Errorf("session: rename workspace: %w", err)
	}
	return ws, nil
}

// DeleteWorkspace removes a workspace and every session stored under it. It
// refuses to delete one whose sessions have a turn in flight, clears the
// current selection when it was that workspace, and drops the cached views of
// the sessions that went with it.
func (c *Controller) DeleteWorkspace(ctx context.Context, id string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: delete workspace: empty id", ErrInvalidArgument)
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.mu.Unlock()

	sessions, err := c.store.QuerySessions(store.SessionQuery{
		WorkspaceID:     id,
		IncludeArchived: true,
		Sort:            store.SessionSortUpdated,
	})
	if err != nil {
		return fmt.Errorf("session: delete workspace: %w", err)
	}

	c.mu.Lock()
	for _, sess := range sessions {
		if c.turns[sess.ID] != nil {
			c.mu.Unlock()
			return fmt.Errorf("%w: delete workspace %q", ErrTurnRunning, id)
		}
	}
	c.mu.Unlock()

	if err := c.store.DeleteWorkspace(id); err != nil {
		return fmt.Errorf("session: delete workspace: %w", err)
	}
	// The rows cascade with the workspace, but the per-session JSONL
	// transcripts live on disk. Remove them and the controller-owned
	// per-session settings so a deleted workspace leaves nothing behind.
	for _, sess := range sessions {
		if err := c.store.RemoveSessionDir(sess.ID); err != nil {
			return fmt.Errorf("session: delete workspace %q: %w", id, err)
		}
		_ = c.store.SetSetting(SessionPermissionKey(sess.ID), "")
		_ = c.store.SetSetting(SessionModeKey(sess.ID), "")
		_ = c.store.SetSetting(SessionModelKey(sess.ID), "")
		_ = c.store.SetSetting(SessionReasoningEffortKey(sess.ID), "")
		_ = c.store.SetSetting(SessionPlanModeKey(sess.ID), "")
		_ = c.store.SetSetting(SessionGoalRoundsKey(sess.ID), "")
	}

	c.mu.Lock()
	for _, sess := range sessions {
		delete(c.views, sess.ID)
		delete(c.pending, sess.ID)
		delete(c.perms, sess.ID)
		delete(c.goals, sess.ID)
		delete(c.team, sess.ID)
		delete(c.modes, sess.ID)
		delete(c.planModes, sess.ID)
		delete(c.toolArgs, sess.ID)
		delete(c.stopRequested, sess.ID)
		c.goalDriver.Reset(sess.ID)
		if c.currentID == sess.ID {
			c.currentID = ""
		}
	}
	c.mu.Unlock()

	c.wsMu.Lock()
	wasCurrent := c.workspaceID == id
	if wasCurrent {
		c.workspaceID = ""
	}
	c.wsMu.Unlock()
	if wasCurrent {
		// Best effort: the workspace is gone, so a stale preference must not
		// resurrect it on the next start.
		_ = c.store.SetSetting(SettingWorkspace, "")
	}
	return nil
}

// RestoreWorkspace selects the workspace persisted as ui.workspace when it
// still exists and returns the selected id. When no preference is stored, or
// it names a workspace that was deleted, the current selection is kept.
func (c *Controller) RestoreWorkspace(ctx context.Context) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return "", ErrClosed
	}

	id, err := c.store.GetSetting(SettingWorkspace)
	if errors.Is(err, store.ErrNotFound) {
		return c.CurrentWorkspaceID(), nil
	}
	if err != nil {
		return "", fmt.Errorf("session: restore workspace: %w", err)
	}
	if strings.TrimSpace(id) == "" {
		return c.CurrentWorkspaceID(), nil
	}
	workspaces, err := c.store.ListWorkspaces()
	if err != nil {
		return "", fmt.Errorf("session: restore workspace: %w", err)
	}
	for _, ws := range workspaces {
		if ws.ID == id {
			c.setWorkspace(id)
			return id, nil
		}
	}
	return c.CurrentWorkspaceID(), nil
}

// CurrentWorkspaceID is the selected workspace id, or "" before one is
// selected.
func (c *Controller) CurrentWorkspaceID() string {
	c.wsMu.Lock()
	defer c.wsMu.Unlock()
	return c.workspaceID
}

// CurrentWorkspace returns the selected workspace. When nothing is selected
// yet it picks the first registered workspace; ok is false when the store has
// no workspaces at all.
func (c *Controller) CurrentWorkspace(ctx context.Context) (store.Workspace, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return store.Workspace{}, false, err
	}
	id := c.CurrentWorkspaceID()
	if id != "" {
		ws, err := c.store.GetWorkspace(id)
		if err == nil {
			return *ws, true, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return store.Workspace{}, false, fmt.Errorf("session: current workspace: %w", err)
		}
	}
	workspaces, err := c.store.ListWorkspaces()
	if err != nil {
		return store.Workspace{}, false, fmt.Errorf("session: current workspace: %w", err)
	}
	if len(workspaces) == 0 {
		return store.Workspace{}, false, nil
	}
	c.setWorkspace(workspaces[0].ID)
	return workspaces[0], true, nil
}

// SearchSessions returns the current workspace's sessions matching query in
// their title or message content, with the sidebar's archive and sort
// options.
func (c *Controller) SearchSessions(ctx context.Context, query string, includeArchived, manualSort bool) ([]store.Session, error) {
	return c.sessionsQuery(ctx, c.CurrentWorkspaceID(), query, includeArchived, manualSort)
}

// SessionsForWorkspace returns one workspace's sessions with the sidebar's
// archive and sort options.
func (c *Controller) SessionsForWorkspace(ctx context.Context, workspaceID string, includeArchived, manualSort bool) ([]store.Session, error) {
	return c.sessionsQuery(ctx, workspaceID, "", includeArchived, manualSort)
}

// sessionsQuery is the shared implementation of the session list accessors.
func (c *Controller) sessionsQuery(ctx context.Context, workspaceID, query string, includeArchived, manualSort bool) ([]store.Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	sort := store.SessionSortUpdated
	if manualSort {
		sort = store.SessionSortManual
	}
	list, err := c.store.QuerySessions(store.SessionQuery{
		WorkspaceID:     workspaceID,
		Search:          query,
		IncludeArchived: includeArchived,
		Sort:            sort,
	})
	if err != nil {
		return nil, fmt.Errorf("session: query sessions: %w", err)
	}
	return list, nil
}

// SetSessionArchived archives or unarchives a session. Archiving hides it
// from the default sidebar and search results without deleting anything.
func (c *Controller) SetSessionArchived(ctx context.Context, id string, archived bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: archive session: empty id", ErrInvalidArgument)
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}
	if _, err := c.store.SetSessionArchived(id, archived); err != nil {
		return fmt.Errorf("session: archive: %w", err)
	}
	return nil
}

// setWorkspace stores id as the current workspace without persisting it.
func (c *Controller) setWorkspace(id string) {
	c.wsMu.Lock()
	c.workspaceID = id
	c.wsMu.Unlock()
}

// ModelStore is the controller's accessor for the persisted model settings.
// The shell's Settings > Models page talks to it; the store remains the
// authority for the on-disk keys.
type ModelStore struct {
	st         *store.Store
	changed    func(reason string) error
	controller *Controller
}

// Models returns the controller's model accessor.
func (c *Controller) Models() *ModelStore {
	return &ModelStore{st: c.store, changed: c.RebuildAgent, controller: c}
}

// List returns every configured model, the default flagged.
func (m *ModelStore) List() ([]store.Model, error) { return m.st.ListModels() }

// Get returns one model by id.
func (m *ModelStore) Get(id string) (*store.Model, error) { return m.st.GetModel(id) }

// Create inserts a model, generating its id and making the first model the
// default. The first model becomes the running agent's model immediately, and
// the rebuild hook runs so provider.* settings and derived tools agree.
func (m *ModelStore) Create(model store.Model) (*store.Model, error) {
	out, err := m.st.CreateModel(model)
	if err != nil {
		return nil, err
	}
	m.applyDefaultModel()
	if m.changed != nil {
		if err := m.changed("model"); err != nil {
			return out, err
		}
	}
	return out, nil
}

// Update replaces an existing model. Editing the default switches the running
// agent to the new model id and rebuilds.
func (m *ModelStore) Update(model store.Model) (*store.Model, error) {
	out, err := m.st.UpdateModel(model)
	if err != nil {
		return nil, err
	}
	m.applyDefaultModel()
	if m.changed != nil {
		if err := m.changed("model"); err != nil {
			return out, err
		}
	}
	return out, nil
}

// Delete removes a model. Deleting the default clears it (and provider.*);
// removing the last configured model also rebuilds, which clears the agent so
// Submit reports ErrNoAgent instead of running with a stale credential.
func (m *ModelStore) Delete(id string) error {
	if err := m.st.DeleteModel(id); err != nil {
		return err
	}
	m.applyDefaultModel()
	if m.changed != nil {
		return m.changed("model")
	}
	return nil
}

// applyDefaultModel points the running agent at the selected default model.
// It is a no-op when no default exists or the controller has no agent; the
// app's rebuild hook owns the rest.
func (m *ModelStore) applyDefaultModel() {
	c := m.controller
	if c == nil {
		return
	}
	model, err := m.st.DefaultModel()
	if err != nil || model == nil {
		return
	}
	name := strings.TrimSpace(model.Model)
	if name == "" {
		name = strings.TrimSpace(model.Name)
	}
	if name == "" {
		return
	}
	c.mu.Lock()
	agent := c.agent
	c.mu.Unlock()
	if agent != nil {
		_ = agent.SetModel(name)
	}
}

// DefaultID returns the selected default model's id.
func (m *ModelStore) DefaultID() (string, error) { return m.st.DefaultModelID() }

// Default returns the selected default model.
func (m *ModelStore) Default() (*store.Model, error) { return m.st.DefaultModel() }

// SetDefault marks id as the selected default, switches the running agent's
// model for the next turn, and asks the app to rebuild so every derived
// setting (provider keys, subagent defaults) matches. The model switch itself
// happens even without a wired rebuild hook.
func (m *ModelStore) SetDefault(id string) error {
	model, err := m.st.GetModel(id)
	if err != nil {
		return err
	}
	if err := m.st.SetDefaultModel(id); err != nil {
		return err
	}
	name := strings.TrimSpace(model.Model)
	if name == "" {
		name = strings.TrimSpace(model.Name)
	}
	if name != "" {
		if c := m.controller; c != nil {
			c.mu.Lock()
			agent := c.agent
			c.mu.Unlock()
			if agent != nil {
				// A model switch that cannot rebuild (for example a test
				// seam without a Provider) is reported by the app hook.
				_ = agent.SetModel(name)
			}
		}
	}
	if m.changed != nil {
		return m.changed("model")
	}
	return nil
}

// DefaultModel returns the selected default model, or a store error wrapping
// ErrNotFound when no default is configured.
func (c *Controller) DefaultModel() (*store.Model, error) { return c.store.DefaultModel() }

// DefaultModelLabel is the display label for the selected default model, used
// by the composer badge. It is "" when no default is configured.
func (c *Controller) DefaultModelLabel() string {
	model, err := c.store.DefaultModel()
	if err != nil || model == nil {
		return ""
	}
	if strings.TrimSpace(model.Name) != "" {
		return strings.TrimSpace(model.Name)
	}
	return strings.TrimSpace(model.Model)
}

// AutomationStore is the controller's accessor for persisted automation tasks.
type AutomationStore struct {
	st *store.Store
}

// Automations returns the controller's automation accessor.
func (c *Controller) Automations() *AutomationStore { return &AutomationStore{st: c.store} }

// List returns every automation task ordered by creation time.
func (a *AutomationStore) List() ([]store.Automation, error) { return a.st.ListAutomations() }

// Get returns one automation task by id.
func (a *AutomationStore) Get(id string) (*store.Automation, error) {
	return a.st.GetAutomation(id)
}

// Create inserts an automation task. The caller computes NextRun from Cron.
func (a *AutomationStore) Create(task store.Automation) (*store.Automation, error) {
	return a.st.CreateAutomation(task)
}

// Update replaces an automation task.
func (a *AutomationStore) Update(task store.Automation) (*store.Automation, error) {
	return a.st.UpdateAutomation(task)
}

// Delete removes an automation task.
func (a *AutomationStore) Delete(id string) error { return a.st.DeleteAutomation(id) }

// SetEnabled flips a task's enabled flag.
func (a *AutomationStore) SetEnabled(id string, enabled bool) (*store.Automation, error) {
	return a.st.SetAutomationEnabled(id, enabled)
}

// MarkRun records a scheduler firing.
func (a *AutomationStore) MarkRun(id string, lastRun time.Time, nextRun *time.Time) (*store.Automation, error) {
	return a.st.MarkAutomationRun(id, lastRun, nextRun)
}

// SetNextRun stores a task's next scheduled instant.
func (a *AutomationStore) SetNextRun(id string, next time.Time) (*store.Automation, error) {
	return a.st.SetAutomationNextRun(id, next)
}

// RunAutomationNow invokes the app's RunNow hook. It reports false when no
// hook is wired, which lets the shell tell the user execution is unavailable.
func (c *Controller) RunAutomationNow(id string) bool {
	if strings.TrimSpace(id) == "" {
		return false
	}
	c.mu.Lock()
	fn := c.RunNow
	c.mu.Unlock()
	if fn == nil {
		return false
	}
	fn(id)
	return true
}

// Plugin settings keys: plugin.<name>.enabled.
const (
	// PluginSettingPrefix prefixes every built-in plugin toggle key.
	PluginSettingPrefix = "plugin."
	// PluginSettingSuffix terminates a plugin toggle key.
	PluginSettingSuffix = ".enabled"
)

// PluginSettingKey renders the settings key for one plugin toggle.
func PluginSettingKey(name string) string {
	return PluginSettingPrefix + name + PluginSettingSuffix
}

// PluginEnabled returns whether name is enabled. An unwritten key means true,
// matching the built-in features that ship on by default.
func (c *Controller) PluginEnabled(name string) (bool, error) {
	return c.PluginEnabledDefault(name, true)
}

// PluginEnabledDefault is PluginEnabled with an explicit fallback for plugins
// that default to off (the experimental list).
func (c *Controller) PluginEnabledDefault(name string, def bool) (bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return def, fmt.Errorf("%w: plugin: empty name", ErrInvalidArgument)
	}
	value, err := c.store.GetSetting(PluginSettingKey(name))
	if errors.Is(err, store.ErrNotFound) {
		return def, nil
	}
	if err != nil {
		return def, fmt.Errorf("session: plugin %q: %w", name, err)
	}
	enabled, perr := strconv.ParseBool(strings.TrimSpace(value))
	if perr != nil {
		return def, nil
	}
	return enabled, nil
}

// SetPluginEnabled persists a plugin toggle and then calls PluginChangeHook,
// if one is wired. Persistence happens first so the hook never observes a
// value that is not durable.
func (c *Controller) SetPluginEnabled(name string, enabled bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%w: plugin: empty name", ErrInvalidArgument)
	}
	if err := c.store.SetSetting(PluginSettingKey(name), strconv.FormatBool(enabled)); err != nil {
		return fmt.Errorf("session: plugin %q: %w", name, err)
	}
	c.mu.Lock()
	hook := c.PluginChangeHook
	c.mu.Unlock()
	if hook != nil {
		hook(name, enabled)
	}
	return c.RebuildAgent("plugin")
}

// SetPluginChangeHook wires the plugin-change callback. It is safe to call
// before or after the controller is in use; the field is read under c.mu.
func (c *Controller) SetPluginChangeHook(fn func(name string, enabled bool)) {
	c.mu.Lock()
	c.PluginChangeHook = fn
	c.mu.Unlock()
}

// SetRunNow wires the automation "Run now" callback.
func (c *Controller) SetRunNow(fn func(id string)) {
	c.mu.Lock()
	c.RunNow = fn
	c.mu.Unlock()
}

// Store exposes the controller's persistence handle for process-level
// services (scheduler, updater) that live in internal/app.
func (c *Controller) Store() *store.Store { return c.store }

// -----------------------------------------------------------------------------
// Declarative agent presets (FP-06) — product wiring.

// DefaultPresetSettingKey is the default preset setting key. The legacy mode
// keys ("session.mode.<id>", the built-in mode names) keep resolving as
// built-in preset aliases.
const DefaultPresetSettingKey = "agent.preset"

// presetSettingPrefix is the per-session preset selection key prefix.
const presetSettingPrefix = "session.preset."

// presetSettingKeyFor is the settings key holding sessionID's preset id.
func presetSettingKeyFor(sessionID string) string { return presetSettingPrefix + sessionID }

// Presets returns the current preset registry (always non-nil after New; the
// zero-value fallback is the built-in catalog).
func (c *Controller) Presets() *presets.Registry {
	c.mu.Lock()
	reg := c.presets
	c.mu.Unlock()
	if reg == nil {
		if fallback, err := presets.Load(presets.Options{KnownTools: c.presetKnownToolsLocked()}); err == nil {
			return fallback
		}
	}
	return reg
}

// presetKnownToolsLocked copies the configured known tool names. The caller
// must not hold c.mu (this helper only reads a slice written before use).
func (c *Controller) presetKnownToolsLocked() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.presetKnownTools...)
}

// ReloadPresets re-reads the user preset files from the data directory. A load
// error keeps the previous registry (fail closed) and is returned so the
// settings UI can show it. A successful reload requests an agent rebuild for
// the next turn (the app installs the hook).
func (c *Controller) ReloadPresets() (*presets.Registry, error) {
	c.mu.Lock()
	known := append([]string(nil), c.presetKnownTools...)
	c.mu.Unlock()
	reg, err := presets.Load(presets.Options{UserDir: c.store.Dir(), KnownTools: known})
	c.mu.Lock()
	if err != nil {
		// Fail closed: keep the previous registry active and expose the error
		// so the settings surface can show why a preset file was rejected.
		c.lastPresetLoadErr = err
		c.mu.Unlock()
		return nil, err
	}
	c.presets = reg
	c.lastPresetLoadErr = nil
	c.mu.Unlock()
	c.rebuildForPresets()
	return reg, nil
}

// SetPresetKnownTools installs the registered tool names used to validate user
// preset allowlists. The app calls it once the tool registry exists; a later
// ReloadPresets validates against it (fail closed on unknown names).
func (c *Controller) SetPresetKnownTools(names []string) {
	normalized := normalizePresetToolNames(names)
	c.mu.Lock()
	c.presetKnownTools = normalized
	c.mu.Unlock()
}

// LastPresetLoadError returns the most recent preset load/reload failure, or nil
// after a successful load.
func (c *Controller) LastPresetLoadError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastPresetLoadErr
}

// SetPresetWarnings records per-session preset build warnings (for example a
// non-empty allowlist that matched no registered tool). The UI reads them
// through PresetWarnings.
func (c *Controller) SetPresetWarnings(sessionID string, warnings []string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || len(warnings) == 0 {
		if sessionID == "" {
			return
		}
	}
	c.mu.Lock()
	if c.presetWarnings == nil {
		c.presetWarnings = make(map[string][]string)
	}
	if len(warnings) == 0 {
		delete(c.presetWarnings, sessionID)
	} else {
		c.presetWarnings[sessionID] = append([]string(nil), warnings...)
	}
	c.mu.Unlock()
}

// PresetWarnings returns the current session's preset build warnings, or the
// session named by sessionID ("" means the current session when it is set).
func (c *Controller) PresetWarnings(sessionID string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := strings.TrimSpace(sessionID)
	if id == "" {
		id = c.currentID
	}
	return append([]string(nil), c.presetWarnings[id]...)
}

// normalizePresetToolNames trims, deduplicates and sorts a known-tools list.
func normalizePresetToolNames(names []string) []string {
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// SetSessionPreset persists sessionID's preset selection. An empty presetID
// clears the selection; an unknown id is rejected with presets.ErrNotFound so
// the UI can warn instead of silently falling back. A successful change
// requests a rebuild so the next turn uses the new tool set/persona.
func (c *Controller) SetSessionPreset(sessionID, presetID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("%w: set session preset: empty session id", ErrInvalidArgument)
	}
	presetID = strings.TrimSpace(presetID)
	if presetID != "" {
		reg := c.Presets()
		if _, ok := reg.Get(presetID); !ok {
			return fmt.Errorf("%w: preset %q", presets.ErrNotFound, presetID)
		}
	}
	if err := c.store.SetSetting(presetSettingKeyFor(sessionID), presetID); err != nil {
		return fmt.Errorf("session: set preset: %w", err)
	}
	c.rebuildForPresets()
	return nil
}

// SessionPresetID returns the session's persisted preset id ("" means the
// default preset applies).
func (c *Controller) SessionPresetID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || c.store == nil {
		return ""
	}
	raw, err := c.store.GetSetting(presetSettingKeyFor(sessionID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(raw)
}

// ResolvePreset returns the effective preset for sessionID and any warnings on
// the resolution path. The precedence is: explicit session selection, the
// default agent.preset setting, the legacy session mode (built-in ids are
// preset ids), then PresetFallbackID. An unknown id never errors: Resolve
// returns the fallback preset plus a warning.
func (c *Controller) ResolvePreset(sessionID string) (presets.Preset, []string) {
	reg := c.Presets()
	id := c.SessionPresetID(sessionID)
	if id == "" {
		if raw, err := c.store.GetSetting(DefaultPresetSettingKey); err == nil {
			id = strings.TrimSpace(raw)
		}
	}
	if id == "" {
		// Legacy mode selection: built-in preset ids mirror the old modes.
		if raw, err := c.store.GetSetting(SessionModeKey(sessionID)); err == nil {
			if mode := strings.TrimSpace(raw); mode != "" {
				if _, ok := reg.Get(mode); ok {
					id = mode
				}
			}
		}
	}
	if id == "" {
		id = presets.PresetFallbackID
	}
	return reg.Resolve(id)
}

// SessionPresetModel returns the preset's default model id for sessionID, or ""
// when the preset does not specify one. A preset model is only a default: an
// explicit per-session model always wins (applySessionModel).
func (c *Controller) SessionPresetModel(sessionID string) string {
	preset, _ := c.ResolvePreset(sessionID)
	return strings.TrimSpace(preset.Model)
}

// rebuildForPresets requests the next-turn rebuild through the app hook. It is
// best effort: a controller without a wired hook (tests, headless edge) still
// keeps the registry updated and resolves the preset on the next turn.
func (c *Controller) rebuildForPresets() {
	_ = c.RebuildAgent("presets")
}

// -----------------------------------------------------------------------------
// Blocking ask_user questions (FP-16) — controller half of the seam.

// questionAnswer is one resolution of a blocking question.
type questionAnswer struct {
	text string
	err  error
}

// questionWaiter is a turn blocked inside AskQuestion.
type questionWaiter struct {
	sessionID string
	ch        chan questionAnswer
}

// AskQuestion publishes a question card and blocks until the user answers
// (AnswerQuestion), the turn context ends, or the caller's timeout elapses.
// The runtime's QuestionGate implementation calls this from inside the turn, so
// the answer is returned as the same ask_user tool call's result and the model
// continues in the same turn. On timeout/cancel the card is withdrawn and the
// caller decides how to degrade (the runtime falls back to the tool's
// fire-and-forget pending result).
func (c *Controller) AskQuestion(ctx context.Context, sessionID, callID string, question tools.Question) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	sessionID = strings.TrimSpace(sessionID)
	callID = strings.TrimSpace(callID)
	question.Question = strings.TrimSpace(question.Question)
	if sessionID == "" || callID == "" || question.Question == "" {
		return "", fmt.Errorf("%w: ask question: empty session id, call id or question", ErrInvalidArgument)
	}
	question.CallID = callID
	question.Status = "pending"
	if question.AskedAt.IsZero() {
		question.AskedAt = time.Now().UTC()
	}
	waiter := &questionWaiter{sessionID: sessionID, ch: make(chan questionAnswer, 1)}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return "", ErrClosed
	}
	if c.questionWaiters == nil {
		c.questionWaiters = make(map[string]*questionWaiter)
	}
	if _, exists := c.questionWaiters[callID]; exists {
		c.mu.Unlock()
		return "", fmt.Errorf("%w: question %q is already pending", ErrInvalidArgument, callID)
	}
	c.questionWaiters[callID] = waiter
	if c.questions[sessionID] == nil {
		c.questions[sessionID] = make(map[string]tools.Question)
	}
	c.questions[sessionID][callID] = question
	snap := c.views[sessionID]
	c.mu.Unlock()
	c.notify(sessionID, snap)

	defer c.withdrawQuestion(sessionID, callID)

	select {
	case answer := <-waiter.ch:
		return answer.text, answer.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// withdrawQuestion removes a still-pending blocking question card (answer,
// timeout and cancellation all pass through here; an answered card has already
// been removed by AnswerQuestion).
func (c *Controller) withdrawQuestion(sessionID, callID string) {
	c.mu.Lock()
	waiter := c.questionWaiters[callID]
	if waiter == nil {
		c.mu.Unlock()
		return
	}
	delete(c.questionWaiters, callID)
	if questions := c.questions[sessionID]; questions != nil {
		delete(questions, callID)
		if len(questions) == 0 {
			delete(c.questions, sessionID)
		}
	}
	snap := c.views[sessionID]
	c.mu.Unlock()
	c.notify(sessionID, snap)
}

// PendingQuestionWaiter reports whether callID is currently blocking inside a
// turn (used by the app gate's diagnostics/tests).
func (c *Controller) PendingQuestionWaiter(callID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.questionWaiters[strings.TrimSpace(callID)]
	return ok
}
