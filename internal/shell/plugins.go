package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// The Plugins page is ZeoKeel's own: a centered column on the brand's 4 DIP
// grid, a native page header, and plugin sections drawn as raised cards of
// native rows. No geometry or color here is sampled from the retired DSH
// reference (ADR 0001); the column, gaps, rows and controls all derive from
// the brand theme (docs/design/native-ui.md).
const (
	// pluginsColumnUnits caps the centered content column: 220 units, 880 DIPs
	// at the brand's Spacing=4, so the list reads as a page column on a wide
	// window instead of stretching edge to edge.
	pluginsColumnUnits float32 = 220
	// pluginsColumnInsetUnits is the room the column keeps from the main area
	// on a narrow window: 24 units, 96 DIPs in total.
	pluginsColumnInsetUnits float32 = 24
	// pluginsIconUnits is the row icon tile: 9 units, 36 DIPs square.
	pluginsIconUnits float32 = 9
	// pluginsGlyphUnits is the mark inside a row's icon tile: 5 units, 20
	// DIPs, the same optical size as mygo's large icons.
	pluginsGlyphUnits float32 = 5
)

// pluginMoreColor is the "More" action's foreground: the theme's tertiary
// label. The retired DSH samples (#6E7278/#C2C6CA) are gone; the brand
// palette's tertiary text is WCAG AA on the plugin cards in both themes
// (a11y_fixes_test pins the role and the contrast), so a semantic token is
// enough and the color follows the theme like every other label.
func pluginMoreColor(t *design.Tokens) ui.Color {
	return t.LabelTertiary
}

// pluginLine turns a line height in baseline pixels into mygo's LineHeight,
// which is a multiple of the font size. Only the remaining baseline-derived
// surfaces use it; new code uses design.TypeMetrics directly.
func pluginLine(size, line float32) float32 { return line / size }

// pluginRow is one row of a plugin section.
type pluginRow struct {
	name, desc    string
	icon          *ui.SVG
	key           string // persisted plugin.<key>.enabled name; "" for display-only
	badge         bool   // shows the "Experimental" pill
	unimplemented bool   // shows the "Not implemented" pill and disables the switch
	toggle        bool   // shows an on/off switch on the right
	on            bool
}

// The row glyphs are ZeoKeel's own monochrome line art on mygo's 16 DIP grid:
// every path uses currentColor, so the row paints them in the brand accent
// and the dark theme stays legible without a second set of files. The shapes
// only need to read at 20 DIPs; they do not imitate any external icon set.
var (
	pluginIconSubagent = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round"><rect x="1.6" y="5.2" width="6.4" height="6.4" rx="1.6"/><rect x="8" y="3" width="6.4" height="6.4" rx="1.6"/></svg>`))

	pluginIconSearch = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"><circle cx="7" cy="7" r="4.6"/><path d="M10.4 10.4 14 14"/></svg>`))

	pluginIconLoop = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="M3.2 8a4.8 4.8 0 0 1 8.2-3.4M12.8 8a4.8 4.8 0 0 1-8.2 3.4"/><path d="M11.4 1.9v2.9H8.5M4.6 14.1v-2.9h2.9"/></svg>`))

	pluginIconShell = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="M2 4.4 6.4 8 2 11.6"/><path d="M8.4 12h5.6"/></svg>`))

	pluginIconTeams = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3"><circle cx="5.2" cy="5.2" r="2.2"/><circle cx="10.8" cy="5.2" r="2.2"/><circle cx="5.2" cy="10.8" r="2.2"/><circle cx="10.8" cy="10.8" r="2.2"/></svg>`))

	pluginIconReview = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round"><path d="M8 1.8 13.2 4v3.8c0 3.3-2.2 5.4-5.2 6.4-3-1-5.2-3.1-5.2-6.4V4z"/><path d="m5.6 8 1.7 1.7L10.6 6.4" stroke-linecap="round"/></svg>`))

	pluginIconDev = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="M5.4 4.6 2 8l3.4 3.4M10.6 4.6 14 8l-3.4 3.4"/></svg>`))

	pluginIconRefresh = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="M13.4 8A5.4 5.4 0 1 1 11.5 4"/><path d="M13.4 1.9v3.2h-3.2"/></svg>`))

	pluginIconSkills = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round"><path d="M8 1.9 9.6 5l3.4.5-2.5 2.4.6 3.4L8 9.7l-3.1 1.6.6-3.4L3 5.5 6.4 5z"/><path d="M12.9 11.4v2.7M11.6 12.8h2.6" stroke-linecap="round"/></svg>`))

	pluginIconMCP = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="M5.4 2.4v5.2a2.6 2.6 0 0 0 5.2 0V2.4"/><path d="M5.4 2.4h1.8M9.4 2.4H11"/><path d="M8 10.2v3.4"/></svg>`))

	pluginIconInfo = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"><circle cx="8" cy="8" r="6.2"/><path d="M8 7.1v4.1"/><circle cx="8" cy="4.8" r="0.9" fill="currentColor" stroke="none"/></svg>`))

	pluginIconPlus = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><path d="M8 2.8v10.4M2.8 8h10.4"/></svg>`))

	pluginIconChevronDown = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M3.8 6.2 8 10.4l4.2-4.2"/></svg>`))

	pluginIconChevronRight = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M6.2 3.8 10.4 8l-4.2 4.2"/></svg>`))
)

