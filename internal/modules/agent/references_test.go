package agent

// Tests for @session reference expansion and reasoning-effort propagation.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// seedReadModel installs messages on an existing session without running the
// provider, so reference expansion can read them.
func seedReadModel(t *testing.T, svc *service, sessionID string, messages ...contracts.ChatMessage) {
	t.Helper()
	svc.mu.Lock()
	defer svc.mu.Unlock()
	state, ok := svc.sessions[sessionID]
	if !ok {
		t.Fatalf("seed unknown session %q", sessionID)
	}
	state.messages = append([]contracts.ChatMessage(nil), messages...)
	state.assistant = -1
}

func TestSendExpandsSessionReferencesInModelInputOnly(t *testing.T) {
	provider := &captureProvider{}
	svc, store := newAttachmentService(t, provider)
	current, err := svc.NewSession("Current")
	if err != nil {
		t.Fatalf("NewSession current: %v", err)
	}
	referenced, err := svc.NewSession("Referenced")
	if err != nil {
		t.Fatalf("NewSession referenced: %v", err)
	}
	seedReadModel(t, svc, referenced,
		contracts.ChatMessage{ID: "r1", Role: contracts.ChatRoleUser, Text: "question from ref"},
		contracts.ChatMessage{ID: "r2", Role: contracts.ChatRoleAssistant, Text: "answer from ref"},
	)
	prompt := "compare with @session:" + referenced
	if err := svc.Send(current, prompt); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, current)

	request := provider.lastRequest()
	text := lastUserMessage(t, request).Text()
	if !strings.Contains(text, `[引用的会话 "Referenced" (`+referenced+`)]`) ||
		!strings.Contains(text, "user: question from ref") ||
		!strings.Contains(text, "assistant: answer from ref") ||
		!strings.Contains(text, "[引用结束]") ||
		!strings.HasSuffix(strings.TrimSpace(text), prompt) {
		t.Fatalf("expanded model input = %q", text)
	}

	// The read model and the durable history keep the original user prompt.
	session, _ := svc.Session(current)
	var modelUser string
	for _, message := range session.Messages {
		if message.Role == contracts.ChatRoleUser {
			modelUser = message.Text
		}
	}
	if modelUser != prompt {
		t.Fatalf("read-model user = %q, want original prompt", modelUser)
	}
	history, err := store.History().Load(context.Background(), current)
	if err != nil {
		t.Fatalf("store history: %v", err)
	}
	var persisted []string
	for _, message := range history {
		if message.Role == rig.RoleUser {
			persisted = append(persisted, message.Text())
		}
	}
	if len(persisted) != 1 || persisted[0] != prompt {
		t.Fatalf("persisted user messages = %q, want original prompt only", persisted)
	}
	events, err := store.Events(context.Background(), current, 0, 0)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	foundRefs := false
	for _, event := range events {
		if event.Type == "session_refs" && strings.Contains(string(event.Payload), referenced) {
			foundRefs = true
		}
	}
	if !foundRefs {
		t.Fatalf("missing session_refs event: %+v", events)
	}
}

