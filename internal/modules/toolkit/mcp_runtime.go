package toolkit

// MCP runtime: one live connection per configured server, a bounded concurrent
// startup phase, transparent reconnect after a session expiry / dead child
// process, and the Plugins read model kept in sync with the live connection.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"
	"github.com/zeokeel/zeokeel-rig/tools/mcp"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// mcpStartupOverallTimeout bounds the whole MCP startup phase. MCP servers are
// started concurrently and every handshake is context-bounded by this budget,
// so a hung endpoint cannot hold the product's first frame for 30s per server
// (the rig per-request default), let alone minutes for several servers.
const mcpStartupOverallTimeout = 5 * time.Second

// mcpStartPlan is one configured server plus its validation outcome. Invalid
// rows keep a diagnostic Plugins entry and never reach the start phase.
type mcpStartPlan struct {
	server    MCPServer
	transport string
	entry     pluginEntry
	invalid   bool
}

// mcpStartOutcome is the result of starting one plan.
type mcpStartOutcome struct {
	client *mcp.Client
	tools  []rig.Tool
	// regular marks the tools that came from the server's tools/list (the
	// Plugins row's "N 个工具" count); the resource/prompt helpers are
	// additive and not part of that figure.
	regular     map[string]bool
	serverName  string
	serverVer   string
	protocolVer string
	err         error
}

// managedMCPServer owns the live client of one configured server and the
// registry entries it contributed. registered holds the reconnect proxies that
// are actually in the shared registry; inner holds the current implementation
// each proxy dispatches to (swapped on reconnect).
type managedMCPServer struct {
	plan        mcpStartPlan
	pluginIndex int

	reconnectMu sync.Mutex

	mu         sync.Mutex
	client     *mcp.Client
	inner      map[string]rig.Tool
	registered map[string]rig.Tool
	// regularRegistered is the number of tools/list tools that actually
	// reached the registry (disabled/duplicate tools excluded): the Plugins
	// row's "N 个工具" figure.
	regularRegistered int
}

// currentClient returns the live client (nil before the first start).
func (s *managedMCPServer) currentClient() *mcp.Client {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client
}

// currentTool returns the live implementation of one model-visible tool.
func (s *managedMCPServer) currentTool(name string) rig.Tool {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner[name]
}

// mcpRuntime owns every MCP connection of one toolkit activation.
type mcpRuntime struct {
	logf     func(string, ...any)
	disabled map[string]bool
	// maxOutput is the product's per-result output budget (0 = default).
	maxOutput int

	mu       sync.Mutex
	registry *rigtool.Registry
	plugins  *pluginsService
	servers  []*managedMCPServer
	closed   bool
}

// newMCPRuntime creates the runtime for one activation.
func newMCPRuntime(logf func(string, ...any), disabled map[string]bool, maxOutput int) *mcpRuntime {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if maxOutput <= 0 {
		maxOutput = defaultMCPMaxOutputBytes
	}
	return &mcpRuntime{logf: logf, disabled: disabled, maxOutput: maxOutput}
}

// prepareMCPServerPlans validates every merged server row and freezes the
// Plugins read-model base entry (name/URL/status) for it.
func prepareMCPServerPlans(servers []MCPServer, logf func(string, ...any)) []mcpStartPlan {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	plans := make([]mcpStartPlan, 0, len(servers))
	for _, server := range servers {
		transport := mcpServerTransport(server)
		plan := mcpStartPlan{
			server:    server,
			transport: transport,
			entry: pluginEntry{plugin: contracts.MCPPlugin{
				Name:   pluginDisplayName(server.Name, nil),
				URL:    strings.TrimSpace(server.URL),
				Status: statusConnectFailed + "未配置 URL",
			}},
		}
		switch transport {
		case mcp.TransportStdio:
			plan.entry.plugin.URL = ""
			if strings.TrimSpace(server.Command) == "" {
				plan.invalid = true
				plan.entry.plugin.Status = statusConnectFailed + "stdio command is required"
				logf("[toolkit] mcp %q: stdio command is required", plan.entry.plugin.Name)
			}
		case mcp.TransportHTTP:
			if plan.entry.plugin.URL == "" {
				// Keep a failed entry so the Plugins screen can show the
				// misconfiguration instead of silently dropping the server.
				plan.invalid = true
				logf("[toolkit] mcp %q: url is required for the http transport", plan.entry.plugin.Name)
			}
		default:
			plan.invalid = true
			// Legacy assembly rows (and unclassified rows) with no URL keep the
			// original missing-URL diagnostic.
			if plan.entry.plugin.URL == "" && strings.TrimSpace(server.Command) == "" {
				plan.entry.plugin.Status = statusConnectFailed + "未配置 URL"
			} else {
				plan.entry.plugin.Status = statusConnectFailed + "unsupported transport"
			}
			logf("[toolkit] mcp %q: unsupported transport %q", plan.entry.plugin.Name, transport)
		}
		plans = append(plans, plan)
	}
	return plans
}

