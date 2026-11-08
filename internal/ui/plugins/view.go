package plugins

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// toolRegistry is the toolkit registry type from the product contract.
type toolRegistry = rigtools.Registry

// catalogTool is one entry of the tool catalogue (converted from the rig
// registry spec so the view code stays product-local).
type catalogTool struct {
	Name        string
	Description string
	// MCP marks tools supplied by an MCP connection (name prefix mcp_); only
	// those get the per-tool switch.
	MCP bool
}

// toolCategoryOrder is the fixed order categories render in.
var toolCategoryOrder = []string{"文件", "终端与任务", "网络", "会话与记忆", "团队与编排", "其他"}

// buildScreen renders one frame of the Plugins screen: the header, the MCP
// server cards (with the native Refresh action) and the searchable tool
// catalogue grouped by category. Service calls run in click handlers only.
func buildScreen(c *ui.Context, runtime contracts.UIRuntime, cfg Config, st *state) {
	t := design.Of(c)
	pluginsService, hasPlugins := st.ctx.Get(contracts.KeyPlugins)
	toolkit, hasToolkit := st.ctx.Get(contracts.KeyToolkit)
	settings, hasSettings := st.ctx.Get(contracts.KeySettings)

	ui.Column(c).FillWidth().Grow(1).Gap(design.Space4).Children(func() {
		ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
			design.Apply(ui.Text(c, st.localized(cfg.Title, "插件", "Plugins")), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, st.localized(cfg.Description,
				"查看已连接的 MCP 服务器与产品内置的工具目录。",
				"Connected MCP servers and the product's built-in tool catalogue.")),
				t.TextMuted(design.TypeXS)).MaxLines(2)
		})

		if st.status != "" {
			color := t.Success
			if st.statusErr {
				color = t.DangerText
			}
			design.Apply(ui.Text(c, st.status), t.Font(design.TypeXXS)).TextColor(color)
		}

		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			ui.Column(c).FillWidth().Gap(design.Space5).Children(func() {
				buildMCPsSection(c, t, runtime, pluginsService, hasPlugins, cfg, st, settings, hasSettings)
				buildToolsSection(c, t, toolkit, hasToolkit, st, cfg, runtime, settings, hasSettings)
				modulesLister, hasModules := st.ctx.Get(contracts.KeyModuleLister)
				buildModulesSection(c, t, st, modulesLister, hasModules)
			})
		})
	})
}

// buildMCPsSection is the MCP plugin connections section.
func buildMCPsSection(
	c *ui.Context,
	t *design.Tokens,
	runtime contracts.UIRuntime,
	service contracts.PluginsService,
	hasService bool,
	cfg Config,
	st *state,
	settings contracts.SettingsService,
	hasSettings bool,
) {
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, st.localized(cfg.MCPSection, "MCP 插件", "MCP plugins")), t.SectionLabel())
			ui.Spacer(c).Grow(1)
			if hasService {
				refresh := ui.Button(c, st.tr("刷新", "Refresh")).Tooltip(st.tr("重新连接并刷新 MCP 服务器状态", "Reconnect and refresh MCP server status"))
				if refresh.Clicked() {
					service.Refresh(context.Background())
					runtime.Invalidate()
				}
			}
		})
		st.loadDisabledServers(settings, hasSettings)
		var servers []contracts.MCPPlugin
		if hasService {
			servers = service.MCP()
		}
		// Disabled servers are not started, so the service omits them; settings
		// still knows the user-configured rows, which restore their cards.
		servers = append(servers, st.disabledServerRows(settings, hasSettings, servers)...)
		if len(servers) == 0 {
			if !hasService {
				buildPluginsMuted(c, t, st.tr("插件服务未启用", "Plugins service not enabled"))
				return
			}
			buildPluginsMuted(c, t, st.tr("还没有配置 MCP 服务器（assembly 的 mcpServers）",
				"No MCP servers configured yet (assembly mcpServers)"))
			return
		}
		for index, server := range servers {
			buildMCPCard(c, t, runtime, st, server, index, settings, hasSettings)
		}
	})
}

// mcpServerKey identifies one MCP server card across frames and refreshes.
func mcpServerKey(server contracts.MCPPlugin, index int) string {
	switch {
	case strings.TrimSpace(server.Name) != "":
		return "name:" + strings.TrimSpace(server.Name)
	case strings.TrimSpace(server.ServerName) != "":
		return "server:" + strings.TrimSpace(server.ServerName)
	case strings.TrimSpace(server.URL) != "":
		return "url:" + strings.TrimSpace(server.URL)
	default:
		return fmt.Sprintf("index:%d", index)
	}
}

