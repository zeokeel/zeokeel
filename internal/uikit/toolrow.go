package uikit

// The compact transcript tool row: the quiet anatomy shared by the registered
// tool-card renderers (internal/ui/toolcards) and the session screen's fallback
// for tools without a renderer. A transcript is mostly prose; a tool call is one
// muted line (icon, name, status, one-line preview, small disclosure), not a
// panel. The trajectory pane keeps its detailed cards for inspection.

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// ToolRowOptions is one compact transcript tool row.
type ToolRowOptions struct {
	// Key is the row's stable identity (one per tool call).
	Key string
	// Icon is the tool's category glyph; nil falls back to the message glyph.
	Icon *ui.SVG
	// Name is the tool name, muted and single-line.
	Name string
	// Status is the localized status label ("完成"/"Completed"); empty hides it.
	Status string
	// StatusColor is the WCAG-AA status text color (success/warning/danger).
	StatusColor ui.Color
	// Preview is the collapsed one-line summary excerpt (already normalized to
	// one line); empty keeps the status at the row end.
	Preview string
	// DetailLabel is the disclosure's accessible label/tooltip (详情/Details).
	DetailLabel string
	// Open is the current disclosure state; Toggle receives the next one (the
	// caller stores it and requests a frame).
	Open   bool
	Toggle func(open bool)
	// Detail builds the expanded body (full summary); nil hides the disclosure.
	Detail func(c *ui.Context)
}

// ToolRow draws one compact transcript tool call. It has no panel background or
// padding: a muted category icon, the muted tool name, a small colored status
// label, the one-line preview and the disclosure glyph all share one line; the
// full summary reads below it when expanded.
func ToolRow(c *ui.Context, o ToolRowOptions) {
	t := design.Of(c)
	th := t.Theme()
	ui.Column(c.Key(o.Key)).FillWidth().Gap(design.Space1).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
			PaddingY(design.Space1).Children(func() {
			icon := o.Icon
			if icon == nil {
				icon = icons.Message()
			}
			ui.Icon(c, icon).Size(design.Space3, design.Space3).TextColor(t.LabelTertiary)
			design.Apply(ui.Text(c, o.Name), t.Font(design.TypeXXS)).
				TextColor(t.LabelSecondary).MaxLines(1).Ellipsis("…")
			if o.Status != "" {
				design.Apply(ui.Text(c, o.Status), t.Font(design.TypeXXXS)).
					TextColor(o.StatusColor).NoWrap()
			}
			if o.Preview != "" {
				design.Apply(ui.Text(c, o.Preview), t.Font(design.TypeXXXS)).
					TextColor(t.LabelTertiary).Grow(1).MaxLines(1).Ellipsis("…")
			} else {
				ui.Spacer(c).Grow(1)
			}
			if o.Detail != nil {
				disclosure := ui.Button(c.Key(o.Key+".detail"), "").
					Label(o.DetailLabel).Tooltip(o.DetailLabel).
					Size(th.Space(5), th.Space(5)).PaddingX(0).Radius(design.RadiusSM)
				disclosure.Children(func() {
					chevron := icons.ChevronRight()
					if o.Open {
						chevron = icons.ChevronDown()
					}
					ui.Icon(c, chevron).Size(design.Space3, design.Space3).TextColor(t.LabelTertiary)
				})
				if disclosure.Clicked() && o.Toggle != nil {
					o.Toggle(!o.Open)
				}
			}
		})
		if o.Open && o.Detail != nil {
			o.Detail(c)
		}
	})
}

// ToolPreview turns a tool summary into a single-line collapsed preview: line
// breaks and runs of whitespace collapse to one space, and the text is cut to
// max runes with an ellipsis.
func ToolPreview(summary string, max int) string {
	trimmed := ""
	space := false
	for _, r := range summary {
		if r == '\n' || r == '\r' || r == '\t' || r == ' ' {
			space = trimmed != ""
			continue
		}
		if space {
			trimmed += " "
			space = false
		}
		trimmed += string(r)
	}
	runes := []rune(trimmed)
	if max <= 0 || len(runes) <= max {
		return trimmed
	}
	return string(runes[:max]) + "…"
}
