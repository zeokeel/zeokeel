// Package agent implements ZeoKeel's agent backend module: the contracts.KeyChat
// chat service backed by the ZeoKeel Rig agent loop.
//
// It owns the in-memory conversation read model (sessions, messages, tool calls,
// running state), the model configuration resolution and the asynchronous run
// lifecycle. The Rig SDK does the actual work: one openai-compatible provider
// and one rig agent per Send, a shared session.History keyed by session ID, the
// product toolkit's tool registry and a gate that allows the tools the product
// already exposed.
//
// UI modules only read the model through contracts.ChatService and receive
// change notifications; the service notifies subscribers synchronously from the
// run goroutine, so a UI must hop to its own thread (UIRuntime.Post).
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rig "github.com/zeokeel/zeokeel-rig"
	rigagent "github.com/zeokeel/zeokeel-rig/agent"
	"github.com/zeokeel/zeokeel-rig/provider/openai"
	"github.com/zeokeel/zeokeel-rig/session"
	"github.com/zeokeel/zeokeel-rig/session/compact"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"
	rigsession "github.com/zeokeel/zeokeel-rig/tools/session"
	rigteam "github.com/zeokeel/zeokeel-rig/tools/team"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// effectiveMaxIterations maps the product's "0 means unlimited" setting to the
// cap passed to Rig (which otherwise falls back to its own 20-round default).
func effectiveMaxIterations(configured int) int {
	if configured <= 0 {
		return agentIterationLimitUnbounded
	}
	return configured
}

// agentIterationLimitUnbounded is passed to Rig when the product's iteration
// limit is 0 ("unlimited"): Rig treats 0 as its own 20-round default, so the
// run loop needs a cap it will never reach in practice.
const agentIterationLimitUnbounded = 1_000_000

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/agent"
	// DefaultModel is the last-resort model when neither a configured model
	// list, the assembly config nor the settings store names one.
	DefaultModel = "gpt-4o-mini"
	// DefaultAPIKeyEnv is the environment variable read for the API key when
	// Config.ApiKeyEnv is empty.
	DefaultAPIKeyEnv = "OPENAI_API_KEY"
	// maxTitleRunes is the length the first prompt is truncated to when a
	// session has no title yet.
	maxTitleRunes = 32
	// maxToolSummaryRunes caps a tool result summary carried in the read
	// model. The transcript's expanded output shows this text whole (bounded
	// and scrollable in the UI), so it is large enough for a real command
	// output or diff tail; the persisted tool_result event carries the same
	// bounded text.
	maxToolSummaryRunes = 4000
	// maxToolArgumentsRunes caps the raw tool-call arguments carried in the
	// read model and the tool_call event: the transcript's input section shows
	// them (pretty JSON or a command), and an unbounded write_file body must
	// not bloat the read model or the durable event log.
	maxToolArgumentsRunes = 4000
	// maxApprovalSummaryRunes caps the human-readable summary in the approval
	// dialog (a prompt, not a result): long arguments are the dialog's raw
	// "parameters" block instead.
	maxApprovalSummaryRunes = 200
	// maxWorkspaceInstructionsBytes bounds the injected workspace instruction
	// file (AGENTS.md by default). Larger files are truncated with a warning.
	maxWorkspaceInstructionsBytes = 32 << 10
	// workspaceInstructionsHeading introduces the injected file content.
	workspaceInstructionsHeading = "# 工作区指令（AGENTS.md）"
)

// Config is the module's typed assembly config.
type Config struct {
	// Model is the default model name.
	Model string `json:"model"`
	// BaseURL is the OpenAI-compatible API base URL (without
	// /chat/completions); empty uses the provider default.
	BaseURL string `json:"baseURL"`
	// ApiKeyEnv names the environment variable read for the API key when the
	// settings store has none (default OPENAI_API_KEY).
	ApiKeyEnv string `json:"apiKeyEnv"`
	// SystemPrompt is the default system prompt.
	SystemPrompt string `json:"systemPrompt"`
	// MaxIterations bounds the model/tool rounds per run; 0 (the default)
	// means no practical limit.
	MaxIterations int `json:"maxIterations"`
	// ContextBudget is the estimated prompt token budget (0 = unlimited).
	ContextBudget int `json:"contextBudget"`
	// ReasoningEffort is the default provider reasoning effort (low/medium/high;
	// empty omits the field). A per-session setting overrides it.
	ReasoningEffort string `json:"reasoningEffort"`
	// RequireFinishReason treats a stream that ends without a finish reason as a
	// truncated/failed turn. The provider hides the [DONE] terminator behind a
	// clean EOF, so this is the product-side guard against silently truncated
	// answers (SDK ErrIncompleteStream covers the definitive signal once
	// available). Settings key chat.requireFinishReason overrides it.
	RequireFinishReason bool `json:"requireFinishReason"`
}

// Module is the agent backend descriptor. It provides contracts.KeyChat.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		registry, err := ctx.Require(contracts.KeyToolkit)
		if err != nil {
			return err
		}
		settings, err := ctx.Require(contracts.KeySettings)
		if err != nil {
			return err
		}
		logger, err := ctx.Require(contracts.KeyLogger)
		if err != nil {
			return err
		}
		store, err := ctx.Require(contracts.KeyStore)
		if err != nil {
			return err
		}
		approvals, err := ctx.Require(contracts.KeyApprovals)
		if err != nil {
			return err
		}
		dataDir, err := ctx.Require(contracts.KeyDataDir)
		if err != nil {
			return err
		}
		svc := newService(cfg, registry, settings, logger, store, approvals)
		svc.dataDir = dataDir
		if workspace, ok := ctx.Get(contracts.KeyWorkspace); ok {
			svc.workspace = workspace
		}
		if memoryStore, ok := ctx.Get(contracts.KeyMemory); ok && memoryStore != nil {
			svc.memoryStore = memoryStore
			if _, err := ctx.Provide(contracts.KeyMemoryExtractor, contracts.MemoryExtractionService(svc)); err != nil {
				return fmt.Errorf("agent: provide memory extractor: %w", err)
			}
		}
		svc.restore(context.Background())
		// The durable store now backs the session-query tools: register them
		// into the shared toolkit registry (the toolkit module has no store
		// dependency, so the session tools join here).
		for _, tool := range rigsession.Tools(store) {
			if err := registry.Register(tool); err != nil {
				logger.Printf("[agent] register session tool %s: %v", tool.Name(), err)
			}
		}
		// The agent owns the synchronous delegation tools: they need the live
		// service (provider config, session history for fork and the child
		// runner), so they register straight into the shared toolkit registry
		// and unregister through kernel effects.
		for _, tool := range newSubagentTools(svc) {
			if err := registry.Register(tool); err != nil {
				return fmt.Errorf("agent: register %q: %w", tool.Name(), err)
			}
			name := tool.Name()
			if _, err := ctx.Effect(func() cordis.Effect {
				return cordis.Effect{cordis.CleanupOf(func() { registry.Unregister(name) })}
			}); err != nil {
				registry.Unregister(name)
				return err
			}
		}
		// The team manager tool set overlaps the synchronous subagent names, so
		// register only names the shared toolkit does not already have.
		teamManager := svc.teamManagerService()
		known := make(map[string]bool, len(registry.Specs()))
		for _, spec := range registry.Specs() {
			known[spec.Name] = true
		}
		for _, tool := range newTeamTools(teamManager) {
			name := strings.TrimSpace(tool.Name())
			if name == "" {
				continue
			}
			if known[name] {
				logger.Printf("[agent] skip team tool %s: duplicate registry name", name)
				continue
			}
			if err := registry.Register(tool); err != nil {
				return fmt.Errorf("agent: register team tool %q: %w", name, err)
			}
			known[name] = true
			if _, err := ctx.Effect(func() cordis.Effect {
				return cordis.Effect{cordis.CleanupOf(func() { registry.Unregister(name) })}
			}); err != nil {
				registry.Unregister(name)
				return err
			}
		}
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(func() {
				if closeErr := teamManager.CloseAll(); closeErr != nil {
					logger.Printf("[agent] close team manager: %v", closeErr)
				}
			})}
		}); err != nil {
			_ = teamManager.CloseAll()
			return err
		}
		if _, err := ctx.Provide(contracts.KeySubagents, contracts.Subagents(svc)); err != nil {
			return fmt.Errorf("agent: provide subagents: %w", err)
		}
		if _, err := ctx.Provide(contracts.KeyTeam, contracts.TeamService(newTeamService(svc))); err != nil {
			return fmt.Errorf("agent: provide team service: %w", err)
		}
		_, err = ctx.Provide(contracts.KeyChat, contracts.ChatService(svc))
		return err
	}).
		AddProvides(
			module.Provides(contracts.KeyChat),
			module.Provides(contracts.KeySubagents),
			module.Provides(contracts.KeyTeam),
			module.Provides(contracts.KeyMemoryExtractor),
		).
		AddRequires(module.Requires(contracts.KeyToolkit)).
		AddRequires(module.Requires(contracts.KeySettings)).
		AddRequires(module.Requires(contracts.KeyLogger)).
		AddRequires(module.Requires(contracts.KeyStore)).
		AddRequires(module.Requires(contracts.KeyApprovals)).
		AddRequires(module.Requires(contracts.KeyDataDir)).
		WithName("ZeoKeel agent backend")
}

