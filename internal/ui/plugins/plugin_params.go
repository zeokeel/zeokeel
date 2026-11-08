package plugins

// Plugin settings section: a curated, read-mostly view of the plugin-related
// settings keys that are actually consumed by the product. It deliberately
// avoids dead configuration: the inventory below was built by grepping the
// settings.Get consumers.
//
// Inventory (consumed keys):
//   - web.search.endpoint / web.search.apiKey: the toolkit's web_search tool
//     reads them on every call, so edits apply immediately.
//   - mcp.servers / mcp.workspaceOptIn / mcp.disabledServers /
//     mcp.disabledTools / tools.disabled: consumed by the toolkit and already
//     surfaced by the MCP cards and switches above, so they are not duplicated
//     here (except the workspace opt-in, which has no other UI and is shown
//     read-only with a restart note).
//
// Deliberately absent (no settings key exists, so exposing one would be dead
// configuration): web search max_results is a per-call tool argument; the shell
// timeout and max output bytes are toolkit assembly config (shellTimeoutMs /
// maxOutputBytes). Subagent depth/concurrency are compile-time constants in
// internal/modules/agent (subagentMaxDepth/subagentMaxConcurrent); they are
// shown read-only for transparency only.

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// pluginParamSpec describes one row of the plugin settings section.
type pluginParamSpec struct {
	// Key is the settings row. Empty for compile-time constants.
	Key string
	// Title/Desc are localized at render time.
	TitleZH, TitleEN string
	DescZH, DescEN   string
	// Sensitive values are never echoed (only a set/unset status is shown) and
	// their edit draft starts empty.
	Sensitive bool
	// Editable opens the native edit modal; read-only rows show a note badge.
	Editable bool
	// FixedValue is the read-only value of a compile-time row.
	FixedValue string
}

// pluginParamSpecs returns the curated parameter inventory (order preserved).
func pluginParamSpecs() []pluginParamSpec {
	return []pluginParamSpec{
		{
			Key: contracts.SettingWebSearchEndpoint, TitleZH: "搜索端点", TitleEN: "Search endpoint",
			DescZH:   "web_search 工具使用的搜索后端；留空则禁用 web_search。修改即时生效。",
			DescEN:   "Search backend used by the web_search tool; leave blank to disable web_search. Applies immediately.",
			Editable: true,
		},
		{
			Key: contracts.SettingWebSearchAPIKey, TitleZH: "API Key", TitleEN: "API Key",
			DescZH:    "以 Authorization: Bearer 发送；敏感值不回显，留空保持不变。修改即时生效。",
			DescEN:    "Sent as Authorization: Bearer; sensitive values are never echoed, leave blank to keep the current key. Applies immediately.",
			Sensitive: true, Editable: true,
		},
		{
			Key: contracts.SettingMCPWorkspaceOptIn, TitleZH: "工作区 mcp.json", TitleEN: "Workspace mcp.json",
			DescZH: "允许从工作区的 mcp.json 读取额外 MCP 服务器（重启生效）。",
			DescEN: "Reads extra MCP servers from the workspace mcp.json (restart applies).",
		},
		{
			TitleZH: "子代理深度", TitleEN: "Delegation depth",
			DescZH:     "子代理委托的最大深度；编译期常量，不可在设置中修改。",
			DescEN:     "Maximum nested delegation depth; a compile-time constant, not configurable here.",
			FixedValue: "1",
		},
		{
			TitleZH: "子代理并发", TitleEN: "Delegation concurrency",
			DescZH:     "一次委托的最大并发子代理数；编译期常量，不可在设置中修改。",
			DescEN:     "Maximum live subagents per delegation; a compile-time constant, not configurable here.",
			FixedValue: "2",
		},
	}
}

// buildPluginParamsSection renders the collapsed plugin parameter section and
// its native edit modal. Hidden without a settings service.
func buildPluginParamsSection(c *ui.Context, t *design.Tokens, st *state,
	settings contracts.SettingsService, hasSettings bool) {
	if !hasSettings || settings == nil {
		return
	}
	specs := pluginParamSpecs()
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, st.tr("插件参数", "Plugin settings")), t.SectionLabel())
			design.Apply(ui.Badge(c, st.tr("已接线", "Wired")), t.Badge()).TextColor(t.LabelSecondary)
		})
		design.Apply(ui.Text(c, st.tr(
			"只展示已被产品实际消费的设置；敏感值仅显示是否已设置。",
			"Only settings actually consumed by the product are shown; sensitive values only report whether they are set.")),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(2)
		if st.paramNotice != "" {
			if st.paramErr {
				design.Apply(ui.Text(c, st.paramNotice), t.Font(design.TypeXXS)).TextColor(t.DangerText)
			} else {
				design.Apply(ui.Badge(c, st.paramNotice), t.Badge()).TextColor(t.Success)
			}
		}
		// A plain disclosure button is used instead of ui.Collapsible so the
		// rows keep their normal flex layout (the collapsible body does not give
		// child rows a usable width for interactive buttons here).
		disclosure := st.tr("展开参数", "Show parameters")
		if st.paramsOpen {
			disclosure = st.tr("收起参数", "Hide parameters")
		}
		if ui.Button(c, disclosure).Clicked() {
			st.paramsOpen = !st.paramsOpen
			c.Invalidate()
		}
		if st.paramsOpen {
			for _, spec := range specs {
				buildPluginParamRow(c, t, st, settings, spec)
			}
		}
	})
	if st.paramEditing {
		buildPluginParamModal(c, t, st, settings)
	}
}

