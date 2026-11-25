package toolkit

// Regression tests for plan M2.5 batch A (G14 #6): one slow MCP server must not
// drag down a whole turn. A tool call that exceeds its server's per-call budget
// fails with a typed, readable error naming the server and the tool, and the
// timed-out call leaves the server usable for the next call.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// slowMCPHTTPServer is a minimal Streamable HTTP MCP server with one "nap" tool
// whose tools/call latency the test controls. Making the first call slow and the
// next one fast is how the "a timed-out call leaves the server usable" property
// is pinned without a product-side test hook.
type slowMCPHTTPServer struct {
	server *httptest.Server
	delay  atomic.Int64 // tools/call latency in nanoseconds
	calls  atomic.Int64 // tools/call requests received
}

// newSlowMCPHTTPServer starts the fake server with the given tools/call latency.
func newSlowMCPHTTPServer(t *testing.T, delay time.Duration) *slowMCPHTTPServer {
	t.Helper()
	fake := &slowMCPHTTPServer{}
	fake.setDelay(delay)
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
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
			result = map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "slow", "version": "1.0.0"},
			}
		case "ping":
			result = map[string]any{}
		case "tools/list":
			result = map[string]any{"tools": []any{oneTool("nap")}}
		case "tools/call":
			fake.calls.Add(1)
			if delay := time.Duration(fake.delay.Load()); delay > 0 {
				select {
				case <-time.After(delay):
				case <-r.Context().Done():
					// The caller gave up (its per-call budget expired): nothing
					// to answer, and the fake server stays up for the next call.
					return
				}
			}
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "pong"}}}
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

// setDelay changes the tools/call latency for later calls.
func (s *slowMCPHTTPServer) setDelay(delay time.Duration) {
	s.delay.Store(int64(delay))
}

// slowServerSettings encodes one HTTP MCP row, with timeoutMs only when non-zero
// (zero leaves the field unset so the documented default applies).
func slowServerSettings(t *testing.T, name, url string, timeoutMs int) *mutableSettings {
	t.Helper()
	row := fmt.Sprintf(`{"name":%q,"url":%q`, name, url)
	if timeoutMs > 0 {
		row += fmt.Sprintf(`,"timeoutMs":%d`, timeoutMs)
	}
	row += `}`
	return newMutableSettings(map[string]string{contracts.SettingMCPServers: `[` + row + `]`})
}

// TestMCPCallTimeoutFailsTheCallWithTypedError is the core regression: a tool
// call that outlives the row's timeoutMs returns *MCPCallTimeoutError naming the
// server and the tool, quickly, instead of blocking the turn.
func TestMCPCallTimeoutFailsTheCallWithTypedError(t *testing.T) {
	fake := newSlowMCPHTTPServer(t, 5*time.Second)
	settings := slowServerSettings(t, "sleepy", fake.server.URL, 120)
	tools, _ := activateMCPToolkit(t, map[string]any{"root": "."}, settings, nil)

	const toolName = "mcp__sleepy__nap"
	started := time.Now()
	_, err := tools.Invoke(context.Background(), rig.ToolCall{Name: toolName, Arguments: "{}"})
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("invoke returned no error, want a per-call timeout")
	}
	var timeoutErr *MCPCallTimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("error %v (%T) is not *MCPCallTimeoutError", err, err)
	}
	if timeoutErr.Server != "sleepy" {
		t.Fatalf("Server = %q, want the configured name %q", timeoutErr.Server, "sleepy")
	}
	if timeoutErr.Tool != toolName {
		t.Fatalf("Tool = %q, want the model-visible name %q", timeoutErr.Tool, toolName)
	}
	if timeoutErr.Timeout != 120*time.Millisecond {
		t.Fatalf("Timeout = %s, want the row's 120ms budget", timeoutErr.Timeout)
	}
	message := err.Error()
	for _, want := range []string{"sleepy", toolName, "120ms"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error text %q does not name %q", message, want)
		}
	}
	if elapsed > 2*time.Second {
		t.Fatalf("call took %s, want the 120ms budget to fire", elapsed)
	}
	// The typed error must stay distinguishable from a whole-run deadline: the
	// runtime classifies context.DeadlineExceeded as a run timeout.
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("timeout error unwraps to context.DeadlineExceeded, which the runtime treats as a run timeout")
	}
}