func TestSendSessionReferenceFiltersAndBudget(t *testing.T) {
	provider := &captureProvider{}
	svc, _ := newAttachmentService(t, provider)
	current, _ := svc.NewSession("Current")
	referenced, _ := svc.NewSession("Referenced")
	messages := make([]contracts.ChatMessage, 0, 30)
	for index := 0; index < 30; index++ {
		messages = append(messages, contracts.ChatMessage{
			ID: fmt.Sprintf("m%d", index), Role: contracts.ChatRoleUser,
			Text: fmt.Sprintf("message %d %s", index, strings.Repeat("x", 500)),
		})
	}
	seedReadModel(t, svc, referenced, messages...)

	// Unknown and self references are ignored; the same reference appears once.
	prompt := "start @session:missing @session:" + current + " @session:" + referenced + " @session:" + referenced + " end"
	if err := svc.Send(current, prompt); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, current)
	text := lastUserMessage(t, provider.lastRequest()).Text()
	if strings.Contains(text, "missing") && strings.Contains(text, `[引用的会话 "missing"`) {
		t.Fatalf("unknown reference expanded: %q", text)
	}
	if count := strings.Count(text, "[引用的会话"); count != 1 {
		t.Fatalf("reference blocks = %d, want one deduplicated referenced session: %q", count, text)
	}
	contextEnd := strings.Index(text, "[引用结束]")
	if contextEnd < 0 {
		t.Fatalf("missing context footer: %q", text)
	}
	// The injected context is bounded; the original prompt still follows it.
	contextPart := []rune(text[:contextEnd])
	if len(contextPart) > maxReferenceContextRunes+32 {
		t.Fatalf("reference context = %d runes, want bounded near %d", len(contextPart), maxReferenceContextRunes)
	}
	if !strings.HasSuffix(strings.TrimSpace(text), "end") {
		t.Fatalf("original prompt missing after context: %q", text)
	}

	// A prompt without references is forwarded unchanged.
	if err := svc.Send(current, "plain prompt"); err != nil {
		t.Fatalf("Send plain: %v", err)
	}
	waitIdle(t, svc, current)
	if got := lastUserMessage(t, provider.lastRequest()).Text(); got != "plain prompt" {
		t.Fatalf("plain prompt = %q, want unchanged", got)
	}
}

// reasoningEffortRequests captures the reasoning_effort field from fake OpenAI
// request bodies.
type reasoningEffortRequests struct {
	mu     sync.Mutex
	values []string
}

func (r *reasoningEffortRequests) record(body []byte) {
	var payload struct {
		ReasoningEffort *string `json:"reasoning_effort"`
	}
	_ = json.Unmarshal(body, &payload)
	value := ""
	if payload.ReasoningEffort != nil {
		value = *payload.ReasoningEffort
	}
	r.mu.Lock()
	r.values = append(r.values, value)
	r.mu.Unlock()
}

func (r *reasoningEffortRequests) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.values...)
}

