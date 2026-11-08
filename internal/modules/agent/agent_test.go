package agent

// Tests for the agent backend module: a fake OpenAI-compatible SSE server drives
// the real Rig provider/agent loop, while the module's ChatService read model is
// asserted from the test goroutine. No network or display is involved.

import (
	"bytes"
	"context"
	"encoding/base64"
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

func (l *fakeLogger) contains(fragment string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, fragment) {
			return true
		}
	}
	return false
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
	provides := make(map[string]bool, len(descriptor.Provides))
	for _, provider := range descriptor.Provides {
		provides[provider.Name] = true
	}
	for _, name := range []string{contracts.KeyChat.Name(), contracts.KeySubagents.Name(), contracts.KeyTeam.Name()} {
		if !provides[name] {
			t.Fatalf("provides = %+v, want %s", descriptor.Provides, name)
		}
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

	workspace    map[string]string
	workspaceLog []workspaceCall
	workspaceErr error
	replaceErr   error
}

// workspaceCall records one SetSessionWorkspace invocation.
type workspaceCall struct {
	sessionID   string
	workspaceID string
}

type fakeSession struct {
	title       string
	workspaceID string
	created     time.Time
	updated     time.Time
	messages    []rig.Message
	events      []rig.SessionEvent
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		sessions:    map[string]*fakeSession{},
		checkpoints: map[string]*rig.Checkpoint{},
		workspace:   map[string]string{},
	}
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

// SetSessionWorkspace implements contracts.SessionWorkspaceStore.
func (s *fakeStore) SetSessionWorkspace(ctx context.Context, sessionID, workspaceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workspaceLog = append(s.workspaceLog, workspaceCall{sessionID: sessionID, workspaceID: workspaceID})
	if s.workspaceErr != nil {
		return s.workspaceErr
	}
	state, ok := s.sessions[sessionID]
	if !ok {
		return errors.New("fake store: unknown session")
	}
	state.workspaceID = workspaceID
	s.workspace[sessionID] = workspaceID
	return nil
}

// workspaceOf returns the recorded workspace association ("" when none).
func (s *fakeStore) workspaceOf(sessionID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workspace[sessionID]
}

// workspaceCalls returns a copy of the SetSessionWorkspace log.
func (s *fakeStore) workspaceCalls() []workspaceCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]workspaceCall(nil), s.workspaceLog...)
}