// startMCPServers starts every valid plan concurrently under one overall
// deadline and returns the outcomes in plan order (invalid plans stay zero).
func (r *mcpRuntime) startMCPServers(ctx context.Context, plans []mcpStartPlan) []mcpStartOutcome {
	outcomes := make([]mcpStartOutcome, len(plans))
	if len(plans) == 0 {
		return outcomes
	}
	if ctx == nil {
		ctx = context.Background()
	}
	startCtx, cancel := context.WithTimeout(ctx, mcpStartupOverallTimeout)
	defer cancel()
	var wait sync.WaitGroup
	for index := range plans {
		if plans[index].invalid {
			continue
		}
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			outcomes[index] = r.startOne(startCtx, plans[index])
		}(index)
	}
	wait.Wait()
	return outcomes
}

// startOne connects one server, lists its tools and builds the registry tools
// (already filtered by the disabled-tool switches).
func (r *mcpRuntime) startOne(ctx context.Context, plan mcpStartPlan) mcpStartOutcome {
	client, err := startMCPClient(ctx, plan.server)
	if err != nil {
		return mcpStartOutcome{err: err}
	}
	outcome := mcpStartOutcome{
		client:      client,
		serverName:  client.ServerName(),
		serverVer:   client.ServerVersion(),
		protocolVer: client.ProtocolVersion(),
	}
	infos, err := client.ListTools(ctx)
	if err != nil {
		_ = client.Close()
		return mcpStartOutcome{err: fmt.Errorf("list tools: %w", err)}
	}
	outcome.tools, outcome.regular = r.buildTools(client, infos)
	return outcome
}

// buildTools adapts the server's tool list to registry tools: the regular
// tools keep rig's "mcp__<server>__<tool>" namespace, the resource/prompt tools
// get an explicit per-server prefix (their rig names carry no server identity
// and used to collide between servers).
func (r *mcpRuntime) buildTools(client *mcp.Client, infos []mcp.ToolInfo) ([]rig.Tool, map[string]bool) {
	regular := filterDisabledTools(mcp.NewTools(client, client.ServerName(), infos, ""), r.disabled, r.logf)
	regular = r.boundOutput(regular)
	tools := append([]rig.Tool(nil), regular...)
	tools = append(tools, r.boundOutput(filterDisabledTools(mcpResourceTools(client), r.disabled, r.logf))...)
	names := make(map[string]bool, len(regular))
	for _, tool := range regular {
		names[tool.Name()] = true
	}
	return tools, names
}

// boundOutput applies the product's output budget to every MCP tool result
// (regular tools plus the resource/prompt helpers), so one large payload cannot
// exceed the configured maxOutputBytes.
func (r *mcpRuntime) boundOutput(tools []rig.Tool) []rig.Tool {
	if r.maxOutput <= 0 {
		return tools
	}
	bounded := make([]rig.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		bounded = append(bounded, &truncatedMCPTool{inner: tool, limit: r.maxOutput})
	}
	return bounded
}

