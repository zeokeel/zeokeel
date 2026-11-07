package toolkit

// Tests for the MCP Plugins read model the toolkit provides: empty
// configuration, a connected server, failed connections (kept as entries), tool
// count refresh, concurrency and lifecycle.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// pluginHarness is one loaded toolkit module.
type pluginHarness struct {
	root    cordis.Context
	loader  *module.Loader
	plugins contracts.PluginsService
	once    sync.Once
}

func (h *pluginHarness) close() {
	h.once.Do(func() {
		_ = h.loader.Close()
		_ = h.root.DisposeAll()
	})
}

// loadPlugins activates the toolkit module with cfg and requires KeyPlugins.
func loadPlugins(t *testing.T, cfg map[string]any) *pluginHarness {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	if _, err := root.Provide(contracts.KeySettings, contracts.SettingsService(testSettings{})); err != nil {
		t.Fatalf("provide settings: %v", err)
	}
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	entry := module.Entry{ID: "toolkit", Module: "zeokeel/toolkit"}
	if cfg != nil {
		entry.Config = cfg
	}
	loader, err := module.Load(root, registry, []module.Entry{entry})
	if err != nil {
		t.Fatalf("load toolkit: %v", err)
	}
	plugins, err := root.Require(contracts.KeyPlugins)
	if err != nil {
		_ = loader.Close()
		t.Fatalf("require KeyPlugins: %v", err)
	}
	h := &pluginHarness{root: root, loader: loader, plugins: plugins}
	t.Cleanup(h.close)
	return h
}

// TestPluginsEmptyWithoutMCPServers covers the UI empty state.
func TestPluginsEmptyWithoutMCPServers(t *testing.T) {
	h := loadPlugins(t, map[string]any{"root": "."})
	if plugins := h.plugins.MCP(); len(plugins) != 0 {
		t.Fatalf("MCP() = %+v, want empty", plugins)
	}
	if plugins := h.plugins.Refresh(context.Background()); len(plugins) != 0 {
		t.Fatalf("Refresh() = %+v, want empty", plugins)
	}
}

// TestPluginsConnectedServer covers the connected read model and Refresh.
func TestPluginsConnectedServer(t *testing.T) {
	server := testMCPHTTPServer(t)
	defer server.Close()
	h := loadPlugins(t, map[string]any{
		"root": ".",
		"mcpServers": []any{
			map[string]any{"name": "test", "url": server.URL},
		},
	})
	plugins := h.plugins.MCP()
	if len(plugins) != 1 {
		t.Fatalf("MCP() = %+v, want one plugin", plugins)
	}
	plugin := plugins[0]
	if plugin.Name != "test" || plugin.URL != server.URL {
		t.Fatalf("plugin identity = %+v", plugin)
	}
	if plugin.ServerName != "test" || plugin.Version != "1.0.0" || plugin.Protocol == "" {
		t.Fatalf("plugin handshake fields = %+v", plugin)
	}
	if plugin.Tools != 1 {
		t.Fatalf("plugin tools = %d, want 1 (the echo tool)", plugin.Tools)
	}
	if plugin.Status != statusConnected {
		t.Fatalf("plugin status = %q, want %q", plugin.Status, statusConnected)
	}

	refreshed := h.plugins.Refresh(context.Background())
	if len(refreshed) != 1 {
		t.Fatalf("Refresh() = %+v, want one plugin", refreshed)
	}
	if refreshed[0].Status != statusConnected || refreshed[0].Tools != 1 ||
		refreshed[0].ServerName != "test" || refreshed[0].Version != "1.0.0" {
		t.Fatalf("refreshed plugin = %+v", refreshed[0])
	}
	// The snapshot returned to callers is a copy.
	plugins[0].Status = "mutated"
	if h.plugins.MCP()[0].Status != statusConnected {
		t.Fatal("MCP() exposes internal plugin state")
	}
}

