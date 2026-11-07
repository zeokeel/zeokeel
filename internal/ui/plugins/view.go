package plugins

import (
	"context"
	"fmt"
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

	ui.Column(c).FillWidth().Grow(1).Gap(design.Space4).Children(func() {
		ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
			design.Apply(ui.Text(c, cfg.Title), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, cfg.Description), t.TextMuted(design.TypeXS)).MaxLines(2)
		})

		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			ui.Column(c).FillWidth().Gap(design.Space5).Children(func() {
				buildMCPsSection(c, t, runtime, pluginsService, hasPlugins, cfg)
				buildToolsSection(c, t, toolkit, hasToolkit, st, cfg)
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
) {
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, cfg.MCPSection), t.SectionLabel())
			ui.Spacer(c).Grow(1)
			if hasService {
				refresh := ui.Button(c, "刷新").Tooltip("重新连接并刷新 MCP 服务器状态")
				if refresh.Clicked() {
					service.Refresh(context.Background())
					runtime.Invalidate()
				}
			}
		})
		if !hasService {
			buildPluginsMuted(c, t, "插件服务未启用")
			return
		}
		servers := service.MCP()
		if len(servers) == 0 {
			buildPluginsMuted(c, t, "还没有配置 MCP 服务器（assembly 的 mcpServers）")
			return
		}
		for _, server := range servers {
			buildMCPCard(c, t, server)
		}
	})
}

// buildMCPCard is one MCP server: name, connection badge, handshake metadata,
// URL and its tool count.
func buildMCPCard(c *ui.Context, t *design.Tokens, server contracts.MCPPlugin) {
	card := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space3).Gap(design.Space2)
	card.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			icon := icons.Plug()
			iconColor := t.LabelSecondary
			if strings.TrimSpace(server.URL) == "" {
				icon = icons.Plugin()
			}
			ui.Icon(c, icon).Size(16, 16).TextColor(iconColor)
			design.Apply(ui.Text(c, firstNonEmpty(server.Name, "(未命名服务器)")), t.Font(design.TypeS)).
				FontWeight(600).TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			design.Apply(ui.Badge(c, pluginStatusLabel(server.Status)), t.Badge()).
				TextColor(pluginStatusColor(t, server.Status))
			design.Apply(ui.Badge(c, fmt.Sprintf("%d 个工具", server.Tools)), t.Badge())
		})
		if meta := mcpMeta(server); meta != "" {
			design.Apply(ui.Text(c, meta), t.TextMuted(design.TypeXXS)).MaxLines(1).Ellipsis("…")
		}
		if url := strings.TrimSpace(server.URL); url != "" {
			design.Apply(ui.Text(c, url), t.MonoFamily()).FontSize(11).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
		}
	})
}

// mcpMeta joins the non-empty handshake fields into one muted line.
func mcpMeta(server contracts.MCPPlugin) string {
	parts := make([]string, 0, 3)
	for _, value := range []string{server.ServerName, server.Version, server.Protocol} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, " · ")
}

// buildToolsSection is the searchable, category-grouped tool catalogue.
func buildToolsSection(
	c *ui.Context,
	t *design.Tokens,
	toolkit *toolRegistry,
	hasToolkit bool,
	st *state,
	cfg Config,
) {
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		var catalog []catalogTool
		if hasToolkit && toolkit != nil {
			for _, spec := range toolkit.Specs() {
				catalog = append(catalog, catalogTool{Name: spec.Name, Description: spec.Description})
			}
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, cfg.ToolsSection), t.SectionLabel())
			ui.Spacer(c).Grow(1)
			if hasToolkit {
				design.Apply(ui.Badge(c, fmt.Sprintf("%d 个工具", len(catalog))), t.Badge())
			}
		})
		if !hasToolkit {
			buildPluginsMuted(c, t, "工具目录未启用")
			return
		}
		ui.SearchField(c, &st.query).Label("搜索工具").Placeholder("搜索工具").FillWidth()
		filtered := filterCatalog(catalog, st.query)
		if len(filtered) == 0 {
			buildPluginsMuted(c, t, "没有匹配的工具")
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
			design.Apply(ui.Text(c, category), t.Font(design.TypeXXS)).
				FontWeight(600).TextColor(t.LabelTertiary)
			for _, tool := range tools {
				buildToolRow(c, t, tool)
			}
		}
	})
}

// buildToolRow is one catalogue entry: mono name plus its description.
func buildToolRow(c *ui.Context, t *design.Tokens, tool catalogTool) {
	design.Apply(ui.Row(c), t.Panel()).FillWidth().Gap(design.Space3).
		AlignItems(ui.Center).Padding(design.Space2, design.Space3).Children(func() {
		design.Apply(ui.Text(c, tool.Name), t.MonoFamily()).FontSize(12).
			TextColor(t.LabelPrimary).NoWrap()
		if description := strings.TrimSpace(tool.Description); description != "" {
			design.Apply(ui.Text(c, description), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).Grow(1).MaxLines(1).Ellipsis("…")
		}
	})
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

// pluginStatusLabel is the connection badge label (the service's own status
// text, or a generic fallback).
func pluginStatusLabel(status string) string {
	return firstNonEmpty(status, "未知状态")
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