// buildMCPCard is one expandable MCP server card: the collapsed header shows
// the name, status and tool count; expanding reveals the transport, the
// handshake metadata and the full status text.
func buildMCPCard(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	st *state, server contracts.MCPPlugin, index int, settings contracts.SettingsService, hasSettings bool) {
	key := mcpServerKey(server, index)
	expanded := st.mcpExpanded[key]

	card := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space3).Gap(design.Space2)
	card.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			icon := icons.Plug()
			iconColor := t.LabelSecondary
			if strings.TrimSpace(server.URL) == "" {
				icon = icons.Plugin()
			}
			ui.Icon(c, icon).Size(16, 16).TextColor(iconColor)
			design.Apply(ui.Text(c, firstNonEmpty(server.Name, st.tr("(未命名服务器)", "(Untitled server)"))), t.Font(design.TypeS)).
				FontWeight(600).TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			// The collapsed badge carries only the short status prefix; the full
			// diagnostic (which can be very long) lives in the expanded details.
			if st.serverDisabled(server.Name) {
				design.Apply(ui.Badge(c, st.tr("已停用（重启生效）", "Disabled (restart applies)")), t.Badge()).
					TextColor(t.LabelTertiary)
			}
			design.Apply(ui.Badge(c, pluginStatusLabel(st, collapsedMCPStatus(server.Status))), t.Badge()).
				TextColor(pluginStatusColor(t, server.Status))
			design.Apply(ui.Badge(c, st.tr(fmt.Sprintf("%d 个工具", server.Tools), fmt.Sprintf("%d tools", server.Tools))), t.Badge())
			toggleLabel := st.tr("详情", "Details")
			if expanded {
				toggleLabel = st.tr("收起", "Hide")
			}
			if ui.Button(c, toggleLabel).Clicked() {
				st.mcpExpanded[key] = !expanded
				runtime.Invalidate()
			}
		})
		if !expanded {
			return
		}
		ui.Divider(c)
		transport := st.tr("本地 stdio", "Local stdio")
		if strings.TrimSpace(server.URL) != "" {
			transport = "HTTP"
		}
		mcpDetailRow(c, t, st.tr("类型", "Type"), transport)
		if url := strings.TrimSpace(server.URL); url != "" {
			mcpDetailRow(c, t, "URL", url)
		}
		mcpDetailRow(c, t, st.tr("服务器", "Server"), mcpDetailValue(server.ServerName))
		mcpDetailRow(c, t, st.tr("版本", "Version"), mcpDetailValue(server.Version))
		mcpDetailRow(c, t, st.tr("协议", "Protocol"), mcpDetailValue(server.Protocol))
		mcpDetailRow(c, t, st.tr("工具数", "Tools"), st.tr(fmt.Sprintf("%d 个工具", server.Tools), fmt.Sprintf("%d tools", server.Tools)))
		mcpDetailRow(c, t, st.tr("状态", "Status"), mcpDetailValue(server.Status))
		if hasSettings && settings != nil {
			if name := firstNonEmpty(server.Name, server.ServerName); name != "" {
				enabled := !st.serverDisabled(name)
				c.Key("mcp-server-switch/" + key)
				toggle := ui.Toggle(c, &enabled, st.tr("启用", "Enabled"))
				if toggle.Changed() {
					st.setServerEnabled(name, enabled, settings)
					runtime.Invalidate()
				}
			}
		}
	})
}

// mcpDetailValue renders one detail value, with an em dash when empty.
func mcpDetailValue(value string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return "—"
}

// mcpDetailRow is one label/value row of the expanded card.
func mcpDetailRow(c *ui.Context, t *design.Tokens, label, value string) {
	ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
		design.Apply(ui.Text(c, label), t.Font(design.TypeXXS)).
			TextColor(t.LabelTertiary).Width(64).Shrink(0)
		design.Apply(ui.Text(c, value), t.Font(design.TypeXS)).
			TextColor(t.LabelPrimary).Grow(1).MaxLines(2).Ellipsis("…")
	})
}

