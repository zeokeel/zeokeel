package uikit

// The product's reading column: the one centered measure the transcript, the
// Context pane and the composer dock build on. It is 960 DIP
// (design.TranscriptWidth), capped at 92% of the viewport so a narrow window
// keeps its margins, and it never grows: a Grow(1) column fills the content
// area, widens the text (~13% in the Context pane) and shifts its left edge
// (~64 DIP against the conversation), so switching tabs jumped the text column.
//
// The horizontal padding belongs to the measure: the column's content starts
// one Space4 inside the 960 DIP frame, and two surfaces that use this helper
// line up their text exactly. Callers own their vertical rhythm (a scroll
// container's padding, a List's PaddingY) and their Gap, which the helper keeps
// at Space5 — the transcript's row gap and the Context pane's section gap are
// the same rhythm.

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// ReadingColumn draws one block of content on the reading measure: a centered
// row holding the 960 DIP column (capped at 92% of the viewport), the shared
// horizontal padding and Space5 between the body's blocks. body builds the
// block's content; it runs inside the column, so the elements it builds are the
// column's children.
func ReadingColumn(c *ui.Context, body func(*ui.Context)) {
	if c == nil || body == nil {
		return
	}
	ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Start).Children(func() {
		ui.Column(c).Gap(design.Space5).PaddingX(design.Space4).
			Width(design.TranscriptWidth).MaxWidthPercent(92).Children(func() {
			body(c)
		})
	})
}