// service is the contracts.ChatService implementation.
type service struct {
	mu sync.Mutex

	cfg       Config
	registry  *rigtools.Registry
	settings  contracts.SettingsService
	logger    contracts.Logger
	history   rig.History // fallback in-memory history when no store is wired
	store     contracts.Store
	approvals contracts.Approvals
	// dataDir is the product data directory: the spill fallback for headless
	// sessions that have no registered workspace.
	dataDir string
	// workspace is optional: when the workspace module is active the backend
	// records which workspace each session belongs to.
	workspace contracts.WorkspaceService

	sessions map[string]*sessionState
	active   string
	// sessionPermissions stores the optional per-session permission overrides
	// (ask/workspace/full/never); they live until session deletion or process
	// exit and never persist to settings.
	sessionPermissions map[string]string
	// sessionPresets stores the optional per-session agent-preset selection
	// (preset ID); cleared on session deletion, never persisted.
	sessionPresets map[string]string
	// allowedToolsMu serializes the read-modify-write of the per-session
	// "always allow" tool set persisted in settings, so parallel tool calls
	// cannot lose entries.
	allowedToolsMu sync.Mutex
	// memoryStore is the optional durable memory backend used by model-driven
	// extraction; extracting is its single-flight guard.
	memoryStore contracts.MemoryStore
	extracting  atomic.Bool

	subscribers    []*subscription
	nextSessionSeq uint64
	nextMessageSeq uint64
	// nextSubagentSeq names auto-named synchronous subagents (sub-<n>).
	nextSubagentSeq atomic.Uint64
	// teamManager owns the session's live teammates (created lazily under mu).
	teamManager *rigteam.Manager
	// subagentActivities is the bounded read model of the synchronous
	// delegations (contracts.SubagentActivityLister) shown by the inspector's
	// Agents pane; its own mutex guards it, never s.mu.
	subagentActivities subagentActivityRegistry

	// providerFactory builds the provider for one run. It is a field so tests
	// can script providers; nil uses openai.New with the effective config.
	providerFactory func(contracts.ChatConfig) (rig.Provider, error)
	// compactorFactory builds the manual-compaction compactor for one run's
	// config. It is a field so tests can script compaction; nil builds the LLM
	// compactor over a fresh provider.
	compactorFactory func(contracts.ChatConfig) (rig.Compactor, error)
}

var (
	_ contracts.ChatService               = (*service)(nil)
	_ contracts.SessionRenamer            = (*service)(nil)
	_ contracts.AttachmentSender          = (*service)(nil)
	_ contracts.SessionForker             = (*service)(nil)
	_ contracts.SessionPinner             = (*service)(nil)
	_ contracts.SessionArchiver           = (*service)(nil)
	_ contracts.SessionRegenerator        = (*service)(nil)
	_ contracts.SessionModelSwitcher      = (*service)(nil)
	_ contracts.SessionWorkspaceSwitcher  = (*service)(nil)
	_ contracts.SessionPermissionSwitcher = (*service)(nil)
	_ contracts.SessionPresetSetter       = (*service)(nil)
	_ contracts.SessionToolPermissions    = (*service)(nil)
	_ contracts.Subagents                 = (*service)(nil)
)

// sessionState is the mutable per-session state.
type sessionState struct {
	id          string
	title       string
	workspaceID string
	createdAt   time.Time
	updatedAt   time.Time
	messages    []contracts.ChatMessage
	running     bool
	err         string
	cancel      context.CancelFunc
	// assistant is the index of the streaming assistant placeholder, or -1.
	assistant int
}

// subscription is one Subscribe callback.
type subscription struct {
	fn func(string)
}

// newService builds a service; it is called by the module factory and tests.
func newService(cfg Config, registry *rigtools.Registry, settings contracts.SettingsService,
	logger contracts.Logger, store contracts.Store, approvals contracts.Approvals) *service {
	cfg = cfg.normalized()
	if logger == nil {
		logger = discardLogger{}
	}
	return &service{
		cfg:                cfg,
		registry:           registry,
		settings:           settings,
		logger:             logger,
		history:            session.NewMemory(),
		store:              store,
		approvals:          approvals,
		sessions:           make(map[string]*sessionState),
		sessionPermissions: make(map[string]string),
		sessionPresets:     make(map[string]string),
	}
}

// historyView is the Rig History the loop persists to: the durable store when
// configured, the in-memory fallback otherwise.
func (s *service) historyView() rig.History {
	if s.store != nil {
		return s.store.History()
	}
	return s.history
}

// currentWorkspaceID reports the current workspace ID when the workspace module
// is wired, ignoring/logging lookup failures (association is best effort).
func (s *service) currentWorkspaceID(ctx context.Context) string {
	if s.workspace == nil {
		return ""
	}
	workspace, ok, err := s.workspace.Current(ctx)
	if err != nil {
		s.logger.Printf("[agent] current workspace: %v", err)
		return ""
	}
	if !ok {
		return ""
	}
	return strings.TrimSpace(workspace.ID)
}

// withWorkspaceInstructions appends the configured workspace instruction file
// (agent.agentsFile, AGENTS.md by default) to this run's system prompt. The
// file is read from the current workspace at run time; a missing file is
// ignored, a read failure or an over-limit file is logged and skipped/truncated,
// and an empty setting disables the injection. Only the in-memory run config is
// changed: nothing is persisted.
func (s *service) withWorkspaceInstructions(ctx context.Context, cfg contracts.ChatConfig) contracts.ChatConfig {
	if s.workspace == nil || s.settings == nil {
		return cfg
	}
	name := strings.TrimSpace(s.settings.Get(contracts.SettingAgentsFile, "AGENTS.md"))
	if name == "" {
		return cfg
	}
	if ctx == nil {
		ctx = context.Background()
	}
	workspace, ok, err := s.workspace.Current(ctx)
	if err != nil {
		s.logger.Printf("[agent] workspace instructions: current workspace: %v", err)
		return cfg
	}
	if !ok || strings.TrimSpace(workspace.Root) == "" {
		return cfg
	}
	path := filepath.Join(workspace.Root, name)
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			s.logger.Printf("[agent] read workspace instructions %s: %v", path, err)
		}
		return cfg
	}
	if len(data) > maxWorkspaceInstructionsBytes {
		data = data[:maxWorkspaceInstructionsBytes]
		s.logger.Printf("[agent] workspace instructions %s exceed %d bytes; truncated", path, maxWorkspaceInstructionsBytes)
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return cfg
	}
	block := workspaceInstructionsHeading + "\n\n" + content
	if strings.TrimSpace(cfg.SystemPrompt) == "" {
		cfg.SystemPrompt = block
	} else {
		cfg.SystemPrompt = strings.TrimRight(cfg.SystemPrompt, "\n") + "\n\n" + block
	}
	return cfg
}

// persistSessionWorkspace records the session→workspace association through the
// optional durable half of the store. Failures are logged: the read model and
// the conversation must not fail because the association could not be saved.
func (s *service) persistSessionWorkspace(sessionID, workspaceID string) {
	if s.store == nil || strings.TrimSpace(workspaceID) == "" {
		return
	}
	recorder, ok := s.store.(contracts.SessionWorkspaceStore)
	if !ok {
		return
	}
	if err := recorder.SetSessionWorkspace(context.Background(), sessionID, workspaceID); err != nil {
		s.logger.Printf("[agent] persist workspace of session %s: %v", sessionID, err)
	}
}

// restore loads durable sessions (metadata + messages) into the in-memory read
// model. Failures are logged and leave the product usable with an empty list.
func (s *service) restore(ctx context.Context) {
	if s.store == nil {
		return
	}
	metas, err := s.store.Sessions(ctx)
	if err != nil {
		s.logger.Printf("[agent] restore: list sessions: %v", err)
		return
	}
	history := s.store.History()
	type restoredSession struct {
		state   *sessionState
		lastErr string
		errTime time.Time
	}
	restored := make([]restoredSession, 0, len(metas))
	for _, meta := range metas {
		state := &sessionState{
			id:          meta.ID,
			title:       meta.Title,
			workspaceID: meta.WorkspaceID,
			createdAt:   meta.CreatedAt,
			updatedAt:   meta.UpdatedAt,
			assistant:   -1,
		}
		if history != nil {
			if messages, err := history.Load(ctx, meta.ID); err == nil {
				state.messages = chatMessagesFromRig(messages)
				// Rig history has no text offsets: replay the durable
				// tool_call/tool_result events onto the read model so a live
				// turn whose calls all came before any prose keeps rendering
				// cards-above-text after a restart. Sessions without tool
				// calls skip the event read entirely.
				s.hydrateToolTimeline(ctx, meta.ID, state)
			} else {
				s.logger.Printf("[agent] restore: load %s: %v", meta.ID, err)
			}
		}
		lastErr, errTime := s.lastRunError(ctx, meta.ID)
		restored = append(restored, restoredSession{state: state, lastErr: lastErr, errTime: errTime})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, restored := range restored {
		state := restored.state
		// Rig history carries no message ids; assign deterministic ids so the
		// settings-backed feedback keys can be hydrated after a restart (the
		// simple no-regenerate sequence reproduces the original msg-N ids).
		for index := range state.messages {
			if strings.TrimSpace(state.messages[index].ID) == "" {
				state.messages[index].ID = s.nextMessageIDLocked()
			}
		}
		// A failed or cancelled last turn keeps its durable run_end event; put
		// the error back on the read model so the failure (and its retry
		// affordance) does not vanish when the app restarts.
		s.hydrateRunErrorLocked(state, restored.lastErr, restored.errTime)
		s.sessions[state.id] = state
	}
	if len(restored) > 0 {
		s.active = restored[0].state.id
	}
}

// lastRunError returns the durable failure of a session's most recent run.
// Only a run_end that is the session's last event counts: a later user message
// means the failed turn was already superseded by newer work.
func (s *service) lastRunError(ctx context.Context, sessionID string) (string, time.Time) {
	if s.store == nil {
		return "", time.Time{}
	}
	events, err := s.store.Last(ctx, sessionID, 1)
	if err != nil {
		s.logger.Printf("[agent] restore: last event of %s: %v", sessionID, err)
		return "", time.Time{}
	}
	if len(events) == 0 || events[len(events)-1].Type != "run_end" {
		return "", time.Time{}
	}
	event := events[len(events)-1]
	var payload struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		s.logger.Printf("[agent] restore: decode run_end of %s: %v", sessionID, err)
		return "", time.Time{}
	}
	if payload.OK {
		return "", time.Time{}
	}
	return strings.TrimSpace(payload.Error), event.Time
}

