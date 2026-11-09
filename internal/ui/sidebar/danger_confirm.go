package sidebar

// buildDangerConfirm is the product's one destructive-confirmation dialog. The
// safe action is the default: the dialog opens with the keyboard on Cancel, so
// Enter (the key users press right after choosing a row action) cancels, Escape
// and a click outside cancel, and only an explicit click on the danger action
// runs onConfirm. It replaces ui.AlertDialog for irreversible actions because
// the native alert makes its *last* button the default and the focused one
// (docs/ui/alert-dialog.md), which would delete on a stray Enter.
//
// The dialog follows the product's dialog convention (uikit.DialogFooter): the
// danger action on the left, the cancel action last (rightmost), the modal
// taking the keyboard and giving it back to the row menu that opened it. It is
// rendered on the window-level overlay slot (buildFooter / workspace overlay),
// so it cannot be clipped by the sidebar.

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// dangerConfirmWidth is the compact dialog width token: a destructive
// confirmation holds one sentence and two actions, not a form.
const dangerConfirmWidth = design.DialogWidthS

// buildDangerConfirm shows the confirmation while *open is true. onConfirm runs
// only for the danger action; onClose runs for every close (confirm, cancel,
// Escape, click outside) after the dialog closed, so the caller can drop the
// pending target. Both callbacks run on the UI thread during a frame.
func buildDangerConfirm(c *ui.Context, t *design.Tokens, open *bool,
	title, message, confirmLabel, cancelLabel string,
	onConfirm, onClose func()) {
	if !*open {
		return
	}
	confirmed := false
	closeDialog := func(confirm bool) func() {
		return func() {
			confirmed = confirm
			*open = false
		}
	}
	ui.Modal(c, open, func() {
		ui.Column(c).Width(dangerConfirmWidth).Gap(design.Space3).Children(func() {
			design.Apply(ui.Text(c, title), t.Font(design.TypeM)).FontWeight(600).
				TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, message), t.Text(design.TypeS)).
				TextColor(t.LabelSecondary).MaxLines(4)
			uikit.DialogFooter(c, []uikit.DialogAction{
				// Danger first (left), the safe action last and focused, as
				// design.DialogFooter orders them. No action is Primary: the
				// canonical action of a destructive confirmation is Cancel.
				{Label: confirmLabel, Danger: true, OnClick: closeDialog(true)},
				{Label: cancelLabel, Default: true, OnClick: closeDialog(false)},
			})
		})
	})
	if *open {
		return
	}
	// Modal closed by its own Escape/backdrop handling (which sets *open false
	// without running an action) or by one of the buttons above.
	if confirmed && onConfirm != nil {
		onConfirm()
	}
	if onClose != nil {
		onClose()
	}
}
