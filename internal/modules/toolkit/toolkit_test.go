package toolkit

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestModuleProvidesRigToolset pins the product/SDK seam: the toolkit module
// builds the registry from the published ZeoKeel Rig toolset and provides it as
// a kernel service.
func TestModuleProvidesRigToolset(t *testing.T) {
	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()

	if _, err := root.Provide(contracts.KeySettings, contracts.SettingsService(testSettings{})); err != nil {
		t.Fatalf("provide settings: %v", err)
	}

	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID:     "toolkit",
		Module: "zeokeel/toolkit",
		Config: map[string]any{"root": ".", "shellTimeoutMs": 5000},
	}})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer func() { _ = loader.Close() }()

	tools, err := root.Require(contracts.KeyToolkit)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}
	specs := tools.Specs()
	if len(specs) < 25 {
		t.Fatalf("registry tools = %d, want at least 25 (builtin/editor/image/terminal/jobs/misc/memory/schedule/worktree)", len(specs))
	}
	names := make(map[string]bool, len(specs))
	for _, spec := range specs {
		names[spec.Name] = true
	}
	for _, want := range []string{
		"bash", "read_file", "str_replace_editor", "read_image",
		"terminal_open", "terminal_list", "job_start", "working_directory", "todo_write",
		"memory", "schedule_create", "enter_worktree", "settings_get",
	} {
		if !names[want] {
			t.Fatalf("registry lacks tool %q (have %v)", want, names)
		}
	}
}

// TestModuleWiresRemoteMCPServer proves the remote-MCP half of the v0.5
// toolset wiring: a configured MCP server is started over Streamable HTTP at
// activation and its tools appear in the shared registry.
func TestModuleWiresRemoteMCPServer(t *testing.T) {
	server := testMCPHTTPServer(t)
	defer server.Close()

	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()

	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID:     "toolkit",
		Module: "zeokeel/toolkit",
		Config: map[string]any{
			"root": ".",
			"mcpServers": []any{
				map[string]any{"name": "test", "url": server.URL},
			},
		},
	}})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer func() { _ = loader.Close() }()

	tools, err := root.Require(contracts.KeyToolkit)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}
	found := false
	for _, spec := range tools.Specs() {
		if strings.Contains(spec.Name, "echo") {
			found = true
		}
	}
	if !found {
		t.Fatalf("registry lacks the MCP echo tool: %v", tools.Specs())
	}
}

// testMCPHTTPServer is a minimal MCP server over Streamable HTTP: initialize,
// notifications/initialized, tools/list and tools/call for one "echo" tool.
func testMCPHTTPServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("server: bad request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-1")
			result = map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "test", "version": "1.0.0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []any{
				map[string]any{
					"name":        "echo",
					"description": "Echo the input",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
				},
			}}
		case "tools/call":
			result = map[string]any{"content": []any{
				map[string]any{"type": "text", "text": "pong"},
			}}
		default:
			t.Errorf("server: unexpected method %q", request.Method)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
}

// testSettings is the host settings seam used by the toolkit tests.
type testSettings struct{}

func (testSettings) Get(key, def string) string  { return def }
func (testSettings) Set(key, value string) error { return nil }
func (testSettings) All() map[string]string      { return nil }
