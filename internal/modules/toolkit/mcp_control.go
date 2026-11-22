package toolkit

// Hot reload for MCP: the connectors tab writes settings.mcp.servers /
// mcp.disabledServers / mcp.workspaceOptIn, then asks this controller to rebuild
// the MCP half without restarting the app. The shared registry (hosttools.KeyRegistry)
// and the Plugins read model keep their pointers; only the connections and their
// tool registrations are replaced.
//
// Connection supervision (startup fan-out, per-server isolation, tool binding,
// transparent reconnect and close) is the Rig SDK tools/mcp/manager; this file
// owns the product around it: settings merge and disabled filters, the shared
// auth runtime lifecycle and settings-backed token store, the Plugins status
// read model, the per-tool enabled catalog and the workspace mcp.json rows.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	cordis "github.com/zeokeel/zeokeel-kernel"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"
	"github.com/zeokeel/zeokeel-rig/tools/mcp"
	"github.com/zeokeel/zeokeel-rig/tools/mcp/manager"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// mcpRow is the product-side state of one configured server: its plan/Plugins
// index, the manager identity it was started under and the number of its
// tools/list tools that actually reached the registry.
type mcpRow struct {
	index      int
	plan       mcpStartPlan
	managerKey string
	// namespace is the model-visible tool namespace override (Server.Key) used
	// when the configured display name is shared by another row; empty keeps
	// today's client.ServerName() naming.
	namespace string
	client    *mcp.Client
	tools     int
	startErr  error
}

// mcpController owns the current manager.Manager and can rebuild it from
// settings.
type mcpController struct {
	logf      func(string, ...any)
	registry  *rigtool.Registry
	plugins   *pluginsService
	auth      *mcpAuthRuntime
	settings  contracts.SettingsService
	maxOutput int
	load      func() ([]MCPServer, map[string]bool)

	mu         sync.Mutex
	manager    *manager.Manager
	plans      []mcpStartPlan
	rows       []*mcpRow
	registered []string
	closed     bool
}

var _ contracts.MCPReloader = (*mcpController)(nil)

func newMCPController(logf func(string, ...any), registry *rigtool.Registry,
	plugins *pluginsService, auth *mcpAuthRuntime, settings contracts.SettingsService,
	maxOutput int, load func() ([]MCPServer, map[string]bool)) *mcpController {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	controller := &mcpController{
		logf: logf, registry: registry, plugins: plugins, auth: auth,
		settings: settings, maxOutput: maxOutput, load: load,
	}
	// The auth runtime's authorized callback (installed at construction) calls
	// controller.reconnectByName; its background refresh loop runs here.
	auth.start()
	return controller
}

// Reload implements contracts.MCPReloader.
func (c *mcpController) Reload(ctx context.Context) ([]contracts.MCPPlugin, error) {
	return c.reload(ctx)
}

var _ contracts.MCPToolCatalog = (*mcpController)(nil)