// buildToolsSection is the searchable, category-grouped tool catalogue.
func buildToolsSection(
	c *ui.Context,
	t *design.Tokens,
	toolkit *toolRegistry,
	hasToolkit bool,
	st *state,
	cfg Config,
	runtime contracts.UIRuntime,
	settings contracts.SettingsService,
	hasSettings bool,
) {
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		st.loadDisabledTools(settings, hasSettings)
		var catalog []catalogTool
		if hasToolkit && toolkit != nil {
			for _, spec := range toolkit.Specs() {
				catalog = append(catalog, catalogTool{
					Name:        spec.Name,
					Description: spec.Description,
					MCP:         isMCPToolName(spec.Name),
				})
			}
		}
		// Disabled tools are not registered, so they are absent from the
		// registry; keep them in the catalogue from either setting so the switch
		// can turn them back on.
		if hasSettings && settings != nil {
			hiddenSet := make(map[string]bool, len(st.disabledTools)+len(st.disabledMCPTools))
			for name := range st.disabledTools {
				hiddenSet[name] = true
			}
			for name := range st.disabledMCPTools {
				hiddenSet[name] = true
			}
			hidden := make([]string, 0, len(hiddenSet))
			for name := range hiddenSet {
				if !catalogHasTool(catalog, name) {
					hidden = append(hidden, name)
				}
			}
			sort.Strings(hidden)
			for _, name := range hidden {
				catalog = append(catalog, catalogTool{Name: name, MCP: isMCPToolName(name)})
			}
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, st.localized(cfg.ToolsSection, "工具目录", "Tool catalogue")), t.SectionLabel())
			ui.Spacer(c).Grow(1)
			if hasToolkit {
				design.Apply(ui.Badge(c, st.tr(fmt.Sprintf("%d 个工具", len(catalog)), fmt.Sprintf("%d tools", len(catalog)))), t.Badge())
			}
		})
		if !hasToolkit {
			buildPluginsMuted(c, t, st.tr("工具目录未启用", "Tool catalogue not enabled"))
			return
		}
		design.Apply(ui.Text(c, st.tr("工具开关在重启后生效。", "Tool switches apply after a restart.")),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
		if st.disabledErr != "" {
			design.Apply(ui.Text(c, st.tr("禁用工具配置无效：", "Invalid disabled-tools configuration: ")+st.disabledErr),
				t.Font(design.TypeXXS)).TextColor(t.DangerText)
		}
		ui.SearchField(c, &st.query).Label(st.tr("搜索工具", "Search tools")).Placeholder(st.tr("搜索工具", "Search tools")).FillWidth()
		filtered := filterCatalog(catalog, st.query)
		if len(filtered) == 0 {
			buildPluginsMuted(c, t, st.tr("没有匹配的工具", "No matching tools"))
			return
		}
		grouped := make(map[string][]catalogTool, len(toolCategoryOrder))
		for _, tool := range filtered {
			category := toolCategory(tool.Name)
			grouped[category] = append(grouped[category], tool)
		}
		for _, category := range toolCategoryOrder {
			tools := grouped[category]
			if len(tools) == 0 {
				continue
			}
			design.Apply(ui.Text(c, st.categoryLabel(category)), t.Font(design.TypeXXS)).
				FontWeight(600).TextColor(t.LabelTertiary)
			for _, tool := range tools {
				buildToolRow(c, t, tool, st, runtime, settings, hasSettings)
			}
		}
	})
}

