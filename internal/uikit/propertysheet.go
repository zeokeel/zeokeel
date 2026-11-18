package uikit

// The product's property sheet (DESIGN.md §5.2 PropertySheet): one read-only
// label/value table shape shared by every page that lists properties. The label
// column is a fixed 160 DIP, so a page's labels line up in one column instead of
// every value floating to the right edge; the value sits left-aligned beside it
// on a 32 DIP row, a hairline divides the rows, and the value is selectable so
// an identifier, a path or a version can be copied out of it.
//
// The About, Diagnostics, Network, Update, Context and MCP-detail pages draw
// this sheet; none of them keeps its own label column, and the older
// right-aligned label/value form is gone rather than lingering beside it.

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

const (
	// PropertySheetLabelWidth is the fixed label column: 120 DIP on the 4 DIP
	// grid. It fits the product's longest label, so the value column starts at the
	// same x on every page and on every row — and the row stops reading as a
	// two-column table with a wide empty gutter between a short label and its
	// value (VD-37).
	PropertySheetLabelWidth float32 = design.Space8*4 - design.Space2
	// PropertySheetDefaultMaxWidth bounds a sheet so a label and its value stay
	// one glance apart instead of spanning a wide page: 560 DIP, the medium
	// form measure. A dense page passes a narrower MaxWidth.
	PropertySheetDefaultMaxWidth float32 = design.DialogWidthM
	// PropertySheetPlaceholder is the muted mark of a row whose value is not
	// known. It is not an invented value: the row stays honest about the gap.
	PropertySheetPlaceholder = "—"
)

// PropertyRow is one label/value row of a PropertySheet.
type PropertyRow struct {
	// Label is the row's name, drawn muted at the row's left; it is also the
	// row's accessible name.
	Label string
	// Value is the row's content, drawn left-aligned and selectable.
	Value string
	// Placeholder replaces the copy when Value is empty; an empty Placeholder
	// falls back to PropertySheetPlaceholder.
	Placeholder string
	// Mono draws the value in the mono family: versions, ids, paths and hashes
	// read as data rather than prose.
	Mono bool
	// Tooltip overrides the value's tooltip; an empty Tooltip uses Value. It is
	// for a value the row may ellipsize.
	Tooltip string
	// Copy adds a copy control after the value, so a path or an id can go to
	// the clipboard without a selection drag. A placeholder value gets no copy
	// control: there is nothing to copy.
	Copy bool
	// CopyLabel names the copy control for assistive technology; an empty
	// label uses the localized default ("复制"/"Copy").
	CopyLabel string
}

// PropertySheetOptions is one property sheet.
type PropertySheetOptions struct {
	// Rows are the label/value rows, in display order.
	Rows []PropertyRow
	// MaxWidth bounds the sheet; zero uses PropertySheetDefaultMaxWidth. Ignored
	// when FullWidth is set.
	MaxWidth float32
	// FullWidth spans the sheet and its row dividers across the parent's full
	// width, matching the enclosing section's divider, instead of the bounded
	// default measure (the Context page's kv dividers stopped at the value
	// column's right edge while the section divider ran full width — 2026-11 UI
	// review B4). The label column stays fixed and the value left-aligned, so the
	// value remains one glance from the label even on a wide sheet.
	FullWidth bool
	// Key namespaces the copy controls when a page shows more than one sheet:
	// the same row label in two sheets must not share one widget identity.
	Key string
	// Locale localizes the default copy label; nil falls back to Chinese.
	Locale contracts.LocaleService
}

// PropertySheet draws one property sheet: the fixed 160 DIP label column, the
// left-aligned value on a 32 DIP row and a divider between rows. The rows are
// read-only; a caller that needs an action row puts it below the sheet.
func PropertySheet(c *ui.Context, o PropertySheetOptions) {
	if c == nil || len(o.Rows) == 0 {
		return
	}
	t := design.Of(c)
	maxWidth := o.MaxWidth
	if maxWidth <= 0 {
		maxWidth = PropertySheetDefaultMaxWidth
	}
	key := o.Key
	if key == "" {
		key = "uikit.property-sheet"
	}
	// The row rules are declared once and drawn between rows (never above the
	// first or below the last), which is why the sheet needs a small gap for the
	// line to sit in rather than a Divider in the loop.
	sheet := ui.Column(c).FillWidth().Gap(design.Space1)
	if !o.FullWidth {
		sheet = sheet.MaxWidth(maxWidth)
	}
	// One declaration of the row rule instead of a Divider inside the loop: the
	// element draws a 1 DIP BorderL2 line between its children, which is the
	// sheet's hairline row for row (A1 matrix: Element.Dividers).
	sheet = sheet.Dividers(1, t.BorderL2)
	sheet.Children(func() {
		for _, row := range o.Rows {
			row := row
			ui.Row(c).FillWidth().Height(design.RowCompactHeight).
				AlignItems(ui.Center).Gap(design.Space3).Children(func() {
				design.Apply(ui.Text(c, row.Label), t.TextMuted(design.TypeXS)).
					Width(PropertySheetLabelWidth).Shrink(0).
					MaxLines(1).Ellipsis("…").Description(row.Label)
				value := row.Value
				empty := strings.TrimSpace(value) == ""
				if empty {
					value = row.Placeholder
					if value == "" {
						value = PropertySheetPlaceholder
					}
				}
				valueStyle := t.Font(design.TypeXS)
				if row.Mono {
					valueStyle = t.MonoText(design.TypeXS)
				}
				valueElement := design.Apply(ui.Text(c, value), valueStyle).
					TextColor(t.LabelPrimary).Grow(1).Shrink(1).
					MaxLines(1).Ellipsis("…")
				if empty {
					// The placeholder is not the property's value: it stays
					// quiet and is not selectable (nothing to copy).
					valueElement = valueElement.TextColor(t.LabelTertiary)
				} else {
					tooltip := row.Tooltip
					if tooltip == "" {
						tooltip = row.Value
					}
					valueElement = valueElement.Selectable().Description(tooltip)
				}
				if row.Copy && !empty {
					label := row.CopyLabel
					if label == "" {
						label = T(o.Locale, "复制", "Copy")
					}
					IconButton(c, IconButtonOptions{
						Key:     key + ".copy." + row.Label,
						Label:   label,
						Tooltip: label,
						Icon:    icons.Copy(),
						Size:    design.IconSizeS,
						OnClick: func() { c.WriteClipboard(row.Value) },
					})
				}
			})
		}
	})
}