// TestMCPCallTimeoutKeepsServerUsableAfterExpiry pins that a timed-out call does
// not tear the server down: the very next call to the same server succeeds and
// the Plugins read model still reports it connected.
func TestMCPCallTimeoutKeepsServerUsableAfterExpiry(t *testing.T) {
	fake := newSlowMCPHTTPServer(t, 5*time.Second)
	settings := slowServerSettings(t, "sleepy", fake.server.URL, 120)
	tools, plugins := activateMCPToolkit(t, map[string]any{"root": "."}, settings, nil)

	const toolName = "mcp__sleepy__nap"
	if _, err := tools.Invoke(context.Background(), rig.ToolCall{Name: toolName, Arguments: "{}"}); err == nil {
		t.Fatal("first call returned no error, want a per-call timeout")
	}

	// Same registry, same tool, same server: the second call must work.
	fake.setDelay(0)
	result, err := tools.Invoke(context.Background(), rig.ToolCall{Name: toolName, Arguments: "{}"})
	if err != nil {
		t.Fatalf("call after a timed-out call failed: %v", err)
	}
	if result.IsError || !strings.Contains(result.Content, "pong") {
		t.Fatalf("call after a timed-out call = %+v, want the server's pong", result)
	}
	if got := fake.calls.Load(); got != 2 {
		t.Fatalf("tools/call requests = %d, want 2 (both calls reached the server)", got)
	}
	entries := plugins.MCP()
	if len(entries) != 1 {
		t.Fatalf("plugin entries = %+v, want one row", entries)
	}
	if !strings.Contains(entries[0].Status, statusConnected) {
		t.Fatalf("plugin status = %q, want the server still connected", entries[0].Status)
	}
	if entries[0].Tools != 1 {
		t.Fatalf("plugin tool count = %d, want 1 (the timeout must not unregister tools)", entries[0].Tools)
	}
}

// TestMCPCallTimeoutDefaultWhenServerSetsNone covers the documented default: a
// row without timeoutMs gets mcpDefaultCallTimeout, both for the tool wrapper and
// for the connection's own per-request timeout (so the transport cannot fire
// first with a bare context deadline).
func TestMCPCallTimeoutDefaultWhenServerSetsNone(t *testing.T) {
	if got := mcpServerCallTimeout(MCPServer{Name: "no-timeout"}); got != mcpDefaultCallTimeout {
		t.Fatalf("mcpServerCallTimeout(no row timeout) = %s, want the default %s", got, mcpDefaultCallTimeout)
	}
	if got := mcpServerCallTimeout(MCPServer{TimeoutMs: 250}); got != 250*time.Millisecond {
		t.Fatalf("mcpServerCallTimeout(timeoutMs=250) = %s, want 250ms (the row wins)", got)
	}
	// Negative rows fall back to the default instead of disabling the bound.
	if got := mcpServerCallTimeout(MCPServer{TimeoutMs: -1}); got != mcpDefaultCallTimeout {
		t.Fatalf("mcpServerCallTimeout(timeoutMs=-1) = %s, want the default", got)
	}
	if mcpDefaultCallTimeout <= 30*time.Second {
		t.Fatalf("default budget %s must exceed the Rig SDK per-request default (30s), else the transport fires an untyped deadline first", mcpDefaultCallTimeout)
	}
	for _, server := range []MCPServer{
		{Name: "http-default", URL: "http://127.0.0.1:1/mcp"},
		{Name: "stdio-default", Command: "/bin/true"},
	} {
		cfg, err := mcpClientConfig(server)
		if err != nil {
			t.Fatalf("mcpClientConfig(%q): %v", server.Name, err)
		}
		if cfg.Timeout != mcpDefaultCallTimeout {
			t.Fatalf("client config for %q has Timeout %s, want the same default %s", server.Name, cfg.Timeout, mcpDefaultCallTimeout)
		}
	}
	// A row that does set timeoutMs still wins on the connection too.
	cfg, err := mcpClientConfig(MCPServer{Name: "explicit", URL: "http://127.0.0.1:1/mcp", TimeoutMs: 250})
	if err != nil {
		t.Fatalf("mcpClientConfig(explicit): %v", err)
	}
	if cfg.Timeout != 250*time.Millisecond {
		t.Fatalf("client config for the explicit row has Timeout %s, want 250ms", cfg.Timeout)
	}
}

