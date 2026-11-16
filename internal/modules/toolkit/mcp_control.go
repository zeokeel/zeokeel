package toolkit

// Hot reload for MCP: the connectors tab writes settings.mcp.servers /
// mcp.disabledServers / mcp.workspaceOptIn, then asks this controller to rebuild
// the MCP half without restarting the app. The shared registry (contracts.KeyToolkit)
// and the Plugins read model keep their pointers; only the connections and their
// tool registrations are replaced.

import (
	"context"
	"errors"
	"strings"
	"sync"

	cordis "github.com/zeokeel/zeokeel-kernel"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// mcpController owns the current MCP runtime and can rebuild it from settings.
type mcpController struct {
	logf      func(string, ...any)
	registry  *rigtool.Registry
	plugins   *pluginsService
	auth      *mcpAuthRuntime
	maxOutput int
	load      func() ([]MCPServer, map[string]bool)

	mu      sync.Mutex
	runtime *mcpRuntime
	closed  bool
}

var _ contracts.MCPReloader = (*mcpController)(nil)

func newMCPController(logf func(string, ...any), registry *rigtool.Registry,
	plugins *pluginsService, auth *mcpAuthRuntime, maxOutput int,
	load func() ([]MCPServer, map[string]bool)) *mcpController {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	controller := &mcpController{
		logf: logf, registry: registry, plugins: plugins, auth: auth,
		maxOutput: maxOutput, load: load,
	}
	// The adapter is stable across reloads; the callback resolves the current
	// runtime under the controller lock.
	auth.onAuthorized = func(name string) { controller.reconnectByName(name) }
	return controller
}

// Reload implements contracts.MCPReloader.
func (c *mcpController) Reload(ctx context.Context) ([]contracts.MCPPlugin, error) {
	return c.reload(ctx)
}

// Load performs the first build during activation (same path as Reload).
func (c *mcpController) Load(ctx context.Context) ([]contracts.MCPPlugin, error) {
	return c.reload(ctx)
}

func (c *mcpController) reload(ctx context.Context) ([]contracts.MCPPlugin, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return c.plugins.MCP(), errors.New("MCP runtime is closed")
	}
	servers, disabled := c.load()

	// Drop the previous connections and their registry entries first: a stale
	// tool would otherwise shadow the rebuilt one.
	if c.runtime != nil {
		c.runtime.stop(c.registry)
		c.runtime = nil
	}
	c.auth.close()

	// OAuth clients must exist before startup for OAuth rows, including rows
	// whose startup fails (the client is what later runs the browser flow).
	for _, server := range servers {
		if !authOAuth2(server.config()) {
			continue
		}
		if _, err := c.auth.clientForServer(server); err != nil {
			c.logf("[toolkit] mcp %q: OAuth setup: %v", server.Name, err)
		}
	}

	runtime := newMCPRuntime(c.logf, disabled, c.maxOutput, c.auth)
	plans := prepareMCPServerPlans(servers, c.logf)
	outcomes := runtime.startMCPServers(ctx, plans)
	entries := make([]pluginEntry, 0, len(plans))
	seen := make(map[string]bool)
	for index, plan := range plans {
		entry := plan.entry
		if plan.invalid {
			entries = append(entries, entry)
			continue
		}
		managed := runtime.registerServer(plan, index)
		outcome := outcomes[index]
		if outcome.err != nil {
			entry.plugin.Status = statusConnectFailed + outcome.err.Error()
			if authStatus := c.auth.failureStatus(plan.server); authStatus != "" {
				entry.plugin.Status = authStatus
			}
			entries = append(entries, entry)
			c.logf("[toolkit] mcp %q: connect failed: %v", entry.plugin.Name, outcome.err)
			continue
		}
		entry.client = outcome.client
		entry.plugin.Name = pluginDisplayName(plan.server.Name, outcome.client)
		entry.plugin.ServerName = outcome.serverName
		entry.plugin.Version = outcome.serverVer
		entry.plugin.Protocol = outcome.protocolVer
		entry.plugin.Status = statusConnected
		wrapped, _ := runtime.attachOutcome(managed, outcome, seen)
		registered := 0
		for _, tool := range wrapped {
			if err := c.registry.Register(tool); err != nil {
				c.logf("[toolkit] mcp %q: skip tool %q: %v", entry.plugin.Name, tool.Name(), err)
				managed.mu.Lock()
				delete(managed.registered, tool.Name())
				delete(managed.inner, tool.Name())
				managed.mu.Unlock()
				continue
			}
			if outcome.regular[tool.Name()] {
				registered++
			}
		}
		managed.mu.Lock()
		managed.regularRegistered = registered
		managed.mu.Unlock()
		entry.plugin.Tools = registered
		entries = append(entries, entry)
		c.logf("[toolkit] mcp %q connected: %d tool(s), protocol %s",
			entry.plugin.Name, registered, entry.plugin.Protocol)
	}
	runtime.bindRegistry(c.registry)
	runtime.bindPlugins(c.plugins)
	c.plugins.replace(entries)
	c.runtime = runtime
	return c.plugins.MCP(), nil
}