func (s *fakeStore) Sessions(ctx context.Context) ([]contracts.SessionMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]contracts.SessionMeta, 0, len(s.sessions))
	for id, state := range s.sessions {
		out = append(out, contracts.SessionMeta{
			ID: id, WorkspaceID: state.workspaceID, Title: state.title, MessageCount: len(state.messages),
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
	return contracts.SessionMeta{ID: id, WorkspaceID: state.workspaceID, Title: state.title,
		MessageCount: len(state.messages), CreatedAt: state.created, UpdatedAt: state.updated}, true, nil
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
	if s.replaceErr != nil {
		return s.replaceErr
	}
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

// --- P2: attachments and images -------------------------------------------

// captureProvider records every request and answers with one short text delta.
type captureProvider struct {
	mu       sync.Mutex
	requests []rig.Request
}

func (p *captureProvider) Name() string { return "capture" }

func (p *captureProvider) Stream(_ context.Context, request rig.Request) (rig.Stream, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	return &scriptedStream{chunks: []rig.Chunk{
		{Delta: "ok"},
		{FinishReason: "stop"},
	}}, nil
}

func (p *captureProvider) lastRequest() rig.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		return rig.Request{}
	}
	return p.requests[len(p.requests)-1]
}

// newAttachmentService builds a service with a fake durable store and a
// capturing provider, returning both so tests can assert persistence.
func newAttachmentService(t *testing.T, provider *captureProvider) (*service, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	svc := newService(Config{}, rigtools.NewRegistry(),
		contracts.SettingsService(newFakeSettings(nil)), &fakeLogger{}, store, fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	return svc, store
}

// lastUserMessage finds the last user message of a captured provider request.
func lastUserMessage(t *testing.T, request rig.Request) rig.Message {
	t.Helper()
	for index := len(request.Messages) - 1; index >= 0; index-- {
		if request.Messages[index].Role == rig.RoleUser {
			return request.Messages[index]
		}
	}
	t.Fatalf("request has no user message: %+v", request.Messages)
	return rig.Message{}
}

// imageAttachment is a tiny PNG-named attachment.
func imageAttachment() contracts.Attachment {
	return contracts.Attachment{
		ID:   "a-image",
		Name: "cat.png",
		MIME: "image/png",
		Data: []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a},
	}
}

// TestSendWithAttachmentsImage covers an image send: the provider receives an
// image data URL content part, and the read model carries the attachment chips.
func TestSendWithAttachmentsImage(t *testing.T) {
	provider := &captureProvider{}
	svc, _ := newAttachmentService(t, provider)
	sender, ok := contracts.ChatService(svc).(contracts.AttachmentSender)
	if !ok {
		t.Fatal("service does not implement contracts.AttachmentSender")
	}
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	attachment := imageAttachment()
	if err := sender.SendWithAttachments(sessionID, "  描述这张图  ", []contracts.Attachment{attachment}); err != nil {
		t.Fatalf("SendWithAttachments: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	user := session.Messages[0]
	if user.Role != contracts.ChatRoleUser || user.Text != "描述这张图" {
		t.Fatalf("user message = %+v", user)
	}
	if len(user.Attachments) != 1 {
		t.Fatalf("attachments = %+v, want one", user.Attachments)
	}
	chip := user.Attachments[0]
	if chip.Name != attachment.Name || chip.MIME != attachment.MIME || !bytes.Equal(chip.Data, attachment.Data) {
		t.Fatalf("attachment chip = %+v, want the sent attachment", chip)
	}

	// The read model is a copy: mutating the snapshot must not change the
	// session or the caller's buffer.
	session.Messages[0].Attachments[0].Data[0] = 0xff
	attachment.Data[0] = 0xff
	refetched, _ := svc.Session(sessionID)
	if refetched.Messages[0].Attachments[0].Data[0] != 0x89 {
		t.Fatalf("read model aliases attachment bytes: %+v", refetched.Messages[0].Attachments[0])
	}

	userMessage := lastUserMessage(t, provider.lastRequest())
	var sawText, sawImage bool
	for _, part := range userMessage.Content {
		switch part.Type {
		case rig.PartText:
			sawText = sawText || part.Text == "描述这张图"
		case rig.PartImageURL:
			if !strings.HasPrefix(part.ImageURL, "data:image/png;base64,") {
				t.Fatalf("image part = %q, want an image/png data URL", part.ImageURL)
			}
			encoded := strings.TrimPrefix(part.ImageURL, "data:image/png;base64,")
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				t.Fatalf("decode image data URL: %v", err)
			}
			if !bytes.Equal(decoded, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}) {
				t.Fatalf("decoded image bytes = %x", decoded)
			}
			sawImage = true
		}
	}
	if !sawText || !sawImage {
		t.Fatalf("provider content parts = %+v, want text + image", userMessage.Content)
	}
}

// TestSendAttachmentsOnly covers a blank prompt with attachments: it is
// allowed, the title comes from the first attachment and the provider receives
// only the image part.
func TestSendAttachmentsOnly(t *testing.T) {
	provider := &captureProvider{}
	svc, _ := newAttachmentService(t, provider)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SendWithAttachments(sessionID, "   ", []contracts.Attachment{imageAttachment()}); err != nil {
		t.Fatalf("SendWithAttachments(attachment only): %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	user := session.Messages[0]
	if user.Text != "" || len(user.Attachments) != 1 {
		t.Fatalf("attachment-only user message = %+v", user)
	}
	if session.Title != "cat.png" {
		t.Fatalf("session title = %q, want the first attachment name", session.Title)
	}
	userMessage := lastUserMessage(t, provider.lastRequest())
	if len(userMessage.Content) != 1 || userMessage.Content[0].Type != rig.PartImageURL {
		t.Fatalf("attachment-only content = %+v, want exactly one image part", userMessage.Content)
	}
}

// TestSendWithAttachmentsValidation covers the empty/unknown cases and the
// equivalence of an attachment-less send with Send.
func TestSendWithAttachmentsValidation(t *testing.T) {
	provider := &captureProvider{}
	svc, _ := newAttachmentService(t, provider)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SendWithAttachments(sessionID, "   ", nil); !errors.Is(err, ErrEmptyPrompt) {
		t.Fatalf("SendWithAttachments(blank, no attachments) = %v, want ErrEmptyPrompt", err)
	}
	if err := svc.SendWithAttachments("missing", "hi", []contracts.Attachment{imageAttachment()}); err == nil {
		t.Fatal("SendWithAttachments(unknown session) returned nil error")
	}
	if err := svc.SendWithAttachments(sessionID, "plain prompt", nil); err != nil {
		t.Fatalf("SendWithAttachments(no attachments): %v", err)
	}
	waitIdle(t, svc, sessionID)
	userMessage := lastUserMessage(t, provider.lastRequest())
	if len(userMessage.Content) != 1 || userMessage.Content[0].Text != "plain prompt" {
		t.Fatalf("plain send content = %+v, want one text part", userMessage.Content)
	}
}

// TestTextAttachmentParts covers non-image files: a text file carries up to
// 32 KiB of content, other files only the header, and both keep their chips.
func TestTextAttachmentParts(t *testing.T) {
	provider := &captureProvider{}
	svc, _ := newAttachmentService(t, provider)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	textFile := contracts.Attachment{ID: "t1", Name: "notes.txt", MIME: "text/plain", Data: []byte("hello notes")}
	pdf := contracts.Attachment{ID: "t2", Name: "spec.pdf", MIME: "application/pdf", Data: []byte("PDF")}
	if err := svc.SendWithAttachments(sessionID, "看看附件", []contracts.Attachment{textFile, pdf}); err != nil {
		t.Fatalf("SendWithAttachments: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	if got := len(session.Messages[0].Attachments); got != 2 {
		t.Fatalf("attachment chips = %d, want 2", got)
	}

	userMessage := lastUserMessage(t, provider.lastRequest())
	var textParts []string
	for _, part := range userMessage.Content {
		if part.Type == rig.PartText {
			textParts = append(textParts, part.Text)
		}
	}
	joined := strings.Join(textParts, "\n")
	for _, want := range []string{"看看附件", "[附件] notes.txt (11 bytes)", "hello notes", "[附件] spec.pdf (3 bytes)"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("text parts %q do not contain %q", joined, want)
		}
	}
	if strings.Contains(joined, "PDF\n") {
		t.Fatalf("binary file content leaked into the prompt: %q", joined)
	}
}

// TestRestoredAttachmentChips covers the restart read model: image data URLs
// and attachment text parts are turned back into chips without their bytes.
func TestRestoredAttachmentChips(t *testing.T) {
	converted := chatMessagesFromRig([]rig.Message{
		{Role: rig.RoleUser, Content: []rig.ContentPart{
			rig.TextPart("看看"),
			rig.ImagePart("data:image/png;base64,AAAA", ""),
		}},
		{Role: rig.RoleUser, Content: []rig.ContentPart{
			rig.TextPart("[附件] notes.txt (5 bytes)\nhello"),
		}},
	})
	if len(converted) != 2 {
		t.Fatalf("converted messages = %d, want 2", len(converted))
	}
	first := converted[0]
	if first.Text != "看看" || len(first.Attachments) != 1 {
		t.Fatalf("restored image message = %+v", first)
	}
	if first.Attachments[0].Name != "图片" || first.Attachments[0].MIME != "image/png" || len(first.Attachments[0].Data) != 0 {
		t.Fatalf("restored image chip = %+v", first.Attachments[0])
	}
	second := converted[1]
	if second.Text != "" || len(second.Attachments) != 1 || second.Attachments[0].Name != "notes.txt" {
		t.Fatalf("restored text-attachment message = %+v", second)
	}
}

// TestAttachmentsSurviveRestart covers the durable store round trip: after a
// service rebuild + restore, the user message still renders attachment chips.
func TestAttachmentsSurviveRestart(t *testing.T) {
	provider := &captureProvider{}
	first, store := newAttachmentService(t, provider)
	sessionID, err := first.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := first.SendWithAttachments(sessionID, "图里是什么", []contracts.Attachment{imageAttachment()}); err != nil {
		t.Fatalf("SendWithAttachments: %v", err)
	}
	waitIdle(t, first, sessionID)

	second := newService(Config{}, rigtools.NewRegistry(),
		contracts.SettingsService(newFakeSettings(nil)), &fakeLogger{}, store, fakeApprovals{})
	second.restore(context.Background())
	session, ok := second.Session(sessionID)
	if !ok {
		t.Fatal("restored service has no session")
	}
	user := session.Messages[0]
	if user.Text != "图里是什么" || len(user.Attachments) != 1 {
		t.Fatalf("restored user message = %+v", user)
	}
	if user.Attachments[0].Name != "图片" || user.Attachments[0].MIME != "image/png" {
		t.Fatalf("restored chip = %+v", user.Attachments[0])
	}
}

// TestUserMessageEventCarriesAttachmentNames covers the durable session event.
func TestUserMessageEventCarriesAttachmentNames(t *testing.T) {
	provider := &captureProvider{}
	svc, store := newAttachmentService(t, provider)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	attachments := []contracts.Attachment{
		imageAttachment(),
		{ID: "t2", Name: "spec.pdf", MIME: "application/pdf", Data: []byte("PDF")},
	}
	if err := svc.SendWithAttachments(sessionID, "两个附件", attachments); err != nil {
		t.Fatalf("SendWithAttachments: %v", err)
	}
	waitIdle(t, svc, sessionID)

	events, err := store.Events(context.Background(), sessionID, 0, 0)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	var found bool
	for _, event := range events {
		if event.Type != "user_message" {
			continue
		}
		var payload struct {
			Text        string   `json:"text"`
			Attachments []string `json:"attachments"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("decode user_message payload: %v", err)
		}
		found = true
		if payload.Text != "两个附件" {
			t.Fatalf("event text = %q", payload.Text)
		}
		if len(payload.Attachments) != 2 || payload.Attachments[0] != "cat.png" || payload.Attachments[1] != "spec.pdf" {
			t.Fatalf("event attachments = %v, want [cat.png spec.pdf]", payload.Attachments)
		}
	}
	if !found {
		t.Fatal("no user_message event recorded")
	}
}

// --- P3: session ↔ workspace association -----------------------------------

// fakeWorkspace is a minimal contracts.WorkspaceService: only Current is used
// by the agent, the file APIs report "not implemented".
type fakeWorkspace struct {
	mu         sync.Mutex
	current    contracts.Workspace
	hasCurrent bool
	currentErr error
}

func (f *fakeWorkspace) setCurrent(workspace contracts.Workspace, ok bool) {
	f.mu.Lock()
	f.current, f.hasCurrent = workspace, ok
	f.mu.Unlock()
}

func (f *fakeWorkspace) setCurrentError(err error) {
	f.mu.Lock()
	f.currentErr = err
	f.mu.Unlock()
}

func (f *fakeWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.currentErr != nil {
		return contracts.Workspace{}, false, f.currentErr
	}
	return f.current, f.hasCurrent, nil
}

func (f *fakeWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.hasCurrent {
		return nil, nil
	}
	return []contracts.Workspace{f.current}, nil
}

func (f *fakeWorkspace) Create(context.Context, string, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, errors.New("fake workspace: not implemented")
}

func (f *fakeWorkspace) Open(context.Context, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, errors.New("fake workspace: not implemented")
}

func (f *fakeWorkspace) Delete(context.Context, string) error {
	return errors.New("fake workspace: not implemented")
}

func (f *fakeWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, errors.New("fake workspace: not implemented")
}

func (f *fakeWorkspace) ReadFile(context.Context, string, int) ([]byte, error) {
	return nil, errors.New("fake workspace: not implemented")
}

func (f *fakeWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, errors.New("fake workspace: not implemented")
}

// newWorkspaceBackedService activates the module with a KeyWorkspace service
// and returns the concrete service plus its fake durable store.
func newWorkspaceBackedService(t *testing.T, workspace contracts.WorkspaceService,
	settings *fakeSettings, logger *fakeLogger) (*service, *fakeStore) {
	t.Helper()
	if settings == nil {
		settings = newFakeSettings(nil)
	}
	if logger == nil {
		logger = &fakeLogger{}
	}
	store := newFakeStore()
	registry := rigtools.NewRegistry()
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyToolkit, registry)
	provide(t, ctx, contracts.KeySettings, contracts.SettingsService(settings))
	provide(t, ctx, contracts.KeyLogger, contracts.Logger(logger))
	provide(t, ctx, contracts.KeyStore, contracts.Store(store))
	provide(t, ctx, contracts.KeyApprovals, contracts.Approvals(fakeApprovals{}))
	provide(t, ctx, contracts.KeyWorkspace, workspace)
	if err := Module().Factory(ctx, Config{}); err != nil {
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
	return svc, store
}

// sessionInfoOf finds one session info by ID.
func sessionInfoOf(t *testing.T, svc *service, sessionID string) contracts.ChatSessionInfo {
	t.Helper()
	for _, info := range svc.Sessions() {
		if info.ID == sessionID {
			return info
		}
	}
	t.Fatalf("session %q not in Sessions()", sessionID)
	return contracts.ChatSessionInfo{}
}

// TestNewSessionRecordsWorkspace covers the NewSession path: the current
// workspace is stored on the read model and persisted through the store.
func TestNewSessionRecordsWorkspace(t *testing.T) {
	workspace := &fakeWorkspace{}
	workspace.setCurrent(contracts.Workspace{ID: "ws-session"}, true)
	svc, store := newWorkspaceBackedService(t, workspace, nil, nil)

	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if info := sessionInfoOf(t, svc, sessionID); info.WorkspaceID != "ws-session" {
		t.Fatalf("WorkspaceID = %q, want ws-session", info.WorkspaceID)
	}
	calls := store.workspaceCalls()
	if len(calls) != 1 || calls[0].sessionID != sessionID || calls[0].workspaceID != "ws-session" {
		t.Fatalf("persist calls = %+v, want one for %s/ws-session", calls, sessionID)
	}
}

// TestFirstSendBindsWorkspace covers a session created with no workspace: the
// first send records the workspace current at that moment.
func TestFirstSendBindsWorkspace(t *testing.T) {
	workspace := &fakeWorkspace{}
	svc, store := newWorkspaceBackedService(t, workspace, nil, nil)
	provider := &captureProvider{}
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if info := sessionInfoOf(t, svc, sessionID); info.WorkspaceID != "" {
		t.Fatalf("WorkspaceID before workspace = %q, want empty", info.WorkspaceID)
	}
	if calls := store.workspaceCalls(); len(calls) != 0 {
		t.Fatalf("workspace persisted before one existed: %+v", calls)
	}

	workspace.setCurrent(contracts.Workspace{ID: "ws-send"}, true)
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)
	if info := sessionInfoOf(t, svc, sessionID); info.WorkspaceID != "ws-send" {
		t.Fatalf("WorkspaceID after first send = %q, want ws-send", info.WorkspaceID)
	}
	if got := store.workspaceOf(sessionID); got != "ws-send" {
		t.Fatalf("durable workspace = %q, want ws-send", got)
	}
}

// TestRestoreWorkspaceID covers restore from the durable session metadata.
func TestRestoreWorkspaceID(t *testing.T) {
	store := newFakeStore()
	ctx := context.Background()
	if err := store.CreateSession(ctx, "sess-restored", "Restored"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := store.SetSessionWorkspace(ctx, "sess-restored", "ws-restored"); err != nil {
		t.Fatalf("SetSessionWorkspace: %v", err)
	}
	svc := newService(Config{}, rigtools.NewRegistry(), newFakeSettings(nil), &fakeLogger{}, store, fakeApprovals{})
	svc.restore(ctx)
	infos := svc.Sessions()
	if len(infos) != 1 || infos[0].WorkspaceID != "ws-restored" {
		t.Fatalf("restored sessions = %+v, want WorkspaceID ws-restored", infos)
	}
}

// TestNoWorkspaceServiceLeavesEmpty covers a host without the workspace
// module: sessions keep an empty workspace and nothing is persisted.
func TestNoWorkspaceServiceLeavesEmpty(t *testing.T) {
	svc := newTestService(t, Config{}, nil, nil)
	if svc.workspace != nil {
		t.Fatal("workspace service wired without a provider")
	}
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if info := sessionInfoOf(t, svc, sessionID); info.WorkspaceID != "" {
		t.Fatalf("WorkspaceID = %q, want empty", info.WorkspaceID)
	}
}

// TestWorkspaceFailuresAreLoggedNotFatal covers lookup and persistence errors:
// the session still works and the errors go to the logger.
func TestWorkspaceFailuresAreLoggedNotFatal(t *testing.T) {
	workspace := &fakeWorkspace{currentErr: errors.New("workspace lookup exploded")}
	logger := &fakeLogger{}
	svc, store := newWorkspaceBackedService(t, workspace, nil, logger)
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if info := sessionInfoOf(t, svc, sessionID); info.WorkspaceID != "" {
		t.Fatalf("WorkspaceID = %q, want empty after a lookup error", info.WorkspaceID)
	}

	workspace.setCurrentError(nil)
	workspace.setCurrent(contracts.Workspace{ID: "ws-failing"}, true)
	store.mu.Lock()
	store.workspaceErr = errors.New("disk full")
	store.mu.Unlock()
	provider := &captureProvider{}
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)
	if info := sessionInfoOf(t, svc, sessionID); info.WorkspaceID != "ws-failing" {
		t.Fatalf("WorkspaceID = %q, want ws-failing in the read model", info.WorkspaceID)
	}
	if !logger.contains("persist workspace") {
		t.Fatalf("persist failure was not logged: %v", logger.lines)
	}
}

// --- P3: session fork ------------------------------------------------------

// TestForkSessionCopiesMessages covers the in-memory fork: new ID/title,
// deep-copied messages with fresh IDs, active switch and subscriber notify.
func TestForkSessionCopiesMessages(t *testing.T) {
	provider := &captureProvider{}
	svc, _ := newAttachmentService(t, provider)
	sourceID, err := svc.NewSession("源会话")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	attachment := contracts.Attachment{ID: "a1", Name: "notes.txt", MIME: "text/plain", Data: []byte("hello")}
	if err := svc.SendWithAttachments(sourceID, "问题", []contracts.Attachment{attachment}); err != nil {
		t.Fatalf("SendWithAttachments: %v", err)
	}
	waitIdle(t, svc, sourceID)

	// Give the assistant turn reasoning/tools so the deep copy is exercised.
	svc.mu.Lock()
	source := svc.sessions[sourceID]
	source.messages[1].Reasoning = "思考过程"
	source.messages[1].Tools = []contracts.ToolCall{{ID: "call-1", Name: "memory", Status: "ok"}}
	svc.mu.Unlock()
	sourceBefore, _ := svc.Session(sourceID)

	notifications := make(chan string, 4)
	unsubscribe, err := svc.Subscribe(func(sessionID string) { notifications <- sessionID })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsubscribe()

	forker, ok := contracts.ChatService(svc).(contracts.SessionForker)
	if !ok {
		t.Fatal("service does not implement contracts.SessionForker")
	}
	newID, err := forker.ForkSession(sourceID)
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if newID == sourceID || !strings.HasPrefix(newID, "sess-") {
		t.Fatalf("fork ID = %q, want a fresh sess- ID", newID)
	}
	select {
	case notified := <-notifications:
		if notified != newID {
			t.Fatalf("notification = %q, want the forked session %q", notified, newID)
		}
	case <-time.After(time.Second):
		t.Fatal("fork did not notify subscribers")
	}

	info := sessionInfoOf(t, svc, newID)
	if info.Title != "副本 - 源会话" {
		t.Fatalf("fork title = %q, want 副本 - 源会话", info.Title)
	}
	if got := svc.ActiveSession(); got != newID {
		t.Fatalf("active session = %q, want the fork %q", got, newID)
	}
	forked, ok := svc.Session(newID)
	if !ok {
		t.Fatal("forked session missing")
	}
	if len(forked.Messages) != len(sourceBefore.Messages) {
		t.Fatalf("fork has %d messages, want %d", len(forked.Messages), len(sourceBefore.Messages))
	}
	sourceIDs := make(map[string]bool, len(sourceBefore.Messages))
	for _, message := range sourceBefore.Messages {
		sourceIDs[message.ID] = true
	}
	forkIDs := make(map[string]bool, len(forked.Messages))
	for index, copied := range forked.Messages {
		original := sourceBefore.Messages[index]
		if copied.ID == "" || copied.ID == original.ID || sourceIDs[copied.ID] || forkIDs[copied.ID] {
			t.Fatalf("forked message %d ID = %q, original %q (must be fresh and unique)", index, copied.ID, original.ID)
		}
		forkIDs[copied.ID] = true
		if copied.Role != original.Role || copied.Text != original.Text ||
			copied.Reasoning != original.Reasoning || copied.Error != original.Error || copied.Streaming {
			t.Fatalf("forked message %d = %+v, want a copy of %+v", index, copied, original)
		}
		if len(copied.Tools) != len(original.Tools) || len(copied.Attachments) != len(original.Attachments) {
			t.Fatalf("forked message %d tools/attachments = %d/%d, want %d/%d", index,
				len(copied.Tools), len(copied.Attachments), len(original.Tools), len(original.Attachments))
		}
	}
	if len(forked.Messages[0].Attachments) != 1 || string(forked.Messages[0].Attachments[0].Data) != "hello" {
		t.Fatalf("forked user attachments = %+v", forked.Messages[0].Attachments)
	}
	if len(forked.Messages[1].Tools) != 1 || forked.Messages[1].Tools[0].ID != "call-1" {
		t.Fatalf("forked assistant tools = %+v", forked.Messages[1].Tools)
	}

	// Mutating the fork must not affect the source (deep copies).
	forked.Messages[0].Text = "mutated"
	forked.Messages[0].Attachments[0].Data[0] = 'X'
	forked.Messages[1].Tools[0].Status = "error"
	reloadedSource, _ := svc.Session(sourceID)
	if reloadedSource.Messages[0].Text != sourceBefore.Messages[0].Text ||
		string(reloadedSource.Messages[0].Attachments[0].Data) != "hello" ||
		reloadedSource.Messages[1].Tools[0].Status != "ok" {
		t.Fatalf("fork aliases source messages: %+v", reloadedSource.Messages)
	}
}

// TestForkRunningSourceSnapshots covers forking a running session: the copy gets
// the current text, is not running itself and the source is not cancelled.
func TestForkRunningSourceSnapshots(t *testing.T) {
	svc := newTestService(t, Config{}, nil, nil)
	sourceID, err := svc.NewSession("running")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	svc.mu.Lock()
	source := svc.sessions[sourceID]
	source.messages = []contracts.ChatMessage{
		{ID: svc.nextMessageIDLocked(), Role: contracts.ChatRoleUser, Text: "question"},
		{ID: svc.nextMessageIDLocked(), Role: contracts.ChatRoleAssistant, Text: "partial answer", Streaming: true},
	}
	source.running = true
	source.assistant = 1
	svc.mu.Unlock()

	newID, err := svc.ForkSession(sourceID)
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	forked, _ := svc.Session(newID)
	if forked.Running {
		t.Fatal("forked session is running")
	}
	if len(forked.Messages) != 2 || forked.Messages[1].Text != "partial answer" || forked.Messages[1].Streaming {
		t.Fatalf("forked snapshot = %+v, want the current text without Streaming", forked.Messages)
	}
	svc.mu.Lock()
	stillRunning := svc.sessions[sourceID].running
	stillStreaming := svc.sessions[sourceID].messages[1].Streaming
	svc.sessions[sourceID].running = false
	svc.mu.Unlock()
	if !stillRunning || !stillStreaming {
		t.Fatalf("fork cancelled/mutated the running source: running=%v streaming=%v", stillRunning, stillStreaming)
	}
}

// TestForkUnknownSession covers the unknown source case.
func TestForkUnknownSession(t *testing.T) {
	svc := newTestService(t, Config{}, nil, nil)
	activeBefore := svc.ActiveSession()
	if _, err := svc.ForkSession("missing"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("ForkSession(missing) = %v, want a readable unknown-session error", err)
	}
	if got := svc.ActiveSession(); got != activeBefore {
		t.Fatalf("failed fork changed the active session: %q -> %q", activeBefore, got)
	}
}

// TestForkTitleFallbacks covers the empty-title placeholder and truncation.
func TestForkTitleFallbacks(t *testing.T) {
	svc := newTestService(t, Config{}, nil, nil)
	emptyID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	forkedEmpty, err := svc.ForkSession(emptyID)
	if err != nil {
		t.Fatalf("ForkSession(empty title): %v", err)
	}
	if info := sessionInfoOf(t, svc, forkedEmpty); info.Title != "副本 - 未命名会话" {
		t.Fatalf("empty-title fork title = %q", info.Title)
	}

	longID, err := svc.NewSession(strings.Repeat("长", 40))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	forkedLong, err := svc.ForkSession(longID)
	if err != nil {
		t.Fatalf("ForkSession(long title): %v", err)
	}
	title := sessionInfoOf(t, svc, forkedLong).Title
	if got := len([]rune(title)); got > maxTitleRunes+1 {
		t.Fatalf("fork title has %d runes, want at most %d", got, maxTitleRunes+1)
	}
	if !strings.HasPrefix(title, "副本 - ") || !strings.HasSuffix(title, "…") {
		t.Fatalf("truncated fork title = %q", title)
	}
}

// TestForkPersistsHistoryAndEvent covers the durable store writes: session row,
// title, rebuilt rig history (user/assistant text + reasoning, no tools) and
// the optional fork event.
func TestForkPersistsHistoryAndEvent(t *testing.T) {
	provider := &captureProvider{}
	svc, store := newAttachmentService(t, provider)
	sourceID, err := svc.NewSession("持久化")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sourceID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sourceID)
	svc.mu.Lock()
	svc.sessions[sourceID].messages[1].Reasoning = "因为所以"
	svc.mu.Unlock()

	newID, err := svc.ForkSession(sourceID)
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	ctx := context.Background()
	metas, err := store.Sessions(ctx)
	if err != nil {
		t.Fatalf("store.Sessions: %v", err)
	}
	var meta contracts.SessionMeta
	found := false
	for _, candidate := range metas {
		if candidate.ID == newID {
			meta, found = candidate, true
			break
		}
	}
	if !found || meta.Title != "副本 - 持久化" || meta.MessageCount != 2 {
		t.Fatalf("stored fork meta = %+v found=%v", meta, found)
	}
	history, err := store.History().Load(ctx, newID)
	if err != nil {
		t.Fatalf("history.Load(fork): %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("stored fork history = %+v, want 2 messages", history)
	}
	if history[0].Role != rig.RoleUser || history[0].Text() != "hello" {
		t.Fatalf("stored user message = %+v", history[0])
	}
	if history[1].Role != rig.RoleAssistant || history[1].Text() != "ok" || history[1].Reasoning != "因为所以" {
		t.Fatalf("stored assistant message = %+v", history[1])
	}
	if len(history[1].ToolCalls) != 0 {
		t.Fatalf("stored assistant tool calls = %+v, want none", history[1].ToolCalls)
	}
	events, err := store.Events(ctx, newID, 0, 0)
	if err != nil {
		t.Fatalf("store.Events: %v", err)
	}
	forkEvent := false
	for _, event := range events {
		if event.Type != "fork" {
			continue
		}
		forkEvent = true
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("fork event payload: %v", err)
		}
		if payload["source"] != sourceID {
			t.Fatalf("fork event source = %v, want %q", payload["source"], sourceID)
		}
	}
	if !forkEvent {
		t.Fatalf("no fork event stored: %+v", events)
	}
}

// TestForkSourceKeepsWorking covers that a fork does not disturb later sends on
// the source session.
func TestForkSourceKeepsWorking(t *testing.T) {
	provider := &captureProvider{}
	svc, _ := newAttachmentService(t, provider)
	sourceID, err := svc.NewSession("source")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sourceID, "one"); err != nil {
		t.Fatalf("Send(one): %v", err)
	}
	waitIdle(t, svc, sourceID)
	newID, err := svc.ForkSession(sourceID)
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	forkedBefore, _ := svc.Session(newID)
	if err := svc.Send(sourceID, "two"); err != nil {
		t.Fatalf("Send(two) after fork: %v", err)
	}
	waitIdle(t, svc, sourceID)
	source, _ := svc.Session(sourceID)
	if len(source.Messages) != 4 {
		t.Fatalf("source messages after second send = %d, want 4", len(source.Messages))
	}
	forkedAfter, _ := svc.Session(newID)
	if len(forkedAfter.Messages) != len(forkedBefore.Messages) {
		t.Fatalf("fork changed when the source ran again: %d -> %d", len(forkedBefore.Messages), len(forkedAfter.Messages))
	}
}

// TestForkConcurrent covers two forks of the same session producing distinct
// sessions.
func TestForkConcurrent(t *testing.T) {
	svc := newTestService(t, Config{}, nil, nil)
	sourceID, err := svc.NewSession("concurrent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	const workers = 2
	var waitGroup sync.WaitGroup
	ids := make(chan string, workers)
	errs := make(chan error, workers)
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			id, err := svc.ForkSession(sourceID)
			if err != nil {
				errs <- err
				return
			}
			ids <- id
		}()
	}
	waitGroup.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for id := range ids {
		if id == sourceID || seen[id] {
			t.Fatalf("duplicate/bad fork ID %q", id)
		}
		seen[id] = true
		if _, ok := svc.Session(id); !ok {
			t.Fatalf("concurrent fork %q missing", id)
		}
	}
	if len(seen) != workers {
		t.Fatalf("forks = %d, want %d", len(seen), workers)
	}
}

// --- P3: session pinning ---------------------------------------------------

// failingSettings implements contracts.SettingsService with a failing Set.
type failingSettings struct{}

func (failingSettings) Get(key, fallback string) string { return fallback }
func (failingSettings) Set(key, value string) error {
	return errors.New("settings write failed")
}
func (failingSettings) All() map[string]string { return nil }

// TestSetSessionPinnedPersistsAndNotifies covers the settings key, the read
// model flag and subscriber notification.
func TestSetSessionPinnedPersistsAndNotifies(t *testing.T) {
	settings := newFakeSettings(nil)
	svc := newTestService(t, Config{}, settings, nil)
	sessionID, err := svc.NewSession("pin me")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	pinner, ok := contracts.ChatService(svc).(contracts.SessionPinner)
	if !ok {
		t.Fatal("service does not implement contracts.SessionPinner")
	}
	notifications := make(chan string, 4)
	unsubscribe, err := svc.Subscribe(func(id string) { notifications <- id })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsubscribe()

	key := contracts.SessionPinnedSettingKey(sessionID)
	if info := sessionInfoOf(t, svc, sessionID); info.Pinned {
		t.Fatal("new session is pinned")
	}
	if err := pinner.SetSessionPinned(sessionID, true); err != nil {
		t.Fatalf("SetSessionPinned(true): %v", err)
	}
	if got := settings.Get(key, ""); got != "true" {
		t.Fatalf("settings %q = %q, want true", key, got)
	}
	if info := sessionInfoOf(t, svc, sessionID); !info.Pinned {
		t.Fatal("Sessions().Pinned did not reflect the persisted flag")
	}
	select {
	case notified := <-notifications:
		if notified != sessionID {
			t.Fatalf("notification = %q, want %q", notified, sessionID)
		}
	case <-time.After(time.Second):
		t.Fatal("SetSessionPinned did not notify subscribers")
	}

	if err := pinner.SetSessionPinned(sessionID, false); err != nil {
		t.Fatalf("SetSessionPinned(false): %v", err)
	}
	if got := settings.Get(key, ""); got != "false" {
		t.Fatalf("settings %q = %q, want false", key, got)
	}
	if info := sessionInfoOf(t, svc, sessionID); info.Pinned {
		t.Fatal("Sessions().Pinned still true after unpin")
	}
	select {
	case <-notifications:
	case <-time.After(time.Second):
		t.Fatal("unpin did not notify subscribers")
	}

	if err := pinner.SetSessionPinned("missing", true); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("SetSessionPinned(missing) = %v, want a readable unknown-session error", err)
	}
}

// TestSessionPinnedWithoutSettingsOrOnFailure covers a missing settings service
// and a failing settings write: pinning fails readably, listing stays false.
func TestSessionPinnedWithoutSettingsOrOnFailure(t *testing.T) {
	noSettings := newService(Config{}, rigtools.NewRegistry(), nil, &fakeLogger{}, newFakeStore(), fakeApprovals{})
	sessionID, err := noSettings.NewSession("no settings")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if info := sessionInfoOf(t, noSettings, sessionID); info.Pinned {
		t.Fatal("session without a settings service is pinned")
	}
	if err := noSettings.SetSessionPinned(sessionID, true); err == nil {
		t.Fatal("SetSessionPinned without settings returned nil error")
	}
	if info := sessionInfoOf(t, noSettings, sessionID); info.Pinned {
		t.Fatal("failed pin changed the read model")
	}

	failing := newTestService(t, Config{}, nil, nil)
	// Replace the working fake settings with the failing implementation.
	failing.settings = failingSettings{}
	failingID, err := failing.NewSession("failing settings")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := failing.SetSessionPinned(failingID, true); err == nil || !strings.Contains(err.Error(), "settings write failed") {
		t.Fatalf("SetSessionPinned with failing settings = %v, want the write error", err)
	}
	if info := sessionInfoOf(t, failing, failingID); info.Pinned {
		t.Fatal("failed settings write changed the read model")
	}
}

// TestSessionPinnedSurvivesRestore covers pins persisted in settings across a
// service rebuild.
func TestSessionPinnedSurvivesRestore(t *testing.T) {
	settings := newFakeSettings(nil)
	store := newFakeStore()
	first := newService(Config{}, rigtools.NewRegistry(), settings, &fakeLogger{}, store, fakeApprovals{})
	sessionID, err := first.NewSession("restored pin")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := first.SetSessionPinned(sessionID, true); err != nil {
		t.Fatalf("SetSessionPinned: %v", err)
	}

	restarted := newService(Config{}, rigtools.NewRegistry(), settings, &fakeLogger{}, store, fakeApprovals{})
	restarted.restore(context.Background())
	infos := restarted.Sessions()
	if len(infos) != 1 || !infos[0].Pinned {
		t.Fatalf("restored Sessions = %+v, want the session pinned", infos)
	}
}

// TestSessionPinnedConcurrent covers concurrent pin/unpin and listing.
func TestSessionPinnedConcurrent(t *testing.T) {
	settings := newFakeSettings(nil)
	svc := newTestService(t, Config{}, settings, nil)
	sessionID, err := svc.NewSession("concurrent pin")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	const workers = 4
	var waitGroup sync.WaitGroup
	errs := make(chan error, workers)
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func(number int) {
			defer waitGroup.Done()
			for iteration := 0; iteration < 20; iteration++ {
				if err := svc.SetSessionPinned(sessionID, (number+iteration)%2 == 0); err != nil {
					errs <- err
					return
				}
				_ = svc.Sessions()
			}
		}(index)
	}
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// --- P3: session archiving -------------------------------------------------

// TestSetSessionArchivedPersistsAndNotifies covers the settings key, the read
// model flag and subscriber notification.
func TestSetSessionArchivedPersistsAndNotifies(t *testing.T) {
	settings := newFakeSettings(nil)
	svc := newTestService(t, Config{}, settings, nil)
	sessionID, err := svc.NewSession("archive me")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	archiver, ok := contracts.ChatService(svc).(contracts.SessionArchiver)
	if !ok {
		t.Fatal("service does not implement contracts.SessionArchiver")
	}
	notifications := make(chan string, 4)
	unsubscribe, err := svc.Subscribe(func(id string) { notifications <- id })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsubscribe()

	key := contracts.SessionArchivedSettingKey(sessionID)
	if info := sessionInfoOf(t, svc, sessionID); info.Archived {
		t.Fatal("new session is archived")
	}
	if err := archiver.SetSessionArchived(sessionID, true); err != nil {
		t.Fatalf("SetSessionArchived(true): %v", err)
	}
	if got := settings.Get(key, ""); got != "true" {
		t.Fatalf("settings %q = %q, want true", key, got)
	}
	if info := sessionInfoOf(t, svc, sessionID); !info.Archived {
		t.Fatal("Sessions().Archived did not reflect the persisted flag")
	}
	select {
	case notified := <-notifications:
		if notified != sessionID {
			t.Fatalf("notification = %q, want %q", notified, sessionID)
		}
	case <-time.After(time.Second):
		t.Fatal("SetSessionArchived did not notify subscribers")
	}

	if err := archiver.SetSessionArchived(sessionID, false); err != nil {
		t.Fatalf("SetSessionArchived(false): %v", err)
	}
	if got := settings.Get(key, ""); got != "false" {
		t.Fatalf("settings %q = %q, want false", key, got)
	}
	if info := sessionInfoOf(t, svc, sessionID); info.Archived {
		t.Fatal("Sessions().Archived still true after unarchive")
	}
	select {
	case <-notifications:
	case <-time.After(time.Second):
		t.Fatal("unarchive did not notify subscribers")
	}

	if err := archiver.SetSessionArchived("missing", true); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("SetSessionArchived(missing) = %v, want a readable unknown-session error", err)
	}
}

// TestSessionArchivedWithoutSettingsOrOnFailure covers a missing settings
// service and a failing settings write.
func TestSessionArchivedWithoutSettingsOrOnFailure(t *testing.T) {
	noSettings := newService(Config{}, rigtools.NewRegistry(), nil, &fakeLogger{}, newFakeStore(), fakeApprovals{})
	sessionID, err := noSettings.NewSession("no settings archive")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if info := sessionInfoOf(t, noSettings, sessionID); info.Archived {
		t.Fatal("session without a settings service is archived")
	}
	if err := noSettings.SetSessionArchived(sessionID, true); err == nil {
		t.Fatal("SetSessionArchived without settings returned nil error")
	}
	if info := sessionInfoOf(t, noSettings, sessionID); info.Archived {
		t.Fatal("failed archive changed the read model")
	}

	failing := newTestService(t, Config{}, nil, nil)
	failing.settings = failingSettings{}
	failingID, err := failing.NewSession("failing settings archive")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := failing.SetSessionArchived(failingID, true); err == nil || !strings.Contains(err.Error(), "settings write failed") {
		t.Fatalf("SetSessionArchived with failing settings = %v, want the write error", err)
	}
	if info := sessionInfoOf(t, failing, failingID); info.Archived {
		t.Fatal("failed settings write changed the archive read model")
	}
}

// TestSessionPinnedAndArchivedIndependent covers the two flags living in
// separate settings keys and not influencing each other.
func TestSessionPinnedAndArchivedIndependent(t *testing.T) {
	settings := newFakeSettings(nil)
	svc := newTestService(t, Config{}, settings, nil)
	sessionID, err := svc.NewSession("both flags")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionPinned(sessionID, true); err != nil {
		t.Fatalf("SetSessionPinned: %v", err)
	}
	if err := svc.SetSessionArchived(sessionID, true); err != nil {
		t.Fatalf("SetSessionArchived: %v", err)
	}
	info := sessionInfoOf(t, svc, sessionID)
	if !info.Pinned || !info.Archived {
		t.Fatalf("info = %+v, want both flags true", info)
	}
	if got := settings.Get(contracts.SessionPinnedSettingKey(sessionID), ""); got != "true" {
		t.Fatalf("pinned key = %q", got)
	}
	if got := settings.Get(contracts.SessionArchivedSettingKey(sessionID), ""); got != "true" {
		t.Fatalf("archived key = %q", got)
	}

	if err := svc.SetSessionPinned(sessionID, false); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	if info := sessionInfoOf(t, svc, sessionID); info.Pinned || !info.Archived {
		t.Fatalf("after unpin info = %+v, want pinned=false archived=true", info)
	}
	if err := svc.SetSessionArchived(sessionID, false); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	if info := sessionInfoOf(t, svc, sessionID); info.Pinned || info.Archived {
		t.Fatalf("after unarchive info = %+v, want both false", info)
	}
}

// TestSessionArchivedSurvivesRestore covers archive flags persisted in settings
// across a service rebuild.
func TestSessionArchivedSurvivesRestore(t *testing.T) {
	settings := newFakeSettings(nil)
	store := newFakeStore()
	first := newService(Config{}, rigtools.NewRegistry(), settings, &fakeLogger{}, store, fakeApprovals{})
	sessionID, err := first.NewSession("restored archive")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := first.SetSessionArchived(sessionID, true); err != nil {
		t.Fatalf("SetSessionArchived: %v", err)
	}

	restarted := newService(Config{}, rigtools.NewRegistry(), settings, &fakeLogger{}, store, fakeApprovals{})
	restarted.restore(context.Background())
	infos := restarted.Sessions()
	if len(infos) != 1 || !infos[0].Archived || infos[0].Pinned {
		t.Fatalf("restored Sessions = %+v, want archived only", infos)
	}
}

// TestSessionArchivedConcurrent covers concurrent archive/unarchive and listing.
func TestSessionArchivedConcurrent(t *testing.T) {
	settings := newFakeSettings(nil)
	svc := newTestService(t, Config{}, settings, nil)
	sessionID, err := svc.NewSession("concurrent archive")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	const workers = 4
	var waitGroup sync.WaitGroup
	errs := make(chan error, workers)
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func(number int) {
			defer waitGroup.Done()
			for iteration := 0; iteration < 20; iteration++ {
				if err := svc.SetSessionArchived(sessionID, (number+iteration)%2 == 0); err != nil {
					errs <- err
					return
				}
				_ = svc.Sessions()
			}
		}(index)
	}
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