// hydrateRunErrorLocked attaches a durable run failure to the restored read
// model: a persisted partial assistant message gets the error, otherwise a
// synthetic assistant error row is appended. The caller holds s.mu.
func (s *service) hydrateRunErrorLocked(state *sessionState, errText string, at time.Time) {
	if state == nil || errText == "" {
		return
	}
	state.err = errText
	kind := classifyRestoredError(errText)
	last := len(state.messages) - 1
	if last >= 0 && state.messages[last].Role == contracts.ChatRoleAssistant {
		state.messages[last].Streaming = false
		state.messages[last].Error = errText
		state.messages[last].ErrorKind = kind
		return
	}
	state.messages = append(state.messages, contracts.ChatMessage{
		ID:        s.nextMessageIDLocked(),
		Role:      contracts.ChatRoleAssistant,
		Error:     errText,
		ErrorKind: kind,
		CreatedAt: at,
	})
}

// classifyRestoredError classifies a durable run error string for the UI.
func classifyRestoredError(errText string) string {
	if strings.EqualFold(strings.TrimSpace(errText), "cancelled") {
		return contracts.MessageErrorKindCancelled
	}
	return contracts.ClassifyError(nil, errText)
}

// normalized applies config defaults.
func (cfg Config) normalized() Config {
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.ApiKeyEnv = strings.TrimSpace(cfg.ApiKeyEnv)
	cfg.SystemPrompt = strings.TrimSpace(cfg.SystemPrompt)
	if cfg.MaxIterations < 0 {
		cfg.MaxIterations = 0
	}
	if cfg.ContextBudget < 0 {
		cfg.ContextBudget = 0
	}
	return cfg
}

// discardLogger drops agent log lines.
type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// --- ChatService: configuration -------------------------------------------

// Config returns the effective default model configuration. The configured model
// list decides the default model (see defaultModelLocked) and the model row's
// own base URL/key win over the global chat.baseURL/chat.apiKey; without a
// configured list the legacy settings/assembly/environment fallbacks apply.
func (s *service) Config() contracts.ChatConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.configLocked()
}

// modelCatalogState is the decoded user model list (modelcatalog.models) together
// with whether the row exists at all. The distinction matters for the default
// model: an absent row means "never saved a list" (the legacy fallbacks apply),
// while a present but empty list means the user removed every model — there is no
// default model then, and the UI must show "not configured" instead of a ghost
// model. A malformed row resolves as absent here (the modelcatalog module owns
// the warning for it).
type modelCatalogState struct {
	rows    []contracts.ModelInfo
	present bool
}

// modelCatalogState decodes the model list state. It only reads the
// goroutine-safe settings store, so callers may use it with or without s.mu.
func (s *service) modelCatalogState() modelCatalogState {
	if s.settings == nil {
		return modelCatalogState{}
	}
	raw := strings.TrimSpace(s.settings.Get(contracts.SettingModelCatalogModels, ""))
	if raw == "" {
		return modelCatalogState{}
	}
	models, err := contracts.ParseModelInfoList(raw)
	if err != nil {
		return modelCatalogState{}
	}
	return modelCatalogState{rows: contracts.SanitizeModelInfoList(models), present: true}
}

// configLocked resolves the effective default config; the caller holds s.mu. A
// nil settings store falls back to the assembly config (tests and embedders may
// run without one; the product always provides it).
func (s *service) configLocked() contracts.ChatConfig {
	catalog := s.modelCatalogState()
	return s.configForModelLocked(s.defaultModelLocked(catalog), catalog)
}

// defaultModelLocked resolves the default model: a non-empty model list decides
// (chat.model when it is one of the rows, else the first row), and without one
// the legacy chain chat.model → assembly config → DefaultModel applies. An
// explicitly saved empty list with no chat.model yields "" (no model configured):
// it must not resurrect a default the user removed.
func (s *service) defaultModelLocked(catalog modelCatalogState) string {
	model := ""
	if s.settings != nil {
		model = strings.TrimSpace(s.settings.Get(contracts.SettingsChatModel, ""))
	}
	if len(catalog.rows) > 0 {
		if id := contracts.DefaultModelID(catalog.rows, s.settings); id != "" {
			model = id
		}
	}
	if model == "" && catalog.present {
		// The user saved a model list and it resolves to no rows, and chat.model
		// carries no legacy model: there is nothing to run. Stay empty so
		// Config().Model is truthful and Send can fail with a readable error.
		return ""
	}
	if model == "" {
		model = s.cfg.Model
	}
	if model == "" {
		model = DefaultModel
	}
	return model
}

// configForModelLocked builds the effective config for one model. The model
// row's own BaseURL/APIKey win, then chat.baseURL/chat.apiKey, then the assembly
// config (URL) and the provider environment variable (key); the system prompt has
// no per-model field, so its fallback (chat.systemPrompt → assembly prompt) is
// unchanged. The caller holds s.mu.
func (s *service) configForModelLocked(model string, catalog modelCatalogState) contracts.ChatConfig {
	baseURL := ""
	apiKey := ""
	systemPrompt := ""
	if s.settings != nil {
		baseURL = strings.TrimSpace(s.settings.Get(contracts.SettingsChatBaseURL, ""))
		apiKey = strings.TrimSpace(s.settings.Get(contracts.SettingsChatAPIKey, ""))
		systemPrompt = strings.TrimSpace(s.settings.Get(contracts.SettingsChatSystemPrompt, ""))
	}
	baseURL, apiKey = contracts.ModelConnection(catalog.rows, model, baseURL, apiKey)
	if baseURL == "" {
		baseURL = s.cfg.BaseURL
	}
	if systemPrompt == "" {
		systemPrompt = s.cfg.SystemPrompt
	}
	if apiKey == "" {
		env := s.cfg.ApiKeyEnv
		if env == "" {
			env = DefaultAPIKeyEnv
		}
		apiKey = strings.TrimSpace(os.Getenv(env))
	}
	return contracts.ChatConfig{
		Model:           model,
		BaseURL:         baseURL,
		APIKey:          apiKey,
		SystemPrompt:    systemPrompt,
		MaxIterations:   s.settingInt(contracts.SettingsChatMaxIterations, s.cfg.MaxIterations, 1),
		ContextBudget:   s.settingInt(contracts.SettingsChatContextBudget, s.cfg.ContextBudget, 0),
		ReasoningEffort: sanitizeReasoningEffort(s.cfg.ReasoningEffort),
	}
}

// configForModelOverride resolves the provider connection for a subagent/team
// model override: an override that names a configured model row uses that row's
// own BaseURL/APIKey, while an unknown override (or no override) keeps the parent
// config's connection. The system prompt and runtime limits stay the parent's.
func (s *service) configForModelOverride(parent contracts.ChatConfig, model string) contracts.ChatConfig {
	model = strings.TrimSpace(model)
	if model == "" || model == parent.Model {
		return parent
	}
	catalog := s.modelCatalogState()
	baseURL, apiKey := contracts.ModelConnection(catalog.rows, model, parent.BaseURL, parent.APIKey)
	cfg := parent
	cfg.Model = model
	cfg.BaseURL = baseURL
	cfg.APIKey = apiKey
	return cfg
}

// settingInt resolves an integer settings override: an empty or malformed row
// keeps the module config, and values below min are ignored (min=0 still accepts
// an explicit 0, which is meaningful for "unlimited"/"disabled").
func (s *service) settingInt(key string, fallback, min int) int {
	if s.settings == nil {
		return fallback
	}
	raw := strings.TrimSpace(s.settings.Get(key, ""))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < min {
		return fallback
	}
	return value
}

// sessionModelLocked resolves the model a session's next run uses: the
// per-session settings override when one was chosen, else the agent preset's
// model, else the default model of the configured model list / legacy chain. The
// caller holds s.mu.
func (s *service) sessionModelLocked(sessionID string) string {
	return s.sessionModelForLocked(sessionID, s.modelCatalogState())
}

// sessionModelForLocked is sessionModelLocked with the already-loaded model list
// state; the caller holds s.mu.
func (s *service) sessionModelForLocked(sessionID string, catalog modelCatalogState) string {
	model := s.defaultModelLocked(catalog)
	if s.settings == nil {
		return model
	}
	if override := strings.TrimSpace(s.settings.Get(contracts.SessionModelSettingKey(sessionID), "")); override != "" {
		return override
	}
	if preset, ok := s.agentPresetForSessionLocked(sessionID); ok && strings.TrimSpace(preset.Model) != "" {
		return strings.TrimSpace(preset.Model)
	}
	return model
}

