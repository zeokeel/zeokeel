package mcp

import "strings"

// Workspace configuration gate.
//
// <workspace>/mcp.json arrives with cloned repositories, so it is untrusted
// data: starting the commands it declares is third-party code execution with no
// approval. The app therefore only reads it when the user explicitly opts in
// through the mcp.workspace_config setting.
const (
	// WorkspaceConfigKey is the setting that gates <workspace>/mcp.json.
	WorkspaceConfigKey = "mcp.workspace_config"
	// WorkspaceConfigOff is the default value: the workspace file is ignored.
	WorkspaceConfigOff = "off"
	// WorkspaceConfigAllow enables the workspace file after an explicit opt-in.
	WorkspaceConfigAllow = "allow"
)

// WorkspaceConfigAllowed reports whether value opts into running
// <workspace>/mcp.json. Only the exact value "allow" (case-insensitive,
// surrounding space ignored) enables it: this setting guards code execution
// from repository content, so ambiguous truthy aliases ("on", "true", "1")
// fail closed. Every other value, including the default "off", keeps workspace
// MCP servers disabled.
func WorkspaceConfigAllowed(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), WorkspaceConfigAllow)
}

// NormalizeWorkspaceConfig renders a raw setting value as the documented value
// ("allow" or "off"). Diagnostics (inspect) use it so an unknown or empty value
// is reported as the effective fail-closed "off" instead of echoing arbitrary
// text.
func NormalizeWorkspaceConfig(value string) string {
	if WorkspaceConfigAllowed(value) {
		return WorkspaceConfigAllow
	}
	return WorkspaceConfigOff
}