// TestMCPCallTimeoutReachesTheRegisteredTool proves the wrapper is installed with
// the resolved budget, not just computed: a row without timeoutMs registers the
// default, a row with timeoutMs registers its own value, and the server label is
// the configured name.
func TestMCPCallTimeoutReachesTheRegisteredTool(t *testing.T) {
	server := testMCPToolListServer(t, []map[string]any{oneTool("quick")})
	defer server.Close()
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"quick","url":"` + server.URL + `"}]`,
	})
	tools, _ := activateMCPToolkit(t, map[string]any{"root": "."}, settings, nil)
	wrapped := lookupRegistryTool(t, tools, "mcp__quick__quick")
	if timeoutTool, ok := wrapped.(*mcpCallTimeoutTool); ok {
		if timeoutTool.timeout != mcpDefaultCallTimeout {
			t.Fatalf("wrapper budget = %s, want the default %s", timeoutTool.timeout, mcpDefaultCallTimeout)
		}
		if timeoutTool.server != "quick" {
			t.Fatalf("wrapper server = %q, want %q", timeoutTool.server, "quick")
		}
	} else {
		t.Fatalf("registered MCP tool is %T, want the per-call timeout wrapper", wrapped)
	}
}

// passthroughTool is a rig.Tool whose Invoke returns a fixed result/error so the
// wrapper's error mapping can be tested without a connection.
type passthroughTool struct{ err error }

func (t *passthroughTool) Name() string           { return "mcp__fake__canned" }
func (t *passthroughTool) Description() string    { return "canned tool" }
func (t *passthroughTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *passthroughTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	if t.err != nil {
		return rig.ToolResult{}, t.err
	}
	return rig.ToolResult{Content: "canned"}, nil
}

// TestMCPCallTimeoutPassesOtherFailuresThrough guards the mapping: a genuine
// protocol/tool error must not be relabelled as a timeout, and a successful call
// keeps its result.
func TestMCPCallTimeoutPassesOtherFailuresThrough(t *testing.T) {
	boom := errors.New("mcp: boom")
	wrapped := withMCPCallTimeout(&passthroughTool{err: boom}, "fake", time.Second)
	_, err := wrapped.Invoke(context.Background(), rig.ToolCall{Name: wrapped.Name()})
	if !errors.Is(err, boom) {
		t.Fatalf("Invoke error = %v, want the inner boom unchanged", err)
	}
	var timeoutErr *MCPCallTimeoutError
	if errors.As(err, &timeoutErr) {
		t.Fatalf("plain protocol error %v was relabelled as a timeout", err)
	}

	ok := withMCPCallTimeout(&passthroughTool{}, "fake", time.Second)
	result, err := ok.Invoke(context.Background(), rig.ToolCall{Name: ok.Name()})
	if err != nil || result.Content != "canned" {
		t.Fatalf("Invoke = %+v, %v; want the inner result unchanged", result, err)
	}
}

// TestMCPCallTimeoutWrapperWithoutBudgetIsTransparent covers the defensive
// nil/non-positive budget paths.
func TestMCPCallTimeoutWrapperWithoutBudgetIsTransparent(t *testing.T) {
	inner := &passthroughTool{}
	if got := withMCPCallTimeout(inner, "fake", 0); got != rig.Tool(inner) {
		t.Fatalf("withMCPCallTimeout(budget 0) = %T, want the inner tool unwrapped", got)
	}
	if got := withMCPCallTimeout(nil, "fake", time.Second); got != nil {
		t.Fatalf("withMCPCallTimeout(nil) = %T, want nil", got)
	}
}

// lookupRegistryTool returns the registered tool with the given name, or nil.
func lookupRegistryTool(t *testing.T, registry *rigtool.Registry, name string) rig.Tool {
	t.Helper()
	for _, tool := range registry.Tools() {
		if tool.Name() == name {
			return tool
		}
	}
	return nil
}
