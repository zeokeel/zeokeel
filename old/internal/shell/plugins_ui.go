package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
)

// builtinPluginDef is one built-in plugin exposed by Settings > Built-in
// plugins. The name is the settings key segment persisted as
// plugin.<name>.enabled, so it is stable across UI changes.
type builtinPluginDef struct {
	name             string
	titleEN          string
	titleZH          string
	descEN           string
	descZH           string
	enabledByDefault bool
}

// builtinPluginDefs are the features that exist in the runtime today. The
// app wires hooks that read these same names; the shell only persists them.
var builtinPluginDefs = []builtinPluginDef{
	{
		name: "subagent", titleEN: "Subagent", titleZH: "子代理",
		descEN:           "Let the agent delegate work to a nested subagent.",
		descZH:           "允许代理把任务委派给嵌套子代理。",
		enabledByDefault: true,
	},
	{
		name: "web_search", titleEN: "Web search", titleZH: "网络搜索",
		descEN:           "Let the agent search the web with the configured provider.",
		descZH:           "允许代理使用已配置的搜索提供方。",
		enabledByDefault: true,
	},
	{
		name: "agent_loop", titleEN: "Agent loop", titleZH: "代理循环",
		descEN:           "Control how the agent dispatches tool calls.",
		descZH:           "控制代理如何分发工具调用。",
		enabledByDefault: true,
	},
	{
		name: "shell", titleEN: "Shell tools", titleZH: "Shell 工具",
		descEN:           "Enable the bash, write_file and edit_file tool group.",
		descZH:           "启用 bash、write_file 和 edit_file 工具组。",
		enabledByDefault: true,
	},
	{
		name: "skills", titleEN: "Skills", titleZH: "技能",
		descEN:           "Load SKILL.md capabilities from the workspace skills folder.",
		descZH:           "从工作区 skills 目录加载 SKILL.md 能力。",
		enabledByDefault: true,
	},
	{
		name: "mcp", titleEN: "MCP servers", titleZH: "MCP 服务",
		descEN:           "Expose tools from configured Model Context Protocol servers.",
		descZH:           "暴露已配置的模型上下文协议服务工具。",
		enabledByDefault: true,
	},
	{
		// The LSP tool is registered by default (internal/app services.go
		// pluginOn("lsp", true)) but has no language server until the user
		// configures lsp.servers; a workspace .zeokeel/lsp.json is untrusted
		// unless lsp.workspace_config explicitly enables it.
		name: "lsp", titleEN: "Language server tools", titleZH: "语言服务器工具",
		descEN:           "Expose definition, references, implementation and hover through a configured language server (lsp.servers); workspace .zeokeel/lsp.json stays untrusted until enabled.",
		descZH:           "通过已配置的语言服务器（lsp.servers）提供定义、引用、实现与悬停；工作区 .zeokeel/lsp.json 默认不信任，需显式开启。",
		enabledByDefault: true,
	},
}

// experimentalPluginDefs are the baseline's experimental toggles. The
// features do not exist yet, but the toggle is still persisted so the state
// survives a restart and a future wiring has one source of truth.
var experimentalPluginDefs = []builtinPluginDef{
	{name: "agent_teams", titleEN: "Agent Teams", titleZH: "代理团队", enabledByDefault: false},
	{name: "auto_review", titleEN: "Auto Authorization Review", titleZH: "自动授权审核", enabledByDefault: false},
	{name: "developer_tools", titleEN: "Developer Tools", titleZH: "开发者工具", enabledByDefault: false},
}

// pluginsSettingsPage is Settings > Built-in plugins: one persisted toggle per
// runtime feature. The app reads plugin.<name>.enabled when it builds or
// gates the agent; PluginChangeHook reports every change.
func (a *App) pluginsSettingsPage(c *ui.Context, t *design.Tokens) {
	ui.Column(c).Gap(design.Space3).Children(func() {
		ui.Column(c).Gap(design.Space1).Children(func() {
			settingsText(c, a.T("plugins.built_in_plugins"), design.TypeM, t.LabelPrimary).FontWeight(600)
			settingsText(c,
				a.T("plugins.turn_runtime_features_on_or_off_changes_apply_to_the_next_turn"),
				design.TypeXXS, t.LabelTertiary)
		})
		ui.Column(c).Gap(2).Children(func() {
			for _, plugin := range builtinPluginDefs {
				a.pluginToggleRow(c, t, plugin)
			}
		})
		ui.Column(c).Gap(design.Space1).Children(func() {
			settingsText(c, a.T("plugins.experimental_plugins"), design.TypeBody, t.LabelPrimary).
				FontWeight(500).Padding(design.Space3, 0, 0, 0)
			ui.Column(c).Gap(2).Children(func() {
				for _, plugin := range experimentalPluginDefs {
					a.pluginToggleRow(c, t, plugin)
				}
			})
		})
	})
}

