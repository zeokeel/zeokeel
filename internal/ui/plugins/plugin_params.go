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
	"github.com/zeokeel/zeokeel/internal/uikit"
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

// buildPluginParamsSection renders the plugin parameter section as a
// uikit.Section (the same title/divider language as the other settings pages)
// holding a native Collapsible with the rows, plus its native edit modal.
// Hidden without a settings service.
func buildPluginParamsSection(c *ui.Context, t *design.Tokens, st *state,
	settings contracts.SettingsService, hasSettings bool) {
	if !hasSettings || settings == nil {
		return
	}
	specs := pluginParamSpecs()
	uikit.Section(c, st.tr("插件参数", "Plugin settings"), func(c *ui.Context) {
		design.Apply(ui.Text(c, st.tr(
			"只展示已被产品实际消费的设置；敏感值仅显示是否已设置。",
			"Only settings actually consumed by the product are shown; sensitive values only report whether they are set.")),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(2)
		if st.paramNotice != "" {
			if st.paramErr {
				uikit.ErrorLine(c, st.paramNotice)
			} else {
				design.Apply(ui.Text(c, st.paramNotice), t.Font(design.TypeXXS)).TextColor(t.SuccessText)
			}
		}
		open := st.paramsOpen
		ui.Collapsible(c, st.tr("显示参数", "Show parameters"), &open, func() {
			st.paramsList.Key = func(index int) any {
				if index < 0 || index >= len(specs) {
					return nil
				}
				if key := strings.TrimSpace(specs[index].Key); key != "" {
					return key
				}
				return specs[index].TitleZH
			}
			st.paramsList.Label = func(index int) string {
				if index < 0 || index >= len(specs) {
					return ""
				}
				return st.tr(specs[index].TitleZH, specs[index].TitleEN)
			}
			ui.List(c, &st.paramsList, len(specs)).
				Label(st.tr("插件参数列表", "Plugin settings list")).
				Gap(design.Space1).
				Rows(func(row ui.ListRow) {
					buildPluginParamRow(row.Context, t, st, settings, specs[row.Index])
				})
		})
		st.paramsOpen = open
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
	if spec.Key != "" {
		c.Key("plugin-param/" + spec.Key)
	} else {
		c.Key("plugin-param/" + spec.TitleZH)
	}
	ui.Column(c).FillWidth().Padding(design.Space2, 0).Gap(design.Space1).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, st.tr(spec.TitleZH, spec.TitleEN)), t.Font(design.TypeXS)).
				FontWeight(600).TextColor(t.LabelPrimary).NoWrap()
			if spec.Key != "" {
				design.Apply(ui.Text(c, spec.Key), t.MonoText(design.TypeXXXS)).
					TextColor(t.LabelTertiary).NoWrap().Shrink(0)
			}
			ui.Spacer(c).Grow(1)
			if spec.Editable {
				edit := ui.Button(c, st.tr("编辑 ", "Edit ")+st.tr(spec.TitleZH, spec.TitleEN))
				if edit.Clicked() {
					st.paramEditing = true
					st.paramKey = spec.Key
					st.paramTitle = st.tr(spec.TitleZH, spec.TitleEN)
					st.paramSensitive = spec.Sensitive
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
			design.Apply(ui.Text(c, value), t.MonoText(design.TypeXXXS)).
				TextColor(t.LabelSecondary).Grow(1).MaxLines(1).Ellipsis("…")
			design.Apply(ui.Text(c, st.tr(spec.DescZH, spec.DescEN)), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).MaxLines(2)
		})
		ui.Divider(c)
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
		save := func() {
			st.savePluginParam(settings)
			c.Invalidate()
		}
		design.Apply(ui.Column(c).Width(design.DialogWidthM).Gap(design.Space3), func(element ui.Element) ui.Element {
			return element.PaddingX(design.Space5).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, st.tr("编辑参数", "Edit parameter")), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, st.tr("参数：", "Setting: ")+st.paramKey), t.MonoText(design.TypeXXXS)).
				TextColor(t.LabelTertiary)
			// The dialog's only field takes the focus as it opens and Enter
			// submits the dialog, as in a desktop form.
			input := ui.TextInput(c, &st.paramDraft).
				Label(st.tr("新值：", "New value: ") + st.paramTitle).
				Placeholder(st.tr("留空保持不变", "Leave blank to keep the current value")).
				FillWidth().AutoFocus().OnTextKey(uikit.EnterTo(save))
			if st.paramSensitive {
				// Credentials use the official Password mode: hidden on screen
				// and kept out of the input's own clipboard path.
				input = input.Password()
			}
			if st.paramErrText != "" {
				uikit.ErrorLine(c, st.paramErrText)
			}
			uikit.DialogFooter(c, []uikit.DialogAction{
				{Label: st.tr("取消", "Cancel"), OnClick: func() {
					st.paramEditing = false
					st.paramErrText = ""
					c.Invalidate()
				}},
				{Label: st.tr("保存", "Save"), Primary: true, OnClick: save},
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
