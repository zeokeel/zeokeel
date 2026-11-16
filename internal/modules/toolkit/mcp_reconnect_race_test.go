package toolkit

// Hot-reload race regressions (2026-11 audit): a tool call on the OLD MCP
// runtime can be inside a slow reconnect (session expired → re-initialize)
// while the user edits connectors and the controller reloads. The stopped
// runtime's in-flight reconnect must then
//
//   - never overwrite the reloaded Plugins read model (identity/status/client),
//   - never unregister the NEW runtime's tools or resurrect disabled ones,
//   - close the connection it just built instead of leaking it.
//
// The serial-reconnect control at the bottom proves the harness itself is
// healthy: without a concurrent reload the same reconnect path works.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// raceMCPServer is a controllable Streamable-HTTP MCP endpoint: the second and
// later initialize (a reconnect) can be slowed down so a reload lands mid
// handshake; sessions can be expired to force the reconnect; the tool list and
// call answer are mutable; pings and session DELETEs (client Close) are counted.
type raceMCPServer struct {
	http       *httptest.Server
	name       string
	version    string
	initDelay  time.Duration
	reinitSeen chan struct{}
	reinitOnce sync.Once
	initCalls  atomic.Int64
	pings      atomic.Int64

	mu          sync.Mutex
	seq         int
	valid       map[string]bool
	deleted     map[string]bool
	tools       []string
	pong        string
	lastSession string
}

func newRaceMCPServer(t *testing.T, name, version string, delay time.Duration, tools []string, pong string) *raceMCPServer {
	t.Helper()
	s := &raceMCPServer{
		name: name, version: version, initDelay: delay,
		reinitSeen: make(chan struct{}),
		valid:      map[string]bool{}, deleted: map[string]bool{},
		tools: tools, pong: pong,
	}
	s.http = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			s.mu.Lock()
			s.deleted[r.Header.Get("Mcp-Session-Id")] = true
			s.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if req.Method == "initialize" {
			n := s.initCalls.Add(1)
			if n > 1 && s.initDelay > 0 {
				s.reinitOnce.Do(func() { close(s.reinitSeen) })
				time.Sleep(s.initDelay)
			}
			s.mu.Lock()
			s.seq++
			session := "race-s-" + strconv.Itoa(s.seq)
			s.valid[session] = true
			s.lastSession = session
			s.mu.Unlock()
			w.Header().Set("Mcp-Session-Id", session)
			raceMCPResult(w, req.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": s.name, "version": s.version},
			})
			return
		}
		s.mu.Lock()
		ok := s.valid[r.Header.Get("Mcp-Session-Id")]
		s.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("session not found"))
			return
		}
		switch req.Method {
		case "tools/list":
			s.mu.Lock()
			list := make([]any, 0, len(s.tools))
			for _, name := range s.tools {
				list = append(list, oneTool(name))
			}
			s.mu.Unlock()
			raceMCPResult(w, req.ID, map[string]any{"tools": list})
		case "tools/call":
			s.mu.Lock()
			pong := s.pong
			s.mu.Unlock()
			raceMCPResult(w, req.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": pong}}})
		case "ping":
			s.pings.Add(1)
			raceMCPResult(w, req.ID, map[string]any{})
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(s.http.Close)
	return s
}

func raceMCPResult(w http.ResponseWriter, id any, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

// expire invalidates every live session so the next tool call fails with a
// retryable session-expired error and triggers the reconnect path.
func (s *raceMCPServer) expire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.valid {
		s.valid[k] = false
	}
}

func (s *raceMCPServer) setTools(tools []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools = tools
}

// sessionDeleted reports whether the given session saw a client Close (DELETE).
func (s *raceMCPServer) sessionDeleted(session string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleted[session]
}

func (s *raceMCPServer) reconnectSession() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSession
}

// triggerStaleReconnect forces the audit interleaving: a tool call starts a
// reconnect against the old server and blocks in its slow re-initialize; while
// it is in flight, settings switch to newURL and the controller reloads. It
// returns a channel closed when the stale call settles. mutate runs right
// before the reload (for example to disable a tool in the new config).
func triggerStaleReconnect(t *testing.T, old *raceMCPServer, settings *mutableSettings,
	newURL string, reloader contracts.MCPReloader, registry interface {
		Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error)
	}, mutate func()) chan struct{} {
	t.Helper()
	old.expire()
	callDone := make(chan struct{})
	go func() {
		defer close(callDone)
		_, _ = registry.Invoke(context.Background(), rig.ToolCall{Name: "mcp__flaky__echo", Arguments: "{}"})
	}()
	select {
	case <-old.reinitSeen:
	case <-time.After(3 * time.Second):
		t.Fatal("reconnect never reached the old server's initialize")
	}
	if mutate != nil {
		mutate()
	}
	if err := settings.Set(contracts.SettingMCPServers, `[{"name":"flaky","url":"`+newURL+`"}]`); err != nil {
		t.Fatal(err)
	}
	if _, err := reloader.Reload(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	return callDone
}

func waitStaleCall(t *testing.T, done chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stale reconnect call never settled")
	}
	time.Sleep(100 * time.Millisecond)
}

