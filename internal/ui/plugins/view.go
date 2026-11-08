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
	"github.com/zeokeel/zeokeel/internal/uikit"
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

// toolListMaxHeight bounds the virtualized tool catalogue so the page keeps its
// header and MCP section in view while the list scrolls on its own.
const toolListMaxHeight float32 = design.Space8 * 18

// buildScreen renders one frame of the Plugins screen: the header, the MCP
// server cards (with the native Refresh action) and the searchable tool
// catalogue in a virtualized native List. Service calls run in click handlers
// only.
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
			if st.statusErr {
				uikit.ErrorLine(c, st.status)
			} else {
				design.Apply(ui.Text(c, st.status), t.Font(design.TypeXXS)).TextColor(t.SuccessText)
			}
		}

		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			ui.Column(c).FillWidth().Gap(design.Space5).Children(func() {
				buildMCPsSection(c, t, runtime, pluginsService, hasPlugins, cfg, st, settings, hasSettings)
				buildToolsSection(c, t, toolkit, hasToolkit, st, cfg, runtime, settings, hasSettings)
				modulesLister, hasModules := st.ctx.Get(contracts.KeyModuleLister)
				buildModulesSection(c, t, st, modulesLister, hasModules)
				buildPluginParamsSection(c, t, st, settings, hasSettings)
			})
		})
	})
}

// buildMCPsSection is the MCP plugin connections section: a uikit.Section with
// the native Refresh action at the section body's top-right.
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
	uikit.Section(c, st.localized(cfg.MCPSection, "MCP 插件", "MCP plugins"), func(c *ui.Context) {
		if hasService {
			ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
				refresh := ui.Button(c, st.tr("刷新", "Refresh")).Tooltip(st.tr("重新连接并刷新 MCP 服务器状态", "Reconnect and refresh MCP server status"))
				if refresh.Clicked() {
					service.Refresh(context.Background())
					runtime.Invalidate()
				}
			})
		}
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
				uikit.EmptyState(c, st.tr("插件服务未启用", "Plugins service not enabled"))
				return
			}
			uikit.EmptyState(c, st.tr("还没有配置 MCP 服务器（assembly 的 mcpServers）",
				"No MCP servers configured yet (assembly mcpServers)"))
			return
		}
		for index, server := range servers {
			buildMCPCard(c, t, runtime, service, st, server, index, settings, hasSettings)
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

// buildMCPCard is one MCP server card: the header row carries the name, the
// connection state and the server's enable switch (the action that matters, at
// the row that names its object); a native Collapsible reveals the transport,
// the handshake metadata and the full status text.
func buildMCPCard(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime, service contracts.PluginsService,
	st *state, server contracts.MCPPlugin, index int, settings contracts.SettingsService, hasSettings bool) {
	key := mcpServerKey(server, index)
	expanded := st.mcpExpanded[key]
	name := firstNonEmpty(server.Name, server.ServerName)
	title := firstNonEmpty(name, st.tr("(未命名服务器)", "(Untitled server)"))

	c.Key("mcp/" + key)
	card := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space3).Gap(design.Space2)
	card.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			icon := icons.Plug()
			iconColor := t.LabelSecondary
			if strings.TrimSpace(server.URL) == "" {
				icon = icons.Plugin()
			}
			ui.Icon(c, icon).Size(16, 16).TextColor(iconColor)
			design.Apply(ui.Text(c, title), t.Font(design.TypeS)).
				FontWeight(600).TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			// One state badge: the disabled state takes precedence over the
			// connection status (the server does not connect while disabled).
			if st.serverDisabled(name) {
				design.Apply(ui.Badge(c, st.tr("已停用（重启生效）", "Disabled (restart applies)")), t.Badge()).
					TextColor(t.LabelTertiary)
			} else {
				design.Apply(ui.Badge(c, pluginStatusLabel(st, collapsedMCPStatus(server.Status))), t.Badge()).
					TextColor(pluginStatusColor(t, server.Status))
			}
			design.Apply(ui.Text(c, st.tr(fmt.Sprintf("%d 个工具", server.Tools), fmt.Sprintf("%d tools", server.Tools))),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).NoWrap()
			// A failed connection offers recovery right where it failed.
			if !st.serverDisabled(name) && pluginStatusFailed(server.Status) && service != nil {
				retryLabel := st.tr("重试连接", "Retry")
				retry := ui.Button(c, retryLabel).
					Tooltip(st.tr("重新连接这个 MCP 服务器", "Reconnect this MCP server"))
				if retry.Clicked() {
					service.Refresh(context.Background())
					runtime.Invalidate()
				}
			}
			if hasSettings && settings != nil && name != "" {
				enabled := !st.serverDisabled(name)
				changed := false
				ui.Field(c, st.tr("启用", "Enabled"), func() {
					sw := ui.Switch(c, &enabled)
					sw.Label(st.tr("启用服务器 ", "Enable server ") + name)
					changed = sw.Changed()
				})
				if changed {
					st.setServerEnabled(name, enabled, settings)
					runtime.Invalidate()
				}
			}
		})
		// The failure reason stays visible on the collapsed row: the user should
		// not have to open the details to know why the connection failed.
		if !st.serverDisabled(name) && pluginStatusFailed(server.Status) {
			if reason := mcpStatusReason(server.Status); reason != "" {
				design.Apply(ui.Text(c, reason), t.Font(design.TypeXXS)).
					TextColor(t.DangerText).MaxLines(2)
			}
		}
		ui.Collapsible(c, st.tr("详情", "Details"), &expanded, func() {
			transport := st.tr("本地 stdio", "Local stdio")
			if strings.TrimSpace(server.URL) != "" {
				transport = "HTTP"
			}
			rows := []uikit.KeyValue{
				{Key: st.tr("类型", "Type"), Value: transport},
			}
			if url := strings.TrimSpace(server.URL); url != "" {
				rows = append(rows, uikit.KeyValue{Key: "URL", Value: url})
			}
			rows = append(rows,
				uikit.KeyValue{Key: st.tr("服务器", "Server"), Value: mcpDetailValue(server.ServerName)},
				uikit.KeyValue{Key: st.tr("版本", "Version"), Value: mcpDetailValue(server.Version)},
				uikit.KeyValue{Key: st.tr("协议", "Protocol"), Value: mcpDetailValue(server.Protocol)},
				uikit.KeyValue{Key: st.tr("工具数", "Tools"),
					Value: st.tr(fmt.Sprintf("%d 个工具", server.Tools), fmt.Sprintf("%d tools", server.Tools))},
				uikit.KeyValue{Key: st.tr("状态", "Status"), Value: mcpDetailValue(server.Status)},
			)
			uikit.KeyValueRows(c, rows)
			buildMCPConnectionConfig(c, t, st, server, settings, hasSettings)
		})
		st.mcpExpanded[key] = expanded
	})
}

