package uikit

// The product's one keyboard key cap. A shortcut hint is only useful when it
// looks like a key and sits next to the action it triggers, so every menu row,
// command-palette row, tooltip and composer hint draws the same chip instead of
// a parenthesised string. mygo has no official key-cap widget; this is a thin
// composition of Text (DESIGN.md §7.1, registered as uikit.kbd).

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// Kbd draws one key cap, as "⌘K" or "Esc". The label is the key or the chord,
// already localized by the caller; it is read by assistive technology as the
// hint's text, so a caller that hides the chord from the visible label (a menu
// row showing only the action name) keeps the chord in its own Label.
func Kbd(c *ui.Context, t *design.Tokens, label string) ui.Element {
	return design.Apply(ui.Text(c, label), t.Kbd()).Margin(design.Space0, design.Space0, 1, design.Space0)
}

// KbdHint draws a key chord at the trailing edge of a row: a Spacer that pushes
// the caps right, then one cap per chord element. A multi-key chord ("⌘ ⇧ K")
// is passed as separate arguments so each key gets its own cap; a single key
// ("Esc") is one argument.
func KbdHint(c *ui.Context, t *design.Tokens, keys ...string) ui.Element {
	return ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(design.Space1).Children(func() {
		for _, key := range keys {
			Kbd(c, t, key)
		}
	})
}