// nativeText draws one run of theme typography: the brand role's size, fixed
// line height and weight in the given semantic color. The Plugins and
// Automation pages share it so their type scale is the theme's, not a set of
// per-screen font sizes.
func nativeText(c *ui.Context, role design.TypeRole, color ui.Color, s string) ui.Element {
	size, line, weight := design.TypeMetrics(role)
	return ui.Text(c, s).FontSize(size).FixedLineHeight(line).FontWeight(weight).TextColor(color)
}

// nativePageColumn is the centered content column shared by the list pages:
// a width on the brand's 4 DIP grid, clamped to the main area with the brand
// inset on a narrow window (a docked right sidebar leaves the column 304
// DIPs wide at the 900 DIP minimum).
func (a *App) nativePageColumn(c *ui.Context, units float32) float32 {
	width := units * design.Space1
	if avail := a.contentWidth(c); avail > 0 {
		if limit := avail - pluginsColumnInsetUnits*design.Space1; width > limit {
			width = limit
		}
		if width > avail {
			width = avail
		}
	}
	if width < minHomeStackW {
		width = minHomeStackW
	}
	if width < 1 {
		width = 1
	}
	return width
}

// plugins draws the Plugins page: the page header, then the Built-in and
// Experimental sections on a scrollable centered column, then the dialogs.
func (a *App) plugins(c *ui.Context, t *design.Tokens) {
	builtin := a.builtinPluginRowsLive()
	experimental := a.experimentalPluginRowsLive()
	th := t.Theme()
	width := a.nativePageColumn(c, pluginsColumnUnits)
	ui.Column(c).Grow(1).AlignItems(ui.Center).Background(t.BgBase).Children(func() {
		a.pluginsHeader(c, t, width)
		// The list scrolls under the fixed header, so the last experimental
		// row (and its switch) stays reachable at the 900x600 minimum.
		ui.Scroll(c).Grow(1).WidthPercent(100).Children(func() {
			ui.Column(c).Width(width).Margin(th.Space(2), ui.Auto, th.Space(6), ui.Auto).
				Gap(th.Space(6)).Children(func() {
				a.pluginSection(c, t, a.T("plugins.built_in_plugins"), builtin, false)
				a.pluginSection(c, t, a.T("plugins.experimental_plugins"), experimental, true)
			})
		})
	})
	a.pluginDialogs(c, t)
}

// pluginsHeader is the page header: the title and one-line description on the
// left, the About entry point next to the description, and Refresh + Add
// plugin as native actions on the right.
func (a *App) pluginsHeader(c *ui.Context, t *design.Tokens, width float32) {
	th := t.Theme()
	ui.Row(c).Width(width).Padding(th.Space(4), 0, th.Space(3), 0).
		Justify(ui.SpaceBetween).AlignItems(ui.End).Children(func() {
		ui.Column(c).Gap(th.Space(1)).Children(func() {
			title := nativeText(c, design.TypeXL, t.LabelPrimary, a.T("plugins.title"))
			title.FontWeight(600)
			ui.Row(c).Gap(th.Space(1)).AlignItems(ui.Center).Children(func() {
				nativeText(c, design.TypeXS, t.LabelSecondary, a.T("plugins.subtitle"))
				a.pluginsIconButton(c, t, pluginIconInfo, a.tr("About plugins", "关于插件"), func() {
					a.pluginUI().aboutOpen = true
				})
			})
		})
		ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
			a.pluginsIconButton(c, t, pluginIconRefresh, a.tr("Refresh", "刷新"), func() {
				a.refreshPlugins(c)
			})
			a.addPluginButton(c, t)
		})
	})
}

