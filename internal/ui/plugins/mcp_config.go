package plugins

// MCP connection config: the expanded server card shows the settings-derived
// configuration (mcp.servers) for servers whose row matches the card's name.
// Servers from assembly/workspace sources have no matching row and keep the
// plain handshake details.

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// sensitiveMCPEnvMarkers are the env key/value markers whose values are masked
// in the connection block (KEY/TOKEN/SECRET/PASSWORD or Authorization).
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

// findMCPServerConfig returns the settings mcp.servers row matching name.
func findMCPServerConfig(rows []contracts.MCPServerConfig, name string) (contracts.MCPServerConfig, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return contracts.MCPServerConfig{}, false
	}
	for _, row := range rows {
		if strings.TrimSpace(row.Name) == name {
			return row, true
		}
	}
	return contracts.MCPServerConfig{}, false
}

// buildMCPConnectionConfig adds the settings-derived "连接配置" block to one
// expanded server card: transport fields, command/args/env (secrets masked) and
// a parse error when the settings row is malformed.
func buildMCPConnectionConfig(c *ui.Context, t *design.Tokens, st *state,
	server contracts.MCPPlugin, settings contracts.SettingsService, hasSettings bool) {
	if !hasSettings || settings == nil {
		return
	}
	rows, err := contracts.ParseMCPServersJSON(settings.Get(contracts.SettingMCPServers, ""))
	if err != nil {
		uikit.ErrorLine(c, st.tr("配置无法解析", "Configuration could not be parsed"))
		return
	}
	row, ok := findMCPServerConfig(rows, firstNonEmpty(server.Name, server.ServerName))
	if !ok {
		return
	}
	design.Apply(ui.Text(c, st.tr("连接配置", "Connection")), t.Font(design.TypeXXS)).
		FontWeight(600).TextColor(t.LabelSecondary)
	details := make([]uikit.KeyValue, 0, 8)
	if name := strings.TrimSpace(row.Name); name != "" {
		details = append(details, uikit.KeyValue{Key: st.tr("名称", "Name"), Value: name})
	}
	if transport := strings.TrimSpace(row.Transport); transport != "" {
		details = append(details, uikit.KeyValue{Key: st.tr("传输", "Transport"), Value: transport})
	}
	if url := strings.TrimSpace(row.URL); url != "" {
		details = append(details, uikit.KeyValue{Key: "URL", Value: url})
	}
	if command := strings.TrimSpace(row.Command); command != "" {
		details = append(details, uikit.KeyValue{Key: st.tr("命令", "Command"), Value: command})
	}
	if len(row.Args) > 0 {
		details = append(details, uikit.KeyValue{Key: st.tr("参数", "Arguments"), Value: strings.Join(row.Args, " ")})
	}
	for _, env := range row.Env {
		details = append(details, uikit.KeyValue{Key: st.tr("环境", "Environment"), Value: maskMCPEnvValue(st, env)})
	}
	uikit.KeyValueRows(c, details)
}
