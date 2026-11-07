package agent

// Tests for the agent backend module: a fake OpenAI-compatible SSE server drives
// the real Rig provider/agent loop, while the module's ChatService read model is
// asserted from the test goroutine. No network or display is involved.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeSettings is an in-memory contracts.SettingsService.
type fakeSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func newFakeSettings(values map[string]string) *fakeSettings {
	if values == nil {
		values = map[string]string{}
	}
	return &fakeSettings{values: values}
}

func (s *fakeSettings) Get(key, fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}

func (s *fakeSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *fakeSettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out
}

// fakeLogger records log lines.
type fakeLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *fakeLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *fakeLogger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.lines)
}

// provide registers one host service on ctx.
func provide[T any](t *testing.T, ctx cordis.Context, key cordis.ServiceKey[T], value T) {
	t.Helper()
	if _, err := ctx.Provide(key, value); err != nil {
		t.Fatalf("provide %s: %v", key.Name(), err)
	}
}

// newTestService activates the module against fake host services and returns the
// concrete ChatService implementation.
func newTestService(t *testing.T, cfg Config, settings *fakeSettings, logger *fakeLogger, tools ...rig.Tool) *service {
	t.Helper()
	if settings == nil {
		settings = newFakeSettings(nil)
	}
	if logger == nil {
		logger = &fakeLogger{}
	}
	registry := rigtools.NewRegistry(tools...)
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })

	provide(t, ctx, contracts.KeyToolkit, registry)
	provide(t, ctx, contracts.KeySettings, contracts.SettingsService(settings))
	provide(t, ctx, contracts.KeyLogger, contracts.Logger(logger))
	provide(t, ctx, contracts.KeyStore, contracts.Store(newFakeStore()))
	provide(t, ctx, contracts.KeyApprovals, contracts.Approvals(fakeApprovals{}))
	if err := Module().Factory(ctx, cfg); err != nil {
		t.Fatalf("activate agent module: %v", err)
	}
	chat, err := ctx.Require(contracts.KeyChat)
	if err != nil {
		t.Fatalf("require KeyChat: %v", err)
	}
	svc, ok := chat.(*service)
	if !ok {
		t.Fatalf("KeyChat is %T, want *service", chat)
	}
	return svc
}

