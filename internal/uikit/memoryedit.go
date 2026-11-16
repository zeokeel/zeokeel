package uikit

// The shared memory-entry editor (2026-11 audit AR-17): the settings memory
// page and the session Memory tab each carried a private edit dialog with the
// same anatomy. This entry owns the modal/title/input/error/footer; the caller
// keeps the draft state, the scope semantics, the save validation and the
// success notification (page badge vs toast), so both surfaces keep their
// behavior while sharing one look.

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// MemoryEditOptions describes one memory-entry edit dialog.
type MemoryEditOptions struct {
	// Open is the caller-owned modal flag; Text the caller-owned draft.
	Open *bool
	Text *string
	// Locale localizes the fixed chrome (title/actions/input suffix); nil
	// keeps the Chinese product default.
	Locale contracts.LocaleService
	// FieldLabel is the visible field/input label ("编辑内容" / "记忆内容").
	FieldLabel string
	// Description is an optional muted line under the title (the session tab
	// explains that scope and creation time are kept).
	Description string
	// Multiline selects the settings page's text area over the session tab's
	// single-line input. Enter submits only in the single-line form; in the
	// text area Enter belongs to the draft (newline).
	Multiline bool
	// Error is the caller's inline validation/save error ("" hides the line).
	Error string
	// SaveDisabled disables the primary action (the session tab disables it
	// on an empty draft; the settings page validates on save instead).
	SaveDisabled bool
	// OnSave runs when Save is chosen (or Enter submits the single-line
	// form); OnCancel when Cancel is chosen. The caller closes the dialog,
	// clears its draft and reports the outcome.
	OnSave   func()
	OnCancel func()
}

// MemoryEditDialog renders the native editor for one memory entry: title,
// optional description, the labeled input, an inline error line and the
// Cancel/Save footer (Save primary, never Default — the input owns Enter).
func MemoryEditDialog(c *ui.Context, t *design.Tokens, o MemoryEditOptions) {
	ui.Modal(c, o.Open, func() {
		submits := !o.Multiline && o.OnSave != nil && !o.SaveDisabled
		if submits {
			// Inside the modal the shortcut lands on the overlay scope, so
			// Enter submits wherever the focus is; the focused input takes
			// Enter first through OnTextKey below (same action). A disabled
			// Save disables Enter too.
			c.OnShortcut(0, ui.KeyEnter, o.OnSave)
		}
		design.Apply(ui.Column(c).Width(design.DialogWidthM).Gap(design.Space4), func(el ui.Element) ui.Element {
			return el.PaddingX(design.Space6).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, T(o.Locale, "编辑记忆", "Edit memory")), t.Font(design.TypeL)).
				FontWeight(design.WeightSemibold).TextColor(t.LabelPrimary)
			if o.Description != "" {
				design.Apply(ui.Text(c, o.Description), t.TextMuted(design.TypeXS)).MaxLines(2)
			}
			if o.Multiline {
				ui.Field(c, o.FieldLabel, func() {
					ui.TextArea(c, o.Text).
						Label(o.FieldLabel + T(o.Locale, " 输入", " input")).
						FillWidth().MinHeight(design.Space6 * 4).AutoFocus()
				})
			} else {
				input := ui.TextInput(c, o.Text).Label(o.FieldLabel).FillWidth()
				if submits {
					save := o.OnSave
					input.OnTextKey(func(k ui.TextKey) bool {
						if k.Key != ui.KeyEnter || k.Mods != 0 {
							return false
						}
						save()
						return true
					})
				}
			}
			if o.Error != "" {
				ErrorLine(c, o.Error)
			}
			ui.Divider(c)
			DialogFooter(c, []DialogAction{
				{Label: T(o.Locale, "取消", "Cancel"), OnClick: o.OnCancel},
				{Label: T(o.Locale, "保存", "Save"), Primary: true, Disabled: o.SaveDisabled, OnClick: o.OnSave},
			})
		})
	})
}