// configForSessionLocked is configForModelLocked with the session's model and its
// own connection applied; the caller holds s.mu.
func (s *service) configForSessionLocked(sessionID string) contracts.ChatConfig {
	catalog := s.modelCatalogState()
	cfg := s.configForModelLocked(s.sessionModelForLocked(sessionID, catalog), catalog)
	cfg.ReasoningEffort = s.sessionEffort(sessionID)
	return cfg
}

// RenameSession updates a session's display title in the read model and, when a
// store is wired, in the durable session row.
func (s *service) RenameSession(sessionID, title string) error {
	title = truncateTitle(strings.TrimSpace(title))
	if title == "" {
		return errors.New("agent: session title must not be empty")
	}
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	state.title = title
	state.updatedAt = time.Now()
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.RenameSession(context.Background(), sessionID, title); err != nil {
			s.logger.Printf("[agent] rename session %s: %v", sessionID, err)
			return err
		}
	}
	s.notify(sessionID)
	return nil
}

// SetSessionPinned implements contracts.SessionPinner: the pin flag is stored
// in the settings store (the old shell persisted pins there, not in the session
// schema), and subscribers are notified so the sidebar can re-sort.
func (s *service) SetSessionPinned(sessionID string, pinned bool) error {
	sessionID = strings.TrimSpace(sessionID)
	s.mu.Lock()
	_, exists := s.sessions[sessionID]
	s.mu.Unlock()
	if !exists {
		return fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	if s.settings == nil {
		return errors.New("agent: settings service is unavailable")
	}
	value := "false"
	if pinned {
		value = "true"
	}
	if err := s.settings.Set(contracts.SessionPinnedSettingKey(sessionID), value); err != nil {
		return fmt.Errorf("agent: persist pinned flag of session %q: %w", sessionID, err)
	}
	s.mu.Lock()
	_, exists = s.sessions[sessionID]
	s.mu.Unlock()
	if exists {
		s.notify(sessionID)
	}
	return nil
}

// SetSessionArchived implements contracts.SessionArchiver: the archive flag is
// stored in the settings store, exactly like pins, and subscribers are notified
// so the sidebar can hide/show the session.
func (s *service) SetSessionArchived(sessionID string, archived bool) error {
	sessionID = strings.TrimSpace(sessionID)
	s.mu.Lock()
	_, exists := s.sessions[sessionID]
	s.mu.Unlock()
	if !exists {
		return fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	if s.settings == nil {
		return errors.New("agent: settings service is unavailable")
	}
	value := "false"
	if archived {
		value = "true"
	}
	if err := s.settings.Set(contracts.SessionArchivedSettingKey(sessionID), value); err != nil {
		return fmt.Errorf("agent: persist archive flag of session %q: %w", sessionID, err)
	}
	s.mu.Lock()
	_, exists = s.sessions[sessionID]
	s.mu.Unlock()
	if exists {
		s.notify(sessionID)
	}
	return nil
}

// sessionPinned reads one session's persisted pin flag ("false" when absent or
// when no settings service is wired).
func sessionPinned(settings contracts.SettingsService, sessionID string) bool {
	if settings == nil {
		return false
	}
	return settings.Get(contracts.SessionPinnedSettingKey(sessionID), "false") == "true"
}

// sessionArchived reads one session's persisted archive flag.
func sessionArchived(settings contracts.SettingsService, sessionID string) bool {
	if settings == nil {
		return false
	}
	return settings.Get(contracts.SessionArchivedSettingKey(sessionID), "false") == "true"
}

// SetSessionModel implements contracts.SessionModelSwitcher: it persists the
// per-session model override, which the next run and ChatSession.Model read.
// An empty model or unknown session is rejected; a failed write leaves the
// effective model unchanged.
func (s *service) SetSessionModel(sessionID, model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return errors.New("agent: session model must not be empty")
	}
	s.mu.Lock()
	_, ok := s.sessions[sessionID]
	settings := s.settings
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	if settings == nil {
		return ErrSettingsUnavailable
	}
	if err := settings.Set(contracts.SessionModelSettingKey(sessionID), model); err != nil {
		return fmt.Errorf("agent: persist model of session %q: %w", sessionID, err)
	}
	s.notify(sessionID)
	return nil
}

// --- ChatService: sessions -------------------------------------------------

// NewSession creates an empty session, marks it active and returns its ID.
func (s *service) NewSession(title string) (string, error) {
	now := time.Now()
	workspaceID := s.currentWorkspaceID(context.Background())
	s.mu.Lock()
	id := s.newSessionIDLocked()
	state := &sessionState{
		id:          id,
		title:       strings.TrimSpace(title),
		workspaceID: workspaceID,
		createdAt:   now,
		updatedAt:   now,
		assistant:   -1,
	}
	s.sessions[id] = state
	s.active = id
	s.mu.Unlock()
	if s.store != nil {
		if err := s.store.CreateSession(context.Background(), id, state.title); err != nil {
			s.logger.Printf("[agent] create session %s: %v", id, err)
		}
	}
	if workspaceID != "" {
		s.persistSessionWorkspace(id, workspaceID)
	}
	s.notify(id)
	return id, nil
}

// ForkSession implements contracts.SessionForker: it duplicates a session's
// read-model messages into a fresh session, makes the copy active, persists it
// best-effort (CreateSession/RenameSession/ReplaceHistory plus a "fork" event)
// and notifies subscribers. A running source is snapshotted, never cancelled.
func (s *service) ForkSession(sessionID string) (string, error) {
	now := time.Now()
	s.mu.Lock()
	source, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return "", fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	newID := s.newSessionIDLocked()
	title := strings.TrimSpace(source.title)
	if title == "" {
		title = "未命名会话"
	}
	title = truncateTitle("副本 - " + title)
	messages := make([]contracts.ChatMessage, 0, len(source.messages))
	for _, message := range source.messages {
		copied := message
		copied.ID = s.nextMessageIDLocked()
		copied.Streaming = false
		copied.Tools = append([]contracts.ToolCall(nil), message.Tools...)
		copied.Attachments = cloneAttachments(message.Attachments)
		messages = append(messages, copied)
	}
	state := &sessionState{
		id:          newID,
		title:       title,
		workspaceID: source.workspaceID,
		createdAt:   now,
		updatedAt:   now,
		messages:    messages,
		assistant:   -1,
	}
	s.sessions[newID] = state
	s.active = newID
	s.mu.Unlock()

	if s.store != nil {
		ctx := context.Background()
		if err := s.store.CreateSession(ctx, newID, title); err != nil {
			s.logger.Printf("[agent] fork: create session %s: %v", newID, err)
		}
		if err := s.store.RenameSession(ctx, newID, title); err != nil {
			s.logger.Printf("[agent] fork: rename session %s: %v", newID, err)
		}
		history := forkHistoryMessages(messages)
		if err := s.store.ReplaceHistory(ctx, newID, history); err != nil {
			s.logger.Printf("[agent] fork: replace history %s: %v", newID, err)
		}
		s.appendEvent(ctx, newID, "fork", map[string]any{"source": sessionID})
	}
	if state.workspaceID != "" {
		s.persistSessionWorkspace(newID, state.workspaceID)
	}
	s.notify(newID)
	return newID, nil
}

// forkHistoryMessages rebuilds a Rig history from a forked read model: user and
// assistant messages only (tool calls are not replayed), with assistant
// reasoning preserved.
func forkHistoryMessages(messages []contracts.ChatMessage) []rig.Message {
	out := make([]rig.Message, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case contracts.ChatRoleUser:
			out = append(out, rig.NewMessage(rig.RoleUser, message.Text))
		case contracts.ChatRoleAssistant:
			forked := rig.NewMessage(rig.RoleAssistant, message.Text)
			forked.Reasoning = message.Reasoning
			out = append(out, forked)
		}
	}
	return out
}

// newSessionIDLocked generates a unique "sess-<unixnano>" ID.
func (s *service) newSessionIDLocked() string {
	for {
		s.nextSessionSeq++
		id := fmt.Sprintf("sess-%d", time.Now().UnixNano())
		if _, exists := s.sessions[id]; !exists {
			return id
		}
	}
}

// DeleteSession removes a session, cancels its run and moves the active session
// to the newest remaining one (or "").
// SetSessionWorkspace implements contracts.SessionWorkspaceSwitcher: it moves
// one of this service's sessions to a workspace (empty clears the binding). The
// workspace service owns existence checks; the agent records the durable
// association when the store exposes SessionWorkspaceStore and updates its
// read model either way.
func (s *service) SetSessionWorkspace(sessionID, workspaceID string) error {
	sessionID = strings.TrimSpace(sessionID)
	workspaceID = strings.TrimSpace(workspaceID)
	s.mu.Lock()
	_, ok := s.sessions[sessionID]
	recorder, canRecord := s.store.(contracts.SessionWorkspaceStore)
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	if workspaceID != "" && !canRecord {
		s.logger.Printf("[agent] session workspace switch: store lacks SessionWorkspaceStore")
	}
	if canRecord {
		if err := recorder.SetSessionWorkspace(context.Background(), sessionID, workspaceID); err != nil {
			return fmt.Errorf("agent: record workspace of session %q: %w", sessionID, err)
		}
	}
	s.mu.Lock()
	if state, exists := s.sessions[sessionID]; exists {
		state.workspaceID = workspaceID
		state.updatedAt = time.Now()
	}
	s.mu.Unlock()
	s.notify(sessionID)
	return nil
}

