package agent

// Tests for Regenerate: rewinding the trailing assistant/tool exchange and
// re-running the last user message through the normal asynchronous run path.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// historySpyStore wraps fakeStore to count ReplaceHistory calls and inject a
// persistence failure.
type historySpyStore struct {
	*fakeStore

	mu         sync.Mutex
	replaces   int
	lastWrite  []rig.Message
	replaceErr error
}

func (s *historySpyStore) ReplaceHistory(ctx context.Context, sessionID string, messages []rig.Message) error {
	s.mu.Lock()
	s.replaces++
	s.lastWrite = append([]rig.Message(nil), messages...)
	err := s.replaceErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.fakeStore.ReplaceHistory(ctx, sessionID, messages)
}

func (s *historySpyStore) replaceCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.replaces
}

func (s *historySpyStore) lastHistory() []rig.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]rig.Message(nil), s.lastWrite...)
}

// regenerateProvider answers each stream with a distinct short text.
type regenerateProvider struct{ calls atomic.Int32 }

func (p *regenerateProvider) Name() string { return "regenerate" }

func (p *regenerateProvider) Stream(context.Context, rig.Request) (rig.Stream, error) {
	call := p.calls.Add(1)
	return &scriptedStream{chunks: []rig.Chunk{
		{Delta: fmt.Sprintf("answer-%d", call)},
		{FinishReason: "stop"},
	}}, nil
}

// newRegenerateService builds a service with a history spy and the scripted
// provider, bypassing the module wiring and the network.
func newRegenerateService(t *testing.T, store *historySpyStore) (*service, *regenerateProvider) {
	t.Helper()
	provider := &regenerateProvider{}
	svc := newService(Config{}, rigtools.NewRegistry(),
		contracts.SettingsService(newFakeSettings(nil)), &fakeLogger{}, store, fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	return svc, provider
}

// seedMessages replaces a session's read-model messages directly; it exercises
// truncation without depending on a tool-capable provider.
func seedMessages(t *testing.T, svc *service, sessionID string, messages []contracts.ChatMessage) {
	t.Helper()
	svc.mu.Lock()
	defer svc.mu.Unlock()
	state, ok := svc.sessions[sessionID]
	if !ok {
		t.Fatalf("seed: unknown session %q", sessionID)
	}
	state.messages = append([]contracts.ChatMessage(nil), messages...)
	state.assistant = -1
}

// TestRegenerateValidation covers the three observable preconditions.
func TestRegenerateValidation(t *testing.T) {
	block := make(chan struct{})
	server := newSSEServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-block:
		case <-time.After(5 * time.Second):
		}
	})
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	})
	svc := newTestService(t, Config{}, settings, nil)
	if err := svc.Regenerate("missing"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("Regenerate(unknown) = %v, want unknown-session error", err)
	}
	id, err := svc.NewSession("chat")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Regenerate(id); !errors.Is(err, ErrNoUserMessage) {
		t.Fatalf("Regenerate(no user message) = %v, want ErrNoUserMessage", err)
	}
	if err := svc.Send(id, "first"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, "run to start", func() bool {
		session, _ := svc.Session(id)
		return session.Running
	})
	if err := svc.Regenerate(id); !errors.Is(err, ErrSessionRunning) {
		t.Fatalf("Regenerate(running) = %v, want ErrSessionRunning", err)
	}
	close(block)
	waitIdle(t, svc, id)
}

