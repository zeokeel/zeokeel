package plugins

// MCP server-level switches (mcp.disabledServers): the plugins page reads the
// JSON list, toggles one server's entry (sorted JSON writes, case-insensitive
// matching) and restores rows for servers the toolkit skipped at activation
// from settings.mcp.servers (assembly/workspace-only rows cannot be rebuilt).

import (
	"context"
	"sort"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// workspaceScopeID resolves the current workspace ID used to scope connector
// rows and the disabled switches. Empty means no workspace. It is cheap
// (workspace.Current is an in-memory read) and refreshes the per-workspace
// caches when a switch lands while the page is mounted, so the connectors tab
// never keeps showing the previous folder's state.
func (st *state) workspaceScopeID() string {
	scope := ""
	if workspace, ok := st.ctx.Get(contracts.KeyWorkspace); ok && workspace != nil {
		if current, has, err := workspace.Current(context.Background()); err == nil && has {
			scope = strings.TrimSpace(current.ID)
		}
	}
	if st.workspaceScopeLoaded && scope != st.workspaceScope {
		st.disabledServersLoaded = false
		st.disabledServers = nil
		st.pendingRestartServers = make(map[string]bool)
		if st.connectors != nil {
			st.connectors.loaded = false
			st.connectors.workspaceLoaded = false
			st.connectors.workspaceID = ""
		}
	}
	st.workspaceScope = scope
	st.workspaceScopeLoaded = true
	return scope
}

// loadDisabledServers parses the workspace-scoped mcp.disabledServers once per
// page lifecycle.
func (st *state) loadDisabledServers(settings contracts.SettingsService, hasSettings bool) {
	if st.disabledServersLoaded {
		return
	}
	st.disabledServersLoaded = true
	st.disabledServers = make(map[string]bool)
	if !hasSettings || settings == nil {
		return
	}
	names, err := contracts.ParseStringListJSON(
		settings.Get(contracts.MCPDisabledServersSettingKey(st.workspaceScopeID()), ""))
	if err != nil {
		st.status = st.tr("禁用服务器配置无效：", "Invalid disabled-servers configuration: ") + err.Error()
		st.statusErr = true
		return
	}
	for _, name := range names {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			st.disabledServers[trimmed] = true
		}
	}
}

// serverDisabled reports whether name is in the disabled set (trimmed,
// case-insensitive; the stored spelling is kept for writes).
func (st *state) serverDisabled(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || len(st.disabledServers) == 0 {
		return false
	}
	for disabled := range st.disabledServers {
		if strings.EqualFold(strings.TrimSpace(disabled), name) {
			return true
		}
	}
	return false
}