// attachServer registers one started connection's tools (wrapped in reconnect
// proxies) and records the managed server. It returns the registry tools and
// how many tools/list tools actually reached the registry (duplicates skipped).
func (r *mcpRuntime) attachServer(pluginIndex int, plan mcpStartPlan, outcome mcpStartOutcome,
	seen map[string]bool) (wrapped []rig.Tool, regularCount int) {
	server := &managedMCPServer{
		plan:        plan,
		pluginIndex: pluginIndex,
		client:      outcome.client,
		inner:       make(map[string]rig.Tool, len(outcome.tools)),
		registered:  make(map[string]rig.Tool, len(outcome.tools)),
	}
	for _, tool := range outcome.tools {
		name := tool.Name()
		if seen[name] {
			r.logf("[toolkit] mcp %q: skip duplicate tool %q", plan.entry.plugin.Name, name)
			continue
		}
		seen[name] = true
		proxy := &mcpReconnectTool{runtime: r, server: server, name: name, meta: tool}
		server.inner[name] = tool
		server.registered[name] = proxy
		wrapped = append(wrapped, proxy)
		if outcome.regular[name] {
			regularCount++
		}
	}
	server.mu.Lock()
	server.regularRegistered = regularCount
	server.mu.Unlock()
	r.mu.Lock()
	r.servers = append(r.servers, server)
	r.mu.Unlock()
	return wrapped, regularCount
}

// bindRegistry makes the shared registry available to the reconnect path.
func (r *mcpRuntime) bindRegistry(registry *rigtool.Registry) {
	r.mu.Lock()
	r.registry = registry
	r.mu.Unlock()
}

// bindPlugins connects the read-model service so reconnects can update status.
func (r *mcpRuntime) bindPlugins(plugins *pluginsService) {
	r.mu.Lock()
	r.plugins = plugins
	r.mu.Unlock()
	if plugins == nil {
		return
	}
	plugins.bindReconnect(func(index int) bool {
		return r.reconnectPlugin(index)
	})
	plugins.bindToolCounter(func(index int) (int, bool) {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, server := range r.servers {
			if server.pluginIndex != index {
				continue
			}
			server.mu.Lock()
			count := server.regularRegistered
			server.mu.Unlock()
			return count, true
		}
		return 0, false
	})
}

// reconnectPlugin rebuilds the connection behind one Plugins row.
func (r *mcpRuntime) reconnectPlugin(index int) bool {
	r.mu.Lock()
	var target *managedMCPServer
	for _, server := range r.servers {
		if server.pluginIndex == index {
			target = server
			break
		}
	}
	r.mu.Unlock()
	if target == nil {
		return false
	}
	return r.reconnect(target, target.currentClient())
}

// updatePlugin refreshes the Plugins read model of one server from its live
// client.
func (r *mcpRuntime) updatePlugin(server *managedMCPServer, status string, toolCount int) {
	r.mu.Lock()
	plugins := r.plugins
	r.mu.Unlock()
	if plugins == nil || server == nil {
		return
	}
	plugins.setConnected(server.pluginIndex, server.currentClient(), status, toolCount)
}