// writeSSE writes OpenAI-style events followed by [DONE].
func writeSSE(t *testing.T, w http.ResponseWriter, events ...string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for _, event := range events {
		if _, err := fmt.Fprintf(w, "data: %s\n\n", event); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// newSSEServer starts a fake OpenAI endpoint at /chat/completions.
func newSSEServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
			http.Error(w, "want POST", http.StatusMethodNotAllowed)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

// waitFor polls condition until it holds or the timeout expires.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// waitIdle waits until the session has no running run.
func waitIdle(t *testing.T, svc *service, sessionID string) contracts.ChatSession {
	t.Helper()
	waitFor(t, "session to become idle", func() bool {
		session, ok := svc.Session(sessionID)
		return ok && !session.Running && len(session.Messages) > 0 &&
			!session.Messages[len(session.Messages)-1].Streaming
	})
	session, _ := svc.Session(sessionID)
	return session
}

// --- tests -----------------------------------------------------------------

// TestSendStreamsAssistantReply covers the happy path: SSE text deltas become
// the assistant message, the session title comes from the first prompt and
// subscribers are notified.
func TestSendStreamsAssistantReply(t *testing.T) {
	server := newSSEServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w,
			`{"choices":[{"delta":{"role":"assistant"}}]}`,
			`{"choices":[{"delta":{"content":"Hello"}}]}`,
			`{"choices":[{"delta":{"content":" world"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		)
	})
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
		contracts.SettingsChatAPIKey:  "test-key",
	})
	svc := newTestService(t, Config{}, settings, nil)

	var notifications atomic.Int32
	unsub, err := svc.Subscribe(func(string) { notifications.Add(1) })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsub()

	id, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if !strings.HasPrefix(id, "sess-") || len(id) <= len("sess-") {
		t.Fatalf("session ID = %q, want sess-<unixnano>", id)
	}
	if svc.ActiveSession() != id {
		t.Fatalf("active session = %q, want %q", svc.ActiveSession(), id)
	}

	if err := svc.Send(id, "Explain Go modules"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, id)
	if len(session.Messages) != 2 {
		t.Fatalf("messages = %d, want user+assistant", len(session.Messages))
	}
	if session.Messages[0].Role != contracts.ChatRoleUser || session.Messages[0].Text != "Explain Go modules" {
		t.Fatalf("user message = %+v", session.Messages[0])
	}
	assistant := session.Messages[1]
	if assistant.Role != contracts.ChatRoleAssistant || assistant.Text != "Hello world" ||
		assistant.Streaming || assistant.Error != "" {
		t.Fatalf("assistant message = %+v", assistant)
	}
	if session.Title != "Explain Go modules" {
		t.Fatalf("title = %q, want the first prompt", session.Title)
	}
	if session.Error != "" || session.Running {
		t.Fatalf("session state = %+v", session)
	}
	if session.CreatedAt.IsZero() || session.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not set: %+v", session)
	}
	if notifications.Load() == 0 {
		t.Fatal("no subscriber notifications")
	}

	cfg := svc.Config()
	if cfg.Model != "test-model" || cfg.BaseURL != server.URL || cfg.APIKey != "test-key" {
		t.Fatalf("Config = %+v, want settings values", cfg)
	}
}

// TestSendValidationAndConflict covers the model-facing validation errors.
func TestSendValidationAndConflict(t *testing.T) {
	block := make(chan struct{})
	server := newSSEServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-block:
		}
	})
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	})
	svc := newTestService(t, Config{}, settings, nil)
	closeBlock := func() { close(block) }
	t.Cleanup(closeBlock)

	id, err := svc.NewSession("chat")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "   "); !errors.Is(err, ErrEmptyPrompt) {
		t.Fatalf("Send(blank) = %v, want ErrEmptyPrompt", err)
	}
	if err := svc.Send("missing", "hi"); err == nil {
		t.Fatal("Send(unknown session) returned nil error")
	}
	if _, err := svc.NewSession("bad"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if err := svc.Send(id, "first"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, "run to start", func() bool {
		session, _ := svc.Session(id)
		return session.Running
	})
	if err := svc.Send(id, "second"); !errors.Is(err, ErrSessionRunning) {
		t.Fatalf("Send while running = %v, want ErrSessionRunning", err)
	}
	if err := svc.Cancel(id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := svc.Cancel(id); err != nil {
		t.Fatalf("second Cancel: %v", err)
	}
	waitIdle(t, svc, id)
}

// TestCancelKeepsPartialContent covers cancellation: the streamed text stays and
// the error is recorded as cancelled.
func TestCancelKeepsPartialContent(t *testing.T) {
	server := newSSEServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial answer\"}}]}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done()
	})
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	})
	svc := newTestService(t, Config{}, settings, nil)
	id, err := svc.NewSession("cancel me")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "write a lot"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, "partial content", func() bool {
		session, ok := svc.Session(id)
		return ok && len(session.Messages) == 2 && strings.Contains(session.Messages[1].Text, "partial")
	})
	if err := svc.Cancel(id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	session := waitIdle(t, svc, id)
	if session.Error != "cancelled" {
		t.Fatalf("session error = %q, want cancelled", session.Error)
	}
	assistant := session.Messages[1]
	if !strings.Contains(assistant.Text, "partial answer") || assistant.Streaming {
		t.Fatalf("assistant after cancel = %+v, want streamed content kept", assistant)
	}
	if assistant.Error != "cancelled" {
		t.Fatalf("assistant error = %q, want cancelled", assistant.Error)
	}
}

// TestEventMappingToolsReasoningAndErrors covers the event mapping without a
// network provider: a scripted provider emits reasoning, one tool call and a
// final answer; the read model must reflect all of it.
func TestEventMappingToolsReasoningAndErrors(t *testing.T) {
	echo := &echoTool{}
	provider := &scriptedProvider{}
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "scripted"})
	svc := newTestService(t, Config{}, settings, nil, echo)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	id, err := svc.NewSession("tools")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "use the tool"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, id)
	assistant := session.Messages[1]
	if assistant.Reasoning != "thinking" {
		t.Fatalf("reasoning = %q, want thinking", assistant.Reasoning)
	}
	if !strings.Contains(assistant.Text, "done") {
		t.Fatalf("assistant text = %q, want the final answer", assistant.Text)
	}
	if len(assistant.Tools) != 1 {
		t.Fatalf("tool calls = %+v, want one", assistant.Tools)
	}
	call := assistant.Tools[0]
	if call.ID != "call-1" || call.Name != "echo" || call.Status != "ok" {
		t.Fatalf("tool call = %+v, want call-1/echo/ok", call)
	}
	if !strings.Contains(call.Summary, "echoed") {
		t.Fatalf("tool summary = %q, want the tool result", call.Summary)
	}
	if echo.calls.Load() != 1 || provider.calls.Load() != 2 {
		t.Fatalf("tool calls = %d, provider streams = %d", echo.calls.Load(), provider.calls.Load())
	}
}

// TestProviderError covers a failing provider: the run completes with an error
// instead of hanging.
func TestProviderError(t *testing.T) {
	server := newSSEServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"invalid api key","type":"auth_error"}}`)
	})
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	})
	svc := newTestService(t, Config{}, settings, nil)
	id, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "hi"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, id)
	if session.Error == "" || !strings.Contains(session.Error, "invalid api key") {
		t.Fatalf("session error = %q, want the provider failure", session.Error)
	}
	if session.Messages[1].Error == "" {
		t.Fatal("assistant message error not set")
	}
}