// SetSessionPermission implements contracts.SessionPermissionSwitcher: it
// overrides one session's approval mode for the rest of its lifetime. Plan and
// chat modes still take precedence in the gate; deleting the session drops the
// override (overrides never touch the global settings).
func (s *service) SetSessionPermission(sessionID, mode string) error {
	sessionID = strings.TrimSpace(sessionID)
	switch raw := strings.ToLower(strings.TrimSpace(mode)); raw {
	case contracts.PermissionFull:
		mode = contracts.PermissionFull
	case contracts.PermissionWorkspace:
		mode = contracts.PermissionWorkspace
	case contracts.PermissionReadOnly, contracts.PermissionAsk, contracts.PermissionNever:
		// The retired "ask"/"never" spellings can still arrive from old
		// automation rows and external callers; they normalize to read-only
		// exactly like the stored setting does.
		mode = contracts.PermissionReadOnly
	default:
		return fmt.Errorf("agent: invalid session permission mode %q", mode)
	}
	s.mu.Lock()
	if _, ok := s.sessions[sessionID]; !ok {
		s.mu.Unlock()
		return fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	s.sessionPermissions[sessionID] = mode
	s.mu.Unlock()
	return nil
}

// sessionPermission returns the stored override ("" when none).
func (s *service) sessionPermission(sessionID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionPermissions[sessionID]
}

func (s *service) DeleteSession(sessionID string) error {
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	cancel := state.cancel
	delete(s.sessions, sessionID)
	delete(s.sessionPermissions, sessionID)
	delete(s.sessionPresets, sessionID)
	active := s.active
	if active == sessionID {
		s.active = s.newestSessionLocked()
		active = s.active
	}
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	// The session's "always allow" tool memory belongs to the session: forget it
	// with the session. The settings row is best effort (the session is already
	// gone; a failed cleanup must not fail the delete).
	s.clearAllowedToolsRow(sessionID)
	if s.store != nil {
		if err := s.store.DeleteSession(context.Background(), sessionID); err != nil {
			s.logger.Printf("[agent] delete session %s: %v", sessionID, err)
		}
	}
	s.notify(sessionID)
	if active != sessionID {
		s.notify(active)
	}
	return nil
}

// newestSessionLocked returns the ID of the most recently updated session.
func (s *service) newestSessionLocked() string {
	newest := ""
	var newestUpdated time.Time
	for id, state := range s.sessions {
		if newest == "" || state.updatedAt.After(newestUpdated) {
			newest = id
			newestUpdated = state.updatedAt
		}
	}
	return newest
}

// Session returns one session read model.
func (s *service) Session(sessionID string) (contracts.ChatSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return contracts.ChatSession{}, false
	}
	return state.snapshot(s.sessionModelLocked(sessionID), s.settings), true
}

// Sessions lists sessions, newest updated first.
func (s *service) Sessions() []contracts.ChatSessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	infos := make([]contracts.ChatSessionInfo, 0, len(s.sessions))
	for _, state := range s.sessions {
		infos = append(infos, contracts.ChatSessionInfo{
			ID:           state.id,
			WorkspaceID:  state.workspaceID,
			Title:        state.title,
			Pinned:       sessionPinned(s.settings, state.id),
			Archived:     sessionArchived(s.settings, state.id),
			MessageCount: len(state.messages),
			Running:      state.running,
			UpdatedAt:    state.updatedAt,
		})
	}
	sort.SliceStable(infos, func(i, j int) bool {
		if !infos[i].UpdatedAt.Equal(infos[j].UpdatedAt) {
			return infos[i].UpdatedAt.After(infos[j].UpdatedAt)
		}
		return infos[i].ID > infos[j].ID
	})
	return infos
}

// ActiveSession returns the session the UI currently shows.
func (s *service) ActiveSession() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

// SetActiveSession switches the active session.
func (s *service) SetActiveSession(sessionID string) error {
	s.mu.Lock()
	if _, ok := s.sessions[sessionID]; !ok {
		s.mu.Unlock()
		return fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	s.active = sessionID
	s.mu.Unlock()
	s.notify(sessionID)
	return nil
}

// --- ChatService: sending --------------------------------------------------

// ErrEmptyPrompt is returned by Send for a blank prompt.
var ErrEmptyPrompt = errors.New("agent: prompt is empty")

// ErrNoModelConfigured is returned by Send/SendWithAttachments when the
// effective config has no model: with an explicitly empty model list (and no
// legacy chat.model) there is no provider target, so the send fails before the
// session changes or any provider request is attempted.
var ErrNoModelConfigured = errors.New("agent: no model configured; add one in Settings → Model")

// ErrSessionRunning is returned by Send/Regenerate when the session already runs.
var ErrSessionRunning = errors.New("agent: session is already running")

// ErrNoUserMessage is returned by Regenerate when the session has no user
// message to retry.
var ErrNoUserMessage = errors.New("agent: no user message to regenerate")

// ErrSettingsUnavailable is returned by SetSessionModel when the service was
// built without a settings store to persist the override.
var ErrSettingsUnavailable = errors.New("agent: settings store is not available")

// Send appends the user prompt, creates the streaming assistant placeholder and
// starts one asynchronous Rig agent run. When the effective config has no model
// it returns ErrNoModelConfigured without touching the session.
func (s *service) Send(sessionID, prompt string) error {
	return s.send(sessionID, prompt, nil)
}

// SendWithAttachments appends a user message carrying the prompt and file/image
// attachments, then starts one asynchronous Rig agent run (contracts
// AttachmentSender). Image attachments travel to the provider as rig image
// content parts (data URLs); other files become bounded text parts. A blank
// prompt is allowed when at least one attachment is present. When the effective
// config has no model it returns ErrNoModelConfigured without touching the
// session.
func (s *service) SendWithAttachments(sessionID, prompt string, attachments []contracts.Attachment) error {
	return s.send(sessionID, prompt, attachments)
}

// send is the shared prompt/attachment send path. The run uses the effective
// config and the shared history; a per-session context makes Cancel possible.
func (s *service) send(sessionID, prompt string, attachments []contracts.Attachment) error {
	prompt = strings.TrimSpace(prompt)
	attachments = cloneAttachments(attachments)
	if prompt == "" && len(attachments) == 0 {
		return ErrEmptyPrompt
	}
	now := time.Now()

	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	if state.running {
		s.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrSessionRunning, sessionID)
	}
	// Resolve the effective config before touching the session: with an explicitly
	// empty model list there is no model to call, so fail before appending the
	// user message or starting a run (the UI points at Settings → Model).
	cfg := s.configForSessionLocked(sessionID)
	if strings.TrimSpace(cfg.Model) == "" {
		s.mu.Unlock()
		return ErrNoModelConfigured
	}
	titleChanged := false
	if strings.TrimSpace(state.title) == "" {
		titleSeed := prompt
		if titleSeed == "" {
			titleSeed = attachmentTitle(attachments)
		}
		state.title = truncateTitle(titleSeed)
		titleChanged = true
	}
	state.messages = append(state.messages,
		contracts.ChatMessage{
			ID:          s.nextMessageIDLocked(),
			Role:        contracts.ChatRoleUser,
			Text:        prompt,
			Attachments: cloneAttachments(attachments),
			CreatedAt:   now,
		},
		contracts.ChatMessage{
			ID:        s.nextMessageIDLocked(),
			Role:      contracts.ChatRoleAssistant,
			Streaming: true,
			CreatedAt: now,
		},
	)
	state.assistant = len(state.messages) - 1
	state.running = true
	state.err = ""
	state.updatedAt = now
	// A session created while no workspace was open binds the workspace that
	// is current on its first send (NewSession otherwise already recorded it).
	boundWorkspaceID := ""
	if state.workspaceID == "" && s.workspace != nil {
		if current := s.currentWorkspaceID(context.Background()); current != "" {
			state.workspaceID = current
			boundWorkspaceID = current
		}
	}
	runCtx, cancel := context.WithCancel(context.Background())
	state.cancel = cancel
	title := state.title
	s.mu.Unlock()

	if s.store != nil {
		if err := s.store.CreateSession(runCtx, sessionID, title); err != nil {
			s.logger.Printf("[agent] persist session %s: %v", sessionID, err)
		}
		if boundWorkspaceID != "" {
			s.persistSessionWorkspace(sessionID, boundWorkspaceID)
		}
		if titleChanged {
			if err := s.store.RenameSession(runCtx, sessionID, title); err != nil {
				s.logger.Printf("[agent] rename session %s: %v", sessionID, err)
			}
		}
		s.appendEvent(runCtx, sessionID, "user_message", map[string]any{
			"text":        prompt,
			"attachments": attachmentNames(attachments),
		})
	}
	expandedPrompt, referenceIDs, referenceBudget := s.expandSessionReferences(sessionID, prompt)
	if referenceBudget != nil {
		s.logger.Printf("[agent] session reference budget: %v", referenceBudget)
	}
	if s.store != nil && len(referenceIDs) > 0 {
		payload := map[string]any{"ids": referenceIDs}
		if referenceBudget != nil {
			payload["budget"] = map[string]any{
				"kind":  referenceBudget.Kind,
				"limit": referenceBudget.Limit,
				"used":  referenceBudget.Used,
			}
		}
		s.appendEvent(runCtx, sessionID, "session_refs", payload)
	}
	input := buildUserInput(expandedPrompt, attachments)
	history := s.historyView()
	if expandedPrompt != prompt {
		originalInput := buildUserInput(prompt, attachments)
		history = newRewritingHistory(history, sessionID, input[0], originalInput[0])
	}
	s.notify(sessionID)
	go s.run(runCtx, sessionID, cfg, input, history, s.thinkingParamsForSession(sessionID, cfg.Model))
	return nil
}

