package gateway

// End-to-end harness for the migrated regression coverage (S4 preparation).
//
// internal/regression is one Go package whose shared helpers import uiroot, so
// S4 deletes it wholesale — taking ~9k lines of *backend* coverage with it
// (classification: 34 backend-only / 33 UI-driven / 2 mixed / 2 helper files;
// see the migration table in the task report). These tests re-establish the four
// behaviours that must survive, at the transport boundary: a real kernel root,
// the real backend modules, a scripted OpenAI-compatible model, and the gateway's
// own protocol (WS + SSE) as the client.
//
// The mapping from the deleted files:
//
//	internal/regression/permission_test.go + approvals_test.go
//	    -> e2e_approval_test.go   (request -> delta -> resolve -> tool result)
//	internal/regression/agent_harness_test.go + usage_test.go
//	    -> e2e_agent_test.go      (start -> streaming -> done / aborted)
//	internal/regression/automation_history_test.go + automation_model_test.go
//	    -> e2e_automation_test.go (create -> run now -> session + history)
//	internal/regression/acp_session_test.go
//	    -> e2e_acp_test.go        (ACP stdio round over the real frame loop)

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/hosttools"
	"github.com/zeokeel/zeokeel/internal/modules/acp"
	"github.com/zeokeel/zeokeel/internal/modules/agent"
	"github.com/zeokeel/zeokeel/internal/modules/approvals"
	"github.com/zeokeel/zeokeel/internal/modules/automation"
	hosttoolsmod "github.com/zeokeel/zeokeel/internal/modules/hosttools"
	"github.com/zeokeel/zeokeel/internal/modules/sessionstore"
)

// --- module activation (the assembly loader's seam) ------------------------

// provideValue provides one service on a test root.
func provideValue[T any](t *testing.T, ctx cordis.Context, key cordis.ServiceKey[T], value T) {
	t.Helper()
	if _, err := ctx.Provide(key, value); err != nil {
		t.Fatalf("provide %s: %v", key.Name(), err)
	}
}

// requireValue reads one provided service.
func requireValue[T any](t *testing.T, ctx cordis.Context, key cordis.ServiceKey[T]) T {
	t.Helper()
	value, err := ctx.Require(key)
	if err != nil {
		t.Fatalf("require %s: %v", key.Name(), err)
	}
	return value
}

// descriptorPlugin adapts a module.Descriptor to the kernel plugin interfaces,
// exactly like module.load does inside the assembly loader.
type descriptorPlugin[T any] struct {
	descriptor *module.Descriptor[T]
	config     T
	inject     []string
}

// Name implements the kernel plugin identity.
func (p *descriptorPlugin[T]) Name() string { return p.descriptor.ID }

// Inject implements the kernel plugin dependency list.
func (p *descriptorPlugin[T]) Inject() []string { return p.inject }

// Activate implements the kernel plugin activation.
func (p *descriptorPlugin[T]) Activate(ctx cordis.Context, _ cordis.Value) error {
	return p.descriptor.Factory(ctx, p.config)
}

// activateModule starts one module like the assembly would and fails the test
// unless it reaches Active.
func activateModule[T any](t *testing.T, ctx cordis.Context, descriptor *module.Descriptor[T], config T) {
	t.Helper()
	plugin := &descriptorPlugin[T]{descriptor: descriptor, config: config}
	for _, requirement := range descriptor.Requires {
		if !requirement.Optional {
			plugin.inject = append(plugin.inject, requirement.Name)
		}
	}
	fiber, err := ctx.Plugin(plugin, nil)
	if err != nil {
		t.Fatalf("activate %s: %v", descriptor.ID, err)
	}
	if state := fiber.State(); state != cordis.Active {
		t.Fatalf("module %s state = %s (%v), want Active", descriptor.ID, state, fiber.ErrorDetail())
	}
}

// --- scripted model server -------------------------------------------------

// modelCall is one captured provider request.
type modelCall struct {
	Method string
	Path   string
	Body   []byte
}

// e2eModel is an OpenAI-compatible streaming endpoint: every request body is
// recorded and respond decides the payload.
type e2eModel struct {
	mu      sync.Mutex
	calls   []modelCall
	respond func(body []byte, call int) (int, string)
	server  *httptest.Server
}

// newE2EModel starts the scripted model server.
func newE2EModel(t *testing.T, respond func(body []byte, call int) (int, string)) *e2eModel {
	t.Helper()
	model := &e2eModel{respond: respond}
	model.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)
		model.mu.Lock()
		model.calls = append(model.calls, modelCall{Method: r.Method, Path: r.URL.Path, Body: []byte(raw)})
		index := len(model.calls) - 1
		respondFn := model.respond
		model.mu.Unlock()
		status, payload := respondFn([]byte(raw), index)
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"scripted model failure","type":"e2e"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(model.server.Close)
	return model
}

// url is the base URL the agent module dials.
func (m *e2eModel) url() string { return m.server.URL }

// callCount is how many provider requests arrived so far.
func (m *e2eModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// lastBody is the most recent provider request body.
func (m *e2eModel) lastBody() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		return nil
	}
	return append([]byte(nil), m.calls[len(m.calls)-1].Body...)
}

// sseAnswer renders one streaming completion carrying text.
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

// sseToolCall renders one streaming completion requesting a tool call.
func sseToolCall(name, arguments string) string {
	return fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,"+
		"\"id\":\"call-e2e\",\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%q}}]},"+
		"\"index\":0,\"finish_reason\":\"tool_calls\"}]}\n\n", name, arguments) +
		"data: [DONE]\n\n"
}