// TestSessionsOrderingAndDelete covers session lifecycle, sorting and active
// selection.
func TestSessionsOrderingAndDelete(t *testing.T) {
	server := newSSEServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w,
			`{"choices":[{"delta":{"content":"ok"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		)
	})
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	})
	svc := newTestService(t, Config{}, settings, nil)

	first, err := svc.NewSession("first")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	second, err := svc.NewSession("second")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if svc.ActiveSession() != second {
		t.Fatalf("active = %q, want the newest session", svc.ActiveSession())
	}
	if infos := svc.Sessions(); len(infos) != 2 {
		t.Fatalf("Sessions = %+v, want two", infos)
	}
	if err := svc.SetActiveSession(first); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	if err := svc.SetActiveSession("missing"); err == nil {
		t.Fatal("SetActiveSession(unknown) returned nil error")
	}

	if err := svc.Send(first, "update first"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, first)
	infos := svc.Sessions()
	if infos[0].ID != first {
		t.Fatalf("Sessions order = %v, want the updated session first", infos)
	}
	if infos[0].MessageCount != 2 || !infos[0].Running {
		// MessageCount is 2; Running must be false after completion. Checking
		// message count only here keeps the assertion about updated ordering.
		if infos[0].MessageCount != 2 {
			t.Fatalf("message count = %d, want 2", infos[0].MessageCount)
		}
	}
	if infos[0].Running {
		t.Fatalf("finished session still marked running: %+v", infos[0])
	}

	if err := svc.DeleteSession(first); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, ok := svc.Session(first); ok {
		t.Fatal("deleted session still exists")
	}
	if svc.ActiveSession() != second {
		t.Fatalf("active after delete = %q, want %q", svc.ActiveSession(), second)
	}
	if err := svc.DeleteSession(second); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if svc.ActiveSession() != "" {
		t.Fatalf("active after deleting all = %q, want empty", svc.ActiveSession())
	}
	if svc.DeleteSession("missing") == nil {
		t.Fatal("DeleteSession(unknown) returned nil error")
	}
}

// TestAutoTitleTruncation covers the first-prompt title and the ~32 rune cap.
func TestAutoTitleTruncation(t *testing.T) {
	server := newSSEServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w,
			`{"choices":[{"delta":{"content":"ok"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		)
	})
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	})
	svc := newTestService(t, Config{}, settings, nil)
	id, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	prompt := "one two three four five six seven eight nine ten eleven twelve"
	if err := svc.Send(id, prompt); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	session, _ := svc.Session(id)
	title := session.Title
	if runes := []rune(strings.TrimSuffix(title, "…")); len(runes) > maxTitleRunes {
		t.Fatalf("title %q exceeds %d runes", title, maxTitleRunes)
	}
	if !strings.HasPrefix(title, "one two three") {
		t.Fatalf("title = %q, want the prompt prefix", title)
	}

	named, err := svc.NewSession("Given title")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(named, "other prompt"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, named)
	if session, _ := svc.Session(named); session.Title != "Given title" {
		t.Fatalf("title = %q, want the given title kept", session.Title)
	}
}

