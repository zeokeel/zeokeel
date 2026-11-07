package regression

// Harness for the new agent-side chains (team orchestration, @session
// references and reasoning effort): a real kernel root with sessionstore +
// approvals + agent activated, a scripted OpenAI-compatible model server that
// captures every provider request body, and the shared toolkit registry.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/agent"
	"github.com/zeokeel/zeokeel/internal/modules/approvals"
	"github.com/zeokeel/zeokeel/internal/modules/sessionstore"
)

// modelCall is one provider request captured by the scripted model server.
type modelCall struct {
	Method string
	Path   string
	Body   []byte
}

// fakeModel is an OpenAI-compatible streaming endpoint: every request body is
// recorded, and respond decides the status and SSE payload. It is safe for
// concurrent runs (several teammates may call it at once).
type fakeModel struct {
	mu      sync.Mutex
	calls   []modelCall
	respond func(body []byte) (status int, payload string)
	server  *httptest.Server
}

func newFakeModel(t *testing.T, respond func(body []byte) (int, string)) *fakeModel {
	t.Helper()
	model := &fakeModel{respond: respond}
	model.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		decoder := json.NewDecoder(r.Body)
		_ = decoder.Decode(&raw)
		model.mu.Lock()
		model.calls = append(model.calls, modelCall{Method: r.Method, Path: r.URL.Path, Body: []byte(raw)})
		respond := model.respond
		model.mu.Unlock()
		status, payload := respond([]byte(raw))
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"scripted model failure","type":"audit"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(model.server.Close)
	return model
}

func (m *fakeModel) url() string { return m.server.URL }

// callsSnapshot returns the captured provider requests in order.
func (m *fakeModel) callsSnapshot() []modelCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]modelCall(nil), m.calls...)
}

// lastBody returns the most recent request body.
func (m *fakeModel) lastBody() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		return nil
	}
	return append([]byte(nil), m.calls[len(m.calls)-1].Body...)
}

// sseAnswer renders one successful streaming completion carrying text.
func sseAnswer(text string) string {
	encoded, err := json.Marshal(text)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf(
		"data: {\"choices\":[{\"delta\":{\"content\":%s},\"index\":0}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{},\"index\":0,\"finish_reason\":\"stop\"}]}\n\n"+
			"data: [DONE]\n\n", encoded)
}

// decodeRequestContent extracts a role's concatenated content from one captured
// OpenAI chat-completions request body (safe outside the test goroutine too).
func decodeRequestContent(body []byte, role string) string {
	var payload struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	var content string
	for _, message := range payload.Messages {
		if message.Role != role {
			continue
		}
		var text string
		if err := json.Unmarshal(message.Content, &text); err == nil {
			content = text
			continue
		}
		// Multimodal content arrays: concatenate the text parts.
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(message.Content, &parts); err != nil {
			continue
		}
		for _, part := range parts {
			if part.Type == "text" {
				content += part.Text
			}
		}
	}
	return content
}

// requestContent is decodeRequestContent for the test goroutine.
func requestContent(t *testing.T, body []byte, role string) string {
	t.Helper()
	return decodeRequestContent(body, role)
}

// requestModel reads the model name of one captured request.
func requestModel(t *testing.T, body []byte) string {
	t.Helper()
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode provider request body: %v", err)
	}
	return payload.Model
}

// requestReasoningEffort reads the top-level reasoning_effort of one captured
// request; found is false when the provider omitted the field.
func requestReasoningEffort(t *testing.T, body []byte) (effort string, found bool) {
	t.Helper()
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode provider request body: %v", err)
	}
	raw, ok := payload["reasoning_effort"]
	if !ok {
		return "", false
	}
	if err := json.Unmarshal(raw, &effort); err != nil {
		t.Fatalf("decode reasoning_effort: %v", err)
	}
	return effort, true
}

