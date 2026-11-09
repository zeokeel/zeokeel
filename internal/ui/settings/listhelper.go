package settings

// Shared helpers of the settings pages' migrated collections and loading
// states. The pages keep their own layout and copy; these helpers wire the
// official mygo controls (ui.List, ui.Spinner) the same way everywhere.

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
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
