package settings

// Shared helpers of the settings pages' migrated collections and loading
// states. The pages keep their own layout and copy; these helpers wire the
// official mygo controls (ui.List, ui.Spinner) the same way everywhere.

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// bindSettingsList wires one settings-page collection to the official ui.List:
// a stable key per item, the accessible row label and single-row selection so
// the keyboard can move through the collection. The row actions stay the
// existing inline buttons (edit/delete/remove/move) — a click on the row body
// only chooses it; no new row action is invented.
func bindSettingsList(state *ui.ListState, selected *int, count int, key func(row int) any, label func(row int) string) {
	// The List remaps the choice by key as rows come and go (a filtered or
	// reordered collection keeps the chosen item), and drops it when the item
	// is gone; only an empty collection needs the index reset here.
	if count == 0 {
		*selected = -1
	}
	state.Key = key
	state.Label = label
	state.Selected = selected
}

// listRowTextColors returns the text colors of one official List row: the
// chosen row paints on the List's accent fill, so its copy uses the accent's
// on-color; the unchosen row keeps the page's label colors. It keeps a
// selected settings row readable instead of dark-on-blue.
func listRowTextColors(t *design.Tokens, selected bool) (primary, secondary, tertiary ui.Color) {
	if selected {
		return t.AccentText, t.AccentText, t.AccentText
	}
	return t.LabelPrimary, t.LabelSecondary, t.LabelTertiary
}

// buildBusySpinner adds the official spinner with an accessible progress name
// to the current row while an async settings action runs. The action's own
// button keeps its existing "…" label and disabled logic; the backends report no
// progress, so the pages use no ui.Progress and fake no percentage.
func buildBusySpinner(c *ui.Context, busy bool, label string) {
	if !busy {
		return
	}
	ui.Spinner(c).Label(label)
}

// buildBusyPrimaryAction is one right-aligned primary page action with the busy
// spinner beside it while the async work runs. It is the loading-aware form of
// the single-action DialogFooter row the pages already rendered: same look,
// alignment and click semantics, plus the visible progress indicator named by
// busyLabel for assistive technology.
func buildBusyPrimaryAction(c *ui.Context, t *design.Tokens, busy bool,
	label, busyLabel string, disabled bool, onClick func()) {
	ui.Row(c).FillWidth().Justify(ui.End).AlignItems(ui.Center).
		Gap(design.Space2).Children(func() {
		buildBusySpinner(c, busy, busyLabel)
		button := design.Apply(ui.PrimaryButton(c, label), t.PrimaryButton())
		button.Disabled(disabled)
		if button.Clicked() && !disabled && onClick != nil {
			onClick()
		}
	})
}

// buildSegmentedControl renders the product's themed segmented control: mygo's
// ui.SegmentedBase (radio group, arrow keys, Changed) with an explicit track and
// segment look. The stock ui.Segmented paints the chosen segment as a white
// surface with a 1px near-white border, which is ~1.3:1 against the track and
// reads as "no selection" (audit V14); the chosen segment here is the raised
// surface with a visible border and 600 weight on the track's hover tint, and
// the others stay secondary text. It returns the control's Changed.
func buildSegmentedControl(c *ui.Context, t *design.Tokens, selected *int,
	label string, labels ...string) bool {
	seg := ui.SegmentedBase(c, selected, len(labels))
	seg.Track.Label(label).Height(design.ControlHeight).Padding(design.Space1).
		Radius(design.RadiusMD).Background(t.BgHover).Children(func() {
		for index, text := range labels {
			segment := seg.Segment(index).Height(design.ControlHeight - design.Space2).
				PaddingX(design.Space4).Radius(design.RadiusSM).Center().Label(text)
			if index == *selected {
				segment.Background(t.BgRaised).BorderWidth(1).BorderColor(t.BorderL3).
					FontWeight(design.WeightSemibold).TextColor(t.LabelPrimary)
			} else {
				segment.TextColor(t.LabelSecondary)
			}
			text := text
			segment.Children(func() {
				design.Apply(ui.Text(c, text), t.Font(design.TypeS)).MaxLines(1)
			})
		}
	})
	return seg.Track.Changed()
}

// buildPropertySheet draws the product's one property sheet (DESIGN.md §5.2
// PropertySheet): a fixed 160 DIP label column, the value left-aligned on a
// 32 DIP row, a divider between rows. The Diagnostics, Network and About pages
// use it instead of the right-aligned rows the old shared helper drew.
func buildPropertySheet(c *ui.Context, t *design.Tokens, rows []uikit.KeyValue) {
	ui.Column(c).FillWidth().Gap(design.Space0).Children(func() {
		for index, row := range rows {
			if index > 0 {
				ui.Divider(c)
			}
			ui.Row(c).FillWidth().Height(design.Space8).
				AlignItems(ui.Center).Gap(design.Space3).Children(func() {
				design.Apply(ui.Text(c, row.Key), t.TextMuted(design.TypeXS)).
					Width(design.Space8 * 5).Shrink(0)
				design.Apply(ui.Text(c, row.Value), t.Font(design.TypeXS)).
					TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…").
					Tooltip(row.Value).Selectable()
			})
		}
	})
}
