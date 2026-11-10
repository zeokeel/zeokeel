package uikit

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// OnAccentBadge renders a compact badge that stays readable on a List row's
// accent selection fill.
//
// A selected List row is painted in the accent color and its own text switches
// to Tokens.AccentText (DESIGN.md §5: an on-accent foreground). A badge that
// keeps the resting Token colors (Tokens.Badge: BgHover fill + LabelSecondary
// text) then lands dim-on-accent: the audit measured about 1.2:1 in the dark
// theme. The product's status vocabulary is never carried by color alone
// (DESIGN.md §5.2: the label is mandatory copy), so on a selected row the
// badge inverts instead of tinting: Tokens.AccentText fills it and the accent
// colors the text — readable in both themes, and still one pill shape.
//
// Use it only while the owning row is selected; the resting row keeps
// StatusBadge / design.Tokens.Badge() so its semantic status color remains.
func OnAccentBadge(c *ui.Context, label string, t *design.Tokens) ui.Element {
	return design.Apply(ui.Badge(c, label), func(el ui.Element) ui.Element {
		return el.Background(t.AccentText).TextColor(t.Accent)
	})
}
