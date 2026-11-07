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
}

var _ contracts.PluginsService = (*pluginsService)(nil)

// newPluginsService snapshots the entries built at activation.
func newPluginsService(entries []pluginEntry) *pluginsService {
	copied := make([]pluginEntry, len(entries))
	copy(copied, entries)
	return &pluginsService{entries: copied}
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
		if err := target.client.Ping(ctx); err != nil {
			s.mu.Lock()
			s.entries[target.index].plugin.Status = statusConnectFailed + err.Error()
			s.mu.Unlock()
			continue
		}
		infos, err := target.client.ListTools(ctx)
		s.mu.Lock()
		entry := &s.entries[target.index]
		if err != nil {
			entry.plugin.Status = statusConnectFailed + err.Error()
			s.mu.Unlock()
			continue
		}
		entry.plugin.ServerName = target.client.ServerName()
		entry.plugin.Version = target.client.ServerVersion()
		entry.plugin.Protocol = target.client.ProtocolVersion()
		entry.plugin.Tools = len(infos)
		entry.plugin.Status = statusConnected
		s.mu.Unlock()
	}
	return s.MCP()
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