// Tools implements contracts.MCPToolCatalog: sorted model-visible tools of one
// configured server plus their current enabled state.
func (c *mcpController) Tools(server string) []contracts.MCPTool {
	server = strings.TrimSpace(server)
	if server == "" {
		return nil
	}
	c.mu.Lock()
	managerNow := c.manager
	var target *mcpRow
	for _, row := range c.rows {
		if strings.EqualFold(strings.TrimSpace(row.plan.server.Name), server) ||
			strings.EqualFold(row.managerKey, server) {
			target = row
			break
		}
	}
	disabled := loadDisabledTools(c.settings, c.settings != nil, c.logf)
	c.mu.Unlock()
	if managerNow == nil || target == nil || target.client == nil {
		return nil
	}
	prefix := mcpToolPrefix(target)
	var tools []contracts.MCPTool
	for _, tool := range managerNow.Tools() {
		name := tool.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		tools = append(tools, contracts.MCPTool{
			Name: name, Description: strings.TrimSpace(tool.Description()),
			Enabled: !disabled[name],
		})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools
}

// SetToolEnabled implements contracts.MCPToolCatalog: persist the tool into
// tools.disabled (or remove it) and rebuild the MCP runtime so the registry and
// cards reflect it. A full rebuild is intentional for the first version; a
// later diff reload can make this per-tool.
func (c *mcpController) SetToolEnabled(ctx context.Context, server, tool string, enabled bool) error {
	server = strings.TrimSpace(server)
	tool = strings.TrimSpace(tool)
	if server == "" || tool == "" {
		return errors.New("server and tool names are required")
	}
	if c.settings == nil {
		return errors.New("settings are unavailable")
	}
	update := func(key string) error {
		names, err := contracts.ParseStringListJSON(c.settings.Get(key, ""))
		if err != nil {
			return fmt.Errorf("parse %s: %w", key, err)
		}
		kept := make([]string, 0, len(names)+1)
		for _, name := range names {
			if strings.TrimSpace(name) != "" && !strings.EqualFold(strings.TrimSpace(name), tool) {
				kept = append(kept, name)
			}
		}
		if !enabled {
			kept = append(kept, tool)
		}
		sort.Strings(kept)
		encoded, err := contracts.EncodeStringListJSON(kept)
		if err != nil {
			return err
		}
		return c.settings.Set(key, encoded)
	}
	if err := update(contracts.SettingToolsDisabled); err != nil {
		return err
	}
	// The legacy MCP-only list must agree, otherwise enabling a tool hidden by
	// the old key would appear to do nothing.
	if err := update(contracts.SettingMCPDisabledTools); err != nil {
		return err
	}
	_, err := c.reload(ctx)
	return err
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
	// tool would otherwise shadow the rebuilt one. The shared auth runtime (and
	// its cached clients/tokens) survives the rebuild.
	c.teardownLocked()

	plans := prepareMCPServerPlans(servers, c.logf)
	disabledNames := make([]string, 0, len(disabled))
	for name := range disabled {
		disabledNames = append(disabledNames, name)
	}
	sort.Strings(disabledNames)

	var mgr *manager.Manager
	managerConfig := manager.Config{
		StartupTimeout: mcpStartupOverallTimeout,
		MaxOutputBytes: c.maxOutput,
		Resources:      true,
		Logger:         c.logf,
		Auth:           c.auth.Runtime(),
		// A transparent/explicit/authorized reconnect must refresh the Plugins
		// row (identity, tool count, status) right away, not on the next reload.
		OnReconnect: func(key string) { c.onManagerReconnect(mgr, key) },
	}
	usedNames := make(map[string]int, len(plans))
	rows := make([]*mcpRow, 0, len(plans))
	for index, plan := range plans {
		if plan.invalid {
			continue
		}
		row := &mcpRow{
			index:      index,
			plan:       plan,
			managerKey: mcpManagerKey(plan.server, index, usedNames),
		}
		clientConfig, err := mcpClientConfig(plan.server)
		if err != nil {
			row.startErr = err
			rows = append(rows, row)
			continue
		}
		oauthConfig, err := c.auth.managerOAuthConfig(plan.server)
		if err != nil {
			row.startErr = err
			rows = append(rows, row)
			continue
		}
		managerServer := manager.Server{
			Name:          row.managerKey,
			Config:        clientConfig,
			OAuth:         oauthConfig,
			DisabledTools: disabledNames,
		}
		// A duplicate display name keeps its row name, but the manager needs a
		// distinct tool namespace/auth identity: pass the stable unique manager
		// key. Unique names keep today's Name-based namespace.
		if base := mcpBaseName(plan.server, index); row.managerKey != base {
			managerServer.Key = row.managerKey
			row.namespace = row.managerKey
		}
		managerConfig.Servers = append(managerConfig.Servers, managerServer)
		rows = append(rows, row)
	}

	mgr = manager.New(managerConfig)
	c.manager = mgr
	if err := mgr.Start(ctx); err != nil {
		_ = mgr.Close()
		c.manager = nil
		c.plans = plans
		c.rows = rows
		return c.plugins.MCP(), err
	}
	c.plans = plans
	c.rows = rows
	c.syncRuntimeLocked()

	entries := c.buildEntriesLocked()
	c.bindPluginsLocked()
	c.plugins.replace(entries)
	for _, row := range c.rows {
		if row.startErr != nil {
			c.logf("[toolkit] mcp %q: connect failed: %v", pluginDisplayName(row.plan.server.Name, nil), row.startErr)
			continue
		}
		if row.client != nil {
			c.logf("[toolkit] mcp %q connected: %d tool(s), protocol %s",
				pluginDisplayName(row.plan.server.Name, row.client), row.tools, row.client.ProtocolVersion())
		}
	}
	return c.plugins.MCP(), nil
}

// teardownLocked unregisters the current manager's tools and closes it. The
// caller holds the controller lock.
func (c *mcpController) teardownLocked() {
	for _, name := range c.registered {
		c.registry.Unregister(name)
	}
	c.registered = nil
	if c.manager != nil {
		_ = c.manager.Close()
		c.manager = nil
	}
	c.plans = nil
	c.rows = nil
}

// syncRuntimeLocked reconciles the registry with the current manager's tools and
// recomputes each row's registered regular-tool count. Proxies keep working
// across a transparent reconnect; this only adds/removes tools whose set
// changed and refreshes the per-row client pointers. The caller holds the lock.
func (c *mcpController) syncRuntimeLocked() {
	if c.manager == nil {
		return
	}
	clients := c.manager.Clients()
	errs := c.manager.Errors()
	for _, row := range c.rows {
		if client := clients[row.managerKey]; client != nil {
			row.client = client
			row.startErr = nil
			continue
		}
		if err := errs[row.managerKey]; err != nil {
			row.startErr = err
		}
	}
	tools := c.manager.Tools()
	owners := make([]*mcpRow, len(tools))
	current := make(map[string]bool, len(tools))
	for index, tool := range tools {
		name := tool.Name()
		current[name] = true
		owners[index] = c.ownerOfToolLocked(name)
	}
	for _, name := range c.registered {
		if !current[name] {
			c.registry.Unregister(name)
		}
	}
	existing := make(map[string]bool, len(c.registered))
	for _, name := range c.registered {
		existing[name] = true
	}
	registered := make([]string, 0, len(tools))
	counts := make(map[*mcpRow]int, len(c.rows))
	for index, tool := range tools {
		name := tool.Name()
		row := owners[index]
		if !existing[name] {
			if err := c.registry.Register(tool); err != nil {
				// A registry-level collision is not a model-visible tool: do not
				// track or count it (the Plugins row counts registered tools).
				c.logf("[toolkit] mcp: skip tool %q: %v", name, err)
				continue
			}
		}
		registered = append(registered, name)
		if row != nil && !c.isResourceToolLocked(row, name) {
			counts[row]++
		}
	}
	c.registered = registered
	for _, row := range c.rows {
		row.tools = counts[row]
	}
}

// ownerOfToolLocked attributes one model-visible tool to the first configured
// row whose server namespace matches it (duplicate names keep the first row).
func (c *mcpController) ownerOfToolLocked(name string) *mcpRow {
	for _, row := range c.rows {
		if row.client == nil {
			continue
		}
		if strings.HasPrefix(name, mcpToolPrefix(row)) {
			return row
		}
	}
	return nil
}

// isResourceToolLocked reports whether name is one of the row's resource/prompt
// helpers (the Plugins count only includes tools/list tools).
func (c *mcpController) isResourceToolLocked(row *mcpRow, name string) bool {
	prefix := mcpToolPrefix(row)
	if prefix == "" {
		return false
	}
	for _, base := range []string{
		mcp.ToolListResources, mcp.ToolReadResource, mcp.ToolListResourceTemplates,
		mcp.ToolListPrompts, mcp.ToolGetPrompt,
	} {
		if prefix+base == name {
			return true
		}
	}
	return false
}

// mcpToolPrefix is the model-visible namespace of one row's tools: the Server.Key
// override for a duplicate display name, else the connection's server name.
func mcpToolPrefix(row *mcpRow) string {
	if row == nil {
		return ""
	}
	namespace := strings.TrimSpace(row.namespace)
	if namespace == "" && row.client != nil {
		namespace = row.client.ServerName()
	}
	if namespace == "" {
		return ""
	}
	return mcp.DefaultPrefix + mcp.SanitizeName(namespace) + "__"
}

// buildEntriesLocked freezes the Plugins read model for the current manager.
func (c *mcpController) buildEntriesLocked() []pluginEntry {
	byIndex := make(map[int]*mcpRow, len(c.rows))
	for _, row := range c.rows {
		byIndex[row.index] = row
	}
	entries := make([]pluginEntry, 0, len(c.plans))
	for index, plan := range c.plans {
		entry := plan.entry
		if plan.invalid {
			entries = append(entries, entry)
			continue
		}
		row := byIndex[index]
		if row == nil || row.client == nil {
			err := errors.New("connection unavailable")
			if row != nil && row.startErr != nil {
				err = row.startErr
			}
			entry.plugin.Status = statusConnectFailed + err.Error()
			if authStatus := c.auth.failureStatus(rowPlanServer(row)); authStatus != "" {
				entry.plugin.Status = authStatus
			}
			entries = append(entries, entry)
			continue
		}
		entry.client = row.client
		entry.plugin.Name = pluginDisplayName(plan.server.Name, row.client)
		entry.plugin.ServerName = row.client.ServerName()
		entry.plugin.Version = row.client.ServerVersion()
		entry.plugin.Protocol = row.client.ProtocolVersion()
		entry.plugin.Status = statusConnected
		entry.plugin.Tools = row.tools
		entries = append(entries, entry)
	}
	return entries
}

// rowPlanServer tolerates a missing row when formatting an auth failure status.
func rowPlanServer(row *mcpRow) MCPServer {
	if row == nil {
		return MCPServer{}
	}
	return row.plan.server
}

// bindPluginsLocked connects the Plugins read model's Reconnect/Refresh hooks to
// the current manager.
func (c *mcpController) bindPluginsLocked() {
	c.plugins.bindReconnect(func(index int) bool {
		return c.reconnectPlugin(index)
	})
	c.plugins.bindToolCounter(func(index int) (int, bool) {
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, row := range c.rows {
			if row.index == index {
				return row.tools, true
			}
		}
		return 0, false
	})
}

// rowByPluginIndexLocked returns the row behind one Plugins index.
func (c *mcpController) rowByPluginIndexLocked(index int) *mcpRow {
	for _, row := range c.rows {
		if row.index == index {
			return row
		}
	}
	return nil
}

// onManagerReconnect refreshes the Plugins row behind a successful manager
// reconnect (transparent, explicit or after authorization) immediately: the
// handshake identity, registered tool count and status stop being stale until
// the next reload. A callback from a replaced manager is ignored (a hot reload
// during the handshake owns the new read model).
func (c *mcpController) onManagerReconnect(mgr *manager.Manager, key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.manager == nil || c.manager != mgr {
		return
	}
	var row *mcpRow
	for _, candidate := range c.rows {
		if strings.EqualFold(candidate.managerKey, key) {
			row = candidate
			break
		}
	}
	if row == nil {
		return
	}
	c.syncRuntimeLocked()
	if row.client == nil {
		return
	}
	c.plugins.setConnected(row.index, row.client, statusConnected, row.tools)
}

// reconnectPlugin rebuilds one Plugins row's connection and refreshes its status
// and tool registrations. It reports whether the row is usable again.
func (c *mcpController) reconnectPlugin(index int) bool {
	c.mu.Lock()
	if c.closed || c.manager == nil {
		c.mu.Unlock()
		return false
	}
	row := c.rowByPluginIndexLocked(index)
	if row == nil {
		c.mu.Unlock()
		return false
	}
	mgr := c.manager
	key := row.managerKey
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), mcpStartupOverallTimeout)
	defer cancel()
	err := mgr.Reconnect(ctx, key)

	c.mu.Lock()
	defer c.mu.Unlock()
	// A reload that landed during the handshake owns the new manager; never
	// write this stale result into its read model.
	if c.manager != mgr || c.closed {
		return false
	}
	row = c.rowByPluginIndexLocked(index)
	if row == nil {
		return false
	}
	if err != nil {
		status := statusConnectFailed + err.Error()
		if authStatus := c.auth.failureStatus(row.plan.server); authStatus != "" {
			status = authStatus
		}
		c.plugins.setStatus(index, status)
		c.logf("[toolkit] mcp %q: reconnect failed: %v", pluginDisplayName(row.plan.server.Name, nil), err)
		return false
	}
	c.syncRuntimeLocked()
	c.plugins.setConnected(index, row.client, statusConnected, row.tools)
	return true
}

// reconnectByName rebuilds the connection whose configured name matches (the
// OAuth adapter calls it after a completed authorization).
func (c *mcpController) reconnectByName(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	c.mu.Lock()
	var target *mcpRow
	for _, row := range c.rows {
		if strings.EqualFold(strings.TrimSpace(row.plan.server.Name), name) ||
			strings.EqualFold(row.managerKey, name) {
			target = row
			break
		}
	}
	c.mu.Unlock()
	if target != nil {
		_ = c.reconnectPlugin(target.index)
	}
}

// close releases the current manager, the shared auth runtime and its refresh
// loop (module unload).
func (c *mcpController) close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	mgr := c.manager
	c.manager = nil
	c.plans = nil
	c.rows = nil
	c.registered = nil
	c.mu.Unlock()
	if mgr != nil {
		_ = mgr.Close()
	}
	c.auth.close()
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
