package uikit

// The product's property sheet (DESIGN.md §5.2 PropertySheet): one read-only
// label/value table shape shared by every page that lists properties. The label
// column is a fixed 160 DIP, so a page's labels line up in one column instead of
// every value floating to the right edge; the value sits left-aligned beside it
// on a 32 DIP row, a hairline divides the rows, and the value is selectable so
// an identifier, a path or a version can be copied out of it.
//
// The About, Diagnostics, Network, Update and Context pages draw this sheet;
// none of them keeps its own label column. KeyValueRows in uikit.go is the
// older, right-aligned form and is deprecated for new code.

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

const (
	// PropertySheetLabelWidth is the fixed label column: 160 DIP, the 32 DIP
	// grid's Space8 five times over. It fits the longest product label, so the
	// value column starts at the same x on every page and on every row.
	PropertySheetLabelWidth float32 = design.Space8 * 5
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
	// MaxWidth bounds the sheet; zero uses PropertySheetDefaultMaxWidth.
	MaxWidth float32
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
	ui.Column(c).FillWidth().MaxWidth(maxWidth).Gap(design.Space0).Children(func() {
		for index, row := range o.Rows {
			row := row
			if index > 0 {
				ui.Divider(c)
			}
			ui.Row(c).FillWidth().Height(design.RowCompactHeight).
				AlignItems(ui.Center).Gap(design.Space3).Children(func() {
				design.Apply(ui.Text(c, row.Label), t.TextMuted(design.TypeXS)).
					Width(PropertySheetLabelWidth).Shrink(0).
					MaxLines(1).Ellipsis("…").Tooltip(row.Label)
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
					valueElement = valueElement.Selectable().Tooltip(tooltip)
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