// pluginsIconButton is the page header's square ghost action: a native button
// base (focus, Enter/Space, hover) with one brand icon.
func (a *App) pluginsIconButton(c *ui.Context, t *design.Tokens, icon *ui.SVG, label string, onClick func()) {
	th := t.Theme()
	button := ui.ButtonBase(c).Size(th.Space(8), th.Space(8)).Center().
		Radius(design.RadiusSM).Label(label).Tooltip(label)
	if button.Hovered() {
		button = button.Background(t.BgHover)
	}
	button.Children(func() {
		ui.Icon(c, icon).Size(th.Space(4), th.Space(4)).TextColor(t.LabelSecondary)
	})
	button.OnClick(onClick)
}

// addPluginButton is the page's primary action: a native PrimaryButton with
// the brand accent fill, a plus glyph and the label, plus a joined menu arrow
// that opens the same built-in-only explanation. Both are real buttons so the
// keyboard reaches them like any other primary action.
func (a *App) addPluginButton(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		main := ui.PrimaryButton(c, "").Label(a.T("plugins.add")).Tooltip(a.T("plugins.add"))
		main.Children(func() {
			ui.Icon(c, pluginIconPlus).Size(th.Space(3.5), th.Space(3.5)).
				TextColor(t.AccentText)
			nativeText(c, design.TypeBody, t.AccentText, a.T("plugins.add")).FontWeight(500)
		})
		main.OnClick(func() { a.pluginUI().addOpen = true })

		more := ui.ButtonBase(c).Size(th.Space(8.5), th.Space(8.5)).Center().
			Radius(design.RadiusMD).Background(t.BgStrong).Border(1, t.BorderL3).
			Label(a.tr("Choose how to add a plugin", "选择添加插件的方式"))
		if more.Hovered() {
			more = more.Background(t.BgHover)
		}
		more.Children(func() {
			ui.Icon(c, pluginIconChevronDown).Size(th.Space(3.5), th.Space(3.5)).
				TextColor(t.LabelPrimary)
		})
		more.OnClick(func() { a.pluginUI().addOpen = true })
	})
}

// pluginSection is one titled card of plugin rows. withMore adds the "More"
// action to the experimental heading.
func (a *App) pluginSection(c *ui.Context, t *design.Tokens, title string, rows []pluginRow, withMore bool) {
	th := t.Theme()
	ui.Column(c).Gap(th.Space(2)).Children(func() {
		ui.Row(c).Height(th.Space(8)).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
				nativeText(c, design.TypeM, t.LabelPrimary, title).FontWeight(600)
			})
			if withMore {
				more := ui.ButtonBase(c).Height(th.Space(7)).PaddingX(th.Space(2)).
					Gap(th.Space(1)).Radius(design.RadiusSM).AlignItems(ui.Center).
					Label(a.tr("More experimental plugins", "更多实验性插件"))
				if more.Hovered() {
					more = more.Background(t.BgHover)
				}
				more.Children(func() {
					nativeText(c, design.TypeBody, t.LabelSecondary, a.T("plugins.more")).FontWeight(500)
					ui.Icon(c, pluginIconChevronRight).Size(th.Space(3.5), th.Space(3.5)).
						TextColor(t.LabelSecondary)
				})
				more.OnClick(func() { a.pluginUI().moreOpen = true })
			}
		})
		card := ui.Column(c).Background(t.BgRaised).Border(1, t.BorderL2).
			Radius(design.RadiusLG).Shadow(0, 2, 8, 0, homeCardShadow(t)).
			PaddingY(th.Space(1))
		card.Children(func() {
			for i, row := range rows {
				a.pluginRow(c, t, row, i < len(rows)-1)
			}
		})
	})
}

