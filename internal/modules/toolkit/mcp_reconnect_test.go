package toolkit

// Regression tests for FR-22: an MCP connection that expired (HTTP session
// 404/410) or whose stdio child process exited is rebuilt on the next call and
// the Plugins read model reflects the recovery.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/tools/mcp"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// expiringMCPHTTPServer is a Streamable HTTP MCP server with one "echo" tool.
// expire() invalidates the currently issued session, which is how an MCP server
// reports a lost session (rig: mcp.ErrSessionExpired); a new initialize gets a
// fresh, valid session.
type expiringMCPHTTPServer struct {
	server     *httptest.Server
	initCalls  atomic.Int64
	mu         sync.Mutex
	sessionSeq int
	valid      map[string]bool
}

func newExpiringMCPHTTPServer(t *testing.T) *expiringMCPHTTPServer {
	t.Helper()
	fake := &expiringMCPHTTPServer{valid: make(map[string]bool)}
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
		if request.Method != "initialize" {
			fake.mu.Lock()
			sessionValid := fake.valid[r.Header.Get("Mcp-Session-Id")]
			fake.mu.Unlock()
			if !sessionValid {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte("session not found"))
				return
			}
		}
		var result any
		switch request.Method {
		case "initialize":
			fake.initCalls.Add(1)
			fake.mu.Lock()
			fake.sessionSeq++
			session := "session-" + strconv.Itoa(fake.sessionSeq)
			fake.valid[session] = true
			fake.mu.Unlock()
			w.Header().Set("Mcp-Session-Id", session)
			result = map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "flaky", "version": "1.0.0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []any{oneTool("echo")}}
		case "ping":
			result = map[string]any{}
		case "tools/call":
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

// expire invalidates every currently issued session.
func (f *expiringMCPHTTPServer) expire() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for session := range f.valid {
		f.valid[session] = false
	}
}

// TestToolkitMCPHTTPReconnectsAfterSessionExpiry is the FR-22 regression: a
// second call after the server lost the session must transparently reconnect
// instead of failing forever.
func TestToolkitMCPHTTPReconnectsAfterSessionExpiry(t *testing.T) {
	fake := newExpiringMCPHTTPServer(t)
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"flaky","url":"` + fake.server.URL + `"}]`,
	})
	tools, plugins := activateMCPToolkit(t, map[string]any{"root": "."}, settings, nil)

	first, err := tools.Invoke(context.Background(), rig.ToolCall{Name: "mcp__flaky__echo", Arguments: "{}"})
	if err != nil || first.Content != "pong" {
		t.Fatalf("first call = %q/%v, want pong", first.Content, err)
	}
	fake.expire()
	second, err := tools.Invoke(context.Background(), rig.ToolCall{Name: "mcp__flaky__echo", Arguments: "{}"})
	if err != nil || second.Content != "pong" {
		t.Fatalf("second call after session expiry = %q/%v, want a reconnect and pong", second.Content, err)
	}
	if got := fake.initCalls.Load(); got < 2 {
		t.Fatalf("initialize calls = %d, want a reconnect (>=2)", got)
	}
	if status := plugins.MCP()[0].Status; !strings.Contains(status, "已连接") {
		t.Fatalf("plugin status after reconnect = %q, want 已连接", status)
	}
}

// TestToolkitMCPRefreshReconnects covers the Plugins page recovery path: the
// read-model refresh reconnects a dead server before reporting its status.
func TestToolkitMCPRefreshReconnects(t *testing.T) {
	fake := newExpiringMCPHTTPServer(t)
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"flaky","url":"` + fake.server.URL + `"}]`,
	})
	_, plugins := activateMCPToolkit(t, map[string]any{"root": "."}, settings, nil)
	fake.expire()
	entries := plugins.Refresh(context.Background())
	if len(entries) != 1 || !strings.Contains(entries[0].Status, "已连接") {
		t.Fatalf("Refresh after expiry = %+v, want a reconnected server", entries)
	}
	if got := fake.initCalls.Load(); got < 2 {
		t.Fatalf("initialize calls = %d, want the refresh to reconnect", got)
	}
}

// restartingStdioServerPath writes a POSIX shell MCP server that dies on the
// first tools/call and serves normally afterwards (one file records spawns).
func restartingStdioServerPath(t *testing.T, counterPath string) string {
	t.Helper()
	script := `#!/bin/sh
countfile="$1"
count=$(cat "$countfile" 2>/dev/null || echo 0)
count=$((count+1))
printf '%s' "$count" > "$countfile"
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"initialize"'*)
      [ -n "$id" ] && printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"restarting","version":"1.0.0"}}}\n' "$id"
      ;;
    *'"method":"tools/list"'*)
      [ -n "$id" ] && printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"echo","description":"Echo","inputSchema":{"type":"object"}}]}}\n' "$id"
      ;;
    *'"method":"tools/call"'*)
      if [ "$count" -lt 2 ]; then exit 1; fi
      [ -n "$id" ] && printf '{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"pong"}]}}\n' "$id"
      ;;
  esac
done
`
	path := filepath.Join(t.TempDir(), "restarting-mcp.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stdio server: %v", err)
	}
	return path
}

// TestToolkitMCPStdioRestartsAfterProcessExit is the stdio half of FR-22: a
// dead child process is respawned and the tool call retried.
func TestToolkitMCPStdioRestartsAfterProcessExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script MCP stdio helper is POSIX-only")
	}
	counter := filepath.Join(t.TempDir(), "spawn-count")
	command := restartingStdioServerPath(t, counter)
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"local","transport":"stdio","command":"` + command + `","args":["` + counter + `"]}]`,
	})
	tools, plugins := activateMCPToolkit(t, map[string]any{"root": "."}, settings, nil)

	result, err := tools.Invoke(context.Background(), rig.ToolCall{Name: "mcp__local__echo", Arguments: "{}"})
	if err != nil || result.Content != "pong" {
		t.Fatalf("call after child exit = %q/%v, want a respawn and pong", result.Content, err)
	}
	raw, _ := os.ReadFile(counter)
	if strings.TrimSpace(string(raw)) != "2" {
		t.Fatalf("stdio server spawn count = %q, want 2 (initial + restart)", strings.TrimSpace(string(raw)))
	}
	if status := plugins.MCP()[0].Status; !strings.Contains(status, "已连接") {
		t.Fatalf("plugin status after stdio restart = %q, want 已连接", status)
	}
}

// TestIsRetryableMCPError pins which transport errors trigger a reconnect.
func TestIsRetryableMCPError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("mcp: unknown tool"), false},
		{mcp.ErrClosed, true},
		{mcp.ErrSessionExpired, true},
		{errors.Join(errors.New("context"), mcp.ErrSessionExpired), true},
		{errors.New("mcp: server process exited: exit status 1"), true},
		{errors.New("mcp: server closed its stdout"), true},
		{errors.New("mcp: write to server: broken pipe"), true},
	}
	for _, tc := range cases {
		if got := isRetryableMCPError(tc.err); got != tc.want {
			t.Fatalf("isRetryableMCPError(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
