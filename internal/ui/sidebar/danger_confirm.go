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
// taking the keyboard and, on every close, giving it back to the row menu button
// that opened it (`trigger`, the handle bound to that button). It is
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
// pending target. Both callbacks run on the UI thread during a frame. errText,
// when non-empty, renders an inline error line above the actions: a failed
// confirm can reopen the dialog with the failure named instead of closing on a
// false success (2026-11 audit UX-10).
func buildDangerConfirm(c *ui.Context, t *design.Tokens, open *bool,
	title, message, errText, confirmLabel, cancelLabel string,
	trigger *ui.Handle, onConfirm, onClose func()) {
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
		panel := ui.Column(c.Key("sidebar.danger-confirm")).Width(dangerConfirmWidth).Gap(design.Space3)
		// The safe action takes the keyboard from the dialog's second frame, not
		// its first (the uikit.ConfirmDanger two-frame rule): mygo records the
		// element that had the focus as an overlay opens, and a confirmation that
		// opens from inside another modal (the workspace switch dialog) would
		// otherwise open with NO focused action, so Enter would run whichever
		// button mygo finds first instead of Cancel (2026-11: the workspace
		// switch → delete flow deleted on a stray Enter).
		settled := ui.Local(panel, "settled", func() bool { return false })
		keyboard := ui.Local(panel, "keyboard", func() bool { return false })
		panel.Children(func() {
			design.Apply(ui.Text(c, title), t.Font(design.TypeM)).FontWeight(600).
				TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, message), t.Text(design.TypeS)).
				TextColor(t.LabelSecondary).MaxLines(4)
			if errText != "" {
				uikit.ErrorLine(c, errText)
			}
			takeKeyboard := *settled && !*keyboard
			*keyboard = *keyboard || takeKeyboard
			uikit.DialogFooter(c, []uikit.DialogAction{
				// Danger first (left), the safe action last and focused, as
				// design.DialogFooter orders them. No action is Primary: the
				// canonical action of a destructive confirmation is Cancel.
				{Label: confirmLabel, Danger: true, OnClick: closeDialog(true)},
				{Label: cancelLabel, Default: true, Focus: takeKeyboard, OnClick: closeDialog(false)},
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
	// The dialog hands the keyboard back to the control that opened it (the row's
	// ⋯ menu button), as every other overlay in the product does: closing with
	// Escape or Enter leaves the user where the action started instead of at the
	// top of the window (A4 interaction audit, focus-return).
	if trigger != nil {
		trigger.Focus(c)
	}
}
