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

// loadDisabledTools parses tools.disabled (all tools) and the legacy
// mcp.disabledTools once per page visit.
func (st *state) loadDisabledTools(settings contracts.SettingsService, hasSettings bool) {
	if st.disabledLoaded {
		return
	}
	st.disabledLoaded = true
	st.disabledTools = make(map[string]bool)
	st.disabledMCPTools = make(map[string]bool)
	if !hasSettings || settings == nil {
		return
	}
	var errs []string
	for _, source := range []struct {
		key    string
		target *map[string]bool
	}{
		{contracts.SettingToolsDisabled, &st.disabledTools},
		{contracts.SettingMCPDisabledTools, &st.disabledMCPTools},
	} {
		names, err := contracts.ParseStringListJSON(settings.Get(source.key, ""))
		if err != nil {
			errs = append(errs, source.key+": "+err.Error())
			continue
		}
		for _, name := range names {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				(*source.target)[trimmed] = true
			}
		}
	}
	st.disabledErr = strings.Join(errs, "; ")
	// Snapshot the startup state: "restart needed" is a net change against it,
	// so flipping a tool off and back on clears the hint instead of leaving a
	// stale warning.
	st.startupDisabledTools = make(map[string]bool, len(st.disabledTools)+len(st.disabledMCPTools))
	for name := range st.disabledTools {
		st.startupDisabledTools[name] = true
	}
	for name := range st.disabledMCPTools {
		st.startupDisabledTools[name] = true
	}
}

// toolDisabled reports whether one tool is disabled by either setting row.
func (st *state) toolDisabled(name string) bool {
	return st.disabledTools[name] || st.disabledMCPTools[name]
}

// toolRestartPending reports whether one tool's switch differs from its
// startup state (the toolkit re-registers tools at process start only).
func (st *state) toolRestartPending(name string) bool {
	return st.startupDisabledTools[name] != st.toolDisabled(name)
}

// setToolEnabled adds/removes one tool from its setting (tools.disabled for
// built-ins, mcp.disabledTools for MCP tools) and writes the sorted JSON list
// back. A failed write is reverted and surfaced in the status line.
func (st *state) setToolEnabled(name string, enabled bool, settings contracts.SettingsService, mcp bool) {
	if st.disabledTools == nil {
		st.disabledTools = make(map[string]bool)
	}
	if st.disabledMCPTools == nil {
		st.disabledMCPTools = make(map[string]bool)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	key, target := contracts.SettingToolsDisabled, st.disabledTools
	if mcp {
		key, target = contracts.SettingMCPDisabledTools, st.disabledMCPTools
	}
	wasDisabled := target[name]
	if enabled {
		delete(target, name)
	} else {
		target[name] = true
	}
	names := make([]string, 0, len(target))
	for disabled := range target {
		if trimmed := strings.TrimSpace(disabled); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	sort.Strings(names)
	encoded, encodeErr := contracts.EncodeStringListJSON(names)
	var writeErr error
	if encodeErr == nil {
		writeErr = settings.Set(key, encoded)
	}
	if failure := encodeErr; failure != nil || writeErr != nil {
		if wasDisabled {
			target[name] = true
		} else {
			delete(target, name)
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

// setMCPToolEnabled is the legacy MCP-only entry point retained for callers
