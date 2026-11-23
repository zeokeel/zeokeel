// Package workspacecleanup removes the product settings a deleted workspace
// owned. It is deliberately settings-only: the workspace folder and its files
// are never touched (deleting a workspace forgets the registration, it does not
// delete the repository).
package workspacecleanup

import (
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Settings removes every scoped product setting that belongs to workspaceID:
// skills.dirs.<id>, the MCP disabled server/tool lists, workspace-scoped
// tool.disabled rows, mcp.oauth.<id>.* token records and the connector rows
// whose WorkspaceID is id (their legacy mcp.oauth.<name-hash> token records are
// removed too). Rows with an empty WorkspaceID are legacy-global and stay; rows
// and keys of other workspaces stay untouched.
func Settings(settings contracts.SettingsService, workspaceID string) error {
	id := strings.TrimSpace(workspaceID)
	if settings == nil || id == "" {
		return nil
	}
	victims := map[string]bool{
		contracts.SkillsDirsSettingKey(id):         true,
		contracts.MCPDisabledServersSettingKey(id): true,
		contracts.MCPDisabledToolsSettingKey(id):   true,
		contracts.SettingToolsDisabled + "." + id:  true,
	}
	// Workspace-scoped key families that do not have a dedicated helper yet
	// (the OAuth token record is keyed mcp.oauth.<workspaceID>.<server>).
	for key := range settings.All() {
		if strings.HasPrefix(key, "mcp.oauth."+id+".") ||
			strings.HasPrefix(key, "skills.dirs."+id+".") {
			victims[key] = true
		}
	}

	// Connector rows: drop only rows owned by this workspace and collect their
	// names so their legacy token records go too.
	rows, err := contracts.ParseMCPServersJSON(settings.Get(contracts.SettingMCPServers, ""))
	if err != nil {
		return err
	}
	kept := make([]contracts.MCPServerConfig, 0, len(rows))
	removed := make([]string, 0)
	for _, row := range rows {
		if strings.TrimSpace(row.WorkspaceID) == id {
			removed = append(removed, row.Name)
			continue
		}
		kept = append(kept, row)
	}
	var firstErr error
	if len(removed) > 0 {
		encoded, err := contracts.EncodeMCPServersJSON(kept)
		if err != nil {
			return err
		}
		if err := settings.Set(contracts.SettingMCPServers, encoded); err != nil {
			return err
		}
		for _, name := range removed {
			victims[contracts.MCPAuthSettingKey(name)] = true
		}
	}
	for key := range victims {
		if err := deleteSetting(settings, key); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// deleteSetting removes one row when the store supports deletion; older settings
// implementations fall back to an empty value (Get then reports unset).
func deleteSetting(settings contracts.SettingsService, key string) error {
	if deleter, ok := settings.(contracts.SettingsDeleter); ok {
		return deleter.Delete(key)
	}
	return settings.Set(key, "")
}
