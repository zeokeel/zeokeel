package plugins

// MCP tool switches on the Plugins tool catalogue. Source identification is by
// the catalogue name: the toolkit registers rig MCP tools with the default
// "mcp__" prefix and MCP resource tools as "mcp_list_resources"/
// "mcp_read_resource", so names beginning with "mcp_" are MCP-supplied. The
// contracts.MCPPlugin read model only carries a tool count, not tool names, so
// the registry name is the reliable per-tool source marker.

import (
	"sort"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// isMCPToolName reports whether a model-visible tool name belongs to an MCP
// connection.
func isMCPToolName(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), "mcp_")
}

// loadDisabledTools parses mcp.disabledTools once per page visit.
func (st *state) loadDisabledTools(settings contracts.SettingsService, hasSettings bool) {
	if st.disabledLoaded {
		return
	}
	st.disabledLoaded = true
	st.disabledTools = make(map[string]bool)
	if !hasSettings || settings == nil {
		return
	}
	names, err := contracts.ParseStringListJSON(settings.Get(contracts.SettingMCPDisabledTools, ""))
	if err != nil {
		st.disabledErr = err.Error()
		return
	}
	for _, name := range names {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			st.disabledTools[trimmed] = true
		}
	}
}

// setMCPToolEnabled adds/removes one MCP tool from mcp.disabledTools and writes
// the sorted JSON list back. A failed write is reverted and surfaced in the
// catalogue status line.
func (st *state) setMCPToolEnabled(name string, enabled bool, settings contracts.SettingsService) {
	if st.disabledTools == nil {
		st.disabledTools = make(map[string]bool)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	wasDisabled := st.disabledTools[name]
	if enabled {
		delete(st.disabledTools, name)
	} else {
		st.disabledTools[name] = true
	}
	names := make([]string, 0, len(st.disabledTools))
	for disabled := range st.disabledTools {
		if trimmed := strings.TrimSpace(disabled); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	sort.Strings(names)
	encoded, encodeErr := contracts.EncodeStringListJSON(names)
	var writeErr error
	if encodeErr == nil {
		writeErr = settings.Set(contracts.SettingMCPDisabledTools, encoded)
	}
	if failure := encodeErr; failure != nil || writeErr != nil {
		if wasDisabled {
			st.disabledTools[name] = true
		} else {
			delete(st.disabledTools, name)
		}
		if failure == nil {
			failure = writeErr
		}
		st.status = st.tr("保存失败：", "Save failed: ") + failure.Error()
		st.statusErr = true
		return
	}
	st.status = st.tr("已保存，重启生效", "Saved; restart applies")
	st.statusErr = false
}
