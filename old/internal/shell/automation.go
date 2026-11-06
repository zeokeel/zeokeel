package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
)

// The Automation page keeps the Plugins page's brand layout: a centered
// content column on the 4 DIP grid (no fixed 960 DIP reference), the brand
// page header and native controls throughout (docs/design/native-ui.md §6).
const (
	// automationColumnUnits is the centered content column: 220 units, 880
	// DIPs at the brand's Spacing=4.
	automationColumnUnits float32 = 220
	// automationRowSingleLineUnits is where a task row keeps its switch and
	// four actions on one line: 140 units, 560 DIPs. Below it the row stacks
	// them, so a 304 DIP page column beside the docked right sidebar still
	// shows every action.
	automationRowSingleLineUnits float32 = 140
)

// The Automation icons are ZeoKeel line art in currentColor, so the theme
// paints them and neither appearance carries a sampled hex value.
var (
	automationPlusIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><path d="M8 2.8v10.4M2.8 8h10.4"/></svg>`))

	automationClockIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><circle cx="8" cy="8" r="6.2"/><path d="M8 4.4V8l2.6 1.9"/></svg>`))
)

// automation draws the Automation tasks page from the live persisted task
// list on the brand's centered column.
func (a *App) automation(c *ui.Context, t *design.Tokens) {
	width := a.automationPageWidth(c)
	ui.Column(c).Grow(1).Background(t.BgBase).Children(func() {
		ui.Column(c).Width(width).Margin(0, ui.Auto).Grow(1).Children(func() {
			a.automationLivePage(c, t)
		})
	})
}

// automationPageWidth is the page column: the shared brand column helper,
// clamped by the main area's width and the brand inset.
func (a *App) automationPageWidth(c *ui.Context) float32 {
	return a.nativePageColumn(c, automationColumnUnits)
}
