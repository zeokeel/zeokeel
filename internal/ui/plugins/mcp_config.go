package plugins

// MCP env masking: the connection facts the settings row carries (mcp.servers)
// are rendered by the connector detail modal's property sheet
// (connectors_edit.go, connectorDetailRows). What stays here is the one rule
// that must not drift: an env entry whose key or value looks secret renders as
// "*** · 值已隐藏", with the key itself never hidden.
//
// The settings-derived "连接配置" block of the expanded server card and the row
// lookup it used were removed: the detail modal renders the same facts from the
// same settings row, so the block was a second rendering of one truth and had
// no caller left (A1 F-08).

import (
	"strings"
)

// sensitiveMCPEnvMarkers are the env key/value markers whose values are masked
// in the connection facts (KEY/TOKEN/SECRET/PASSWORD or Authorization).
var sensitiveMCPEnvMarkers = []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "AUTHORIZATION"}

// mcpEnvValueSensitive reports whether an env key or value looks secret.
func mcpEnvValueSensitive(text string) bool {
	upper := strings.ToUpper(text)
	for _, marker := range sensitiveMCPEnvMarkers {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

// maskMCPEnvValue renders one KEY=VALUE env entry, masking secret values as
// "*** · 值已隐藏" (the key itself is never hidden).
func maskMCPEnvValue(st *state, entry string) string {
	trimmed := strings.TrimSpace(entry)
	key, value, ok := strings.Cut(trimmed, "=")
	if !ok {
		return trimmed
	}
	if !mcpEnvValueSensitive(key) && !mcpEnvValueSensitive(value) {
		return trimmed
	}
	return strings.TrimSpace(key) + "=*** · " + st.tr("值已隐藏", "value hidden")
}
