package toolkit

// MCP plugin state for the Plugins screen: the toolkit owns the MCP clients, so
// it also owns the read model the screen shows for each configured server.
// CONNECTIONS are established once at activation (failures are rendered as
// "连接失败" entries, not activation errors); Refresh pings the live clients and
// re-lists their tools without closing anything.

import (
	"context"
	"strings"
	"sync"

	"github.com/zeokeel/zeokeel-rig/tools/mcp"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// pluginEntry is the toolkit's internal state for one configured server.
type pluginEntry struct {
	plugin contracts.MCPPlugin
	client *mcp.Client
}

// pluginsService implements contracts.PluginsService.
type pluginsService struct {
	mu      sync.Mutex
	entries []pluginEntry
	// reconnect rebuilds the connection behind one entry (set by the MCP
	// runtime). It reports whether the row is usable again.
	reconnect func(index int) bool
	// toolCounter reports how many tools of one server actually reached the
	// registry (disabled/duplicate tools excluded), so a Refresh cannot count
	// tools the model never sees.
	toolCounter func(index int) (int, bool)
}

var _ contracts.PluginsService = (*pluginsService)(nil)

// newPluginsService snapshots the entries built at activation.
func newPluginsService(entries []pluginEntry) *pluginsService {
	copied := make([]pluginEntry, len(entries))
	copy(copied, entries)
	return &pluginsService{entries: copied}
}

// bindReconnect connects the runtime's reconnect path to the read model.
func (s *pluginsService) bindReconnect(reconnect func(index int) bool) {
	s.mu.Lock()
	s.reconnect = reconnect
	s.mu.Unlock()
}

// bindToolCounter connects the registry-backed tool count used by Refresh.
func (s *pluginsService) bindToolCounter(counter func(index int) (int, bool)) {
	s.mu.Lock()
	s.toolCounter = counter
	s.mu.Unlock()
}

// setStatus updates one row's status line.
func (s *pluginsService) setStatus(index int, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 0 || index >= len(s.entries) {
		return
	}
	s.entries[index].plugin.Status = status
}

// setConnected refreshes one row from a live client: the client pointer (so a
// later Refresh talks to the current connection), the handshake identity, the
// registered tool count and the status line.
func (s *pluginsService) setConnected(index int, client *mcp.Client, status string, toolCount int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 0 || index >= len(s.entries) {
		return
	}
	entry := &s.entries[index]
	entry.client = client
	entry.plugin.Status = status
	entry.plugin.Tools = toolCount
	if client != nil {
		entry.plugin.ServerName = client.ServerName()
		entry.plugin.Version = client.ServerVersion()
		entry.plugin.Protocol = client.ProtocolVersion()
	}
}

// MCP implements contracts.PluginsService.
func (s *pluginsService) MCP() []contracts.MCPPlugin {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]contracts.MCPPlugin, 0, len(s.entries))
	for _, entry := range s.entries {
		out = append(out, entry.plugin)
	}
	return out
}

// Refresh implements contracts.PluginsService: every live client is pinged and
// its tool count refreshed; failed clients are marked "连接失败". Network calls
// run without the state mutex so MCP()/concurrent Refresh calls stay responsive.
func (s *pluginsService) Refresh(ctx context.Context) []contracts.MCPPlugin {
	if ctx == nil {
		ctx = context.Background()
	}
	type target struct {
		index  int
		client *mcp.Client
	}
	s.mu.Lock()
	targets := make([]target, 0, len(s.entries))
	for index, entry := range s.entries {
		if entry.client != nil {
			targets = append(targets, target{index: index, client: entry.client})
		}
	}
	s.mu.Unlock()

	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			break
		}
		client := target.client
		if err := client.Ping(ctx); err != nil {
			// A dead connection is recovered here (the Plugins page refresh is
			// the user's retry path): give the runtime a chance to rebuild it
			// and retry against the new client.
			if !s.tryReconnect(target.index) {
				s.setStatus(target.index, statusConnectFailed+err.Error())
				continue
			}
			s.mu.Lock()
			client = s.entries[target.index].client
			s.mu.Unlock()
			if client == nil {
				s.setStatus(target.index, statusConnectFailed+"连接已关闭")
				continue
			}
			if pingErr := client.Ping(ctx); pingErr != nil {
				s.setStatus(target.index, statusConnectFailed+pingErr.Error())
				continue
			}
		}
		infos, err := client.ListTools(ctx)
		if err != nil {
			s.setStatus(target.index, statusConnectFailed+err.Error())
			continue
		}
		count := len(infos)
		s.mu.Lock()
		counter := s.toolCounter
		s.mu.Unlock()
		if counter != nil {
			if registered, ok := counter(target.index); ok {
				count = registered
			}
		}
		s.setConnected(target.index, client, statusConnected, count)
	}
	return s.MCP()
}

// tryReconnect asks the runtime to rebuild one dead connection.
func (s *pluginsService) tryReconnect(index int) bool {
	s.mu.Lock()
	reconnect := s.reconnect
	s.mu.Unlock()
	if reconnect == nil {
		return false
	}
	return reconnect(index)
}

const (
	// statusConnected is shown for a healthy MCP connection.
	statusConnected = "已连接"
	// statusConnectFailed prefixes every failure status.
	statusConnectFailed = "连接失败: "
)

// pluginDisplayName resolves the server identity used by the Plugins list:
// the configured name wins, then the name reported by the server handshake,
// then the generic "remote".
func pluginDisplayName(configured string, client *mcp.Client) string {
	if name := strings.TrimSpace(configured); name != "" {
		return name
	}
	if client != nil {
		if name := strings.TrimSpace(client.ServerName()); name != "" {
			return name
		}
	}
	return "remote"
}
