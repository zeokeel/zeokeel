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
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

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
		_, err = ctx.Provide(contracts.KeyChat, contracts.ChatService(newService(cfg, registry, settings, logger)))
		return err
	}).
		AddProvides(module.Provides(contracts.KeyChat)).
		AddRequires(module.Requires(contracts.KeyToolkit)).
		AddRequires(module.Requires(contracts.KeySettings)).
		AddRequires(module.Requires(contracts.KeyLogger)).
		WithName("ZeoKeel agent backend")
}

// service is the contracts.ChatService implementation.
type service struct {
	mu sync.Mutex

	cfg      Config
	registry *rigtools.Registry
	settings contracts.SettingsService
	logger   contracts.Logger
	history  rig.History

	sessions map[string]*sessionState
	active   string

	subscribers    []*subscription
	nextSessionSeq uint64
	nextMessageSeq uint64

	// providerFactory builds the provider for one run. It is a field so tests
	// can script providers; nil uses openai.New with the effective config.
	providerFactory func(contracts.ChatConfig) (rig.Provider, error)
}

var _ contracts.ChatService = (*service)(nil)

// sessionState is the mutable per-session state.
type sessionState struct {
	id        string
	title     string
	createdAt time.Time
	updatedAt time.Time
	messages  []contracts.ChatMessage
	running   bool
	err       string
	cancel    context.CancelFunc
	// assistant is the index of the streaming assistant placeholder, or -1.
	assistant int
}

// subscription is one Subscribe callback.
type subscription struct {
	fn func(string)
}

// newService builds a service; it is called by the module factory and tests.
func newService(cfg Config, registry *rigtools.Registry, settings contracts.SettingsService, logger contracts.Logger) *service {
	cfg = cfg.normalized()
	if logger == nil {
		logger = discardLogger{}
	}
	return &service{
		cfg:      cfg,
		registry: registry,
		settings: settings,
		logger:   logger,
		history:  session.NewMemory(),
		sessions: make(map[string]*sessionState),
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

// --- ChatService: sessions -------------------------------------------------

// NewSession creates an empty session, marks it active and returns its ID.
func (s *service) NewSession(title string) (string, error) {
	now := time.Now()
	s.mu.Lock()
	id := s.newSessionIDLocked()
	state := &sessionState{
		id:        id,
		title:     strings.TrimSpace(title),
		createdAt: now,
		updatedAt: now,
		assistant: -1,
	}
	s.sessions[id] = state
	s.active = id
	s.mu.Unlock()
	s.notify(id)
	return id, nil
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
			Title:        state.title,
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
// starts one asynchronous Rig agent run. The run uses the effective config and
// the shared history; a per-session context makes Cancel possible.
func (s *service) Send(sessionID, prompt string) error {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
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
	if strings.TrimSpace(state.title) == "" {
		state.title = truncateTitle(prompt)
	}
	state.messages = append(state.messages,
		contracts.ChatMessage{
			ID:        s.nextMessageIDLocked(),
			Role:      contracts.ChatRoleUser,
			Text:      prompt,
			CreatedAt: now,
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
	runCtx, cancel := context.WithCancel(context.Background())
	state.cancel = cancel
	cfg := s.configLocked()
	s.mu.Unlock()

	s.notify(sessionID)
	go s.run(runCtx, sessionID, cfg, prompt)
	return nil
}

// nextMessageIDLocked generates a message ID.
func (s *service) nextMessageIDLocked() string {
	s.nextMessageSeq++
	return fmt.Sprintf("msg-%d", s.nextMessageSeq)
}

// run executes one Rig agent run in the background.
func (s *service) run(ctx context.Context, sessionID string, cfg contracts.ChatConfig, prompt string) {
	provider, err := s.newProvider(cfg)
	if err != nil {
		s.finish(sessionID, ctx, fmt.Errorf("create provider: %w", err))
		return
	}
	runner, err := rigagent.New(rig.AgentSpec{
		Provider:      provider,
		Model:         cfg.Model,
		SystemPrompt:  cfg.SystemPrompt,
		MaxIterations: cfg.MaxIterations,
		Tools:         s.registry.Tools(),
		History:       s.history,
		Gate:          gate.AllowAll(),
		ContextBudget: cfg.ContextBudget,
		Logger:        s.logger,
	})
	if err != nil {
		s.finish(sessionID, ctx, fmt.Errorf("create agent: %w", err))
		return
	}
	_, runErr := runner.Run(ctx, rig.RunRequest{
		SessionID: sessionID,
		Input:     []rig.Message{rig.NewMessage(rig.RoleUser, prompt)},
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

	s.notify(sessionID)
	if runErr != nil && !errors.Is(runErr, context.Canceled) && !errors.Is(runErr, context.DeadlineExceeded) {
		s.logger.Printf("[agent] session %s run failed: %v", sessionID, runErr)
	}
}

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