// pluginToggleRow is one plugin: title and description, with the switch on
// the right. Clicking the row toggles it, which keeps the small switch easy
// to hit in the dense settings column.
func (a *App) pluginToggleRow(c *ui.Context, t *design.Tokens, plugin builtinPluginDef) {
	on := a.pluginEnabledValue(plugin.name, plugin.enabledByDefault)
	row := ui.Row(c).Height(54).Padding(design.Space2, design.Space2).
		Gap(design.Space3).Radius(design.RadiusMD).AlignItems(ui.Center)
	if row.Hovered() {
		row = row.Background(t.BgHover)
	}
	row.Children(func() {
		ui.Column(c).Grow(1).Gap(1).Children(func() {
			settingsText(c, a.pluginTitle(plugin), design.TypeBody, t.LabelPrimary).FontWeight(500)
			if desc := a.pluginDesc(plugin); desc != "" {
				settingsText(c, desc, design.TypeXXS, t.LabelTertiary).MaxLines(1)
			}
		})
		if a.Ctrl != nil {
			if _, ok := pluginDetailForKey(plugin.name); ok {
				configure := ui.Row(c).Height(28).PaddingX(design.Space2).
					Radius(design.RadiusSM).Border(1, t.BorderL2).AlignItems(ui.Center).
					Label(a.tr("Configure ", "配置") + a.pluginTitle(plugin)).
					Role(ui.RoleButton).Focusable()
				configure.Children(func() {
					settingsText(c, a.pluginLocalized("Configure", "配置"), design.TypeXXS,
						t.LabelPrimary)
				})
				configure.OnClick(func() {
					if spec, ok := pluginDetailForKey(plugin.name); ok {
						a.openPluginDetail(spec)
					}
				})
			}
		}
		if a.Ctrl != nil {
			a.pluginSwitchLive(c, t, on, a.tr("Enable ", "启用")+a.pluginTitle(plugin), func() {
				a.togglePlugin(c, plugin.name, on)
			})
		} else {
			a.pluginSwitch(c, t, on)
		}
	})
	if a.Ctrl != nil {
		row.OnClick(func() { a.togglePlugin(c, plugin.name, on) })
	}
}

// pluginTitle localizes a plugin's display name: the i18n catalog wins when it
// has a live-plugin entry, otherwise the definition's inline Chinese title is
// used for a Chinese UI (the LSP row has no catalog entry by design).
func (a *App) pluginTitle(plugin builtinPluginDef) string {
	if key := "plugins.live." + plugin.name + ".title"; hasTranslation(key) {
		return a.T(key)
	}
	if a.lang() == LangZH && plugin.titleZH != "" {
		return plugin.titleZH
	}
	return plugin.titleEN
}

// pluginDesc localizes a plugin's description with the same catalog-first,
// inline-bilingual fallback as pluginTitle.
func (a *App) pluginDesc(plugin builtinPluginDef) string {
	if key := "plugins.live." + plugin.name + ".desc"; hasTranslation(key) {
		return a.T(key)
	}
	if a.lang() == LangZH && plugin.descZH != "" {
		return plugin.descZH
	}
	return plugin.descEN
}

// pluginEnabledValue reads a toggle with def as the fallback for an unset key.
func (a *App) pluginEnabledValue(name string, def bool) bool {
	if a.Ctrl == nil {
		return def
	}
	enabled, err := a.Ctrl.PluginEnabledDefault(name, def)
	if err != nil {
		return def
	}
	return enabled
}

