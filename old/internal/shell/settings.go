package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/brand"
	"github.com/zeokeel/zeokeel/old/internal/design"
)

// Settings panel metrics. The panel is responsive and token-derived: the
// brand's 4 DIP spacing grid sizes it (880 DIP cap, 720 DIP readable width),
// and the viewport clamps it so the minimum 900x600 window keeps it on screen.
// No value is sampled from an external reference page.
const (
	// settingsPanelUnits caps the panel width: 220 spacing units, 880 DIPs at
	// the brand Spacing=4.
	settingsPanelUnits float32 = 220
	// settingsPanelHeightUnits caps the panel height: 190 units, 760 DIPs.
	settingsPanelHeightUnits float32 = 190
	// settingsPanelMarginUnits is the room the panel keeps from the window
	// edge: 8 units, 32 DIPs per side.
	settingsPanelMarginUnits float32 = 8
	// settingsPanelFloorUnits keeps the clamped panel usable on a window below
	// the 900x600 minimum: 80 units, 320 DIPs.
	settingsPanelFloorUnits float32 = 80
	// settingsNavUnits is the nav column's width: 50 units, 200 DIPs.
	settingsNavUnits float32 = 50
)

// settingsPanelSize returns the centered panel's size: the brand cap clamped
// to the window with a 32 DIP margin, so the header, nav and body stay on
// screen at the 900x600 minimum window.
func settingsPanelSize(c *ui.Context) (float32, float32) {
	th := c.Theme()
	w, h := th.Space(settingsPanelUnits), th.Space(settingsPanelHeightUnits)
	if cw, ch := c.Size(); cw > 0 && ch > 0 {
		margin := th.Space(settingsPanelMarginUnits)
		if maxW := cw - 2*margin; w > maxW {
			w = maxW
		}
		if maxH := ch - 2*margin; h > maxH {
			h = maxH
		}
	}
	if floor := th.Space(settingsPanelFloorUnits); w < floor {
		w = floor
	}
	if h < 1 {
		h = 1
	}
	return w, h
}

// settingsNavWidth returns the nav column width: 200 DIPs on a wide window,
// at most a third of the panel on the narrowest ones.
func settingsNavWidth(c *ui.Context) float32 {
	th := c.Theme()
	w := th.Space(settingsNavUnits)
	if pw, _ := settingsPanelSize(c); pw > 0 {
		if limit := pw / 3; w > limit {
			w = limit
		}
	}
	if floor := th.Space(40); w < floor {
		w = floor
	}
	return w
}

// settingsText draws text at one of the design type roles. mygo's
// LineHeight is a multiple of the font size, so the role's line height,
// in DIPs, goes through FixedLineHeight.
func settingsText(c *ui.Context, s string, role design.TypeRole, color ui.Color) ui.Element {
	size, line, weight := design.TypeMetrics(role)
	return ui.Text(c, s).FontSize(size).FixedLineHeight(line).FontWeight(weight).TextColor(color)
}

// settingsCardSelectedEdge is the brand neutral outline of a selected
// preset/theme card that does not paint the accent (plugin pages read it).
// It mirrors the dark palette's tertiary text, which stays visible on both
// the light and the dark surface.
var settingsCardSelectedEdge = brand.DarkPalette().TextTertiary

// settingsPanelColor is the modal surface: the brand's raised surface in both
// appearances, so the panel separates from the scrimmed page behind it.
func settingsPanelColor(t *design.Tokens) ui.Color {
	return t.BgRaised
}

// settingsFillColor is the neutral control fill: the brand's strong raised
// surface (light #F1F3F5, dark #272736), used by the filled pills and the
// plugin-detail actions that still draw their own chrome.
func settingsFillColor(t *design.Tokens) ui.Color {
	return t.BgStrong
}

// settingsControlColor is a control's fill inside a group card: the panel
// surface, which separates from the card's BgStrong in both appearances.
func settingsControlColor(t *design.Tokens) ui.Color {
	return t.BgRaised
}

// settingsCardOutline is an unselected theme card's outline.
func settingsCardOutline(t *design.Tokens) ui.Color {
	return t.BorderL2
}

// Settings icons: brand-weight line icons on the 16px grid. The gear, sun,
// moon, monitor and chevron are Lucide's (ISC); the database, sliders and
// person are drawn on the same grid.
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

// settingsNavActiveColor is the selected nav cell's fill: the brand's
// translucent hover tint over the nav surface, in either appearance.
func settingsNavActiveColor(t *design.Tokens) ui.Color {
	return t.BgHover
}

// settingsNavCell is one row of the nav list, built on the native button base
// so hover/press/focus/role and keyboard activation come from mygo. The
// selected cell takes the brand hover tint and an accent icon.
func (a *App) settingsNavCell(c *ui.Context, t *design.Tokens, label string, active bool, icon *ui.SVG) ui.Element {
	th := t.Theme()
	cell := ui.ButtonBase(c).FillWidth().Height(th.Space(10)).
		PaddingX(th.Space(3)).Gap(th.Space(2)).Radius(design.RadiusSM).
		AlignItems(ui.Center).
		Label(label).Role(ui.RoleButton).Focusable()
	if active || cell.Hovered() {
		cell = cell.Background(settingsNavActiveColor(t))
	}
	iconColor := t.LabelSecondary
	textColor := t.LabelSecondary
	weight := 400
	if active {
		iconColor = t.Accent
		textColor = t.LabelPrimary
		weight = 500
	}
	cell.Children(func() {
		ui.Image(c, icon).Size(th.Space(4), th.Space(4)).TextColor(iconColor)
		settingsText(c, label, design.TypeBody, textColor).FontWeight(weight)
	})
	return cell
}

// settingsRowStyle tweaks a row where a page departs from the common case:
// its title line height, its description color, its gap, and whether it is
// the last row of a group (the last row drops the separator).
type settingsRowStyle struct {
	titleLine float32
	descColor ui.Color
	gap       float32
	last      bool
}

// settingsRow is one option inside a group card: title and description on the
// left, a control on the right. Rows are separated by the theme's regular
// divider; the last row of a group omits it.
func (a *App) settingsRow(c *ui.Context, t *design.Tokens, title, desc string, st settingsRowStyle, control func()) {
	th := t.Theme()
	gap := st.gap
	if gap == 0 {
		gap = th.Space(4)
	}
	descColor := st.descColor
	if descColor == (ui.Color{}) {
		descColor = t.LabelTertiary
	}
	row := ui.Row(c).Padding(th.Space(2), th.Space(4)).Gap(gap).
		MinHeight(th.Space(11)).AlignItems(ui.Center)
	if !st.last {
		row = row.BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2)
	}
	row.Children(func() {
		ui.Column(c).Grow(1).Gap(th.Space(1)).Children(func() {
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
