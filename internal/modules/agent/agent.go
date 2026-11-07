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
	"sort"
	"strings"
	"sync"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rig "github.com/zeokeel/zeokeel-rig"
	rigagent "github.com/zeokeel/zeokeel-rig/agent"
	"github.com/zeokeel/zeokeel-rig/gate"
	"github.com/zeokeel/zeokeel-rig/provider/openai"
	"github.com/zeokeel/zeokeel-rig/session"
	"github.com/zeokeel/zeokeel-rig/session/compact"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"
	rigsession "github.com/zeokeel/zeokeel-rig/tools/session"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/agent"
	// DefaultModel is used when neither the assembly, the settings store nor
	// the environment configures a model.
	DefaultModel = "gpt-4o-mini"
	// DefaultAPIKeyEnv is the environment variable read for the API key when
	// Config.ApiKeyEnv is empty.
	DefaultAPIKeyEnv = "OPENAI_API_KEY"
	// maxTitleRunes is the length the first prompt is truncated to when a
	// session has no title yet.
	maxTitleRunes = 32
	// maxToolSummaryRunes caps a tool result summary carried in the read model.
	maxToolSummaryRunes = 200
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
	// MaxIterations bounds the model/tool rounds per run (0 = Rig default 20).
	MaxIterations int `json:"maxIterations"`
	// ContextBudget is the estimated prompt token budget (0 = unlimited).
	ContextBudget int `json:"contextBudget"`
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
		svc := newService(cfg, registry, settings, logger, store, approvals)
		if workspace, ok := ctx.Get(contracts.KeyWorkspace); ok {
			svc.workspace = workspace
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
		_, err = ctx.Provide(contracts.KeyChat, contracts.ChatService(svc))
		return err
	}).
		AddProvides(module.Provides(contracts.KeyChat)).
		AddRequires(module.Requires(contracts.KeyToolkit)).
		AddRequires(module.Requires(contracts.KeySettings)).
		AddRequires(module.Requires(contracts.KeyLogger)).
		AddRequires(module.Requires(contracts.KeyStore)).
		AddRequires(module.Requires(contracts.KeyApprovals)).
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
	// workspace is optional: when the workspace module is active the backend
	// records which workspace each session belongs to.
	workspace contracts.WorkspaceService

	sessions map[string]*sessionState
	active   string

	subscribers    []*subscription
	nextSessionSeq uint64
	nextMessageSeq uint64

	// providerFactory builds the provider for one run. It is a field so tests
	// can script providers; nil uses openai.New with the effective config.
	providerFactory func(contracts.ChatConfig) (rig.Provider, error)
}

var (
	_ contracts.ChatService      = (*service)(nil)
	_ contracts.SessionRenamer   = (*service)(nil)
	_ contracts.AttachmentSender = (*service)(nil)
	_ contracts.SessionForker    = (*service)(nil)
	_ contracts.SessionPinner    = (*service)(nil)
	_ contracts.SessionArchiver  = (*service)(nil)
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
		cfg:       cfg,
		registry:  registry,
		settings:  settings,
		logger:    logger,
		history:   session.NewMemory(),
		store:     store,
		approvals: approvals,
		sessions:  make(map[string]*sessionState),
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
	model := make([]*sessionState, 0, len(metas))
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
			} else {
				s.logger.Printf("[agent] restore: load %s: %v", meta.ID, err)
			}
		}
		model = append(model, state)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, state := range model {
		s.sessions[state.id] = state
	}
	if len(model) > 0 {
		s.active = model[0].id
	}
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

// Config returns the effective model configuration. The settings store wins
// over the assembly config (the settings page is the runtime control surface),
// and the API key falls back to Config.ApiKeyEnv / OPENAI_API_KEY.
func (s *service) Config() contracts.ChatConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.configLocked()
}

