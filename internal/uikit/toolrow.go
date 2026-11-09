package uikit

// The compact transcript tool row: the quiet anatomy shared by the registered
// tool-card renderers (internal/ui/toolcards) and the session screen's fallback
// for tools without a renderer. A transcript is mostly prose; a tool call is one
// muted disclosure row (icon, friendly name, status, one-line preview, chevron),
// not a panel. The trajectory pane keeps its detailed cards for inspection.

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
	// Name is the friendly, localized tool name shown in the row.
	Name string
	// RawName is the protocol tool id: it is the row's tooltip and accessible
	// description only, never visible copy. Empty (or equal to Name) adds none.
	RawName string
	// Status is the localized status label ("完成"/"Completed"); empty hides it.
	Status string
	// StatusColor is the WCAG-AA status text color (success/warning/danger).
	StatusColor ui.Color
	// Running shows the official indeterminate-work spinner before the status
	// text for a call that is still running.
	Running bool
	// Preview is the collapsed one-line summary excerpt (already normalized to
	// one line); empty keeps the status at the row end.
	Preview string
	// Open is the current disclosure state; Toggle receives the next one (the
	// caller stores it and requests a frame).
	Open   bool
	Toggle func(open bool)
	// Detail builds the expanded body (full summary); nil keeps the row a plain
	// line with no disclosure.
	Detail func(c *ui.Context)
}

// ToolRow draws one compact transcript tool call as one official mygo
// disclosure (ui.CollapsibleBase): the whole row is the trigger, so assistive
// technology sees expanded/collapsed, Enter and Space work, and the panel grows
// into view. It has no panel background or card: a muted category icon, the
// friendly tool name, the status (with the official spinner while running), the
// one-line preview and the chevron share one muted line; the full summary reads
// below it when expanded.
func ToolRow(c *ui.Context, o ToolRowOptions) {
	t := design.Of(c)
	buildLine := func(c *ui.Context, progress float32, disclosure bool) {
		icon := o.Icon
		if icon == nil {
			icon = icons.Message()
		}
		ui.Icon(c, icon).Size(design.Space3, design.Space3).TextColor(t.LabelTertiary)
		design.Apply(ui.Text(c, o.Name), t.Font(design.TypeXXS)).
			TextColor(t.LabelSecondary).MaxLines(1).Ellipsis("…")
		if o.Running {
			ui.Spinner(c).Label(o.Status).Size(design.Space3, design.Space3).
				TextColor(o.StatusColor)
		}
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
		if disclosure {
			ui.Icon(c, icons.ChevronRight()).Size(design.Space3, design.Space3).
				TextColor(t.LabelTertiary).Rotate(90 * progress)
		}
	}

	// A call without a detail body has nothing to disclose: build one quiet
	// line instead of a trigger that opens an empty panel.
	if o.Detail == nil {
		ui.Row(c.Key(o.Key)).FillWidth().AlignItems(ui.Center).
			Gap(design.Space2).PaddingY(design.Space1).Children(func() {
			buildLine(c, 0, false)
		})
		return
	}

	open := o.Open
	parts := ui.CollapsibleBase(c.Key(o.Key), &open)
	// The disclosure is named by the friendly tool name and its status: the
	// status text and the spinner inside a disclosure button are not nodes of
	// their own for assistive technology, so the state travels in the name.
	label := o.Name
	if o.Status != "" {
		label = o.Name + " · " + o.Status
	}
	trigger := parts.Trigger.FillWidth().AlignItems(ui.Center).
		Gap(design.Space2).PaddingY(design.Space1).Radius(design.RadiusSM).
		Label(label)
	if o.RawName != "" && o.RawName != o.Name {
		trigger = trigger.Tooltip(o.RawName)
	}
	if parts.Trigger.Hovered() || parts.Trigger.Focused() {
		trigger.Background(t.BgHover)
	}
	trigger.Children(func() {
		buildLine(c, parts.Progress(), true)
	})
	parts.Panel(func() {
		design.Apply(ui.Column(c), t.Gap(design.Space1)).
			PaddingStart(design.Space3 + design.Space2).PaddingEnd(design.Space2).
			PaddingY(design.Space1).Children(func() {
			o.Detail(c)
		})
	})
	// The framework already flipped the bound open state before Toggle runs:
	// the caller only persists it (and keeps the transcript's place).
	if parts.Trigger.Changed() && o.Toggle != nil {
		o.Toggle(open)
	}
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