// requestContent extracts one role's concatenated content from a captured
// OpenAI chat-completions request body.
func requestContent(body []byte, role string) string {
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
			content += text
			continue
		}
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

// --- the rig ---------------------------------------------------------------

// e2eRig is one gateway served over a real kernel root with the real backend
// modules and a scripted model.
type e2eRig struct {
	t        *testing.T
	root     cordis.Context
	settings *fakeSettings
	gateway  *Gateway
	served   *runningGateway
	model    *e2eModel
	registry *rigtool.Registry
	chat     contracts.ChatService
	conn     *testWSConn
}

// e2eOptions configures the rig.
type e2eOptions struct {
	// approvalMode is the approvals assembly config ("workspace" by default).
	approvalMode string
	// tools are extra tools registered in the shared toolkit registry.
	tools []rig.Tool
	// activateAutomation starts the automation module too.
	activateAutomation bool
	// activateACP starts the ACP module too.
	activateACP bool
	// activateHostTools registers the rig host tools (ask_user, plan_review,
	// exit_plan_mode, the goal trio) the way the serve assembly does: they are
	// what the plan-review and ask surfaces are driven through.
	activateHostTools bool
}

// newE2ERig boots the backend modules on a kernel root, provides the gateway's
// host services, and serves the gateway on a kernel-assigned port.
func newE2ERig(t *testing.T, respond func(body []byte, call int) (int, string), opts e2eOptions) *e2eRig {
	t.Helper()
	if respond == nil {
		respond = func([]byte, int) (int, string) { return 0, sseAnswer("ok") }
	}
	if opts.approvalMode == "" {
		opts.approvalMode = contracts.PermissionWorkspace
	}
	model := newE2EModel(t, respond)
	settings := newFakeSettings(map[string]string{
		contracts.SettingsChatBaseURL:    model.url(),
		contracts.SettingsChatModel:      "e2e-model",
		contracts.SettingsChatAPIKey:     "e2e-key",
		contracts.SettingAgentPermission: opts.approvalMode,
	})
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	provideValue(t, root, contracts.KeySettings, contracts.SettingsService(settings))
	provideValue(t, root, contracts.KeyLogger, contracts.Logger(testLogger{t: t}))
	provideValue(t, root, contracts.KeyDataDir, t.TempDir())
	registry := rigtool.NewRegistry(opts.tools...)
	provideValue(t, root, hosttools.KeyRegistry, registry)

	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Provide(root); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	// The same module set the serve product loads (minus the UI-free extras).
	activateModule(t, root, sessionstore.Module(), sessionstore.Config{})
	if opts.activateHostTools {
		// Registers the host tools into the registry provided above, binding them
		// to the gateway's UserInteraction/PlanReviewer/ArtifactPresenter.
		activateModule(t, root, hosttoolsmod.Module(), hosttoolsmod.Config{Interactive: true})
	}
	activateModule(t, root, approvals.Module(), approvals.Config{Mode: opts.approvalMode})
	activateModule(t, root, agent.Module(), agent.Config{Model: "e2e-model", Memory: false})
	if opts.activateAutomation {
		activateModule(t, root, automation.Module(), automation.Config{TickMs: 3_600_000})
	}
	if opts.activateACP {
		activateModule(t, root, acp.Module(), acp.Config{})
	}
	served := serveGateway(t, gateway)
	rig := &e2eRig{
		t:        t,
		root:     root,
		settings: settings,
		gateway:  gateway,
		served:   served,
		model:    model,
		registry: registry,
		chat:     requireValue(t, root, contracts.KeyChat),
	}
	t.Cleanup(func() { rig.close() })
	return rig
}

// close stops the served gateway.
func (r *e2eRig) close() {
	if r.conn != nil {
		r.conn.close()
		r.conn = nil
	}
	r.served.stop(r.t)
}

// connect opens the control-plane connection and completes the handshake.
func (r *e2eRig) connect() *testWSConn {
	r.t.Helper()
	if r.conn != nil {
		return r.conn
	}
	r.conn = dialTestWS(r.t, r.served.address, map[string]string{
		"Origin":        r.served.info.Origin(),
		"Authorization": "Bearer " + r.served.info.Token,
	})
	resultOf(r.t, r.conn.call("initialize", map[string]any{"protocolVersion": ProtocolVersion}))
	resultOf(r.t, r.conn.call("ready", nil))
	return r.conn
}

// newSession creates one conversation through the backend service.
func (r *e2eRig) newSession(title string) string {
	r.t.Helper()
	id, err := r.chat.NewSession(title)
	if err != nil {
		r.t.Fatalf("NewSession: %v", err)
	}
	return id
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// approvalProbe is a tool whose job is to be approved: its name is not in the
// workspace auto-allow list, so the agent's gate asks the user.
type approvalProbe struct {
	mu    sync.Mutex
	calls []string
}

// Name implements rig.Tool.
func (p *approvalProbe) Name() string { return "e2e_probe" }

// Description implements rig.Tool.
func (p *approvalProbe) Description() string {
	return "e2e probe tool used to exercise the approval gate"
}

// Schema implements rig.Tool.
func (p *approvalProbe) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"note": map[string]any{"type": "string"}},
	}
}

// Invoke implements rig.Tool.
func (p *approvalProbe) Invoke(_ context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	p.mu.Lock()
	p.calls = append(p.calls, call.Arguments)
	p.mu.Unlock()
	return rig.ToolResult{Content: "probe ran"}, nil
}

// callCount reports how often the tool ran.
func (p *approvalProbe) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}