// buildToolRow is one catalogue entry: mono name plus its description and, for
// MCP tools, the per-tool enable switch. The catalogue is a dense reference
// list, so rows are plain (no card per tool); category labels and spacing carry
// the grouping.
func buildToolRow(c *ui.Context, t *design.Tokens, tool catalogTool, st *state,
	runtime contracts.UIRuntime, settings contracts.SettingsService, hasSettings bool) {
	design.Apply(ui.Row(c), func(el ui.Element) ui.Element {
		return el.BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2)
	}).FillWidth().Gap(design.Space3).
		AlignItems(ui.Center).Padding(design.Space2, design.Space2).Children(func() {
		design.Apply(ui.Text(c, tool.Name), t.MonoFamily()).FontSize(12).
			TextColor(t.LabelPrimary).NoWrap()
		description := strings.TrimSpace(tool.Description)
		if description != "" {
			design.Apply(ui.Text(c, description), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).Grow(1).MaxLines(1).Ellipsis("…")
		} else {
			ui.Spacer(c).Grow(1)
		}
		if !hasSettings || settings == nil {
			design.Apply(ui.Text(c, st.tr("设置存储未启用", "Settings store not enabled")),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
			return
		}
		disabled := st.disabledTools
		if tool.MCP {
			disabled = st.disabledMCPTools
		}
		enabled := !disabled[tool.Name]
		c.Key("tool-switch/" + tool.Name)
		toggle := ui.Toggle(c, &enabled, st.tr("启用", "Enabled"))
		if toggle.Changed() {
			st.setToolEnabled(tool.Name, enabled, settings, tool.MCP)
			runtime.Invalidate()
		}
		if !enabled {
			design.Apply(ui.Text(c, st.tr("已禁用（重启生效）", "Disabled (restart applies)")),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
		}
	})
}

// catalogHasTool reports whether the registered catalogue already contains name.
func catalogHasTool(catalog []catalogTool, name string) bool {
	for _, tool := range catalog {
		if tool.Name == name {
			return true
		}
	}
	return false
}

// filterCatalog keeps the tools whose name or description contains the query,
// case insensitively. An empty query keeps every tool.
func filterCatalog(catalog []catalogTool, query string) []catalogTool {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return catalog
	}
	filtered := make([]catalogTool, 0, len(catalog))
	for _, tool := range catalog {
		haystack := strings.ToLower(tool.Name + "\x00" + tool.Description)
		if strings.Contains(haystack, needle) {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

// toolCategory maps a tool name to its catalogue category by prefix.
func toolCategory(name string) string {
	switch {
	case name == "read_file", name == "write_file", name == "edit_file",
		name == "list_dir", name == "glob", name == "grep":
		return "文件"
	case name == "bash", strings.HasPrefix(name, "terminal_"),
		strings.HasPrefix(name, "job_"):
		return "终端与任务"
	case strings.HasPrefix(name, "web_"):
		return "网络"
	case strings.HasPrefix(name, "session_"), strings.HasPrefix(name, "memory_"),
		name == "memory":
		return "会话与记忆"
	case strings.HasPrefix(name, "team_"), name == "subagent", name == "workflow",
		name == "ralph", name == "todo":
		return "团队与编排"
	default:
		return "其他"
	}
}

// collapsedMCPStatus shortens a status for the collapsed card header: it keeps
// the part before the first colon (the state) and bounds its length.
func collapsedMCPStatus(status string) string {
	trimmed := strings.TrimSpace(status)
	if cut := strings.IndexAny(trimmed, ":："); cut >= 0 {
		trimmed = strings.TrimSpace(trimmed[:cut])
	}
	runes := []rune(trimmed)
	if len(runes) > 12 {
		return string(runes[:12]) + "…"
	}
	return trimmed
}

// pluginStatusLabel is the connection badge label (the service's own status
// text, or a generic fallback).
func pluginStatusLabel(st *state, status string) string {
	return firstNonEmpty(status, st.tr("未知状态", "Unknown status"))
}

// categoryLabel translates one canonical catalogue category for display.
func (st *state) categoryLabel(category string) string {
	switch category {
	case "文件":
		return st.tr("文件", "Files")
	case "终端与任务":
		return st.tr("终端与任务", "Terminal & jobs")
	case "网络":
		return st.tr("网络", "Web")
	case "会话与记忆":
		return st.tr("会话与记忆", "Session & memory")
	case "团队与编排":
		return st.tr("团队与编排", "Team & orchestration")
	default:
		return st.tr("其他", "Other")
	}
}

// pluginStatusColor colors the connection badge.
func pluginStatusColor(t *design.Tokens, status string) ui.Color {
	lower := strings.ToLower(strings.TrimSpace(status))
	switch {
	case strings.Contains(lower, "已连接"), strings.Contains(lower, "connected"),
		strings.Contains(lower, "ok"), strings.Contains(lower, "成功"):
		return t.Success
	case strings.Contains(lower, "失败"), strings.Contains(lower, "fail"),
		strings.Contains(lower, "error"), strings.Contains(lower, "不可用"):
		return t.DangerText
	default:
		return t.LabelSecondary
	}
}

// buildPluginsMuted is the section's muted empty/hint row.
func buildPluginsMuted(c *ui.Context, t *design.Tokens, text string) {
	design.Apply(ui.Text(c, text), t.TextMuted(design.TypeXS)).MaxLines(2)
}