// reconnectByName rebuilds one connection after an OAuth authorization.
func (c *mcpController) reconnectByName(name string) {
	c.mu.Lock()
	runtime := c.runtime
	c.mu.Unlock()
	if runtime != nil {
		runtime.reconnectByName(name)
	}
}

// close releases the current runtime (module unload).
func (c *mcpController) close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	runtime := c.runtime
	c.runtime = nil
	c.mu.Unlock()
	if runtime != nil {
		runtime.close()
	}
}

// loadMCPServers rebuilds the merged server list from the assembly, settings,
// the workspace opt-in and the disabled lists. It mirrors the activation merge
// exactly and is called on every hot reload.
func loadMCPServers(ctx cordis.Context, cfg Config, settings contracts.SettingsService,
	hasSettings bool, logf func(string, ...any)) ([]MCPServer, map[string]bool) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	raw := ""
	if hasSettings {
		raw = settings.Get(contracts.SettingMCPServers, "")
	}
	servers := mergeMCPServers(cfg.MCPServers, raw, logf)
	workspaceOptIn := false
	if hasSettings {
		workspaceOptIn = mcpWorkspaceOptInEnabled(settings.Get(contracts.SettingMCPWorkspaceOptIn, ""))
	}
	if workspaceOptIn {
		if workspaceService, ok := ctx.Get(contracts.KeyWorkspace); ok && workspaceService != nil {
			rows := readWorkspaceMCPServers(context.Background(), workspaceService, logf)
			if len(rows) > 0 {
				servers = mergeMCPServerRows(servers, rows, "workspace mcp.json", false, logf)
			}
		}
	}
	if hasSettings {
		var skipped int
		servers, skipped = filterDisabledMCPServers(servers,
			settings.Get(contracts.SettingMCPDisabledServers, ""), logf)
		if skipped > 0 {
			logf("[toolkit] skipped %d disabled MCP server(s) (%s)", skipped, contracts.SettingMCPDisabledServers)
		}
	}
	disabledTools := loadDisabledTools(settings, hasSettings, logf)
	return servers, disabledTools
}

// loadDisabledTools reads the union of tools.disabled and the legacy
// mcp.disabledTools list.
func loadDisabledTools(settings contracts.SettingsService, hasSettings bool,
	logf func(string, ...any)) map[string]bool {
	disabled := make(map[string]bool)
	if !hasSettings {
		return disabled
	}
	for _, key := range []string{contracts.SettingToolsDisabled, contracts.SettingMCPDisabledTools} {
		names, err := contracts.ParseStringListJSON(settings.Get(key, ""))
		if err != nil {
			logf("[toolkit] parse %s: %v", key, err)
			continue
		}
		for _, name := range names {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				disabled[trimmed] = true
			}
		}
	}
	return disabled
}