// TestSubscribeLifecycle covers subscribe/unsubscribe and panic containment.
func TestSubscribeLifecycle(t *testing.T) {
	svc := newTestService(t, Config{}, nil, nil)

	var first, second atomic.Int32
	unsubFirst, err := svc.Subscribe(func(string) { first.Add(1) })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if _, err := svc.Subscribe(func(string) { second.Add(1) }); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if _, err := svc.Subscribe(nil); err != nil {
		t.Fatalf("Subscribe(nil): %v", err)
	}
	// A panicking subscriber must not break the service.
	if _, err := svc.Subscribe(func(string) { panic("boom") }); err != nil {
		t.Fatalf("Subscribe(panic): %v", err)
	}

	if _, err := svc.NewSession("one"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if first.Load() == 0 || second.Load() == 0 {
		t.Fatalf("subscribers not notified: first=%d second=%d", first.Load(), second.Load())
	}
	firstCount, secondCount := first.Load(), second.Load()
	unsubFirst()
	unsubFirst() // unsub is idempotent
	if _, err := svc.NewSession("two"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if first.Load() != firstCount {
		t.Fatalf("unsubscribed callback still fired: %d -> %d", firstCount, first.Load())
	}
	if second.Load() <= secondCount {
		t.Fatalf("remaining callback did not fire: %d", second.Load())
	}
}

// TestConfigPrecedence covers module config vs settings vs environment.
func TestConfigPrecedence(t *testing.T) {
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:        "settings-model",
		contracts.SettingsChatBaseURL:      "https://settings.example/v1",
		contracts.SettingsChatSystemPrompt: "settings prompt",
		contracts.SettingsChatAPIKey:       "settings-key",
	})
	svc := newTestService(t, Config{
		Model:         "module-model",
		BaseURL:       "https://module.example/v1",
		SystemPrompt:  "module prompt",
		MaxIterations: 7,
		ContextBudget: 100,
	}, settings, nil)
	cfg := svc.Config()
	if cfg.Model != "settings-model" || cfg.BaseURL != "https://settings.example/v1" ||
		cfg.SystemPrompt != "settings prompt" || cfg.APIKey != "settings-key" {
		t.Fatalf("Config = %+v, want the settings values", cfg)
	}
	if cfg.MaxIterations != 7 || cfg.ContextBudget != 100 {
		t.Fatalf("limits = %d/%d, want 7/100", cfg.MaxIterations, cfg.ContextBudget)
	}

	// Settings present but the API key missing: fall back to the env var.
	envSettings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel: "env-model",
	})
	t.Setenv("RIG_AGENT_TEST_KEY", "env-key")
	envSvc := newTestService(t, Config{
		BaseURL:   "https://module.example/v1",
		ApiKeyEnv: "RIG_AGENT_TEST_KEY",
	}, envSettings, nil)
	envCfg := envSvc.Config()
	if envCfg.Model != "env-model" || envCfg.BaseURL != "https://module.example/v1" {
		t.Fatalf("Config = %+v, want settings model and module base URL", envCfg)
	}
	if envCfg.APIKey != "env-key" {
		t.Fatalf("API key = %q, want the env fallback", envCfg.APIKey)
	}

	// Nothing configured: the default model applies.
	t.Setenv("OPENAI_API_KEY", "")
	empty := newTestService(t, Config{}, nil, nil)
	if got := empty.Config().Model; got != DefaultModel {
		t.Fatalf("default model = %q, want %q", got, DefaultModel)
	}
}

