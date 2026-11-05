package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// The Settings panel geometry: an 800x800 panel with a 28px radius, a
// 188px nav column and a 564px options column inset 24px.
const (
	settingsWidth      float32 = 800
	settingsHeight     float32 = 800
	settingsRadius     float32 = 28
	settingsNavW       float32 = 188
	settingsNavPad     float32 = 12
	settingsNavTop     float32 = 22
	settingsTitleGap   float32 = 18
	settingsCellH      float32 = 40
	settingsHeaderH    float32 = 54
	settingsHeaderTop  float32 = 20
	settingsHeaderEnd  float32 = 14
	settingsOptionsPad float32 = 24
	settingsTextEnd    float32 = 48 // gap between a row's text and its control
	settingsControlH   float32 = 36
	settingsCardH      float32 = 84
	// settingsViewportMargin keeps the panel clear of the window edges at the
	// 900x600 minimum, so the title and header actions stay on screen.
	settingsViewportMargin float32 = 24
)

// settingsPanelSize clamps the 800x800 baseline panel to the current window.
// The body scrolls inside it; the panel itself never starts above the window.
func settingsPanelSize(c *ui.Context) (float32, float32) {
	w, h := settingsWidth, settingsHeight
	if cw, ch := c.Size(); cw > 0 && ch > 0 {
		if maxW := cw - 2*settingsViewportMargin; w > maxW {
			w = maxW
		}
		if maxH := ch - 2*settingsViewportMargin; h > maxH {
			h = maxH
		}
	}
	if w < 320 {
		w = 320
	}
	if h < 240 {
		h = 240
	}
	return w, h
}

// settingsText draws text at one of the design type roles. mygo's
// LineHeight is a multiple of the font size, so the role's line height,
// in DIPs, goes through FixedLineHeight.
func settingsText(c *ui.Context, s string, role design.TypeRole, color ui.Color) ui.Element {
	size, line, weight := design.TypeMetrics(role)
	return ui.Text(c, s).FontSize(size).FixedLineHeight(line).FontWeight(weight).TextColor(color)
}

// Baseline surfaces the shared tokens do not expose, sampled from
// retired DSH reference design as the
// home screen does.
var (
	settingsSurface          = ui.Hex("#F5F6F7") // selectors, buttons, selected card
	settingsNavSelected      = ui.Hex("#EBEEF2") // the selected nav cell
	settingsCardBorder       = ui.RGBA(0, 0, 0, 0.16)
	settingsCardSelectedEdge = ui.Hex("#ADB2B8")

	settingsPanelDark      = ui.Hex("#2C2C2E")
	settingsSurfaceDark    = ui.Hex("#353638")
	settingsNavActiveDark  = ui.Hex("#43454A")
	settingsCardBorderDark = ui.RGBA(255, 255, 255, 0.2)
)

// settingsPanelColor is the modal surface: white in the light theme as the
// baseline has it, the raised surface in the dark one.
func settingsPanelColor(t *design.Tokens) ui.Color {
	if t.Dark {
		return settingsPanelDark
	}
	return t.BgBase
}

// settingsFillColor is the baseline's control fill.
func settingsFillColor(t *design.Tokens) ui.Color {
	if t.Dark {
		return settingsSurfaceDark
	}
	return settingsSurface
}

// settingsNavActiveColor is the selected nav cell's tint.

// settingsCardOutline is an unselected theme card's outline.
func settingsCardOutline(t *design.Tokens) ui.Color {
	if t.Dark {
		return settingsCardBorderDark
	}
	return settingsCardBorder
}