// TestMCPStaleReconnectKeepsReloadedReadModel: after the reload installed the
// new server identity, the old runtime's late reconnect must not overwrite the
// row, and a later Refresh must ping the NEW connection only.
func TestMCPStaleReconnectKeepsReloadedReadModel(t *testing.T) {
	old := newRaceMCPServer(t, "old-server", "1.0.0", 800*time.Millisecond, []string{"echo"}, "pong-old")
	fresh := newRaceMCPServer(t, "new-server", "2.0.0", 0, []string{"echo"}, "pong-new")
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"flaky","url":"` + old.http.URL + `"}]`,
	})
	_, registry, plugins, reloader := activateMCPToolkitWithRoot(t, map[string]any{"root": "."}, settings)
	first, err := registry.Invoke(context.Background(), rig.ToolCall{Name: "mcp__flaky__echo", Arguments: "{}"})
	if err != nil || first.Content != "pong-old" {
		t.Fatalf("first call = %q/%v", first.Content, err)
	}

	callDone := triggerStaleReconnect(t, old, settings, fresh.http.URL, reloader, registry, nil)
	afterReload := plugins.MCP()
	if len(afterReload) != 1 || afterReload[0].Version != "2.0.0" {
		t.Fatalf("reload did not install the new identity: %+v", afterReload)
	}
	waitStaleCall(t, callDone)

	final := plugins.MCP()
	if len(final) != 1 || final[0].Version != "2.0.0" || final[0].Status != statusConnected {
		t.Errorf("stale reconnect overwrote the reloaded read model: %+v (want version 2.0.0, connected)", final)
	}
	oldPings, newPings := old.pings.Load(), fresh.pings.Load()
	plugins.Refresh(context.Background())
	if got := old.pings.Load() - oldPings; got != 0 {
		t.Errorf("Refresh pinged the OLD server %d time(s): the read model still holds the leaked old client", got)
	}
	if got := fresh.pings.Load() - newPings; got == 0 {
		t.Errorf("Refresh never pinged the new server: entry.client was not the reloaded connection")
	}
}

// TestMCPStaleReconnectKeepsNewToolRegistration: the old server's re-handshake
// tool list no longer contains echo; the stale reconnect must not unregister
// the NEW runtime's echo registration.
func TestMCPStaleReconnectKeepsNewToolRegistration(t *testing.T) {
	old := newRaceMCPServer(t, "old-server", "1.0.0", 800*time.Millisecond, []string{"echo"}, "pong-old")
	fresh := newRaceMCPServer(t, "new-server", "2.0.0", 0, []string{"echo"}, "pong-new")
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"flaky","url":"` + old.http.URL + `"}]`,
	})
	_, registry, _, reloader := activateMCPToolkitWithRoot(t, map[string]any{"root": "."}, settings)
	if _, err := registry.Invoke(context.Background(), rig.ToolCall{Name: "mcp__flaky__echo", Arguments: "{}"}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	old.setTools(nil) // the stale re-handshake will report no tools
	callDone := triggerStaleReconnect(t, old, settings, fresh.http.URL, reloader, registry, nil)
	if !registryHasTool(registry, "mcp__flaky__echo") {
		t.Fatal("reload itself lost the new runtime's echo registration")
	}
	waitStaleCall(t, callDone)
	if !registryHasTool(registry, "mcp__flaky__echo") {
		t.Fatalf("stale reconnect unregistered the NEW runtime's echo although the new server provides it")
	}
	result, err := registry.Invoke(context.Background(), rig.ToolCall{Name: "mcp__flaky__echo", Arguments: "{}"})
	if err != nil || result.Content != "pong-new" {
		t.Errorf("echo after the race = %q/%v, want pong-new (routed to the new server)", result.Content, err)
	}
}

