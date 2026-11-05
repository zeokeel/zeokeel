package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// The automation page's measurements, from the DSH baseline's computed
// styles (baseline/css/03-automation.json): a 960-wide content column,
// 48px above and below the empty state, and the page heading's 32px
// action row under 28px of top padding.
const (
	automationContentWidth float32 = 960
	automationGutter       float32 = 8 // the baseline's scrollbar gutter

	automationHeadingPadTop float32 = 28
	automationHeadingHeight float32 = 60
	automationHeadingGap    float32 = 24

	automationTabHeight float32 = 28
	automationTabGap    float32 = 12
	automationTabPadX   float32 = 10
	automationFiltersMB float32 = 14

	automationSearchHeight float32 = 38
	automationSearchPadX   float32 = 10
	automationSearchGap    float32 = 6
	automationSearchMB     float32 = 16

	automationEmptyPadY float32 = 48
	automationEmptyPadX float32 = 20

	// automationRowSingleLineW is where a task row keeps its switch and four
	// actions on one line. Below it the row stacks them, so a 304px page
	// column beside the docked right sidebar still shows every action.
	automationRowSingleLineW float32 = 560
)

// The icons of the automation page, drawn as the baseline's line art.
var (
	automationPlusIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 13 13">
		<path d="M6.5 2V11M2 6.5H11" fill="none" stroke="#FFFFFF" stroke-width="1"/>
	</svg>`))
	automationSearchIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 14 14">
		<circle cx="5.5" cy="5.5" r="4" fill="none" stroke="#81858C" stroke-width="0.9"/>
		<path d="M8.7 8.7L12.5 12.5" fill="none" stroke="#81858C" stroke-width="0.9" stroke-linecap="round"/>
	</svg>`))
	automationClockIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">
		<circle cx="12" cy="12" r="9" fill="none" stroke="#ADB2B8" stroke-width="1.5"/>
		<path d="M12 6V12L16 15" fill="none" stroke="#ADB2B8" stroke-width="1.6" stroke-linecap="round"/>
	</svg>`))
)

// automation draws the Automation tasks page: heading, filters, search and
// either the baseline empty state (the static App.Ctrl == nil path) or the
// live persisted task list.
func (a *App) automation(c *ui.Context, t *design.Tokens) {
	width := a.automationPageWidth(c)
	// At 1600x1000 the baseline reserves the 8px scrollbar gutter; the
	// narrower 48px-inset column is centered in the full content width so the
	// heading lands at DSH's x=328 at 1200x800.
	gutter := automationGutter
	if width < automationContentWidth {
		gutter = 0
	}
	ui.Column(c).Grow(1).Background(t.BgBase).Padding(0, gutter, 0, 0).Children(func() {
		ui.Column(c).Width(width).
			Margin(0, ui.Auto).Grow(1).Children(func() {
			if a.Ctrl != nil {
				a.automationLivePage(c, t)
				return
			}
			a.automationHeading(c, t)
			a.automationFilters(c, t)
			a.automationSearch(c, t)
			a.automationEmpty(c, t)
		})
	})
}

// automationPageWidth is the centered content column: min(960,
// contentWidth-96), DSH's 824px column with 48px side insets at 1200x800.
func (a *App) automationPageWidth(c *ui.Context) float32 {
	return a.pageColumnWidth(c, automationContentWidth)
}

// automationHeading is the h1 and the dark "New" action at the page's top.
func (a *App) automationHeading(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Height(automationHeadingHeight).Label(a.tr("Automation page heading", "自动化页面标题")).
		Padding(automationHeadingPadTop, 0, 0, 0).
		AlignItems(ui.Center).Justify(ui.SpaceBetween).
		Margin(0, 0, automationHeadingGap, 0).Children(func() {
		ui.Text(c, a.T("automation.automation_tasks")).FontSize(20).FontWeight(500).LineHeight(1.4).
			TextColor(t.LabelPrimary)
		ui.Row(c).Height(32).PaddingX(12).Gap(4).Radius(16).
			AlignItems(ui.Center).Background(t.LabelPrimary).Children(func() {
			ui.Row(c).Size(16, 16).Center().Children(func() {
				ui.Icon(c, automationPlusIcon).Size(13, 13).
					TextColor(t.OnPrimary)
			})
			ui.Text(c, a.T("automation.new")).FontSize(13).LineHeight(20.0 / 13.0).
				TextColor(t.OnPrimary)
		})
	})
}

// automationFilters is the All / Enabled / Inactive tab row; All starts
// selected, tinted as the baseline's active filter.
func (a *App) automationFilters(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Height(automationTabHeight).Gap(automationTabGap).
		AlignItems(ui.Center).Margin(0, 0, automationFiltersMB, 0).Children(func() {
		a.automationFilterTab(c, t, a.T("automation.all"), true)
		a.automationFilterTab(c, t, a.T("automation.enabled"), false)
		a.automationFilterTab(c, t, a.T("automation.inactive"), false)
	})
}

func (a *App) automationFilterTab(c *ui.Context, t *design.Tokens, label string, active bool) {
	tab := ui.Row(c).Height(automationTabHeight).PaddingX(automationTabPadX).
		Radius(14).AlignItems(ui.Center)
	if active {
		tab = tab.Background(t.BgHover)
	}
	tab.Children(func() {
		text := ui.Text(c, label).FontSize(14).LineHeight(22.0 / 14.0).Top(1)
		if active {
			text = text.TextColor(t.LabelPrimary)
		} else {
			text = text.TextColor(t.LabelTertiary)
		}
	})
}

// automationSearch is the rounded search field with its magnifier and
// placeholder.
func (a *App) automationSearch(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Height(automationSearchHeight).Label(a.T("automation.search")).
		PaddingX(automationSearchPadX).Gap(automationSearchGap).
		Border(1, t.BorderL3).Radius(design.RadiusMD).
		AlignItems(ui.Center).Margin(0, 0, automationSearchMB, 0).Children(func() {
		ui.Row(c).Size(16, 16).Center().Children(func() {
			ui.Icon(c, automationSearchIcon).Size(14, 14).
				TextColor(t.LabelTertiary)
		})
		ui.Text(c, a.T("automation.search")).FontSize(14).LineHeight(22.0 / 14.0).Top(1).
			TextColor(t.LabelTertiary)
	})
}

// automationEmpty is the centered "no tasks" state: a clock icon, a hint
// and the outline action that creates the first task.
func (a *App) automationEmpty(c *ui.Context, t *design.Tokens) {
	ui.Column(c).Padding(automationEmptyPadY, automationEmptyPadX).
		AlignItems(ui.Center).Children(func() {
		ui.Icon(c, automationClockIcon).Size(24, 24).
			TextColor(ui.Hex("#ADB2B8")).Margin(0, 0, 12, 0)
		ui.Text(c, a.T("automation.empty")).
			FontSize(14).LineHeight(22.0/14.0).Top(0.5).
			// The baseline's text stack gives this line 402px; the system
			// rasterizer here gives 398, so a hair of tracking closes it.
			LetterSpacing(0.05).
			TextColor(t.LabelTertiary).Margin(0, 0, 8, 0)
		ui.Row(c).Height(36).PaddingX(14).Radius(design.RadiusMD).
			Border(1, t.BorderL3).AlignItems(ui.Center).Justify(ui.Center).
			Background(t.BgBase).Margin(16, 0, 0, 0).Children(func() {
			ui.Text(c, a.T("automation.new_automation_task")).FontSize(14).LineHeight(22.0 / 14.0).Top(1).
				TextColor(t.LabelPrimary)
		})
	})
}