// pluginStatusFailed reports whether an MCP connection state is a failure the
// row should offer a retry for.
func pluginStatusFailed(status string) bool {
	lower := strings.ToLower(strings.TrimSpace(status))
	return strings.Contains(lower, "失败") || strings.Contains(lower, "fail") ||
		strings.Contains(lower, "error") || strings.Contains(lower, "不可用")
}

// mcpStatusReason exposes the human-readable reason inside an MCP status
// string: the text after the first colon when the service put the underlying
// error there, or the whole status otherwise.
func mcpStatusReason(status string) string {
	trimmed := strings.TrimSpace(status)
	if cut := strings.IndexAny(trimmed, ":："); cut >= 0 && cut+1 < len(trimmed) {
		return strings.TrimSpace(trimmed[cut+1:])
	}
	return trimmed
}

// toolDescriptionSummary shortens the developer-docs description to a scannable
// first phrase; the full text stays in the row's tooltip.
func toolDescriptionSummary(description string) string {
	summary := strings.TrimSpace(description)
	if cut := strings.IndexAny(summary, ".。;；"); cut > 0 {
		summary = strings.TrimSpace(summary[:cut])
	}
	const maxRunes = 96
	runes := []rune(summary)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return summary
}

// mcpDetailValue renders one detail value, with an em dash when empty.
func mcpDetailValue(value string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return "—"
}