// reconnect rebuilds one dead connection. observed is the client the caller saw
// fail: when another goroutine already replaced it this is a no-op success.
func (r *mcpRuntime) reconnect(target *managedMCPServer, observed *mcp.Client) bool {
	if target == nil {
		return false
	}
	r.mu.Lock()
	closed, registry, plugins := r.closed, r.registry, r.plugins
	r.mu.Unlock()
	if closed || registry == nil {
		return false
	}
	target.reconnectMu.Lock()
	defer target.reconnectMu.Unlock()
	if current := target.currentClient(); current != observed {
		// Someone else already reconnected (or the first start is still in
		// flight): report the connection as usable instead of redoing work.
		return current != nil
	}
	plan := target.plan
	timeout := mcpStartupOverallTimeout
	if plan.server.TimeoutMs > 0 {
		timeout = time.Duration(plan.server.TimeoutMs) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	client, err := startMCPClient(ctx, plan.server)
	if err != nil {
		if plugins != nil {
			plugins.setStatus(target.pluginIndex, statusConnectFailed+err.Error())
		}
		r.logf("[toolkit] mcp %q: reconnect failed: %v", plan.entry.plugin.Name, err)
		return false
	}
	infos, err := client.ListTools(ctx)
	if err != nil {
		_ = client.Close()
		if plugins != nil {
			plugins.setStatus(target.pluginIndex, statusConnectFailed+err.Error())
		}
		r.logf("[toolkit] mcp %q: reconnect list tools failed: %v", plan.entry.plugin.Name, err)
		return false
	}
	fresh, regular := r.buildTools(client, infos)
	registered := r.swapTools(target, registry, fresh, regular)
	target.mu.Lock()
	target.client = client
	target.mu.Unlock()
	if plugins != nil {
		plugins.setConnected(target.pluginIndex, client, statusConnected, registered)
	}
	if old := observed; old != nil {
		_ = old.Close()
	}
	r.logf("[toolkit] mcp %q: reconnected: %d tool(s)", plan.entry.plugin.Name, registered)
	return true
}

// swapTools replaces the server's registry entries with the tools of a fresh
// connection. Proxies that stay are reused (their dispatch target is swapped),
// new tools are registered and vanished tools are unregistered.
func (r *mcpRuntime) swapTools(target *managedMCPServer, registry *rigtool.Registry,
	fresh []rig.Tool, regular map[string]bool) int {
	freshByName := make(map[string]rig.Tool, len(fresh))
	for _, tool := range fresh {
		freshByName[tool.Name()] = tool
	}
	target.mu.Lock()
	oldRegistered := target.registered
	target.registered = make(map[string]rig.Tool, len(freshByName))
	target.inner = make(map[string]rig.Tool, len(freshByName))
	target.mu.Unlock()

	for name := range oldRegistered {
		if _, kept := freshByName[name]; !kept {
			registry.Unregister(name)
		}
	}
	registered := 0
	for name, tool := range freshByName {
		target.mu.Lock()
		target.inner[name] = tool
		proxy, kept := oldRegistered[name]
		target.mu.Unlock()
		if kept {
			target.mu.Lock()
			target.registered[name] = proxy
			target.mu.Unlock()
			if regular[name] {
				registered++
			}
			continue
		}
		freshProxy := &mcpReconnectTool{runtime: r, server: target, name: name, meta: tool}
		proxy = freshProxy
		if err := registry.Register(proxy); err != nil {
			r.logf("[toolkit] mcp %q: skip tool %q on reconnect: %v", target.plan.entry.plugin.Name, name, err)
			target.mu.Lock()
			delete(target.inner, name)
			target.mu.Unlock()
			continue
		}
		target.mu.Lock()
		target.registered[name] = proxy
		target.mu.Unlock()
		if regular[name] {
			registered++
		}
	}
	target.mu.Lock()
	target.regularRegistered = registered
	target.mu.Unlock()
	return registered
}

// close releases every MCP connection (module unload).
func (r *mcpRuntime) close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	servers := r.servers
	r.servers = nil
	r.mu.Unlock()
	for _, server := range servers {
		if client := server.currentClient(); client != nil {
			_ = client.Close()
		}
	}
}

// mcpReconnectTool is the registry entry of one MCP tool. It dispatches to the
// server's current implementation and, when the transport died, rebuilds the
// connection and retries the call once.
type mcpReconnectTool struct {
	runtime *mcpRuntime
	server  *managedMCPServer
	name    string
	meta    rig.Tool
}

var _ rig.Tool = (*mcpReconnectTool)(nil)

// Name implements rig.Tool.
func (t *mcpReconnectTool) Name() string { return t.meta.Name() }

// Description implements rig.Tool.
func (t *mcpReconnectTool) Description() string { return t.meta.Description() }

// Schema implements rig.Tool.
func (t *mcpReconnectTool) Schema() map[string]any { return t.meta.Schema() }