// TestPluginsFailedServerKeepsOthers covers a failed connection: activation
// succeeds, the entry is reported with a failure status and the healthy server
// still works.
func TestPluginsFailedServerKeepsOthers(t *testing.T) {
	server := testMCPHTTPServer(t)
	defer server.Close()
	h := loadPlugins(t, map[string]any{
		"root": ".",
		"mcpServers": []any{
			map[string]any{"name": "broken", "url": "http://127.0.0.1:1", "timeoutMs": 300},
			map[string]any{"name": "healthy", "url": server.URL},
		},
	})
	plugins := h.plugins.MCP()
	if len(plugins) != 2 {
		t.Fatalf("MCP() = %+v, want two plugins", plugins)
	}
	if plugins[0].Name != "broken" || !strings.Contains(plugins[0].Status, "连接失败") ||
		plugins[0].Tools != 0 || !strings.Contains(plugins[0].Status, "127.0.0.1:1") {
		t.Fatalf("failed plugin = %+v", plugins[0])
	}
	if plugins[1].Name != "healthy" || plugins[1].Status != statusConnected || plugins[1].Tools != 1 {
		t.Fatalf("healthy plugin = %+v", plugins[1])
	}
	refreshed := h.plugins.Refresh(context.Background())
	if len(refreshed) != 2 ||
		!strings.Contains(refreshed[0].Status, "连接失败") ||
		refreshed[1].Status != statusConnected {
		t.Fatalf("Refresh() = %+v", refreshed)
	}
}

// TestPluginsRefreshMarksClosedServer covers a server that disappears after
// activation: Refresh's ping failure flips the status.
func TestPluginsRefreshMarksClosedServer(t *testing.T) {
	server := testMCPHTTPServer(t)
	h := loadPlugins(t, map[string]any{
		"root": ".",
		"mcpServers": []any{
			map[string]any{"name": "temporary", "url": server.URL, "timeoutMs": 500},
		},
	})
	if plugins := h.plugins.MCP(); len(plugins) != 1 || plugins[0].Status != statusConnected {
		t.Fatalf("initial plugins = %+v", plugins)
	}
	server.Close()
	refreshed := h.plugins.Refresh(context.Background())
	if len(refreshed) != 1 || !strings.Contains(refreshed[0].Status, "连接失败") {
		t.Fatalf("refreshed plugins = %+v, want a failure status", refreshed)
	}
}

// TestPluginsConcurrentAccess covers concurrent MCP()/Refresh() calls.
func TestPluginsConcurrentAccess(t *testing.T) {
	server := testMCPHTTPServer(t)
	defer server.Close()
	h := loadPlugins(t, map[string]any{
		"root": ".",
		"mcpServers": []any{
			map[string]any{"name": "concurrent", "url": server.URL},
		},
	})
	const workers = 4
	var waitGroup sync.WaitGroup
	errs := make(chan error, workers*2)
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			if plugins := h.plugins.MCP(); len(plugins) != 1 || plugins[0].Tools != 1 {
				errs <- fmt.Errorf("unexpected plugin state: %+v", plugins)
			}
			if plugins := h.plugins.Refresh(context.Background()); len(plugins) != 1 ||
				plugins[0].Status != statusConnected {
				errs <- fmt.Errorf("unexpected plugin state: %+v", plugins)
			}
		}()
	}
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// TestPluginsUnavailableAfterDispose covers the module lifecycle: after the
// kernel context is disposed the service key is gone (clients already closed by
// the toolkit cleanup).
func TestPluginsUnavailableAfterDispose(t *testing.T) {
	root := cordis.Root()
	if _, err := root.Provide(contracts.KeySettings, contracts.SettingsService(testSettings{})); err != nil {
		t.Fatalf("provide settings: %v", err)
	}
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{ID: "toolkit", Module: "zeokeel/toolkit"}})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := root.Require(contracts.KeyPlugins); err != nil {
		_ = loader.Close()
		t.Fatalf("require KeyPlugins: %v", err)
	}
	if err := root.DisposeAll(); err != nil {
		t.Fatalf("DisposeAll: %v", err)
	}
	if _, ok := root.Get(contracts.KeyPlugins); ok {
		t.Fatal("KeyPlugins is still available after DisposeAll")
	}
	_ = loader.Close()
}
