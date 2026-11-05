package design

import "github.com/egoist/mygo/ui"

// Baseline spacing steps (4px grid), from docs/baseline/css.md.
const (
	Space1 float32 = 4
	Space2 float32 = 8
	Space3 float32 = 12
	Space4 float32 = 16
	Space5 float32 = 20
	Space6 float32 = 24
	Space8 float32 = 32
)

// Baseline radii, from baseline/css/summary.json.
const (
	RadiusXS  float32 = 4
	RadiusSM  float32 = 8
	RadiusMD  float32 = 12
	RadiusLG  float32 = 16
	RadiusXL  float32 = 20
	RadiusMax float32 = 999
)

// RowState is a list row's interaction state.
type RowState int

const (
	RowNormal RowState = iota
	RowHover
	RowSelected
)

// Page fills a screen area with the page background.
func (t *Tokens) Page() Style {
	return func(el ui.Element) ui.Element {
		return el.Background(t.BgBase)
	}
}

// Sidebar is the 280px source list (baseline geometry).
func (t *Tokens) Sidebar() Style {
	return func(el ui.Element) ui.Element {
		return el.Width(280).Background(t.BgSidebar).
			BorderWidth(0, 1, 0, 0).BorderColor(t.BorderL3)
	}
}

// Main is the flexible content column.
func (t *Tokens) Main() Style {
	return func(el ui.Element) ui.Element {
		return el.Grow(1).Background(t.BgBase)
	}
}

// BrandRow is the top row of the sidebar: logo + wordmark.
func (t *Tokens) BrandRow() Style {
	return func(el ui.Element) ui.Element {
		return el.Height(64).PaddingX(Space4).Gap(Space2)
	}
}

// NavRow is a sidebar navigation row.
func (t *Tokens) NavRow(active bool) Style {
	return func(el ui.Element) ui.Element {
		el = el.Height(36).PaddingX(Space3).Radius(RadiusSM)
		if active {
			return el.Background(t.BgHover)
		}
		return el
	}
}

// SectionLabel is a small uppercase sidebar section title.
func (t *Tokens) SectionLabel() Style {
	return func(el ui.Element) ui.Element {
		return Apply(el, t.Font(TypeXXXS), func(e ui.Element) ui.Element {
			return e.TextColor(t.LabelTertiary).PaddingX(Space2)
		})
	}
}

// Row is a list row (session, plugin, task).
func (t *Tokens) Row(state RowState) Style {
	return func(el ui.Element) ui.Element {
		el = el.Height(40).PaddingX(Space3).Radius(RadiusSM)
		if state != RowNormal {
			el = el.Background(t.BgHover)
		}
		return el
	}
}

// Panel is a bordered surface (cards, composer, dialogs).
func (t *Tokens) Panel() Style {
	return func(el ui.Element) ui.Element {
		return el.Background(t.BgRaised).Border(1, t.BorderL2).Radius(RadiusLG)
	}
}

// Composer is the message input box (baseline: 868 wide, 116 tall, radius 16-20).
func (t *Tokens) Composer() Style {
	return func(el ui.Element) ui.Element {
		return Apply(el, t.Panel(), func(e ui.Element) ui.Element {
			return e.Width(868).Radius(RadiusXL).Shadow(0, 2, 4, 0, ui.RGBA(0, 0, 0, 0.05))
		})
	}
}

// PrimaryButton is the baseline "New Session" button: the BgStrong surface,
// a 1px BorderL3 edge, the readable LabelPrimary foreground, 38px tall,
// 12px radius. BgStrong is white in Light and #43454A in Dark, so OnPrimary
// (white/black) was invisible on the light fill; LabelPrimary contrasts with
// BgStrong in both themes (18.9:1 light, 9.2:1 dark).
func (t *Tokens) PrimaryButton() Style {
	return func(el ui.Element) ui.Element {
		return el.Height(38).PaddingX(Space4).Radius(RadiusMD).
			Background(t.BgStrong).Border(1, t.BorderL3).
			TextColor(t.LabelPrimary).FontWeight(500)
	}
}

// Tab is a Chat/Trajectory tab.
func (t *Tokens) Tab(active bool) Style {
	return func(el ui.Element) ui.Element {
		el = el.Height(25).PaddingX(Space2)
		if active {
			return el.TextColor(t.Accent)
		}
		return el.TextColor(t.LabelSecondary)
	}
}

// Badge is a small rounded label ("Preview", counts).
func (t *Tokens) Badge() Style {
	return func(el ui.Element) ui.Element {
		return el.PaddingX(Space2).PaddingY(Space1).Radius(RadiusMax).
			Background(t.BgHover).TextColor(t.LabelSecondary)
	}
}