// agentHarness bundles the services the new chain tests drive.
type agentHarness struct {
	h        *harness
	chat     contracts.ChatService
	team     contracts.TeamService
	registry *rigtool.Registry
	model    *fakeModel
}

// startAgent boots sessionstore, approvals (auto mode, so tools never block) and
// the agent module on the host services, with the scripted model server as the
// configured provider.
func startAgent(t *testing.T, agentConfig agent.Config, respond func(body []byte) (int, string)) *agentHarness {
	t.Helper()
	if respond == nil {
		respond = func([]byte) (int, string) { return 0, sseAnswer("ok") }
	}
	return startAgentWith(t, agentConfig, newFakeModel(t, respond))
}

// startAgentWith is startAgent with a caller-provided model server and tools
// pre-registered in the shared toolkit registry (to force name-conflict paths).
func startAgentWith(t *testing.T, agentConfig agent.Config, model *fakeModel, seed ...rig.Tool) *agentHarness {
	t.Helper()
	h := newHarness(t)
	if err := h.settings.Set(contracts.SettingsChatBaseURL, model.url()); err != nil {
		t.Fatalf("set chat base URL: %v", err)
	}
	if err := h.settings.Set(contracts.SettingsChatModel, "audit-model"); err != nil {
		t.Fatalf("set chat model: %v", err)
	}
	if err := h.settings.Set(contracts.SettingsChatAPIKey, "audit-key"); err != nil {
		t.Fatalf("set chat api key: %v", err)
	}

	registry := rigtool.NewRegistry(seed...)
	provide(t, h.ctx, contracts.KeyToolkit, registry)
	activate(t, h.ctx, sessionstore.Module(), sessionstore.Config{})
	activate(t, h.ctx, approvals.Module(), approvals.Config{Mode: approvals.ModeAuto})
	activate(t, h.ctx, agent.Module(), agentConfig)
	return &agentHarness{
		h:        h,
		chat:     requireService(t, h.ctx, contracts.KeyChat),
		team:     requireService(t, h.ctx, contracts.KeyTeam),
		registry: registry,
		model:    model,
	}
}

// newChatSession creates one chat session.
func (a *agentHarness) newChatSession(t *testing.T, title string) string {
	t.Helper()
	sessionID, err := a.chat.NewSession(title)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return sessionID
}

// waitIdle polls the chat read model until the session's run finished.
func (a *agentHarness) waitIdle(t *testing.T, sessionID string) contracts.ChatSession {
	t.Helper()
	var last contracts.ChatSession
	waitFor(t, "session "+sessionID+" run to finish", func() bool {
		chatSession, ok := a.chat.Session(sessionID)
		if !ok {
			return false
		}
		last = chatSession
		return !chatSession.Running
	})
	return last
}

// sendAndWait sends one prompt and waits for the asynchronous run to finish.
func (a *agentHarness) sendAndWait(t *testing.T, sessionID, prompt string) contracts.ChatSession {
	t.Helper()
	before := len(a.model.callsSnapshot())
	if err := a.chat.Send(sessionID, prompt); err != nil {
		t.Fatalf("Send(%q): %v", prompt, err)
	}
	chatSession := a.waitIdle(t, sessionID)
	if after := len(a.model.callsSnapshot()); after <= before {
		t.Fatalf("the run for %q made no provider request (%d -> %d calls)", prompt, before, after)
	}
	return chatSession
}

// bodyAfter returns the captured request bodies created after index.
func (a *agentHarness) bodiesAfter(index int) []modelCall {
	calls := a.model.callsSnapshot()
	if index >= len(calls) {
		return nil
	}
	return calls[index:]
}

// fakeRigTool is a marker rig.Tool used to force registry name conflicts.
type fakeRigTool struct{ name string }

func (t fakeRigTool) Name() string           { return t.name }
func (t fakeRigTool) Description() string    { return "audit marker tool" }
func (t fakeRigTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t fakeRigTool) Invoke(_ context.Context, _ rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{}, nil
}