// Regenerate drops the trailing assistant/tool exchange of sessionID and
// re-runs the last user message without appending a second user message. The
// existing user message (text and attachments) is preserved in the read model,
// while the durable history is replaced with the turn's pre-answer state before
// the run (trailing assistant/tool messages dropped, the user message kept) so
// Rig continues from a history that is waiting for an answer.
//
// Like Send it returns after starting the asynchronous run. It intentionally
// does not append another user_message event: the history replacement rewinds
// the existing turn and the run's own run_start/run_end events describe the
// retry. When the effective config has no model it returns ErrNoModelConfigured
// without rewinding the turn.
func (s *service) Regenerate(sessionID string) error {
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	if state.running {
		s.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrSessionRunning, sessionID)
	}
	userIndex := -1
	for index := len(state.messages) - 1; index >= 0; index-- {
		if state.messages[index].Role == contracts.ChatRoleUser {
			userIndex = index
			break
		}
	}
	if userIndex < 0 {
		s.mu.Unlock()
		return ErrNoUserMessage
	}
	// Mirror Send: with no model configured there is nothing to call, so fail
	// before rewinding the turn (no provider request, no read-model change).
	cfg := s.configForSessionLocked(sessionID)
	if strings.TrimSpace(cfg.Model) == "" {
		s.mu.Unlock()
		return ErrNoModelConfigured
	}
	user := state.messages[userIndex]
	user.Attachments = cloneAttachments(user.Attachments)

	// Keep the complete previous slice for a rollback when history persistence
	// fails; the reslice+append below allocates a new backing array anyway.
	previous := append([]contracts.ChatMessage(nil), state.messages...)
	previousAssistant := state.assistant
	previousErr := state.err
	previousUpdatedAt := state.updatedAt

	now := time.Now()
	truncated := make([]contracts.ChatMessage, userIndex+1, userIndex+2)
	copy(truncated, state.messages[:userIndex+1])
	truncated = append(truncated, contracts.ChatMessage{
		ID:        s.nextMessageIDLocked(),
		Role:      contracts.ChatRoleAssistant,
		Streaming: true,
		CreatedAt: now,
	})
	state.messages = truncated
	state.assistant = len(state.messages) - 1
	state.running = true
	state.err = ""
	state.updatedAt = now
	runCtx, cancel := context.WithCancel(context.Background())
	state.cancel = cancel
	s.mu.Unlock()

	// Persist the rewound turn: the history up to the last user message
	// (assistant/tool messages after it dropped) plus that user message itself.
	// The run then starts with no new input, so Rig continues from the
	// "waiting for an answer" history exactly once. Persisting the user message
	// here also means an interrupted retry keeps it, matching the read model.
	var input []rig.Message
	if s.store != nil {
		history := forkHistoryMessages(previous[:userIndex])
		history = append(history, buildUserInput(user.Text, user.Attachments)...)
		if err := s.store.ReplaceHistory(runCtx, sessionID, history); err != nil {
			cancel()
			s.mu.Lock()
			if current, ok := s.sessions[sessionID]; ok {
				current.messages = previous
				current.assistant = previousAssistant
				current.running = false
				current.cancel = nil
				current.err = previousErr
				current.updatedAt = previousUpdatedAt
			}
			s.mu.Unlock()
			return fmt.Errorf("agent: regenerate history for %q: %w", sessionID, err)
		}
	} else {
		// Without a durable store the in-memory Rig history has no copy of the
		// user message, so feed it as the run input instead.
		input = buildUserInput(user.Text, user.Attachments)
	}

	s.notify(sessionID)
	go s.run(runCtx, sessionID, cfg, input, s.historyView(), s.thinkingParamsForSession(sessionID, cfg.Model))
	return nil
}

// attachmentTitle is the title seed for an attachment-only send.
func attachmentTitle(attachments []contracts.Attachment) string {
	for _, attachment := range attachments {
		if name := attachmentDisplayName(attachment); name != "" {
			return name
		}
	}
	return "附件"
}

// attachmentDisplayName is the name shown for one attachment (never empty).
func attachmentDisplayName(attachment contracts.Attachment) string {
	if name := strings.TrimSpace(attachment.Name); name != "" {
		return name
	}
	return "附件"
}

// attachmentNames returns the raw display names carried by the durable
// user_message event.
func attachmentNames(attachments []contracts.Attachment) []string {
	names := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		names = append(names, attachmentDisplayName(attachment))
	}
	return names
}

// cloneAttachments copies the chips and their bytes, so the read model never
// aliases the caller's buffers.
func cloneAttachments(attachments []contracts.Attachment) []contracts.Attachment {
	if len(attachments) == 0 {
		return nil
	}
	out := make([]contracts.Attachment, len(attachments))
	copy(out, attachments)
	for index := range out {
		if len(out[index].Data) > 0 {
			out[index].Data = append([]byte(nil), out[index].Data...)
		}
	}
	return out
}

// maxAttachmentTextBytes bounds the text-file body inlined into a text part.
const maxAttachmentTextBytes = 32 << 10

// buildUserInput turns a prompt plus attachments into the initial user message
// for one Rig run: a text part for the prompt, image content parts (data URLs)
// for images and bounded text parts for every other file.
func buildUserInput(prompt string, attachments []contracts.Attachment) []rig.Message {
	if len(attachments) == 0 {
		return []rig.Message{rig.NewMessage(rig.RoleUser, prompt)}
	}
	parts := make([]rig.ContentPart, 0, len(attachments)+1)
	if prompt != "" {
		parts = append(parts, rig.TextPart(prompt))
	}
	for _, attachment := range attachments {
		if attachment.IsImage() {
			if dataURL := attachment.DataURL(); dataURL != "" {
				parts = append(parts, rig.ImagePart(dataURL, ""))
				continue
			}
		}
		parts = append(parts, rig.TextPart(attachmentText(attachment)))
	}
	if len(parts) == 0 {
		parts = append(parts, rig.TextPart(""))
	}
	return []rig.Message{{Role: rig.RoleUser, Content: parts}}
}

// attachmentText renders one non-image attachment as a text part. Text files
// carry up to 32 KiB of their content; other files only carry the header.
func attachmentText(attachment contracts.Attachment) string {
	header := fmt.Sprintf("[附件] %s (%d bytes)", attachmentDisplayName(attachment), attachment.Size())
	if attachment.Size() == 0 || !strings.HasPrefix(attachment.MIME, "text/") {
		return header
	}
	content := strings.ToValidUTF8(string(attachment.Data), "")
	if len(content) > maxAttachmentTextBytes {
		cut := maxAttachmentTextBytes
		for cut > 0 && (content[cut]&0xC0) == 0x80 {
			cut--
		}
		content = content[:cut] + "\n[truncated]"
	}
	return header + "\n" + content
}

// nextMessageIDLocked generates a message ID.
func (s *service) nextMessageIDLocked() string {
	s.nextMessageSeq++
	return fmt.Sprintf("msg-%d", s.nextMessageSeq)
}

// run executes one Rig agent run in the background with the already-built
// initial input messages.
func (s *service) run(ctx context.Context, sessionID string, cfg contracts.ChatConfig, input []rig.Message,
	history rig.History, thinking map[string]any) {
	tools := s.toolsForSession(sessionID)
	// Rebuild the complete model-facing prompt for this run: identity +
	// environment first (OS, shell, tool root, approval policy), then the
	// configured prompt with the workspace instructions and preset, then
	// per-tool guidance for the tools this run actually receives.
	cfg.SystemPrompt = s.buildRunSystemPrompt(ctx, sessionID, cfg, toolNameSet(tools))
	provider, err := s.newProvider(cfg, thinking)
	if err != nil {
		s.finish(sessionID, ctx, fmt.Errorf("create provider: %w", err))
		return
	}
	if history == nil {
		history = s.historyView()
	}
	generation := s.modelCatalogInfo(cfg.Model)
	// The product's default is unlimited; Rig treats 0 as its built-in 20-round
	// default, so a 0 limit is passed as an effectively unreachable cap.
	maxIterations := effectiveMaxIterations(cfg.MaxIterations)
	// Loop hygiene: one per-run repeat guard appends a DSH-style advisory
	// reminder after the Nth consecutive identical tool call without blocking
	// the call (see loop_guard.go). A new run starts a fresh repeat chain.
	runTools := withRepeatGuard(tools, newRepeatToolGuard())
	spec := rig.AgentSpec{
		Provider:        provider,
		Model:           cfg.Model,
		SystemPrompt:    cfg.SystemPrompt,
		MaxIterations:   maxIterations,
		Temperature:     generation.Temperature,
		TopP:            generation.TopP,
		MaxTokens:       generation.MaxTokens,
		Tools:           runTools,
		History:         s.spilledHistory(history),
		Gate:            s.gate(sessionID),
		ContextBudget:   cfg.ContextBudget,
		Logger:          s.logger,
		CheckpointStore: s.checkpointStore(),
	}
	if cfg.ContextBudget > 0 {
		spec.Compactor = compact.LLMCompactor{
			Provider: provider,
			Model:    cfg.Model,
			Fallback: compact.WindowCompactor{},
		}
	}
	runner, err := rigagent.New(spec)
	if err != nil {
		s.finish(sessionID, ctx, fmt.Errorf("create agent: %w", err))
		return
	}
	s.appendEvent(ctx, sessionID, "run_start", map[string]any{"model": cfg.Model})
	startedAt := time.Now()
	// The usage tracker is run-local: Rig's RunResult.Usage accumulates every
	// model call, while only the last call's prompt tokens describe the context
	// the model actually saw (MessageUsage.ContextTokens).
	usage := &turnUsage{}
	result, runErr := runner.Run(ctx, rig.RunRequest{
		SessionID: sessionID,
		Input:     input,
		Emit:      func(event rig.Event) { s.handleEvent(sessionID, event, usage) },
	})
	s.recordRunUsage(ctx, sessionID, result, time.Since(startedAt).Milliseconds(), usage.lastPromptTokens)
	s.finish(sessionID, ctx, runErr)
}