// TestMCPStaleReconnectDoesNotResurrectDisabledTool: the new config disables
// extra; the stale reconnect (whose old server now lists extra) must not
// re-register it — the disable switch must survive the race.
func TestMCPStaleReconnectDoesNotResurrectDisabledTool(t *testing.T) {
	old := newRaceMCPServer(t, "old-server", "1.0.0", 800*time.Millisecond, []string{"echo"}, "pong-old")
	fresh := newRaceMCPServer(t, "new-server", "2.0.0", 0, []string{"echo", "extra"}, "pong-new")
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"flaky","url":"` + old.http.URL + `"}]`,
	})
	_, registry, _, reloader := activateMCPToolkitWithRoot(t, map[string]any{"root": "."}, settings)
	if _, err := registry.Invoke(context.Background(), rig.ToolCall{Name: "mcp__flaky__echo", Arguments: "{}"}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	old.setTools([]string{"echo", "extra"})
	callDone := triggerStaleReconnect(t, old, settings, fresh.http.URL, reloader, registry, func() {
		encoded, err := contracts.EncodeStringListJSON([]string{"mcp__flaky__extra"})
		if err != nil {
			t.Fatal(err)
		}
		if err := settings.Set(contracts.SettingToolsDisabled, encoded); err != nil {
			t.Fatal(err)
		}
	})
	if registryHasTool(registry, "mcp__flaky__extra") {
		t.Fatal("reload itself registered the disabled tool — harness broken")
	}
	waitStaleCall(t, callDone)
	if registryHasTool(registry, "mcp__flaky__extra") {
		result, err := registry.Invoke(context.Background(), rig.ToolCall{Name: "mcp__flaky__extra", Arguments: "{}"})
		t.Errorf("stale reconnect resurrected the disabled tool extra; invoke = %q/%v (pong-old means it routes to the OLD server)", result.Content, err)
	}
}

// TestMCPStaleReconnectClosesFreshConnection: the connection the stale
// reconnect built during its slow handshake must be explicitly closed when the
// guard aborts the swap — the old server must see the session DELETE.
func TestMCPStaleReconnectClosesFreshConnection(t *testing.T) {
	old := newRaceMCPServer(t, "old-server", "1.0.0", 800*time.Millisecond, []string{"echo"}, "pong-old")
	fresh := newRaceMCPServer(t, "new-server", "2.0.0", 0, []string{"echo"}, "pong-new")
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"flaky","url":"` + old.http.URL + `"}]`,
	})
	_, registry, _, reloader := activateMCPToolkitWithRoot(t, map[string]any{"root": "."}, settings)
	if _, err := registry.Invoke(context.Background(), rig.ToolCall{Name: "mcp__flaky__echo", Arguments: "{}"}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	callDone := triggerStaleReconnect(t, old, settings, fresh.http.URL, reloader, registry, nil)
	waitStaleCall(t, callDone)
	session := old.reconnectSession()
	deadline := time.Now().Add(2 * time.Second)
	for !old.sessionDeleted(session) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !old.sessionDeleted(session) {
		t.Errorf("stale reconnect leaked its fresh connection: old server session %q never saw a Close (DELETE)", session)
	}
}

// TestMCPSerialReconnectControl is the healthy-path control: without a
// concurrent reload the same harness reconnects correctly (identity, status,
// tool count) and closes the expired connection.
func TestMCPSerialReconnectControl(t *testing.T) {
	old := newRaceMCPServer(t, "old-server", "1.0.0", 200*time.Millisecond, []string{"echo"}, "pong-old")
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"flaky","url":"` + old.http.URL + `"}]`,
	})
	_, registry, plugins, _ := activateMCPToolkitWithRoot(t, map[string]any{"root": "."}, settings)
	if _, err := registry.Invoke(context.Background(), rig.ToolCall{Name: "mcp__flaky__echo", Arguments: "{}"}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	old.expire()
	result, err := registry.Invoke(context.Background(), rig.ToolCall{Name: "mcp__flaky__echo", Arguments: "{}"})
	if err != nil || result.Content != "pong-old" {
		t.Fatalf("control invalid: serial reconnect failed: %q/%v", result.Content, err)
	}
	row := plugins.MCP()[0]
	if row.Version != "1.0.0" || row.Status != statusConnected || row.Tools != 1 {
		t.Fatalf("control invalid: read model after serial reconnect = %+v", row)
	}
	if !registryHasTool(registry, "mcp__flaky__echo") {
		t.Fatal("control invalid: echo lost after serial reconnect")
	}
}