// pluginRow is one plugin: a brand icon tile, its name and description, and
// the switch on the right. The row height is content-derived (theme padding,
// role line heights) and the switch is mygo's native Switch, so the control
// keeps the platform's toggle behavior and accessibility.
func (a *App) pluginRow(c *ui.Context, t *design.Tokens, p pluginRow, divider bool) {
	th := t.Theme()
	on, key := p.on, p.key
	if key == "" && a.Ctrl != nil && p.toggle {
		key = pluginKeyForLabel(p.name)
	}
	if a.Ctrl != nil && key != "" {
		on = a.pluginEnabledValue(key, pluginDefaultEnabled(key))
	}
	row := ui.Row(c).PaddingY(th.Space(3)).PaddingX(th.Space(3)).Gap(th.Space(3)).
		AlignItems(ui.Center).Radius(design.RadiusMD)
	if divider {
		row = row.BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2)
	}
	if row.Hovered() {
		row = row.Background(t.BgHover)
	}
	row.Children(func() {
		tile := ui.Row(c).Size(th.Space(pluginsIconUnits), th.Space(pluginsIconUnits)).
			Shrink(0).Radius(design.RadiusMD).Background(t.BgHover).Center()
		tile.Children(func() {
			ui.Icon(c, p.icon).Size(th.Space(pluginsGlyphUnits), th.Space(pluginsGlyphUnits)).
				TextColor(t.Accent)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(th.Space(1)).Children(func() {
			nameRow := ui.Row(c).Gap(th.Space(2)).AlignItems(ui.Center)
			if a.Ctrl != nil {
				nameRow = nameRow.Label(a.tr("View ", "查看") + a.pluginRowName(p.name)).
					Role(ui.RoleButton).Focusable().OnClick(func() { a.openPluginInstance(p.name) })
			}
			nameRow.Children(func() {
				nativeText(c, design.TypeBody, t.LabelPrimary, a.pluginRowName(p.name)).
					FontWeight(500)
				if p.badge {
					a.pluginBadge(c, t)
				}
				if p.unimplemented {
					a.pluginUnimplementedBadge(c, t)
				}
			})
			nativeText(c, design.TypeXS, t.LabelTertiary, a.pluginRowDesc(p.desc)).MaxLines(2)
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

// pluginBadge is the small "Experimental" pill beside a plugin name: the
// brand accent at the low tint, the accent itself for the tiny label.
func (a *App) pluginBadge(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Row(c).PaddingX(th.Space(1.5)).PaddingY(th.Space(0.5)).Radius(design.RadiusMax).
		Background(t.Accent.Alpha(0.12)).AlignItems(ui.Center).Children(func() {
		nativeText(c, design.TypeXXXS, t.Accent, a.T("plugins.experimental_badge")).FontWeight(500)
	})
}

// pluginUnimplementedBadge marks an experimental row whose feature does not
// exist in the runtime yet: its switch is disabled, so the pill says why
// instead of leaving the user to discover it (PD-08).
func (a *App) pluginUnimplementedBadge(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Row(c).PaddingX(th.Space(1.5)).PaddingY(th.Space(0.5)).Radius(design.RadiusMax).
		Background(t.WarningText.Alpha(0.12)).AlignItems(ui.Center).Children(func() {
		nativeText(c, design.TypeXXXS, t.WarningText, a.T("plugins.not_implemented")).FontWeight(500)
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
	"Shell tools":               "plugins.row.shell",
	"Skills":                    "plugins.row.skills",
	"MCP servers":               "plugins.row.mcp",
	"Agent Teams":               "plugins.row.agent_teams",
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

// pluginSwitch is the non-interactive switch of the static/preview path: the
// same native control, drawn from the brand theme, with no toggle wired.
func (a *App) pluginSwitch(c *ui.Context, t *design.Tokens, on bool) {
	value := on
	ui.Switch(c, &value)
}

// pluginSwitchLive is the Plugins and Automation row switch: mygo's native
// Switch (semantics, keyboard, focus ring and animation for free) bound to the
// current persisted value. The click is reported through toggle, which writes
// the store; the next frame re-reads it.
func (a *App) pluginSwitchLive(c *ui.Context, t *design.Tokens, on bool, label string, toggle func()) {
	value := on
	sw := ui.Switch(c, &value).Label(label)
	if sw.Changed() {
		toggle()
	}
}
