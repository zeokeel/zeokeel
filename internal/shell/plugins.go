package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// The plugins page is the baseline's centered 960-wide column in the main
// area: a title block, then the built-in and experimental sections.
const pluginPageWidth float32 = 960

// Colors the plugins page uses that the design tokens do not cover.
// Values are from the retired DSH reference design. The "More" label has a
// theme-aware dark value (46-plugins-dark.json); the accent pill follows
// the theme's accent token instead of hard-coding the light hue.
var (
	pluginIconMuted     = ui.Hex("#ADB2B8") // info and refresh glyphs
	pluginMoreLabel     = ui.Hex("#6E7278") // light "More" action label
	pluginMoreLabelDark = ui.Hex("#C2C6CA") // dark "More" action label
)

// pluginMoreColor is the "More" action's foreground for the current theme.
func pluginMoreColor(t *design.Tokens) ui.Color {
	if t.Dark {
		return pluginMoreLabelDark
	}
	return pluginMoreLabel
}

// pluginLine turns a line height in baseline pixels into mygo's LineHeight,
// which is a multiple of the font size.
func pluginLine(size, line float32) float32 { return line / size }

// pluginRow is one row of a plugin section.
type pluginRow struct {
	name, desc string
	icon       *ui.SVG
	badge      bool // shows the "Experimental" pill
	toggle     bool // shows an on/off switch on the right
	on         bool
}

// The row glyphs follow the baseline's own artwork: small vector marks in
// the accent hues of the retired reference capture.
var (
	pluginIconSubagent = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">
<rect x="9" y="9" width="13" height="13" rx="3.5" fill="#45C2B8"/>
<rect x="2" y="2" width="12" height="12" rx="3" fill="#7C88F5"/>
</svg>`))

	pluginIconSearch = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">
<circle cx="10.5" cy="10.5" r="7" fill="none" stroke="#5C8FF2" stroke-width="3.2"/>
<circle cx="10.5" cy="10.5" r="3.4" fill="none" stroke="#45C2C9" stroke-width="1.6"/>
<line x1="16" y1="16" x2="21.5" y2="21.5" stroke="#5C8FF2" stroke-width="3.2" stroke-linecap="round"/>
</svg>`))

	pluginIconLoop = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">
<circle cx="12" cy="12" r="6" fill="#5B7CF0"/>
<circle cx="12" cy="6" r="5.6" fill="#7B8CF5"/>
<circle cx="12" cy="18" r="5.6" fill="#6FA0F8"/>
<circle cx="6" cy="12" r="5.6" fill="#A78DF2"/>
<circle cx="18" cy="12" r="5.6" fill="#8B7BF0"/>
<path d="M12 7 L17 12 L12 17 L7 12 Z" fill="#FFFFFF"/>
</svg>`))

	pluginIconShell = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">
<polyline points="4 5 12 11.5 4 18" fill="none" stroke="#5C93F5" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/>
<line x1="14" y1="18" x2="20" y2="18" stroke="#5C93F5" stroke-width="3" stroke-linecap="round"/>
</svg>`))

	pluginIconTeams = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">
<rect x="2.5" y="3" width="8.5" height="8.5" rx="2" fill="#6D8CF5"/>
<rect x="13" y="3" width="8.5" height="8.5" rx="2" fill="#F0A860"/>
<rect x="2.5" y="13.5" width="8.5" height="8.5" rx="2" fill="#40D8E0"/>
<rect x="13" y="13.5" width="8.5" height="8.5" rx="2" fill="#78B0F8"/>
</svg>`))

	pluginIconVoice = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">
<rect x="1.5" y="10.5" width="3" height="4" rx="1.5" fill="#78B0F0"/>
<rect x="6.5" y="7.5" width="3" height="10" rx="1.5" fill="#5C93F5"/>
<rect x="11.5" y="3.5" width="3" height="18" rx="1.5" fill="#5C93F5"/>
<rect x="16.5" y="8" width="3" height="9" rx="1.5" fill="#5C93F5"/>
<rect x="21.5" y="11" width="3" height="3" rx="1.5" fill="#78B0F0"/>
</svg>`))

	pluginIconReview = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">
<path d="M12 1.5 L21.5 5 V12 C21.5 17.5 17.5 21.5 12 23 C6.5 21.5 2.5 17.5 2.5 12 V5 Z" fill="#7FA5F5"/>
<path d="M6.5 12 L10.5 16 L18 8" fill="none" stroke="#FFFFFF" stroke-width="2.8" stroke-linecap="round" stroke-linejoin="round"/>
</svg>`))

	pluginIconDev = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">
<rect x="1.5" y="3" width="17" height="10" rx="2.5" fill="#B8D0F8"/>
<rect x="1.5" y="9" width="17" height="10" rx="2.5" fill="#4878E8"/>
<circle cx="17.5" cy="17.5" r="5" fill="none" stroke="#40B8E8" stroke-width="2.4"/>
<line x1="21.5" y1="21.5" x2="24.5" y2="24.5" stroke="#40B8E8" stroke-width="2.4" stroke-linecap="round"/>
</svg>`))

	pluginIconRefresh = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">
