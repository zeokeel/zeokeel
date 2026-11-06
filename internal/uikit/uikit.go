// Package uikit is ZeoKeel's shared UI library: small compositions of mygo's
// own native controls (ui.Text, ui.Row, ui.Column, ui.Divider, ui.Spacer). It
// never hand-draws a control; the point is that every module gets the same
// page/section/row look without copying layout code.
//
// Build helpers take the frame's *ui.Context and are only valid inside a build
// callback (see docs/ARCHITECTURE.md §4/§9): they must build, not act.
package uikit

import (
	"github.com/egoist/mygo/ui"
)

// PageHeader draws a page title with an optional muted subtitle. This is the
// first block of a screen or settings page body; the host's screen header slot
// stays the place for actions.
func PageHeader(c *ui.Context, title, subtitle string) {
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		ui.Text(c, title).Bold().FontSize(22)
		if subtitle != "" {
			ui.Text(c, subtitle).FontSize(13).TextColor(c.Theme().TextMuted)
		}
	})
}

// Section draws one titled body block. body may be nil for a title-only
// section (a divider rule is still drawn).
func Section(c *ui.Context, title string, body func(body *ui.Context)) {
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, title).Bold().FontSize(15)
		ui.Divider(c)
		if body != nil {
			body(c)
		}
	})
}

// Bullet draws one bullet list row: a muted bullet glyph and the text.
func Bullet(c *ui.Context, text string) {
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "•").FontSize(13).TextColor(c.Theme().TextMuted)
		ui.Text(c, text).FontSize(13)
	})
}

// KeyValue is one row of KeyValueRows.
type KeyValue struct {
	Key   string
	Value string
}

// KeyValueRows draws label/value rows with the label muted on the left and the
// value right-aligned. It is the read-only "property sheet" pattern used by
// the About page and later by workspace/session details.
func KeyValueRows(c *ui.Context, rows []KeyValue) {
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		for _, row := range rows {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, row.Key).FontSize(13).TextColor(c.Theme().TextMuted)
				ui.Spacer(c).Grow(1)
				ui.Text(c, row.Value).FontSize(13)
			})
		}
	})
}

// EmptyState is the muted placeholder a module shows when an injection point
// has no contribution yet (home regions, empty settings surfaces).
func EmptyState(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(13).TextColor(c.Theme().TextMuted)
}