// Settings icons: the baseline's 16px line icons. The gear, sun, moon,
// monitor and chevron are Lucide's (ISC); the database, sliders and
// person are drawn to the baseline's measured 16x16 geometry.
var (
	settingsIconGear = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9.671 4.136a2.34 2.34 0 0 1 4.659 0 2.34 2.34 0 0 0 3.319 1.915 2.34 2.34 0 0 1 2.33 4.033 2.34 2.34 0 0 0 0 3.831 2.34 2.34 0 0 1-2.33 4.033 2.34 2.34 0 0 0-3.319 1.915 2.34 2.34 0 0 1-4.659 0 2.34 2.34 0 0 0-3.32-1.915 2.34 2.34 0 0 1-2.33-4.033 2.34 2.34 0 0 0 0-3.831A2.34 2.34 0 0 1 6.35 6.051a2.34 2.34 0 0 0 3.319-1.915"/><circle cx="12" cy="12" r="3"/></svg>`))

	settingsIconDatabase = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.25" stroke-linecap="round" stroke-linejoin="round"><ellipse cx="6.4" cy="3.4" rx="4.7" ry="1.7"/><path d="M1.7 3.4v8.6a4.7 1.7 0 0 0 9.4 0V3.4"/><path d="M1.7 7.7a4.7 1.7 0 0 0 9.4 0"/><circle cx="12.2" cy="12" r="1.9"/></svg>`))

	settingsIconSliders = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.25" stroke-linecap="round"><path d="M2 3.5h6.4M12.4 3.5H14M2 7.5h1.9M7.9 7.5H14M2 12.5h6.4M12.4 12.5H14"/><circle cx="10.4" cy="3.5" r="1.4"/><circle cx="5.4" cy="7.5" r="1.4"/><circle cx="10.4" cy="12.5" r="1.4"/></svg>`))

	settingsIconPerson = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.25" stroke-linecap="round" stroke-linejoin="round"><circle cx="7.7" cy="4.2" r="2.2"/><path d="M2.7 13.2a5 5 0 0 1 10 0"/><circle cx="3.1" cy="12.9" r="1.3"/><circle cx="12.3" cy="12.9" r="1.3"/></svg>`))

	settingsIconSun = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41"/></svg>`))

	settingsIconMoon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20.985 12.486a9 9 0 1 1-9.473-9.472c.405-.022.617.46.402.803a6 6 0 0 0 8.268 8.268c.344-.215.825-.004.803.401"/></svg>`))

	settingsIconMonitor = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="20" height="14" x="2" y="3" rx="2"/><line x1="8" x2="16" y1="21" y2="21"/><line x1="12" x2="12" y1="17" y2="21"/></svg>`))

	settingsIconChevron = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m6 9 6 6 6-6"/></svg>`))

	settingsIconClose = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.15" stroke-linecap="round"><path d="M2.4 2.4l9.2 9.2M11.6 2.4l-9.2 9.2"/></svg>`))
)

// settingsNavActiveColor is the selected nav cell's fill.
func settingsNavActiveColor(t *design.Tokens) ui.Color {
	if t.Dark {
		return settingsNavActiveDark
	}
	return settingsNavSelected
}

// settingsNavCell is one row of the nav list; the first is selected.
func (a *App) settingsNavCell(c *ui.Context, t *design.Tokens, label string, active bool, icon *ui.SVG) ui.Element {
	cell := ui.Row(c).Height(settingsCellH).PaddingX(design.Space3).
		Gap(design.Space2).Radius(design.RadiusMD).AlignItems(ui.Center).
		Label(label).Role(ui.RoleButton).Focusable()
	if active {
		cell = cell.Background(settingsNavActiveColor(t))
	}
	cell.Children(func() {
		ui.Image(c, icon).Size(16, 16).TextColor(t.LabelPrimary)
		settingsText(c, label, design.TypeBody, t.LabelPrimary)
	})
	return cell
}

// settingsRowStyle tweaks a row where the baseline departs from the common
// case: its title line height, its description color and its gap.
type settingsRowStyle struct {
	titleLine float32
	descColor ui.Color
	gap       float32
}

// settingsRow is one option: title and description on the left, a control
// on the right, with the baseline's 1px bottom separator.
func (a *App) settingsRow(c *ui.Context, t *design.Tokens, title, desc string, st settingsRowStyle, control func()) {
	gap := st.gap
	if gap == 0 {
		gap = design.Space2
	}
	descColor := st.descColor
	if descColor == (ui.Color{}) {
		descColor = t.LabelTertiary
	}
	ui.Row(c).Padding(design.Space4, 0, design.Space4, 0).Gap(gap).
		AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2).Children(func() {
		ui.Column(c).Grow(1).Padding(0, settingsTextEnd, 0, 0).
			Gap(design.Space1).Children(func() {
			titleEl := settingsText(c, title, design.TypeBody, t.LabelPrimary)
			if st.titleLine > 0 {
				titleEl.FixedLineHeight(st.titleLine)
			}
			if desc != "" {
				settingsText(c, desc, design.TypeXXS, descColor)
			}
		})
		control()
	})
}

// settingsAppearance is the appearance group: a title over three theme cards.

// settingsThemeCard is one 183x84 appearance card.

// settingsSelector draws a 36px select pill with a chevron.

// settingsFontSize draws the baseline's font size stepper: a 72px pill, the
// unit, and an expander chevron.

// settingsChevronButton is a 24x24 chevron hit area.

// settingsSwitch draws the baseline's 36x20 switch, on or off.

// settingsActionButton is a filled 36px button, as a.T("settings.edit_shortcuts") is.

// settingsOutlineButton is the header's bordered a.T("settings.open_configuration_file").

// settingsClose draws the header's X button.
