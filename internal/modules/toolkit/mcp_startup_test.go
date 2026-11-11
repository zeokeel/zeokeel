package toolkit

// Regression tests for FR-23: MCP servers start concurrently under one bounded
// startup budget, and a settings row can set its own per-request timeout, so a
// hung endpoint cannot block product startup for 30s per server.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// hangingMCPHTTPServer accepts the connection and never answers.
func hangingMCPHTTPServer(t *testing.T) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})
	return server
}

// TestMergeMCPServersSettingsTimeoutMs pins the new settings field: a row's
// timeoutMs is carried into the runtime server config.
func TestMergeMCPServersSettingsTimeoutMs(t *testing.T) {
	merged := mergeMCPServers(nil, `[{"name":"slow","url":"http://127.0.0.1:1/mcp","timeoutMs":250}]`, nil)
	if len(merged) != 1 {
		t.Fatalf("merged = %+v, want one server", merged)
	}
	if merged[0].TimeoutMs != 250 {
		t.Fatalf("timeoutMs = %d, want 250 (settings row must reach the runtime config)", merged[0].TimeoutMs)
	}
}

// TestToolkitMCPStartupTimeoutFromSettingsRow covers the per-server timeout: a
// hung server with timeoutMs=300 must fail fast instead of using the 30s rig
// default.
func TestToolkitMCPStartupTimeoutFromSettingsRow(t *testing.T) {
	server := hangingMCPHTTPServer(t)
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"hung","url":"` + server.URL + `","timeoutMs":300}]`,
	})
	started := time.Now()
	_, plugins := activateMCPToolkit(t, map[string]any{"root": "."}, settings, nil)
	elapsed := time.Since(started)
	entries := plugins.MCP()
	if len(entries) != 1 || !strings.Contains(entries[0].Status, "连接失败") {
		t.Fatalf("entries = %+v, want a failed entry", entries)
	}
	if elapsed >= 3*time.Second {
		t.Fatalf("activation with timeoutMs=300 took %s, want < 3s", elapsed)
	}
	t.Logf("activation with settings timeoutMs=300: %s", elapsed)
}

// TestToolkitMCPStartupRunsServersConcurrently: two hung servers with a 400ms
// per-server timeout must finish in about one timeout, not two.
func TestToolkitMCPStartupRunsServersConcurrently(t *testing.T) {
	first := hangingMCPHTTPServer(t)
	second := hangingMCPHTTPServer(t)
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"hung-a","url":"` + first.URL + `","timeoutMs":400},` +
			`{"name":"hung-b","url":"` + second.URL + `","timeoutMs":400}]`,
	})
	started := time.Now()
	_, plugins := activateMCPToolkit(t, map[string]any{"root": "."}, settings, nil)
	elapsed := time.Since(started)
	entries := plugins.MCP()
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want two failed entries", entries)
	}
	for _, entry := range entries {
		if !strings.Contains(entry.Status, "连接失败") {
			t.Fatalf("entry %+v, want a connection failure", entry)
		}
	}
	// Sequential startup needs >= 800ms; concurrent stays near 400ms.
	if elapsed >= 800*time.Millisecond {
		t.Fatalf("two 400ms-timeout servers took %s, want concurrent (< 800ms)", elapsed)
	}
	t.Logf("concurrent startup of two hung servers: %s", elapsed)
}

// TestToolkitMCPStartupOverallBound: without a configured timeout the whole MCP
// phase is still bounded by one product-level budget (5s), not 30s per server.
func TestToolkitMCPStartupOverallBound(t *testing.T) {
	first := hangingMCPHTTPServer(t)
	second := hangingMCPHTTPServer(t)
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"hung-a","url":"` + first.URL + `"},` +
			`{"name":"hung-b","url":"` + second.URL + `"}]`,
	})
	started := time.Now()
	_, plugins := activateMCPToolkit(t, map[string]any{"root": "."}, settings, nil)
	elapsed := time.Since(started)
	if len(plugins.MCP()) != 2 {
		t.Fatalf("entries = %+v, want two failed entries", plugins.MCP())
	}
	if elapsed >= 9*time.Second {
		t.Fatalf("two default-timeout hung servers took %s, want the 5s startup budget", elapsed)
	}
	t.Logf("bounded startup of two hung servers: %s", elapsed)
}
