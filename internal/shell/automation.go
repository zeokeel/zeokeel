package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// The automation page's layout measurements: a 960-wide content column,
// 48px above and below the empty state, and the page heading's action row
// under 28px of top padding.
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

// automation draws the Automation tasks page from the live persisted task
// list. The retired static baseline empty-state render was deleted (pixel
// parity retired 2026-11-05).
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
			a.automationLivePage(c, t)
		})
	})
}

// automationPageWidth is the centered content column: min(960,
// contentWidth-96), DSH's 824px column with 48px side insets at 1200x800.
func (a *App) automationPageWidth(c *ui.Context) float32 {
	return a.pageColumnWidth(c, automationContentWidth)
}