// newProvider constructs the run provider, honouring the test seam. thinking
// carries the already-resolved per-session request fields.
func (s *service) newProvider(cfg contracts.ChatConfig, thinking map[string]any) (rig.Provider, error) {
	if s.providerFactory != nil {
		provider, err := s.providerFactory(cfg)
		if err != nil {
			return nil, err
		}
		return withThinkingParams(provider, thinking), nil
	}
	env := s.cfg.ApiKeyEnv
	if env == "" {
		env = DefaultAPIKeyEnv
	}
	provider, err := openai.New(openai.Config{
		BaseURL:      cfg.BaseURL,
		APIKey:       cfg.APIKey,
		APIKeyEnv:    env,
		DefaultModel: cfg.Model,
		// The SDK detects a stream that ends before a finish_reason or [DONE]
		// and returns openai.ErrIncompleteStream; the product only decides
		// whether a clean terminator is required (chat.requireFinishReason).
		RequireCompleteStream: s.requireFinishReason(),
	})
	if err != nil {
		return nil, err
	}
	return withThinkingParams(provider, thinking), nil
}

// checkpointStore returns the durable checkpoint view when a store is wired.
func (s *service) checkpointStore() rig.CheckpointStore {
	if s.store == nil {
		return nil
	}
	return s.store.CheckpointStore()
}

// appendEvent records one durable session event; store failures are logged but
// never break a run.
func (s *service) appendEvent(ctx context.Context, sessionID, eventType string, fields map[string]any) {
	if s.store == nil {
		return
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		s.logger.Printf("[agent] marshal %s event: %v", eventType, err)
		return
	}
	if err := s.store.AppendEvent(ctx, rig.SessionEvent{
		SessionID: sessionID,
		Type:      eventType,
		Time:      time.Now(),
		Payload:   payload,
	}); err != nil {
		s.logger.Printf("[agent] append %s event: %v", eventType, err)
	}
}

// handleEvent applies one Rig event to the current assistant message and
// notifies subscribers. The callback runs on the run goroutine; turn is the
// run-local usage tracker (nil is accepted for callers without one).
func (s *service) handleEvent(sessionID string, event rig.Event, turn *turnUsage) {
	// Rig reports one usage event per model call and accumulates the run total
	// itself; remember the prompt tokens of the newest call so the read model can
	// expose the real context size of the turn (RunResult.Usage is a billing
	// total). Recorded before the session lookup: a session deleted mid-run still
	// must not poison the next event's tracking.
	if turn != nil && event.Type == rig.EventUsage && event.Usage != nil {
		turn.lastPromptTokens = event.Usage.PromptTokens
	}
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return
	}
	if message := state.assistantMessage(); message != nil {
		switch event.Type {
		case rig.EventTextDelta:
			message.Text += event.Text
		case rig.EventReasoningDelta:
			message.Reasoning += event.Reasoning
		case rig.EventToolCall:
			if event.Call != nil {
				textOffset := len(message.Text)
				arguments := boundedToolArguments(event.Call.Arguments)
				message.Tools = append(message.Tools, contracts.ToolCall{
					ID:         event.Call.ID,
					Name:       event.Call.Name,
					Status:     "running",
					Arguments:  arguments,
					TextOffset: textOffset,
				})
				// The turn records live tool calls, so its offsets are a known
				// timeline even when they are all zero (the model called tools
				// before writing any prose): the transcript must interleave by
				// offset instead of falling back to whole-text-then-cards.
				message.ToolTimelineKnown = true
				s.appendEvent(context.Background(), sessionID, "tool_call", map[string]any{
					"id": event.Call.ID, "name": event.Call.Name, "arguments": arguments,
					"text_offset": textOffset,
				})
			}
		case rig.EventToolResult:
			if event.Call != nil && event.Result != nil {
				for index := range message.Tools {
					if message.Tools[index].ID != event.Call.ID {
						continue
					}
					// A user stop surfaces as an error result carrying the
					// provider's cancellation text ("context canceled"). That is
					// not a tool failure: keep it a distinct neutral status so
					// the UI shows 已取消/Cancelled instead of a red 失败, and do
					// not carry the raw English cancellation text into the
					// transcript summary.
					status := "ok"
					content := summarizeToolResult(event.Result.Content)
					if event.Result.IsError {
						if toolResultCancelled(event.Result.Content) {
							status = "cancelled"
							content = ""
						} else {
							status = "error"
						}
					}
					message.Tools[index].Status = status
					message.Tools[index].Summary = content
					s.appendEvent(context.Background(), sessionID, "tool_result", map[string]any{
						"id": event.Call.ID, "name": event.Call.Name,
						"status": status, "content": content,
					})
					break
				}
			}
		case rig.EventError:
			errText := "run failed"
			if event.Err != nil {
				errText = event.Err.Error()
			}
			message.Error = errText
			message.ErrorKind = contracts.ClassifyError(event.Err, errText)
			state.err = errText
		}
	}
	state.updatedAt = time.Now()
	s.mu.Unlock()
	s.notify(sessionID)
}

// assistantMessage returns the current assistant message or nil.
func (state *sessionState) assistantMessage() *contracts.ChatMessage {
	if state.assistant < 0 || state.assistant >= len(state.messages) {
		return nil
	}
	return &state.messages[state.assistant]
}

// finish marks the run complete: streaming stops, running clears and a failure
// or cancellation is recorded while keeping the streamed content.
func (s *service) finish(sessionID string, ctx context.Context, runErr error) {
	errText := ""
	if ctx != nil && ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			errText = "cancelled"
		} else {
			errText = ctx.Err().Error()
		}
	} else if runErr != nil {
		errText = runErr.Error()
	}

	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return
	}
	if message := state.assistantMessage(); message != nil {
		message.Streaming = false
		if errText != "" {
			if message.Error == "" {
				message.Error = errText
			}
			// A user-initiated stop records the literal text "cancelled", which
			// ClassifyError does not match ("context canceled" is the provider
			// spelling). Classify the context cancellation explicitly so the UI
			// renders the localized 已取消/Cancelled row instead of a generic
			// error (the restored-session path already did this).
			switch {
			case ctx != nil && errors.Is(ctx.Err(), context.Canceled):
				message.ErrorKind = contracts.MessageErrorKindCancelled
			case ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
				message.ErrorKind = contracts.MessageErrorKindTimeout
			default:
				message.ErrorKind = contracts.ClassifyError(runErr, message.Error)
			}
			// Tool calls still running when the run was stopped are cancelled,
			// not failed: mark them with the neutral status and drop the raw
			// backend text so no English "context canceled" shows as a red
			// failure in the transcript.
			if ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
				for index := range message.Tools {
					if strings.EqualFold(strings.TrimSpace(message.Tools[index].Status), "running") {
						message.Tools[index].Status = "cancelled"
						message.Tools[index].Summary = ""
					}
				}
			}
		} else {
			// A successful turn never carries stale error details.
			message.Error = ""
			message.ErrorKind = ""
		}
	}
	state.running = false
	state.cancel = nil
	if errText != "" {
		state.err = errText
	}
	state.updatedAt = time.Now()
	s.mu.Unlock()

	s.appendEvent(context.Background(), sessionID, "run_end", map[string]any{
		"ok": errText == "", "error": errText,
	})
	if errText != "" {
		s.persistInterruptedTurn(sessionID, errText)
	}
	s.notify(sessionID)
	if runErr != nil && !errors.Is(runErr, context.Canceled) && !errors.Is(runErr, context.DeadlineExceeded) {
		s.logger.Printf("[agent] session %s run failed: %v", sessionID, runErr)
	}
}