// TestConcurrentSessions covers concurrent runs on different sessions.
func TestConcurrentSessions(t *testing.T) {
	var requests atomic.Int32
	server := newSSEServer(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writeSSE(t, w,
			`{"choices":[{"delta":{"content":"answer"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		)
	})
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	})
	svc := newTestService(t, Config{}, settings, nil)
	const sessions = 5
	ids := make([]string, 0, sessions)
	for index := 0; index < sessions; index++ {
		id, err := svc.NewSession(fmt.Sprintf("session-%d", index))
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		ids = append(ids, id)
	}
	var waitGroup sync.WaitGroup
	for _, id := range ids {
		waitGroup.Add(1)
		go func(sessionID string) {
			defer waitGroup.Done()
			if err := svc.Send(sessionID, "hello"); err != nil {
				t.Errorf("Send(%s): %v", sessionID, err)
			}
		}(id)
	}
	waitGroup.Wait()
	for _, id := range ids {
		session := waitIdle(t, svc, id)
		if session.Messages[1].Text != "answer" {
			t.Fatalf("session %s text = %q", id, session.Messages[1].Text)
		}
	}
	if requests.Load() != sessions {
		t.Fatalf("requests = %d, want %d", requests.Load(), sessions)
	}
}

// TestModuleManifest pins the descriptor contract.
func TestModuleManifest(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID || ModuleID != "zeokeel/agent" {
		t.Fatalf("module ID = %q, want zeokeel/agent", descriptor.ID)
	}
	if descriptor.Factory == nil {
		t.Fatal("module has a nil factory")
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyChat.Name() {
		t.Fatalf("provides = %+v, want KeyChat", descriptor.Provides)
	}
	required := make(map[string]bool, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		if requirement.Optional {
			t.Fatalf("requirement %q is optional, want mandatory", requirement.Name)
		}
		required[requirement.Name] = true
	}
	for _, name := range []string{
		contracts.KeyToolkit.Name(),
		contracts.KeySettings.Name(),
		contracts.KeyLogger.Name(),
	} {
		if !required[name] {
			t.Fatalf("module does not require %q; requires = %v", name, descriptor.Requires)
		}
	}
}

// TestConfigJSONTags pins the assembly config keys.
func TestConfigJSONTags(t *testing.T) {
	raw, err := json.Marshal(Config{
		Model:         "m",
		BaseURL:       "u",
		ApiKeyEnv:     "e",
		SystemPrompt:  "s",
		MaxIterations: 3,
		ContextBudget: 100,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{`"model"`, `"baseURL"`, `"apiKeyEnv"`, `"systemPrompt"`, `"maxIterations"`, `"contextBudget"`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("config JSON %s lacks %s", raw, key)
		}
	}
}

// --- scripted provider and tool -------------------------------------------

// echoTool returns its argument text.
type echoTool struct{ calls atomic.Int32 }

func (e *echoTool) Name() string           { return "echo" }
func (e *echoTool) Description() string    { return "Echo the arguments." }
func (e *echoTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (e *echoTool) Invoke(_ context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	e.calls.Add(1)
	return rig.ToolResult{Content: "echoed: " + call.Arguments}, nil
}

// scriptedProvider emits a reasoning delta, one tool call and then a final
// answer on its second invocation.
type scriptedProvider struct{ calls atomic.Int32 }

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) Stream(context.Context, rig.Request) (rig.Stream, error) {
	call := p.calls.Add(1)
	if call == 1 {
		return &scriptedStream{chunks: []rig.Chunk{
			{Reasoning: "thinking"},
			{ToolCalls: []rig.ToolCallDelta{{Index: 0, ID: "call-1", Name: "echo", Arguments: `{"text":"hi"}`}}},
			{FinishReason: "tool_calls"},
		}}, nil
	}
	return &scriptedStream{chunks: []rig.Chunk{
		{Delta: "done"},
		{FinishReason: "stop"},
	}}, nil
}

// scriptedStream replays fixed chunks and then io.EOF.
type scriptedStream struct {
	chunks []rig.Chunk
	index  int
}

func (s *scriptedStream) Recv() (rig.Chunk, error) {
	if s.index >= len(s.chunks) {
		return rig.Chunk{}, io.EOF
	}
	chunk := s.chunks[s.index]
	s.index++
	return chunk, nil
}

// --- in-memory test doubles for the P1 durable store and approvals ---------

// fakeStore is an in-memory contracts.Store used by the module unit tests; the
// real SQLite implementation is tested in internal/modules/sessionstore.
type fakeStore struct {
	mu          sync.Mutex
	sessions    map[string]*fakeSession
	checkpoints map[string]*rig.Checkpoint
}

type fakeSession struct {
	title    string
	created  time.Time
	updated  time.Time
	messages []rig.Message
	events   []rig.SessionEvent
}

func newFakeStore() *fakeStore {
	return &fakeStore{sessions: map[string]*fakeSession{}, checkpoints: map[string]*rig.Checkpoint{}}
}

func (s *fakeStore) sessionLocked(id string) *fakeSession {
	state, ok := s.sessions[id]
	if !ok {
		now := time.Now()
		state = &fakeSession{created: now, updated: now}
		s.sessions[id] = state
	}
	return state
}

func (s *fakeStore) CreateSession(ctx context.Context, id, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.sessionLocked(id)
	if state.title == "" {
		state.title = title
	}
	return nil
}

func (s *fakeStore) DeleteSession(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	delete(s.checkpoints, id)
	return nil
}

func (s *fakeStore) RenameSession(ctx context.Context, id, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionLocked(id).title = title
	return nil
}

func (s *fakeStore) Sessions(ctx context.Context) ([]contracts.SessionMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]contracts.SessionMeta, 0, len(s.sessions))
	for id, state := range s.sessions {
		out = append(out, contracts.SessionMeta{
			ID: id, Title: state.title, MessageCount: len(state.messages),
			CreatedAt: state.created, UpdatedAt: state.updated,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func (s *fakeStore) GetSessionMeta(ctx context.Context, id string) (contracts.SessionMeta, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[id]
	if !ok {
		return contracts.SessionMeta{}, false, nil
	}
	return contracts.SessionMeta{ID: id, Title: state.title, MessageCount: len(state.messages),
		CreatedAt: state.created, UpdatedAt: state.updated}, true, nil
}

func (s *fakeStore) AppendEvent(ctx context.Context, event rig.SessionEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.sessionLocked(event.SessionID)
	event.Seq = int64(len(state.events) + 1)
	state.events = append(state.events, event)
	state.updated = time.Now()
	return nil
}

func (s *fakeStore) Events(ctx context.Context, sessionID string, from, to int64) ([]rig.SessionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.sessionLocked(sessionID)
	var out []rig.SessionEvent
	for _, event := range state.events {
		if event.Seq >= from && (to <= 0 || event.Seq <= to) {
			out = append(out, event)
		}
	}
	return out, nil
}

func (s *fakeStore) Search(ctx context.Context, query string, limit int) ([]rig.SessionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []rig.SessionEvent
	for _, state := range s.sessions {
		for _, event := range state.events {
			if query == "" || strings.Contains(event.Type+" "+string(event.Payload), query) {
				out = append(out, event)
				if limit > 0 && len(out) >= limit {
					return out, nil
				}
			}
		}
	}
	return out, nil
}

func (s *fakeStore) Last(ctx context.Context, sessionID string, limit int) ([]rig.SessionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.sessionLocked(sessionID)
	events := state.events
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	return append([]rig.SessionEvent(nil), events...), nil
}

func (s *fakeStore) History() rig.History                 { return fakeHistory{store: s} }
func (s *fakeStore) CheckpointStore() rig.CheckpointStore { return fakeCheckpoints{store: s} }

func (s *fakeStore) ReplaceHistory(ctx context.Context, sessionID string, messages []rig.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.sessionLocked(sessionID)
	state.messages = append([]rig.Message(nil), messages...)
	state.updated = time.Now()
	return nil
}

type fakeHistory struct{ store *fakeStore }

func (h fakeHistory) Load(ctx context.Context, sessionID string) ([]rig.Message, error) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	state := h.store.sessionLocked(sessionID)
	return append([]rig.Message(nil), state.messages...), nil
}

func (h fakeHistory) Append(ctx context.Context, sessionID string, messages ...rig.Message) error {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	state := h.store.sessionLocked(sessionID)
	state.messages = append(state.messages, messages...)
	state.updated = time.Now()
	return nil
}

type fakeCheckpoints struct{ store *fakeStore }

func (c fakeCheckpoints) Load(ctx context.Context, sessionID string) (*rig.Checkpoint, error) {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	return c.store.checkpoints[sessionID], nil
}

func (c fakeCheckpoints) Save(ctx context.Context, sessionID string, checkpoint *rig.Checkpoint) error {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	c.store.checkpoints[sessionID] = checkpoint
	return nil
}

func (c fakeCheckpoints) Clear(ctx context.Context, sessionID string) error {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	delete(c.store.checkpoints, sessionID)
	return nil
}

// fakeApprovals auto-allows every tool call by default.
type fakeApprovals struct{ mode string }

func (a fakeApprovals) Mode() string {
	if a.mode == "" {
		return "auto"
	}
	return a.mode
}
func (a fakeApprovals) Request(ctx context.Context, request contracts.ApprovalRequest) (contracts.ApprovalDecision, error) {
	return contracts.ApprovalAllow, nil
}
func (a fakeApprovals) Pending() []contracts.ApprovalRequest { return nil }
func (a fakeApprovals) Resolve(id string, decision contracts.ApprovalDecision) error {
	return nil
}
func (a fakeApprovals) Subscribe(fn func()) (func(), error) { return func() {}, nil }