// TestRegenerateRetriesLastExchange is the end-to-end path over a fake SSE
// provider: the trailing assistant reply is replaced, the earlier exchange is
// kept, the last user message stays, and no duplicate user_message event is
// written.
func TestRegenerateRetriesLastExchange(t *testing.T) {
	var (
		mu       sync.Mutex
		payloads [][]byte
	)
	server := newSSEServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		payloads = append(payloads, body)
		var payload struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		answer := fmt.Sprintf("answer-%d", len(payloads))
		mu.Unlock()
		writeSSE(t, w,
			`{"choices":[{"delta":{"content":"`+answer+`"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		)
	})
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	})
	svc := newTestService(t, Config{}, settings, nil)
	var notifications atomic.Int32
	unsub, err := svc.Subscribe(func(string) { notifications.Add(1) })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsub()

	id, err := svc.NewSession("chat")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "first question"); err != nil {
		t.Fatalf("Send first: %v", err)
	}
	waitIdle(t, svc, id)
	if err := svc.Send(id, "second question"); err != nil {
		t.Fatalf("Send second: %v", err)
	}
	before := waitIdle(t, svc, id)
	if len(before.Messages) != 4 || before.Messages[3].Text != "answer-2" {
		t.Fatalf("before regenerate = %+v, want user/assistant/user/assistant", before.Messages)
	}
	userID := before.Messages[2].ID
	notifyBefore := notifications.Load()

	if err := svc.Regenerate(id); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	session := waitIdle(t, svc, id)
	if len(session.Messages) != 4 {
		t.Fatalf("messages after regenerate = %d, want 4: %+v", len(session.Messages), session.Messages)
	}
	if session.Messages[0].Text != "first question" || session.Messages[1].Text != "answer-1" ||
		session.Messages[2].Text != "second question" || session.Messages[2].ID != userID {
		t.Fatalf("regenerate did not preserve history: %+v", session.Messages)
	}
	if session.Messages[3].Text != "answer-3" || session.Messages[3].Role != contracts.ChatRoleAssistant ||
		session.Messages[3].Streaming {
		t.Fatalf("regenerated assistant = %+v", session.Messages[3])
	}
	if notifications.Load() <= notifyBefore {
		t.Fatal("regenerate did not notify subscribers")
	}

	// The retry request contains the earlier exchange and the last user message
	// exactly once (no duplicated user prompt from the rewind).
	lastPayload := payloads[len(payloads)-1]
	var payload struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(lastPayload, &payload); err != nil {
		t.Fatalf("decode last request: %v", err)
	}
	userCount := 0
	lastUserContent := ""
	for _, message := range payload.Messages {
		if message.Role == "user" {
			userCount++
			lastUserContent = string(message.Content)
		}
		if strings.Contains(string(message.Content), "answer-2") {
			t.Fatalf("last model request still carries the dropped reply: %s", message.Content)
		}
	}
	if userCount != 2 {
		t.Fatalf("last model request has %d user messages, want first+retried", userCount)
	}
	if !strings.Contains(lastUserContent, "second question") {
		t.Fatalf("last model request last user content = %s", lastUserContent)
	}
	if strings.Count(lastUserContent, "second question") != 1 {
		t.Fatalf("retried user message duplicated in request: %s", lastUserContent)
	}

	store := svc.store.(*fakeStore)
	history, err := store.History().Load(context.Background(), id)
	if err != nil {
		t.Fatalf("store history: %v", err)
	}
	if len(history) != 4 {
		t.Fatalf("store history = %d messages, want 4: %+v", len(history), history)
	}
	events, err := store.Events(context.Background(), id, 0, 0)
	if err != nil {
		t.Fatalf("store events: %v", err)
	}
	counts := map[string]int{}
	for _, event := range events {
		counts[event.Type]++
	}
	if counts["user_message"] != 2 {
		t.Fatalf("user_message events = %d, want 2 (no duplicate from regenerate)", counts["user_message"])
	}
	if counts["run_start"] != 3 || counts["run_end"] != 3 {
		t.Fatalf("run events = start:%d end:%d, want 3/3", counts["run_start"], counts["run_end"])
	}

	// A second regenerate still targets the same last user message.
	if err := svc.Regenerate(id); err != nil {
		t.Fatalf("second Regenerate: %v", err)
	}
	again := waitIdle(t, svc, id)
	if len(again.Messages) != 4 || again.Messages[2].ID != userID || again.Messages[3].Text != "answer-4" {
		t.Fatalf("second regenerate = %+v", again.Messages)
	}
}

// TestRegenerateTruncatesToolMessages covers a read model whose trailing
// assistant carries tool calls: all of it is dropped and the pre-user history is
// what ReplaceHistory persists.
func TestRegenerateTruncatesToolMessages(t *testing.T) {
	store := &historySpyStore{fakeStore: newFakeStore()}
	svc, provider := newRegenerateService(t, store)
	id, err := svc.NewSession("tools")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	seedMessages(t, svc, id, []contracts.ChatMessage{
		{ID: "u1", Role: contracts.ChatRoleUser, Text: "first"},
		{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "first answer"},
		{ID: "u2", Role: contracts.ChatRoleUser, Text: "second"},
		{ID: "a2", Role: contracts.ChatRoleAssistant, Text: "old answer", Tools: []contracts.ToolCall{
			{ID: "call-1", Name: "echo", Status: "ok"},
		}},
	})
	if err := svc.Regenerate(id); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	session := waitIdle(t, svc, id)
	if len(session.Messages) != 4 {
		t.Fatalf("messages = %d, want 4: %+v", len(session.Messages), session.Messages)
	}
	if session.Messages[2].Text != "second" || session.Messages[3].Text != "answer-1" {
		t.Fatalf("messages after regenerate = %+v", session.Messages)
	}
	if len(session.Messages[3].Tools) != 0 {
		t.Fatalf("regenerated assistant kept old tool calls: %+v", session.Messages[3].Tools)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls.Load())
	}
	if store.replaceCount() != 1 {
		t.Fatalf("ReplaceHistory calls = %d, want 1", store.replaceCount())
	}
	written := store.lastHistory()
	if len(written) != 2 || written[0].Role != rig.RoleUser || written[1].Role != rig.RoleAssistant {
		t.Fatalf("ReplaceHistory messages = %+v, want the pre-user history", written)
	}
	if text := messageText(written[0]); text != "first" {
		t.Fatalf("ReplaceHistory first user = %q, want first", text)
	}
	// The retried user turn is re-appended by the run itself (the same input
	// path Send uses), so the durable history still ends with the retried
	// exchange exactly once.
	final, err := store.History().Load(context.Background(), id)
	if err != nil {
		t.Fatalf("load final history: %v", err)
	}
	if len(final) != 4 || messageText(final[2]) != "second" || messageText(final[3]) != "answer-1" {
		t.Fatalf("final durable history = %+v, want first turn + retried exchange", final)
	}
}

// TestRegeneratePersistFailureRestoresState covers a failed history write: the
// call returns an error, the read model is rolled back and no run starts.
func TestRegeneratePersistFailureRestoresState(t *testing.T) {
	store := &historySpyStore{
		fakeStore:  newFakeStore(),
		replaceErr: errors.New("disk full"),
	}
	svc, provider := newRegenerateService(t, store)
	id, err := svc.NewSession("fail")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	seedMessages(t, svc, id, []contracts.ChatMessage{
		{ID: "u1", Role: contracts.ChatRoleUser, Text: "question"},
		{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "old answer"},
	})
	if err := svc.Regenerate(id); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("Regenerate = %v, want the persistence error", err)
	}
	session, ok := svc.Session(id)
	if !ok || session.Running {
		t.Fatalf("session after failed regenerate = %+v, want idle", session)
	}
	if len(session.Messages) != 2 || session.Messages[1].Text != "old answer" ||
		session.Messages[1].Streaming {
		t.Fatalf("messages after failed regenerate = %+v, want rollback", session.Messages)
	}
	if provider.calls.Load() != 0 {
		t.Fatalf("provider calls = %d, want 0 after persistence failure", provider.calls.Load())
	}
}

// TestRegenerateConcurrentReaders exercises Regenerate against concurrent
// Sessions/Session reads under the race detector.
func TestRegenerateConcurrentReaders(t *testing.T) {
	store := &historySpyStore{fakeStore: newFakeStore()}
	svc, _ := newRegenerateService(t, store)
	id, err := svc.NewSession("race")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	seedMessages(t, svc, id, []contracts.ChatMessage{
		{ID: "u1", Role: contracts.ChatRoleUser, Text: "question"},
		{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "old"},
	})
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_ = svc.Regenerate(id)
				_ = svc.Sessions()
				_, _ = svc.Session(id)
				_ = svc.Config()
			}
		}()
	}
	wg.Wait()
	waitIdle(t, svc, id)
	session := waitIdle(t, svc, id)
	if len(session.Messages) != 2 || session.Messages[0].Text != "question" {
		t.Fatalf("messages after concurrent regenerates = %+v", session.Messages)
	}
}

// TestRegenerateSetsRunning covers the asynchronous lifecycle: Regenerate
// returns before the model finishes, marks the session running, and notifies
// subscribers while the retried run is in flight.
func TestRegenerateSetsRunning(t *testing.T) {
	var calls atomic.Int32
	block := make(chan struct{})
	server := newSSEServer(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			writeSSE(t, w,
				`{"choices":[{"delta":{"content":"first answer"}}]}`,
				`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial retry\"}}]}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-block:
		case <-time.After(5 * time.Second):
		}
	})
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	})
	svc := newTestService(t, Config{}, settings, nil)
	id, err := svc.NewSession("async")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "question"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	var notifications atomic.Int32
	unsub, err := svc.Subscribe(func(string) { notifications.Add(1) })
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsub()
	if err := svc.Regenerate(id); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	if err := svc.Regenerate(id); !errors.Is(err, ErrSessionRunning) {
		t.Fatalf("concurrent Regenerate = %v, want ErrSessionRunning", err)
	}
	waitFor(t, "regenerate run to start", func() bool {
		session, ok := svc.Session(id)
		return ok && session.Running
	})
	if notifications.Load() == 0 {
		t.Fatal("regenerate did not notify subscribers")
	}
	if err := svc.Cancel(id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	close(block)
	waitIdle(t, svc, id)
}