// persistInterruptedTurn makes a failed or cancelled run survive a restart:
// the streamed assistant text is persisted into the durable history and the
// graceful stop clears the interruption checkpoint. Without this the user's
// partial answer was lost and the next run injected Rig's crash-recovery
// marker ("ZeoKeel exited before it finished") for a user-initiated stop.
//
// Only text-only assistant turns are persisted: a partial turn that already
// requested tools cannot be replayed without its tool results, so it stays in
// the read model while the durable run_end event lets restore synthesize the
// error row.
func (s *service) persistInterruptedTurn(sessionID, errText string) {
	if s.store == nil || errText == "" {
		return
	}
	ctx := context.Background()
	// A graceful stop is not a crash: clear the checkpoint so the next run
	// does not replay the "interrupted" repair marker for it.
	if checkpoints := s.store.CheckpointStore(); checkpoints != nil {
		if err := checkpoints.Clear(ctx, sessionID); err != nil {
			s.logger.Printf("[agent] clear checkpoint of %s: %v", sessionID, err)
		}
	}
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return
	}
	message := state.assistantMessage()
	partial := contracts.ChatMessage{}
	save := message != nil && len(message.Tools) == 0 &&
		(strings.TrimSpace(message.Text) != "" || strings.TrimSpace(message.Reasoning) != "")
	if save {
		partial = *message
		partial.Streaming = false
	}
	s.mu.Unlock()
	if !save {
		return
	}
	history := s.store.History()
	if history == nil {
		return
	}
	messages, err := history.Load(ctx, sessionID)
	if err != nil {
		s.logger.Printf("[agent] load history of %s: %v", sessionID, err)
		return
	}
	// The runner persists the user message before running; only append the
	// partial assistant answer after it. A non-user tail means the runner
	// already recorded more of this turn (do not append a second assistant).
	if len(messages) > 0 && messages[len(messages)-1].Role != rig.RoleUser {
		return
	}
	assistant := rig.NewMessage(rig.RoleAssistant, partial.Text)
	assistant.Reasoning = partial.Reasoning
	messages = append(messages, assistant)
	if err := s.store.ReplaceHistory(ctx, sessionID, messages); err != nil {
		s.logger.Printf("[agent] persist interrupted turn of %s: %v", sessionID, err)
	}
}

// summarizeToolArguments bounds the raw tool-call arguments shown in the
// approval dialog's summary. It stays short on purpose: the dialog also shows
// the full raw arguments block, and a 4k-character summary would push the
// decision buttons off a small window.
func summarizeToolArguments(arguments string) string {
	summary := strings.Join(strings.Fields(arguments), " ")
	runes := []rune(summary)
	if len(runes) <= maxApprovalSummaryRunes {
		return summary
	}
	return string(runes[:maxApprovalSummaryRunes]) + "…"
}

// boundedToolArguments bounds the raw tool-call arguments carried in the read
// model and the tool_call event while keeping their line breaks (the UI pretty-
// prints JSON / shows a command). Only a pathologically large argument (a whole
// file body) is cut, with the same ellipsis convention as the result summary.
func boundedToolArguments(arguments string) string {
	trimmed := strings.TrimSpace(arguments)
	runes := []rune(trimmed)
	if len(runes) <= maxToolArgumentsRunes {
		return trimmed
	}
	return string(runes[:maxToolArgumentsRunes]) + "…"
}

// chatMessagesFromRig converts persisted Rig messages into the UI read model.
// Tool results are represented on the assistant tool list; tool messages
// themselves are skipped because the trajectory comes from events.
func chatMessagesFromRig(messages []rig.Message) []contracts.ChatMessage {
	out := make([]contracts.ChatMessage, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case rig.RoleUser:
			text, attachments := userMessageContent(message)
			out = append(out, contracts.ChatMessage{
				Role:        contracts.ChatRoleUser,
				Text:        text,
				Attachments: attachments,
				CreatedAt:   messageTime(message),
			})
		case rig.RoleAssistant:
			chatMessage := contracts.ChatMessage{
				Role:      contracts.ChatRoleAssistant,
				Text:      messageText(message),
				Reasoning: message.Reasoning,
				CreatedAt: messageTime(message),
			}
			for _, call := range message.ToolCalls {
				chatMessage.Tools = append(chatMessage.Tools, contracts.ToolCall{
					ID: call.ID, Name: call.Name, Status: "ok",
				})
			}
			out = append(out, chatMessage)
		}
	}
	return out
}

func messageText(message rig.Message) string {
	var parts []string
	for _, part := range message.Content {
		if part.Type == rig.PartText && part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "")
}

// userMessageContent reconstructs the user message read model from persisted
// Rig parts: prompt text is the non-attachment text, attachment chips are
// restored from text parts written by attachmentText and from image data URLs.
func userMessageContent(message rig.Message) (string, []contracts.Attachment) {
	var textParts []string
	var attachments []contracts.Attachment
	for _, part := range message.Content {
		switch part.Type {
		case rig.PartText:
			if name, ok := parseAttachmentText(part.Text); ok {
				attachments = append(attachments, contracts.Attachment{Name: name})
				continue
			}
			if part.Text != "" {
				textParts = append(textParts, part.Text)
			}
		case rig.PartImageURL:
			if part.ImageURL == "" {
				continue
			}
			attachments = append(attachments, contracts.Attachment{
				Name: "图片",
				MIME: dataURLMIME(part.ImageURL),
			})
		}
	}
	return strings.Join(textParts, ""), attachments
}

// parseAttachmentText reports whether text is one of the attachment text parts
// written by attachmentText and returns its display name.
func parseAttachmentText(text string) (string, bool) {
	const prefix = "[附件] "
	if !strings.HasPrefix(text, prefix) {
		return "", false
	}
	header := text
	if newline := strings.IndexByte(header, '\n'); newline >= 0 {
		header = header[:newline]
	}
	rest := strings.TrimPrefix(header, prefix)
	name := rest
	if open := strings.LastIndex(rest, " ("); open >= 0 {
		if suffix := rest[open:]; strings.HasSuffix(suffix, " bytes)") {
			name = rest[:open]
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "附件"
	}
	return name, true
}

// dataURLMIME extracts the media type from a data URL ("" for other URLs).
func dataURLMIME(dataURL string) string {
	if !strings.HasPrefix(dataURL, "data:") {
		return ""
	}
	rest := strings.TrimPrefix(dataURL, "data:")
	if index := strings.IndexByte(rest, ';'); index >= 0 {
		return rest[:index]
	}
	if index := strings.IndexByte(rest, ','); index >= 0 {
		return rest[:index]
	}
	return ""
}

func messageTime(rig.Message) time.Time { return time.Time{} }

// snapshot returns a deep copy of the session read model.
func (state *sessionState) snapshot(model string, settings contracts.SettingsService) contracts.ChatSession {
	out := contracts.ChatSession{
		ID:        state.id,
		Title:     state.title,
		Model:     model,
		Running:   state.running,
		Error:     state.err,
		CreatedAt: state.createdAt,
		UpdatedAt: state.updatedAt,
		Messages:  make([]contracts.ChatMessage, 0, len(state.messages)),
	}
	for _, message := range state.messages {
		copied := message
		copied.Tools = append([]contracts.ToolCall(nil), message.Tools...)
		copied.Attachments = cloneAttachments(message.Attachments)
		out.Messages = append(out.Messages, copied)
	}
	return out
}

// --- ChatService: cancellation and notifications ---------------------------

// Cancel stops the in-flight run of a session (idempotent).
func (s *service) Cancel(sessionID string) error {
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	cancel := state.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// Subscribe registers a change callback and returns its unsubscriber.
func (s *service) Subscribe(fn func(sessionID string)) (func(), error) {
	if fn == nil {
		return func() {}, nil
	}
	listener := &subscription{fn: fn}
	s.mu.Lock()
	s.subscribers = append(s.subscribers, listener)
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			for index, candidate := range s.subscribers {
				if candidate == listener {
					s.subscribers = append(s.subscribers[:index], s.subscribers[index+1:]...)
					return
				}
			}
		})
	}, nil
}

// notify calls every subscriber outside the service lock. A panicking UI
// callback must not take the agent run down, so each call is contained.
func (s *service) notify(sessionID string) {
	s.mu.Lock()
	subscribers := append([]*subscription(nil), s.subscribers...)
	s.mu.Unlock()
	for _, listener := range subscribers {
		if listener == nil || listener.fn == nil {
			continue
		}
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					s.logger.Printf("[agent] subscriber panicked: %v", recovered)
				}
			}()
			listener.fn(sessionID)
		}()
	}
}

// --- helpers ---------------------------------------------------------------

// truncateTitle turns the first prompt into a session title of ~32 runes.
func truncateTitle(prompt string) string {
	runes := []rune(strings.Join(strings.Fields(prompt), " "))
	if len(runes) <= maxTitleRunes {
		return string(runes)
	}
	return string(runes[:maxTitleRunes]) + "…"
}

// toolResultCancelled reports whether a failed tool result is really the user
// stopping the run: providers and the toolkit spell it "context canceled",
// "cancelled while waiting ..." and similar, and none of those is a tool bug.
// The read model keeps them as the neutral "cancelled" status the UI renders
// as 已取消/Cancelled, instead of a red failure.
func toolResultCancelled(content string) bool {
	text := strings.ToLower(strings.TrimSpace(content))
	if text == "" {
		return false
	}
	for _, needle := range []string{
		"context canceled",
		"context cancelled",
		"operation was canceled",
		"operation was cancelled",
		"cancelled while waiting",
		"canceled while waiting",
	} {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

// summarizeToolResult bounds a tool result carried in the read model while
// keeping its line structure: command output and diffs read line by line, so
// only runs of spaces within one line collapse (and blank-line runs are capped),
// never the line breaks themselves. The bound is maxToolSummaryRunes; a cut
// result ends with the truncation ellipsis the UI turns into its notice.
func summarizeToolResult(content string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")
	kept := make([]string, 0, len(lines))
	blank := 0
	for _, line := range lines {
		line = strings.TrimRight(strings.Join(strings.Fields(line), " "), " ")
		if line == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		kept = append(kept, line)
	}
	summary := strings.TrimSpace(strings.Join(kept, "\n"))
	runes := []rune(summary)
	if len(runes) <= maxToolSummaryRunes {
		return summary
	}
	return string(runes[:maxToolSummaryRunes]) + "…"
}