<path d="M21 12 a9 9 0 1 1-3.2-6.9" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
<polyline points="21 3 21 9 15 9" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
</svg>`))

	pluginIconInfo = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">
<circle cx="12" cy="12" r="10" fill="none" stroke="currentColor" stroke-width="2"/>
<line x1="12" y1="17" x2="12" y2="11" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
<circle cx="12" cy="7.5" r="1.3" fill="currentColor"/>
</svg>`))

	pluginIconPlus = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">
<line x1="12" y1="5" x2="12" y2="19" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
<line x1="5" y1="12" x2="19" y2="12" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
</svg>`))

	pluginIconChevronDown = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">
<polyline points="6 9 12 15 18 9" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
</svg>`))

	pluginIconChevronRight = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">
<polyline points="9 6 15 12 9 18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
</svg>`))
)

func (a *App) plugins(c *ui.Context, t *design.Tokens) {
	builtin := a.builtinPluginRowsLive()
	experimental := a.experimentalPluginRowsLive()
	ui.Column(c).Grow(1).AlignItems(ui.Center).Children(func() {
		a.pluginsHeader(c, t)
		// The plugin list scrolls under the fixed title block, so the last
		// experimental row (and its switches) stays reachable at 900x600.
		ui.Scroll(c).Grow(1).WidthPercent(100).Children(func() {
			ui.Column(c).Width(a.pluginsPageWidth(c)).Margin(design.Space8, ui.Auto, design.Space8, ui.Auto).Gap(design.Space8).Children(func() {
				a.pluginSection(c, t, a.T("plugins.section.builtin"), builtin, false)
				a.pluginSection(c, t, a.T("plugins.experimental_plugins"), experimental, true)
			})
		})
	})
	a.pluginDialogs(c, t)
}

// pluginsPageWidth caps the 960px baseline column to the content column minus
// DSH's 48px side insets, so the Add plugin split button is not clipped at
// 1200px (824px column, right edge 1152) or when the docked right sidebar
// leaves a narrower main column at the 900px minimum window.
func (a *App) pluginsPageWidth(c *ui.Context) float32 {
	return a.pageColumnWidth(c, pluginPageWidth)
}

// pluginsHeader is the title block: heading and subtitle on the left, the
// refresh and "Add plugin" actions flush right, as in the baseline.
func (a *App) pluginsHeader(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Width(a.pluginsPageWidth(c)).Label(a.tr("Plugins page header", "插件页面标题")).
		Padding(28, 0, 0, 0).
		Justify(ui.SpaceBetween).AlignItems(ui.Start).Children(func() {
		ui.Column(c).Gap(design.Space1).Children(func() {
			// TypeL: 20/28/500 in the baseline.
			ui.Text(c, a.T("plugins.title")).FontSize(20).LineHeight(pluginLine(20, 28)).FontWeight(500).
				TextColor(t.LabelPrimary)
			ui.Row(c).Gap(design.Space1).AlignItems(ui.Center).Children(func() {
				// TypeXS: 13/20/400 in the baseline.
				ui.Text(c, a.T("plugins.subtitle")).
					FontSize(13).LineHeight(pluginLine(13, 20)).TextColor(t.LabelSecondary)
				info := ui.Row(c).Width(20).Height(20).Center()
				if a.Ctrl != nil {
					info = info.Label(a.tr("About plugins", "关于插件")).Role(ui.RoleButton).Focusable()
					info.OnClick(func() { a.pluginUI().aboutOpen = true })
				}
				info.Children(func() {
					ui.Icon(c, pluginIconInfo).FontSize(12).TextColor(pluginIconMuted)
				})
			})
		})
		ui.Row(c).Gap(design.Space4).AlignItems(ui.Center).Children(func() {
			refresh := ui.Row(c).Width(28).Height(28).Radius(design.RadiusSM).Center()
			if a.Ctrl != nil {
				refresh = refresh.Label(a.tr("Refresh", "刷新")).Role(ui.RoleButton).Focusable()
				refresh.OnClick(func() { a.refreshPlugins(c) })
			}
			refresh.Children(func() {
				ui.Icon(c, pluginIconRefresh).FontSize(16).TextColor(pluginIconMuted)
			})
			a.addPluginButton(c, t)
		})
	})
}

// addPluginButton is the baseline's dark split button: label and plus on
// the left, a menu arrow on the right.
func (a *App) addPluginButton(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Children(func() {
		main := ui.Row(c).Width(114).Height(32).Radius(12, 0, 0, 12).
			Background(t.LabelPrimary).PaddingX(14).Gap(design.Space1).
			AlignItems(ui.Center).Center()
		if a.Ctrl != nil {
			main = main.Label(a.T("plugins.add")).Role(ui.RoleButton).Focusable()
			main.OnClick(func() { a.pluginUI().addOpen = true })
		}
		main.Children(func() {
			ui.Icon(c, pluginIconPlus).FontSize(13).TextColor(t.OnPrimary)
			ui.Text(c, a.T("plugins.add")).FontSize(13).LineHeight(pluginLine(13, 20)).FontWeight(500).
				TextColor(t.OnPrimary)
		})
		more := ui.Row(c).Width(32).Height(32).Radius(0, 12, 12, 0).
			Background(t.LabelPrimary).BorderWidth(0, 0, 0, 1).
			BorderColor(ui.RGBA(255, 255, 255, 0.30)).Center()
		if a.Ctrl != nil {
			more = more.Label(a.tr("Choose how to add a plugin", "选择添加插件的方式")).
				Role(ui.RoleButton).Focusable()
			more.OnClick(func() { a.pluginUI().addOpen = true })
		}
		more.Children(func() {
			ui.Icon(c, pluginIconChevronDown).FontSize(12).TextColor(t.OnPrimary)
		})
	})
}

// pluginSection is one titled list of plugin rows. withMore adds the
// "More" action to the section heading, as the experimental list has.
func (a *App) pluginSection(c *ui.Context, t *design.Tokens, title string, rows []pluginRow, withMore bool) {
	ui.Column(c).Gap(design.Space2).Children(func() {
		header := ui.Row(c).Height(22).AlignItems(ui.Center)
		if withMore {
			header = header.Height(28).Justify(ui.SpaceBetween)
		}
		header.Children(func() {
			// TypeBody is 14/22/400; the section title is its 500 weight.
			ui.Text(c, title).FontSize(14).LineHeight(pluginLine(14, 22)).FontWeight(500).
				TextColor(t.LabelPrimary).Translate(0, 1)
			if withMore {
				more := ui.Row(c).Height(28).Padding(0, 0, 0, design.Space2).Gap(design.Space1).
					Radius(design.RadiusSM).AlignItems(ui.Center)
				if a.Ctrl != nil {
					more = more.Label(a.tr("More experimental plugins", "更多实验性插件")).
						Role(ui.RoleButton).Focusable()
					more.OnClick(func() { a.pluginUI().moreOpen = true })
				}
				more.Children(func() {
					moreColor := pluginMoreColor(t)
					ui.Text(c, a.T("plugins.more")).FontSize(14).LineHeight(pluginLine(14, 22)).
						TextColor(moreColor).Translate(0, 1)
					ui.Icon(c, pluginIconChevronRight).FontSize(14).
						TextColor(moreColor)
				})
			}
		})
		ui.Column(c).Gap(2).Children(func() {
			for _, row := range rows {
				a.pluginRow(c, t, row)
			}
		})
	})
}

// pluginRow is one plugin: a 50px icon tile, its name and description,
// and for experimental plugins the switch on the right. A live row reads its
// persisted toggle and clicking the row flips it.
func (a *App) pluginRow(c *ui.Context, t *design.Tokens, p pluginRow) {
	on, key := p.on, ""
	if a.Ctrl != nil && p.toggle {
		if key = pluginKeyForLabel(p.name); key != "" {
			on = a.pluginEnabledValue(key, pluginDefaultEnabled(key))
		}
	}
	row := ui.Row(c).Height(66).Gap(14).AlignItems(ui.Center)
	row.Children(func() {
		ui.Row(c).Width(50).Height(50).Radius(design.RadiusLG).
			Border(1, t.BorderL3).Center().Children(func() {
			ui.Image(c, p.icon).Width(24).Height(24).Fit(ui.Contain)
		})
		ui.Column(c).Grow(1).Gap(design.Space1).Children(func() {
			nameRow := ui.Row(c).Height(20).Gap(design.Space2).AlignItems(ui.Center)
			if a.Ctrl != nil {
				nameRow = nameRow.Label(a.tr("View ", "查看") + a.pluginRowName(p.name)).
					Role(ui.RoleButton).Focusable()
				nameRow.OnClick(func() { a.openPluginInstance(p.name) })
			}
			nameRow.Children(func() {
				// Row name: TypeBody 14 at 20px line, 500 weight.
				ui.Text(c, a.pluginRowName(p.name)).FontSize(14).LineHeight(pluginLine(14, 20)).FontWeight(500).
					TextColor(t.LabelPrimary).Translate(0, 1)
				if p.badge {
					a.pluginBadge(c, t)
				}
			})
			// Description: 13/18 in the baseline.
			ui.Text(c, a.pluginRowDesc(p.desc)).FontSize(13).LineHeight(pluginLine(13, 18)).
				TextColor(t.LabelTertiary)
		})
		if p.toggle {
			if a.Ctrl != nil && key != "" {
				current := on
				a.pluginSwitchLive(c, t, on, a.tr("Enable ", "启用")+a.pluginRowName(p.name), func() {
					a.togglePlugin(c, key, current)
				})
			} else {
				a.pluginSwitch(c, t, on)
			}
		}
	})
}

// pluginBadge is the blue "Experimental" pill beside a plugin name. The pill
// uses the theme's accent so the dark theme gets DSH's #7AAAFF on its 10%
// tint instead of the light accent, which is too dark to read there.
func (a *App) pluginBadge(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Height(18).PaddingX(design.Space2).Radius(design.RadiusMax).
		Background(t.Accent.Alpha(0.10)).AlignItems(ui.Center).Translate(-1, 0).Children(func() {
		ui.Text(c, a.T("plugins.experimental_badge")).FontSize(10).LineHeight(pluginLine(10, 10)).
			FontWeight(500).TextColor(t.Accent)
	})
}

// pluginRowNameKeys maps a row's stable English name to its catalog prefix.
// The English name stays the settings-key source (pluginKeyForLabel), so only
// the display string is localized.
var pluginRowNameKeys = map[string]string{
	"Subagent":                  "plugins.row.subagent",
	"Web search":                "plugins.row.web_search",
	"Agent loop":                "plugins.row.agent_loop",
	"Shell":                     "plugins.row.shell",
	"Agent Teams":               "plugins.row.agent_teams",
	"Voice input":               "plugins.row.voice_input",
	"Auto Authorization Review": "plugins.row.auto_review",
	"Developer Tools":           "plugins.row.developer_tools",
}

// pluginRowDescKeys maps each static/live English description to its key.
var pluginRowDescKeys = map[string]string{
	"Set Subagent recursion depth, count, and models.":                                                      "plugins.row.subagent.desc",
	"Set up the DeepSeek search provider.":                                                                  "plugins.row.web_search.desc",
	"Control how the Agent dispatches tool calls.":                                                          "plugins.row.agent_loop.desc",
	"Limit how long each command may run and how much it may output.":                                       "plugins.row.shell.desc",
	"Enable team collaboration, team tools, the member roster, and the shared task board.":                  "plugins.row.agent_teams.desc",
	"Transcribe recordings locally with SenseVoice; first use requires installing dependencies":             "plugins.row.voice_input.desc",
	"Add an Auto review permission mode that uses the model to assess authorization before each tool call.": "plugins.row.auto_review.desc",
	"Inspect and debug raw session data, grouped chat messages, and the NodeJS backend.":                    "plugins.row.developer_tools.desc",
	"Let the agent search the web with the configured provider.":                                            "plugins.live.web_search.desc",
	"Load SKILL.md capabilities from the workspace skills folder.":                                          "plugins.live.skills.desc",
	"Expose tools from configured Model Context Protocol servers.":                                          "plugins.live.mcp.desc",
	"Enable the bash, write_file and edit_file tool group.":                                                 "plugins.live.shell.desc",
	"Let the agent delegate work to a nested subagent.":                                                     "plugins.live.subagent.desc",
}

// pluginRowName localizes a row's display name, falling back to the English
// name for rows without a catalog entry.
func (a *App) pluginRowName(name string) string {
	if key := pluginRowNameKeys[name]; key != "" {
		return a.T(key + ".name")
	}
	return name
}

// pluginRowDesc localizes a row's description, falling back to English.
func (a *App) pluginRowDesc(desc string) string {
	if key := pluginRowDescKeys[desc]; key != "" {
		return a.T(key)
	}
	return desc
}

// white knob, left off and right on.
func (a *App) pluginSwitch(c *ui.Context, t *design.Tokens, on bool) {
	align := ui.Start
	if on {
		align = ui.End
	}
	ui.Row(c).Width(36).Height(20).Radius(design.RadiusMax).Background(t.BorderL3).
		Padding(2).AlignItems(ui.Center).Justify(align).Children(func() {
		ui.Box(c).Width(16).Height(16).Radius(design.RadiusMax).Background(t.White)
	})
}