// buildToolsSection is the searchable tool catalogue: a uikit.Section holding a
// native virtualized List (keyed by tool name) with the category label above the
// first tool of each category, so the 70+ row list builds only what shows.
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
	uikit.Section(c, st.localized(cfg.ToolsSection, "工具目录", "Tool catalogue"), func(c *ui.Context) {
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
		// The count follows the search filter, so it always names what shows.
		filtered := sortCatalogByCategory(filterCatalog(catalog, st.query))
		if hasToolkit {
			design.Apply(ui.Text(c, st.tr(fmt.Sprintf("%d 个工具", len(filtered)), fmt.Sprintf("%d tools", len(filtered)))),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
		}
		if !hasToolkit {
			uikit.EmptyState(c, st.tr("工具目录未启用", "Tool catalogue not enabled"))
			return
		}
		design.Apply(ui.Text(c, st.tr("工具开关在重启后生效。", "Tool switches apply after a restart.")),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
		if st.disabledErr != "" {
			uikit.ErrorLine(c, st.tr("禁用工具配置无效：", "Invalid disabled-tools configuration: ")+st.disabledErr)
		}
		searchLabel := st.tr("搜索工具", "Search tools")
		ui.SearchField(c, &st.query).Label(searchLabel).Placeholder(searchLabel).FillWidth()
		if len(filtered) == 0 {
			uikit.EmptyStateBlock(c, st.tr("没有匹配的工具", "No matching tools"),
				st.tr("换个关键词试试。", "Try another search term."), nil)
			return
		}
		ui.List(c, nil, len(filtered)).
			ItemKey(func(row int) any { return filtered[row].Name }).
			MaxHeight(toolListMaxHeight).
			Rows(func(row ui.ListRow) {
				tool := filtered[row.Index]
				showCategory := row.Index == 0 ||
					toolCategory(filtered[row.Index-1].Name) != toolCategory(tool.Name)
				buildToolRow(row.Context, t, tool, showCategory, st, runtime, settings, hasSettings)
			})
	})
}

// buildToolRow is one catalogue entry: mono name, its description and, for MCP
// tools, the per-tool enable switch. The catalogue is a dense reference list, so
// rows are plain (no card per tool); category labels and dividers carry the
// grouping.
func buildToolRow(c *ui.Context, t *design.Tokens, tool catalogTool, showCategory bool,
	st *state, runtime contracts.UIRuntime, settings contracts.SettingsService, hasSettings bool) {
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		if showCategory {
			design.Apply(ui.Text(c, st.categoryLabel(toolCategory(tool.Name))), t.Font(design.TypeXXS)).
				FontWeight(600).TextColor(t.LabelTertiary).Padding(design.Space1, design.Space2, 0, design.Space2)
		}
		ui.Row(c).FillWidth().Gap(design.Space3).
			AlignItems(ui.Center).Padding(design.Space2, design.Space2).Children(func() {
			design.Apply(ui.Text(c, tool.Name), t.MonoFamily()).FontSize(12).
				TextColor(t.LabelPrimary).NoWrap()
			description := strings.TrimSpace(tool.Description)
			if description != "" {
				display := toolDescriptionSummary(description)
				design.Apply(ui.Text(c, display), t.Font(design.TypeXXS)).
					TextColor(t.LabelTertiary).Grow(1).MaxLines(1).Ellipsis("…").
					Tooltip(description)
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
			changed := false
			sw := ui.Switch(c, &enabled)
			sw.Label(st.tr("启用工具 ", "Enable tool ") + tool.Name)
			changed = sw.Changed()
			if changed {
				st.setToolEnabled(tool.Name, enabled, settings, tool.MCP)
				if st.pendingRestartTools == nil {
					st.pendingRestartTools = make(map[string]bool)
				}
				st.pendingRestartTools[tool.Name] = true
				runtime.Invalidate()
			}
			if st.pendingRestartTools[tool.Name] {
				// The toolkit registers its tools at startup: the change applies
				// after a restart, and the row says so until then.
				design.Apply(ui.Text(c, st.tr("待重启", "Restart needed")),
					t.Font(design.TypeXXS)).TextColor(t.WarningText).NoWrap()
			}
			if !enabled {
				design.Apply(ui.Text(c, st.tr("已禁用（重启生效）", "Disabled (restart applies)")),
					t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).NoWrap()
			}
		})
		ui.Divider(c)
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

// categoryRank orders the catalogue categories; unknown categories sort last.
func categoryRank(category string) int {
	for index, known := range toolCategoryOrder {
		if known == category {
			return index
		}
	}
	return len(toolCategoryOrder)
}

// sortCatalogByCategory groups the catalogue in the fixed category order (then
// by name) so the virtualized list shows one section per category instead of
// interleaving categories in registry order.
func sortCatalogByCategory(catalog []catalogTool) []catalogTool {
	ordered := append([]catalogTool(nil), catalog...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := categoryRank(toolCategory(ordered[i].Name)), categoryRank(toolCategory(ordered[j].Name))
		if left != right {
			return left < right
		}
		return ordered[i].Name < ordered[j].Name
	})
	return ordered
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
		return t.SuccessText
	case strings.Contains(lower, "失败"), strings.Contains(lower, "fail"),
		strings.Contains(lower, "error"), strings.Contains(lower, "不可用"):
		return t.DangerText
	default:
		return t.LabelSecondary
	}
}