func newEffortSSEServer(t *testing.T, captured *reasoningEffortRequests) *httptest.Server {
	t.Helper()
	return newSSEServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.record(body)
		writeSSE(t, w,
			`{"choices":[{"delta":{"content":"effort ok"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		)
	})
}

func TestReasoningEffortFromSettingsAndConfig(t *testing.T) {
	captured := &reasoningEffortRequests{}
	server := newEffortSSEServer(t, captured)
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	})
	svc := newTestService(t, Config{}, settings, nil)
	id, err := svc.NewSession("effort")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := settings.Set(contracts.SessionEffortSettingKey(id), "high"); err != nil {
		t.Fatalf("Set effort: %v", err)
	}
	if err := svc.Send(id, "first"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	if got := captured.snapshot(); len(got) != 1 || got[0] != "high" {
		t.Fatalf("effort requests = %v, want high", got)
	}

	// Regenerate uses the same session effort.
	if err := svc.Regenerate(id); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	waitIdle(t, svc, id)
	if got := captured.snapshot(); len(got) != 2 || got[1] != "high" {
		t.Fatalf("regenerate effort requests = %v", got)
	}

	// An invalid per-session value is ignored (field omitted).
	if err := settings.Set(contracts.SessionEffortSettingKey(id), "bogus"); err != nil {
		t.Fatalf("Set invalid effort: %v", err)
	}
	if err := svc.Send(id, "second"); err != nil {
		t.Fatalf("Send second: %v", err)
	}
	waitIdle(t, svc, id)
	if got := captured.snapshot(); len(got) != 3 || got[2] != "" {
		t.Fatalf("invalid effort requests = %v, want omitted", got)
	}
}

func TestReasoningEffortConfigDefault(t *testing.T) {
	captured := &reasoningEffortRequests{}
	server := newEffortSSEServer(t, captured)
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	})
	svc := newTestService(t, Config{ReasoningEffort: "medium"}, settings, nil)
	id, err := svc.NewSession("effort-default")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	if got := captured.snapshot(); len(got) != 1 || got[0] != "medium" {
		t.Fatalf("config default effort = %v, want medium", got)
	}
}

func TestReasoningEffortReachesScriptedProviderExtra(t *testing.T) {
	provider := &captureProvider{}
	settings := newFakeSettings(nil)
	svc := newTestService(t, Config{}, settings, nil)
	var providerConfig contracts.ChatConfig
	svc.providerFactory = func(cfg contracts.ChatConfig) (rig.Provider, error) {
		providerConfig = cfg
		return provider, nil
	}
	id, err := svc.NewSession("effort-extra")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := settings.Set(contracts.SessionEffortSettingKey(id), "high"); err != nil {
		t.Fatalf("Set effort: %v", err)
	}
	if err := svc.Send(id, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	request := provider.lastRequest()
	if got, _ := request.Extra["reasoning_effort"].(string); got != "high" {
		t.Fatalf("request Extra[reasoning_effort] = %v, want high", request.Extra)
	}
	if providerConfig.ReasoningEffort != "high" {
		t.Fatalf("provider ChatConfig.ReasoningEffort = %q, want high", providerConfig.ReasoningEffort)
	}
}

func TestChatConfigCarriesReasoningEffortDefault(t *testing.T) {
	svc := newService(Config{ReasoningEffort: "medium"}, rigtool.NewRegistry(),
		contracts.SettingsService(newFakeSettings(nil)), &fakeLogger{}, nil, fakeApprovals{})
	if got := svc.Config().ReasoningEffort; got != "medium" {
		t.Fatalf("Config().ReasoningEffort = %q, want medium", got)
	}
	invalid := newService(Config{ReasoningEffort: "bogus"}, rigtool.NewRegistry(),
		contracts.SettingsService(newFakeSettings(nil)), &fakeLogger{}, nil, fakeApprovals{})
	if got := invalid.Config().ReasoningEffort; got != "" {
		t.Fatalf("invalid Config().ReasoningEffort = %q, want empty", got)
	}
}

// TestSessionReferenceBudgetEvent pins the typed budget classification written
// into the session_refs event when a referenced transcript is truncated.
func TestSessionReferenceBudgetEvent(t *testing.T) {
	provider := &captureProvider{}
	svc, store := newAttachmentService(t, provider)
	current, _ := svc.NewSession("Current")
	referenced, _ := svc.NewSession("Referenced")
	messages := make([]contracts.ChatMessage, 0, 30)
	for index := 0; index < 30; index++ {
		messages = append(messages, contracts.ChatMessage{
			ID: fmt.Sprintf("b%d", index), Role: contracts.ChatRoleUser,
			Text: fmt.Sprintf("short %d", index),
		})
	}
	seedReadModel(t, svc, referenced, messages...)
	if err := svc.Send(current, "compare @session:"+referenced); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, current)
	events, err := store.Events(context.Background(), current, 0, 0)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	payload := ""
	for _, event := range events {
		if event.Type == "session_refs" {
			payload = string(event.Payload)
		}
	}
	if !strings.Contains(payload, `"budget"`) || !strings.Contains(payload, `"kind":"messages"`) ||
		!strings.Contains(payload, `"used":10`) {
		t.Fatalf("session_refs payload = %q, want a messages budget classification", payload)
	}
}

// TestSessionReferenceRuneBudgetEvent covers the rune-cap truncation kind.
func TestSessionReferenceRuneBudgetEvent(t *testing.T) {
	provider := &captureProvider{}
	svc, store := newAttachmentService(t, provider)
	current, _ := svc.NewSession("Current")
	referenced, _ := svc.NewSession("Referenced")
	seedReadModel(t, svc, referenced, contracts.ChatMessage{
		ID: "r1", Role: contracts.ChatRoleUser, Text: strings.Repeat("x", maxReferenceContextRunes+2000),
	})
	if err := svc.Send(current, "long @session:"+referenced); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, current)
	events, err := store.Events(context.Background(), current, 0, 0)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	payload := ""
	for _, event := range events {
		if event.Type == "session_refs" {
			payload = string(event.Payload)
		}
	}
	if !strings.Contains(payload, `"budget"`) || !strings.Contains(payload, `"kind":"runes"`) {
		t.Fatalf("session_refs payload = %q, want a runes budget classification", payload)
	}
}