// setServerEnabled adds/removes one server name from mcp.disabledServers and
// writes the sorted JSON list back. A failed write is rolled back and surfaced
// in the shared status line.
func (st *state) setServerEnabled(name string, enabled bool, settings contracts.SettingsService) {
	if st.disabledServers == nil {
		st.disabledServers = make(map[string]bool)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	previous := make(map[string]bool, len(st.disabledServers))
	for key := range st.disabledServers {
		previous[key] = true
	}
	removeCaseInsensitive := func() {
		for key := range st.disabledServers {
			if strings.EqualFold(strings.TrimSpace(key), name) {
				delete(st.disabledServers, key)
			}
		}
	}
	if enabled {
		removeCaseInsensitive()
	} else {
		removeCaseInsensitive()
		st.disabledServers[name] = true
	}
	names := make([]string, 0, len(st.disabledServers))
	for disabled := range st.disabledServers {
		if trimmed := strings.TrimSpace(disabled); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	sort.Strings(names)
	encoded, encodeErr := contracts.EncodeStringListJSON(names)
	var writeErr error
	if encodeErr == nil {
		writeErr = settings.Set(contracts.MCPDisabledServersSettingKey(st.workspaceScopeID()), encoded)
	}
	if failure := encodeErr; failure != nil || writeErr != nil {
		st.disabledServers = previous
		if failure == nil {
			failure = writeErr
		}
		st.status = st.tr("保存失败：", "Save failed: ") + failure.Error()
		st.statusErr = true
		return
	}
	if st.mcpHotReload() {
		// The reloader rebuilds the connections immediately; no restart-pending
		// state is needed.
		st.clearPendingEnabledServer(name)
		st.requestMCPReload(st.tr("已应用连接器启停变更", "Connector enablement applied"))
		return
	}
	// Older compositions without a reloader keep the restart-pending copy.
	if enabled {
		st.pendingRestartServers[name] = true
	} else {
		st.clearPendingEnabledServer(name)
		st.status = st.tr("已停用（重启后断开）", "Disabled (disconnects after restart)")
	}
	if enabled {
		st.status = st.tr("已启用（重启后连接）", "Enabled (connects after restart)")
	}
	st.statusErr = false
}

// clearPendingEnabledServer drops one name from pendingRestartServers, matching
// the stored spelling case-insensitively.
func (st *state) clearPendingEnabledServer(name string) {
	name = strings.TrimSpace(name)
	for pending := range st.pendingRestartServers {
		if strings.EqualFold(strings.TrimSpace(pending), name) {
			delete(st.pendingRestartServers, pending)
		}
	}
}

// enabledPendingServerRows restores rows the user enabled on this page but that
// the toolkit has not started yet (servers start at launch). Without this row
// the card would vanish the moment its switch is turned back on, which reads as
// a deletion; the row says the server is configured and connects after restart.
func (st *state) enabledPendingServerRows(settings contracts.SettingsService,
	hasSettings bool, live []contracts.MCPPlugin) []contracts.MCPPlugin {
	if !hasSettings || settings == nil || len(st.pendingRestartServers) == 0 {
		return nil
	}
	rows, err := contracts.ParseMCPServersJSON(settings.Get(contracts.SettingMCPServers, ""))
	if err != nil {
		return nil
	}
	liveNames := make(map[string]bool, len(live))
	for _, server := range live {
		for _, name := range []string{server.Name, server.ServerName} {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				liveNames[strings.ToLower(trimmed)] = true
			}
		}
	}
	restored := make([]contracts.MCPPlugin, 0, len(rows))
	for _, row := range rows {
		if !contracts.MCPServerVisibleInWorkspace(row, st.workspaceScopeID()) {
			continue
		}
		name := strings.TrimSpace(row.Name)
		if name == "" || liveNames[strings.ToLower(name)] || st.serverDisabled(name) ||
			!st.pendingEnabled(name) {
			continue
		}
		liveNames[strings.ToLower(name)] = true
		restored = append(restored, contracts.MCPPlugin{
			Name:   name,
			URL:    strings.TrimSpace(row.URL),
			Status: st.tr("已启用（重启后连接）", "Enabled (connects after restart)"),
		})
	}
	sort.SliceStable(restored, func(left, right int) bool {
		return strings.ToLower(restored[left].Name) < strings.ToLower(restored[right].Name)
	})
	return restored
}

// pendingEnabled reports whether one server was enabled during this visit.
func (st *state) pendingEnabled(name string) bool {
	name = strings.TrimSpace(name)
	for pending := range st.pendingRestartServers {
		if strings.EqualFold(strings.TrimSpace(pending), name) {
			return true
		}
	}
	return false
}

// disabledServerRows restores the disabled rows that settings.mcp.servers can
// describe and the live plugins service no longer lists. Assembly/workspace
// rows are not in settings, so they cannot be restored here (ignored).
func (st *state) disabledServerRows(settings contracts.SettingsService, hasSettings bool,
	live []contracts.MCPPlugin) []contracts.MCPPlugin {
	if !hasSettings || settings == nil || len(st.disabledServers) == 0 {
		return nil
	}
	rows, err := contracts.ParseMCPServersJSON(settings.Get(contracts.SettingMCPServers, ""))
	if err != nil {
		return nil
	}
	liveNames := make(map[string]bool, len(live))
	for _, server := range live {
		for _, name := range []string{server.Name, server.ServerName} {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				liveNames[strings.ToLower(trimmed)] = true
			}
		}
	}
	restored := make([]contracts.MCPPlugin, 0, len(rows))
	for _, row := range rows {
		if !contracts.MCPServerVisibleInWorkspace(row, st.workspaceScopeID()) {
			continue
		}
		name := strings.TrimSpace(row.Name)
		if name == "" || liveNames[strings.ToLower(name)] || !st.serverDisabled(name) {
			continue
		}
		liveNames[strings.ToLower(name)] = true
		restored = append(restored, contracts.MCPPlugin{
			Name:   name,
			URL:    strings.TrimSpace(row.URL),
			Status: st.tr("已停用（重启生效）", "Disabled (restart applies)"),
		})
	}
	sort.SliceStable(restored, func(left, right int) bool {
		return strings.ToLower(restored[left].Name) < strings.ToLower(restored[right].Name)
	})
	return restored
}