// configLocked resolves the effective config; the caller holds s.mu.
func (s *service) configLocked() contracts.ChatConfig {
	model := strings.TrimSpace(s.settings.Get(contracts.SettingsChatModel, ""))
	if model == "" {
		model = s.cfg.Model
	}
	if model == "" {
		model = DefaultModel
	}
	baseURL := strings.TrimRight(strings.TrimSpace(s.settings.Get(contracts.SettingsChatBaseURL, "")), "/")
	if baseURL == "" {
		baseURL = s.cfg.BaseURL
	}
	systemPrompt := strings.TrimSpace(s.settings.Get(contracts.SettingsChatSystemPrompt, ""))
	if systemPrompt == "" {
		systemPrompt = s.cfg.SystemPrompt
	}
	apiKey := strings.TrimSpace(s.settings.Get(contracts.SettingsChatAPIKey, ""))
	if apiKey == "" {
		env := s.cfg.ApiKeyEnv
		if env == "" {
			env = DefaultAPIKeyEnv
		}
		apiKey = strings.TrimSpace(os.Getenv(env))
	}
	return contracts.ChatConfig{
		Model:         model,
		BaseURL:       baseURL,
		APIKey:        apiKey,
		SystemPrompt:  systemPrompt,
		MaxIterations: s.cfg.MaxIterations,
		ContextBudget: s.cfg.ContextBudget,
	}
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
func (s *service) DeleteSession(sessionID string) error {
	s.mu.Lock()
	state, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("agent: unknown chat session %q", sessionID)
	}
	cancel := state.cancel
	delete(s.sessions, sessionID)
	active := s.active
	if active == sessionID {
		s.active = s.newestSessionLocked()
		active = s.active
	}
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
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
	return state.snapshot(), true
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

// ErrSessionRunning is returned by Send when the session already runs.
var ErrSessionRunning = errors.New("agent: session is already running")

// Send appends the user prompt, creates the streaming assistant placeholder and
// starts one asynchronous Rig agent run.
func (s *service) Send(sessionID, prompt string) error {
	return s.send(sessionID, prompt, nil)
}

// SendWithAttachments appends a user message carrying the prompt and file/image
// attachments, then starts one asynchronous Rig agent run (contracts
// AttachmentSender). Image attachments travel to the provider as rig image
// content parts (data URLs); other files become bounded text parts. A blank
// prompt is allowed when at least one attachment is present.
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
	cfg := s.configLocked()
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
	s.notify(sessionID)
	go s.run(runCtx, sessionID, cfg, buildUserInput(prompt, attachments))
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
func (s *service) run(ctx context.Context, sessionID string, cfg contracts.ChatConfig, input []rig.Message) {
	provider, err := s.newProvider(cfg)
	if err != nil {
		s.finish(sessionID, ctx, fmt.Errorf("create provider: %w", err))
		return
	}
	spec := rig.AgentSpec{
		Provider:        provider,
		Model:           cfg.Model,
		SystemPrompt:    cfg.SystemPrompt,
		MaxIterations:   cfg.MaxIterations,
		Tools:           s.registry.Tools(),
		History:         s.historyView(),
		Gate:            s.gate(),
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
	_, runErr := runner.Run(ctx, rig.RunRequest{
		SessionID: sessionID,
		Input:     input,
		Emit:      func(event rig.Event) { s.handleEvent(sessionID, event) },
	})
	s.finish(sessionID, ctx, runErr)
}

// newProvider constructs the run provider, honouring the test seam.
func (s *service) newProvider(cfg contracts.ChatConfig) (rig.Provider, error) {
	if s.providerFactory != nil {
		return s.providerFactory(cfg)
	}
	env := s.cfg.ApiKeyEnv
	if env == "" {
		env = DefaultAPIKeyEnv
	}
	return openai.New(openai.Config{
		BaseURL:      cfg.BaseURL,
		APIKey:       cfg.APIKey,
		APIKeyEnv:    env,
		DefaultModel: cfg.Model,
	})
}

// gate builds the approval gate: the approvals service when wired (its mode
// decides whether it asks, auto-allows or denies), a permissive gate otherwise.
func (s *service) gate() rig.Gate {
	if s.approvals == nil {
		return gate.AllowAll()
	}
	return gate.Func(func(ctx context.Context, tool rig.Tool, call rig.ToolCall) (rig.Decision, error) {
		name := call.Name
		if tool != nil && strings.TrimSpace(tool.Name()) != "" {
			name = tool.Name()
		}
		summary := summarizeToolArguments(call.Arguments)
		decision, err := s.approvals.Request(ctx, contracts.ApprovalRequest{
			Tool:      name,
			Summary:   summary,
			Arguments: call.Arguments,
			CreatedAt: time.Now(),
		})
		if err != nil {
			return "", err
		}
		switch decision {
		case contracts.ApprovalAllow:
			return rig.DecisionAllow, nil
		case contracts.ApprovalAllowAlways:
			return rig.DecisionAllowAlways, nil
		default:
			return rig.DecisionDeny, nil
		}
	})
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
// notifies subscribers. The callback runs on the run goroutine.
func (s *service) handleEvent(sessionID string, event rig.Event) {
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
				message.Tools = append(message.Tools, contracts.ToolCall{
					ID:     event.Call.ID,
					Name:   event.Call.Name,
					Status: "running",
				})
				s.appendEvent(context.Background(), sessionID, "tool_call", map[string]any{
					"id": event.Call.ID, "name": event.Call.Name, "arguments": event.Call.Arguments,
				})
			}
		case rig.EventToolResult:
			if event.Call != nil && event.Result != nil {
				for index := range message.Tools {
					if message.Tools[index].ID != event.Call.ID {
						continue
					}
					status := "ok"
					if event.Result.IsError {
						status = "error"
					}
					message.Tools[index].Status = status
					message.Tools[index].Summary = summarizeToolResult(event.Result.Content)
					s.appendEvent(context.Background(), sessionID, "tool_result", map[string]any{
						"id": event.Call.ID, "name": event.Call.Name,
						"status": status, "content": summarizeToolResult(event.Result.Content),
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
		if errText != "" && message.Error == "" {
			message.Error = errText
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
	s.notify(sessionID)
	if runErr != nil && !errors.Is(runErr, context.Canceled) && !errors.Is(runErr, context.DeadlineExceeded) {
		s.logger.Printf("[agent] session %s run failed: %v", sessionID, runErr)
	}
}

// summarizeToolArguments bounds the raw tool-call arguments shown to the user.
func summarizeToolArguments(arguments string) string {
	summary := strings.Join(strings.Fields(arguments), " ")
	runes := []rune(summary)
	if len(runes) <= maxToolSummaryRunes {
		return summary
	}
	return string(runes[:maxToolSummaryRunes]) + "…"
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
func (state *sessionState) snapshot() contracts.ChatSession {
	out := contracts.ChatSession{
		ID:        state.id,
		Title:     state.title,
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

// summarizeToolResult collapses a tool result to one bounded line.
func summarizeToolResult(content string) string {
	summary := strings.Join(strings.Fields(content), " ")
	runes := []rune(summary)
	if len(runes) <= maxToolSummaryRunes {
		return summary
	}
	return string(runes[:maxToolSummaryRunes]) + "…"
}