// buildPluginParamRow renders one parameter row: title, key, current value,
// description and the edit action or the read-only note.
func buildPluginParamRow(c *ui.Context, t *design.Tokens, st *state,
	settings contracts.SettingsService, spec pluginParamSpec) {
	value, note := pluginParamValue(st, settings, spec)
	design.Apply(ui.Column(c), t.Panel()).FillWidth().Padding(design.Space2).Gap(design.Space1).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, st.tr(spec.TitleZH, spec.TitleEN)), t.Font(design.TypeXS)).
				FontWeight(600).TextColor(t.LabelPrimary).NoWrap()
			if spec.Key != "" {
				design.Apply(ui.Text(c, spec.Key), t.MonoFamily()).FontSize(11).
					TextColor(t.LabelTertiary).NoWrap().Shrink(0)
			}
			ui.Spacer(c).Grow(1)
			if spec.Editable {
				edit := ui.Button(c, st.tr("编辑 ", "Edit ")+st.tr(spec.TitleZH, spec.TitleEN))
				if edit.Clicked() {
					st.paramEditing = true
					st.paramKey = spec.Key
					st.paramTitle = st.tr(spec.TitleZH, spec.TitleEN)
					st.paramDraft = ""
					if !spec.Sensitive {
						st.paramDraft = strings.TrimSpace(settings.Get(spec.Key, ""))
					}
					st.paramErrText = ""
					c.Invalidate()
				}
			} else if note != "" {
				design.Apply(ui.Badge(c, note), t.Badge()).TextColor(t.LabelTertiary)
			}
		})
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, value), t.MonoFamily()).FontSize(11).
				TextColor(t.LabelSecondary).Grow(1).MaxLines(1).Ellipsis("…")
			design.Apply(ui.Text(c, st.tr(spec.DescZH, spec.DescEN)), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).MaxLines(2)
		})
	})
}

// pluginParamValue formats the current value and the read-only note: sensitive
// rows only report set/unset, compile-time rows their fixed value, the
// workspace opt-in its boolean state.
func pluginParamValue(st *state, settings contracts.SettingsService, spec pluginParamSpec) (value, note string) {
	switch {
	case spec.FixedValue != "":
		return spec.FixedValue, st.tr("编译期常量", "Compile-time constant")
	case spec.Sensitive:
		if strings.TrimSpace(settings.Get(spec.Key, "")) != "" {
			return st.tr("API Key 已设置", "API key set"), ""
		}
		return st.tr("API Key 未设置", "API key not set"), ""
	case spec.Key == contracts.SettingMCPWorkspaceOptIn:
		raw := strings.ToLower(strings.TrimSpace(settings.Get(spec.Key, "")))
		// This row has no editor: it is consumed at startup, so a restart note is
		// the honest status.
		enabled := raw == "true" || raw == "1" || raw == "yes" || raw == "on"
		if enabled {
			return st.tr("已启用", "Enabled"), st.tr("需重启", "Restart required")
		}
		return st.tr("未启用", "Disabled"), st.tr("需重启", "Restart required")
	default:
		current := strings.TrimSpace(settings.Get(spec.Key, ""))
		if current == "" {
			return st.tr("（空）", "(empty)"), ""
		}
		return current, ""
	}
}

// buildPluginParamModal renders the native edit dialog for one parameter.
func buildPluginParamModal(c *ui.Context, t *design.Tokens, st *state, settings contracts.SettingsService) {
	ui.Modal(c, &st.paramEditing, func() {
		design.Apply(ui.Column(c).Width(560).Gap(design.Space3), func(element ui.Element) ui.Element {
			return element.PaddingX(design.Space5).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, st.tr("编辑参数", "Edit parameter")), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, st.tr("参数：", "Setting: ")+st.paramKey), t.MonoFamily()).
				FontSize(11).TextColor(t.LabelTertiary)
			ui.TextInput(c, &st.paramDraft).
				Label(st.tr("新值：", "New value: ") + st.paramTitle).
				Placeholder(st.tr("留空保持不变", "Leave blank to keep the current value")).FillWidth()
			if st.paramErrText != "" {
				design.Apply(ui.Text(c, st.paramErrText), t.Font(design.TypeXXS)).TextColor(t.DangerText)
			}
			ui.Row(c).FillWidth().Justify(ui.End).Gap(design.Space2).Children(func() {
				if ui.Button(c, st.tr("取消", "Cancel")).Clicked() {
					st.paramEditing = false
					st.paramErrText = ""
					c.Invalidate()
				}
				save := design.Apply(ui.PrimaryButton(c, st.tr("保存", "Save")), t.PrimaryButton())
				if save.Clicked() {
					st.savePluginParam(settings)
					c.Invalidate()
				}
			})
		})
	})
}

// savePluginParam persists the draft. Blank drafts keep the current value (the
// sensitive API key can never be cleared from this modal).
func (st *state) savePluginParam(settings contracts.SettingsService) {
	draft := strings.TrimSpace(st.paramDraft)
	if draft == "" {
		st.paramEditing = false
		st.paramErrText = ""
		st.paramNotice = st.tr("已保存", "Saved")
		st.paramErr = false
		return
	}
	if err := settings.Set(st.paramKey, draft); err != nil {
		st.paramErrText = st.tr("保存失败：", "Save failed: ") + err.Error()
		st.paramErr = true
		st.paramNotice = st.paramErrText
		return
	}
	st.paramEditing = false
	st.paramErrText = ""
	st.paramNotice = st.tr("已保存", "Saved")
	st.paramErr = false
}