// togglePlugin persists the inverse of current and reports through a toast.
// The controller's PluginChangeHook runs after the value is durable.
func (a *App) togglePlugin(c *ui.Context, name string, current bool) {
	if a.Ctrl == nil {
		return
	}
	enabled := !current
	if err := a.Ctrl.SetPluginEnabled(name, enabled); err != nil {
		c.Toast(a.T("plugins.could_not_update_plugin"))
		return
	}
	if enabled {
		if en, zh := pluginUnsupportedReason(name); en != "" {
			c.Toast(a.pluginLocalized(en, zh))
			return
		}
		c.Toast(a.T("plugins.plugin_enabled"))
	} else {
		c.Toast(a.T("plugins.plugin_disabled"))
	}
}

// SetPluginChangeHook wires the app's plugin callback. The app uses it to
// rebuild or gate the agent's tool registry on the next turn.
func (a *App) SetPluginChangeHook(fn func(name string, enabled bool)) {
	if a.Ctrl != nil {
		a.Ctrl.SetPluginChangeHook(fn)
	}
}

// pluginKeyForLabel maps a Plugins-page row title to its settings name. Rows
// without a runtime feature return "" and keep the baseline's visual toggle.
func pluginKeyForLabel(label string) string {
	switch label {
	case "Subagent":
		return "subagent"
	case "Web search":
		return "web_search"
	case "Agent loop":
		return "agent_loop"
	case "Shell", "Shell tools":
		return "shell"
	case "Agent Teams":
		return "agent_teams"
	case "Auto Authorization Review":
		return "auto_review"
	case "Developer Tools":
		return "developer_tools"
	default:
		return ""
	}
}

// pluginDefaultEnabled is the fallback state for a row's persisted toggle: every
// built-in runtime feature (including the LSP tool and its language servers)
// is on by default, matching the app's pluginOn(name, true) build gates; only
// the experimental, not-yet-implemented rows default off.
func pluginDefaultEnabled(name string) bool {
	for _, plugin := range experimentalPluginDefs {
		if plugin.name == name {
			return false
		}
	}
	return true
}

// builtinPluginRowsLive builds the Plugins page's built-in rows from the same
// builtinPluginDefs Settings > Built-in plugins uses, so both pages list the
// same plugins with the same persisted plugin.<name>.enabled keys and the same
// localized names (PD-08). The row name/description are already localized here
// because a new built-in (the LSP tool) has no plugins.row.* catalog entries;
// rows with one keep resolving through pluginRowName/pluginRowDesc.
func (a *App) builtinPluginRowsLive() []pluginRow {
	rows := make([]pluginRow, 0, len(builtinPluginDefs))
	for _, def := range builtinPluginDefs {
		icon := pluginRowIcons[def.name]
		if icon == nil {
			icon = pluginIconInfo
		}
		rows = append(rows, pluginRow{
			name: a.pluginLocalized(def.titleEN, def.titleZH),
			desc: a.pluginLocalized(def.descEN, def.descZH),
			icon: icon, key: def.name,
			toggle: true, on: a.pluginEnabledValue(def.name, def.enabledByDefault),
		})
	}
	return rows
}

// pluginRowIcons maps one builtinPluginDef name to the Plugins page's row
// glyph. The page and the settings list name the same six plugins, so the
// icons live here rather than in each row literal.
var pluginRowIcons = map[string]*ui.SVG{
	"subagent":   pluginIconSubagent,
	"web_search": pluginIconSearch,
	"agent_loop": pluginIconLoop,
	"shell":      pluginIconShell,
	"skills":     pluginIconSkills,
	"mcp":        pluginIconMCP,
}

// experimentalPluginRowsLive builds the experimental rows with persisted
// toggles. The features do not exist yet, but the switches keep their state.
func (a *App) experimentalPluginRowsLive() []pluginRow {
	rows := make([]pluginRow, 0, len(experimentalPluginDefs))
	icons := []*ui.SVG{pluginIconTeams, pluginIconReview, pluginIconDev}
	descs := []string{
		"Enable team collaboration, team tools, the member roster, and the shared task board.",
		"Add an Auto review permission mode that uses the model to assess authorization before each tool call.",
		"Inspect and debug raw session data, grouped chat messages, and the NodeJS backend.",
	}
	for i, plugin := range experimentalPluginDefs {
		rows = append(rows, pluginRow{
			name: plugin.titleEN, desc: descs[i], icon: icons[i],
			key: plugin.name, badge: true, unimplemented: true, toggle: true,
			on: a.pluginEnabledValue(plugin.name, false),
		})
	}
	return rows
}
