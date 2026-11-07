package plugins

// MCP server-level switches (mcp.disabledServers): the plugins page reads the
// JSON list, toggles one server's entry (sorted JSON writes, case-insensitive
// matching) and restores rows for servers the toolkit skipped at activation
// from settings.mcp.servers (assembly/workspace-only rows cannot be rebuilt).

import (
	"sort"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// loadDisabledServers parses mcp.disabledServers once per frame lifecycle.
func (st *state) loadDisabledServers(settings contracts.SettingsService, hasSettings bool) {
	if st.disabledServersLoaded {
		return
	}
	st.disabledServersLoaded = true
	st.disabledServers = make(map[string]bool)
	if !hasSettings || settings == nil {
		return
	}
	names, err := contracts.ParseStringListJSON(settings.Get(contracts.SettingMCPDisabledServers, ""))
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
		writeErr = settings.Set(contracts.SettingMCPDisabledServers, encoded)
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
	st.status = st.tr("已保存", "Saved")
	st.statusErr = false
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
