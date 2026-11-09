package uikit

// The product's one icon-only button: a bare glyph in a square hit target of
// at least design.IconSizeL, named for assistive technology and, when it says
// more than the name, described by a Tooltip. mygo v0.8.3 has no official icon
// button (DESIGN.md §6.3/§7.4), so this entry component wraps ui.ButtonBase:
// the base owns the pointer, the keyboard (Enter and Space), the focus, the
// theme's focus ring and `Disabled`; this file owns the one product look for
// the hover, pressed, selected and danger states, the target size and the
// accessible name. The shell and the modules call IconButton instead of
// drawing a ButtonBase of their own (DESIGN.md §7.1), so hover/pressed cannot
// go missing on one of them again and the sizes stay on design.IconSize*.
//
// The glyph is decoration that assistive technology does not read: the Label
// names the button. A Tooltip is optional: the shell's drawer close passes
// none because a keyboard-raised tooltip is an overlay with its own Escape
// handler, and it would take the first Escape before the drawer's own handler.

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// IconButtonVariant is the look of one icon button; the zero value is the
// quiet ghost the product's chrome uses.
type IconButtonVariant int

const (
	// IconButtonGhost is a button with no fill at rest: the page shows through
	// until the pointer is on it or the keyboard focus reaches it (BgHover),
	// and a press or the selected state tints it (BgSelected). It is the
	// default look of the shell's and the inspectors' icon actions.
	IconButtonGhost IconButtonVariant = iota
	// IconButtonSubtle is a button that keeps a quiet fill at rest
	// (BgHover), for an icon action that must be visible without being
	// hovered, as inside a card or beside a field.
	IconButtonSubtle
	// IconButtonDanger is a destructive icon action: the glyph takes
	// DangerText, and the interaction tints stay neutral so the danger color
	// is never a background wash.
	IconButtonDanger
)

// IconButtonOptions is one icon-only button, as the shell (collapse/expand,
// rail destinations, pane closes), the sidebar (add workspace) and the
// inspector (close/refresh/copy/more) draw them.
type IconButtonOptions struct {
	// Key is the button's stable identity when it is built among changing
	// siblings (a loop of rail destinations, a row action); empty keeps the
	// positional identity.
	Key string
	// Label names the button for assistive technology; required, and the only
	// name a glyph-only button has.
	Label string
	// Tooltip is the tip shown when the pointer rests on the button or the
	// keyboard focus reaches it. Empty adds none (and no description for
	// assistive technology).
	Tooltip string
	// Icon is the glyph from internal/icons. Nil renders Content instead.
	Icon *ui.SVG
	// Content builds the button's inside for a control that shows something
	// other than an SVG icon: the rail's brand mark is a bitmap logo. It is
	// ignored when Icon is set.
	Content func(*ui.Context)
	// Size is the glyph's side: design.IconSizeS (16), M (20) or L (24). Zero
	// uses IconSizeM. The hit target adds one 4 DIP inset around the glyph and
	// is never smaller than IconSizeL (24 DIP).
	Size float32
	// Variant is the look; zero is IconButtonGhost.
	Variant IconButtonVariant
	// Selected reports and paints the selected state (BgSelected): a rail
	// destination for the current screen, a pane switch that is on.
	Selected bool
	// Toggle makes the button a destination that reads as a toggle for
	// assistive technology (RoleToggleButton), so Selected reads as pressed;
	// a plain button role drops that state (the rail's current screen).
	Toggle bool
	// Disabled takes neither clicks nor the keyboard focus (mygo dims it to
	// half opacity).
	Disabled bool
	// OnClick runs when the button is clicked, or pressed with Enter or Space
	// while it has the keyboard focus.
	OnClick func()
}

// IconButton draws one icon-only button with the product's one look and
// reports it to assistive technology as a button named by Label. It returns
// the element so a caller can attach the row menu or the conditional reveal
// that belongs to that button, as the session list's and the task card's own
// actions do (PopoverMenuButton, Opacity/IgnoreInput).
func IconButton(c *ui.Context, o IconButtonOptions) ui.Element {
	t := design.Of(c)
	glyph := o.Size
	if glyph <= 0 {
		glyph = design.IconSizeM
	}
	// One 4 DIP inset around the glyph, and never below the desktop's 24 DIP
	// hit target (design.IconSizeL): S=24, M=28, L=32.
	target := glyph + design.Space2
	if target < design.IconSizeL {
		target = design.IconSizeL
	}

	var button ui.Element
	if o.Key != "" {
		button = ui.ButtonBase(c.Key(o.Key))
	} else {
		button = ui.ButtonBase(c)
	}
	button = button.Size(target, target).Center().Radius(design.RadiusSM)
	if o.Toggle {
		// A destination of the shell's rail is a toggle-like control: only the
		// toggle role carries Selected's state to assistive technology as
		// "pressed" (accessibility.md: a button role drops it).
		button = button.Role(ui.RoleToggleButton)
	}
	button = button.Label(o.Label)
	if o.Toggle || o.Selected {
		button = button.AccessSelected(o.Selected)
	}
	if o.Tooltip != "" {
		button = button.Tooltip(o.Tooltip)
	}
	if o.Disabled {
		button = button.Disabled(true)
	}

	// The base rings the keyboard focus with the theme's ring; the fill tells
	// the states apart — pressed and selected use the stronger selection tint,
	// hover and keyboard focus the hover tint.
	switch {
	case o.Selected || button.Pressed():
		button = button.Background(t.BgSelected)
	case button.Hovered() || button.Focused():
		if o.Variant == IconButtonSubtle {
			button = button.Background(t.BgSelected)
		} else {
			button = button.Background(t.BgHover)
		}
	case o.Variant == IconButtonSubtle:
		button = button.Background(t.BgHover)
	}

	color := t.LabelSecondary
	if o.Variant == IconButtonDanger {
		color = t.DangerText
	}
	button.Children(func() {
		switch {
		case o.Icon != nil:
			ui.Icon(c, o.Icon).Size(glyph, glyph).TextColor(color)
		case o.Content != nil:
			o.Content(c)
		}
	})
	// Run the action in the pass that saw the click, as the hand-drawn buttons
	// did: the settings surface's close mutates the modal's open flag and reads
	// it right after the build, so mygo's deferred Element.OnClick (which runs
	// after the view returns) would leave that flag stale for the frame.
	if o.OnClick != nil && button.Clicked() {
		o.OnClick()
	}
	return button
}