// Invoke implements rig.Tool.
func (t *mcpReconnectTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	inner := t.server.currentTool(t.name)
	if inner == nil {
		return rig.ToolResult{}, fmt.Errorf("mcp: tool %q is no longer available", t.name)
	}
	observed := t.server.currentClient()
	result, err := inner.Invoke(ctx, call)
	if err == nil || !isRetryableMCPError(err) {
		return result, err
	}
	if !t.runtime.reconnect(t.server, observed) {
		return result, err
	}
	inner = t.server.currentTool(t.name)
	if inner == nil {
		return result, err
	}
	return inner.Invoke(ctx, call)
}

// isRetryableMCPError reports whether err means "the connection died and a
// reconnect can fix it": a lost HTTP session, a closed client, or a stdio child
// process that exited / stopped reading.
func isRetryableMCPError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, mcp.ErrSessionExpired) || errors.Is(err, mcp.ErrClosed) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"server process exited",
		"server closed its stdout",
		"write to server",
		"broken pipe",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// defaultMCPMaxOutputBytes bounds one MCP tool result when the assembly does
// not configure maxOutputBytes. It matches the product's generic output budget
// range: large enough for real tool payloads, small enough to keep one MCP
// server from blowing up the model context.
const defaultMCPMaxOutputBytes = 64 << 10

// truncateMCPContent bounds one MCP tool result. The returned bool reports
// whether anything was dropped (the caller keeps the result's IsError field).
// The head and tail of the payload are both preserved and a
// "[truncated: N bytes]" marker replaces the dropped middle, so the model can
// tell the result is incomplete (a silent cut would read as a complete answer).
func truncateMCPContent(content string, limit int) (string, bool) {
	if limit <= 0 || len(content) <= limit {
		return content, false
	}
	dropped := 0
	for attempt := 0; attempt < 4; attempt++ {
		marker := fmt.Sprintf("\n[truncated: %d bytes]\n", dropped)
		remaining := limit - len(marker)
		if remaining < 2 {
			// Tiny budget: a hard cut is the only option, but keep it valid UTF-8.
			return validUTF8Prefix(content, limit), true
		}
		headLen := remaining * 3 / 4
		tailLen := remaining - headLen
		if headLen+tailLen > len(content) {
			tailLen = len(content) - headLen
		}
		head := validUTF8Prefix(content, headLen)
		tail := validUTF8Suffix(content, tailLen)
		if len(head)+len(tail) > len(content) {
			tail = ""
		}
		dropped = len(content) - len(head) - len(tail)
		candidate := head + fmt.Sprintf("\n[truncated: %d bytes]\n", dropped) + tail
		if len(candidate) <= limit {
			return candidate, true
		}
	}
	return validUTF8Prefix(content, limit), true
}

// validUTF8Prefix returns content[:limit] backed off to a rune boundary.
func validUTF8Prefix(content string, limit int) string {
	if limit >= len(content) {
		return content
	}
	if limit < 0 {
		return ""
	}
	for limit > 0 && !utf8.RuneStart(content[limit]) {
		limit--
	}
	return content[:limit]
}

// validUTF8Suffix returns the last limit bytes of content advanced to a rune
// boundary.
func validUTF8Suffix(content string, limit int) string {
	if limit >= len(content) {
		return content
	}
	if limit < 0 {
		return ""
	}
	start := len(content) - limit
	for start < len(content) && !utf8.RuneStart(content[start]) {
		start++
	}
	return content[start:]
}

// truncatedMCPTool applies the product's output budget to one MCP tool result
// (regular tools, resource tools and prompt helpers all go through it).
type truncatedMCPTool struct {
	inner rig.Tool
	limit int
}

var _ rig.Tool = (*truncatedMCPTool)(nil)

// Name implements rig.Tool.
func (t *truncatedMCPTool) Name() string { return t.inner.Name() }

// Description implements rig.Tool.
func (t *truncatedMCPTool) Description() string { return t.inner.Description() }

// Schema implements rig.Tool.
func (t *truncatedMCPTool) Schema() map[string]any { return t.inner.Schema() }

// Invoke implements rig.Tool.
func (t *truncatedMCPTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	result, err := t.inner.Invoke(ctx, call)
	if err != nil {
		return result, err
	}
	result.Content, _ = truncateMCPContent(result.Content, t.limit)
	return result, nil
}
