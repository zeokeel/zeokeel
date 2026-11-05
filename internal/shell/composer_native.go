package shell

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// Brand composer pieces shared by the Session and Home screens: one card, one
// native input, one send control and the same focus/hover feedback, so both
// composers paint from the brand theme without local copies drifting apart.
//
// The card owns the surface, the single soft shadow and the focus/hover
// border; the native TextArea inside keeps its placeholder, multiline editing,
// IME, undo, caret and focus semantics.

// brandComposerCard is the raised card around a composer: the brand surface,
// the theme corner, a 1 DIP border, one soft shadow and spacing on the 4 DIP
// grid.
func brandComposerCard(c *ui.Context, t *design.Tokens, width float32, label string) ui.Element {
	th := t.Theme()
	return design.Apply(ui.Column(c), t.Panel()).
		Width(width).
		Radius(th.Radius).
		Border(1, t.BorderL2).
		Shadow(0, 2, 8, 0, homeCardShadow(t)).
		Padding(th.Space(2)).
		Gap(th.Space(2)).
		Label(label)
}

// brandComposerInput is the native multiline input inside the card: mygo's
// TextArea with its own fill and border cleared, because the card carries the
// focus and hover feedback.
func brandComposerInput(c *ui.Context, t *design.Tokens, value *string, label, placeholder string,
	onKey func(k ui.TextKey) bool, ref *ui.Handle) ui.Element {
	th := t.Theme()
	return ui.TextArea(c, value).
		Label(label).
		Placeholder(placeholder).
		Background(ui.Transparent).
		Border(0, ui.Transparent).
		Padding(0, th.Space(1)).
		MinHeight(th.Space(9)).
		Grow(1).
		Bind(ref).
		OnTextKey(onKey)
}

// brandComposerFocus paints the card's border for the native input's hover and
// keyboard focus, since the input's own border is off inside the card.
func brandComposerFocus(card, input ui.Element, t *design.Tokens) ui.Element {
	switch {
	case input.Focused():
		return card.Border(1, t.Accent)
	case input.Hovered():
		return card.Border(1, t.BorderL3)
	}
	return card
}

// brandIconButton is a borderless brand icon button for the composer toolbar
// and screen headers: mygo's ButtonBase keeps the native focus, Enter/Space
// and disabled semantics, and the brand hover tint gives it a look.
func brandIconButton(c *ui.Context, t *design.Tokens, icon *ui.SVG, label string, onClick func()) ui.Element {
	th := t.Theme()
	button := ui.ButtonBase(c).Size(th.Space(7), th.Space(7)).Center().
		Radius(design.RadiusSM).Label(label).Tooltip(label)
	if button.Hovered() {
		button = button.Background(t.BgHover)
	}
	button.Children(func() {
		ui.Icon(c, icon).Size(th.Space(4), th.Space(4)).TextColor(t.LabelSecondary)
	})
	if onClick != nil {
		button.OnClick(onClick)
	}
	return button
}

// brandSendButton is a composer's primary control: a native ButtonBase (focus,
// Enter/Space and the disabled state) painted in the brand accent. The fill
// sits on an enabled parent so the disabled look is the brand idle tint
// (sendIdleFill) rather than the framework's 50% dim of the accent fill; the
// inner control is still the one that takes the focus and reports the click.
func brandSendButton(c *ui.Context, t *design.Tokens, label string, disabled, running bool, onClick func()) {
	th := t.Theme()
	fill, glyph := t.Accent, t.AccentText
	if disabled {
		fill, glyph = sendIdleFill(t), sendIdleGlyph(t)
	}
	wrap := ui.Row(c).Size(th.Space(9), th.Space(9)).Radius(th.Radius).
		Background(fill).Center()
	wrap.Children(func() {
		button := ui.ButtonBase(c).Size(th.Space(9), th.Space(9)).Center().
			Label(label).Tooltip(label).Disabled(disabled)
		button.Children(func() {
			if running {
				ui.Box(c).Size(th.Space(2.5), th.Space(2.5)).Radius(design.RadiusXS).
					Background(glyph)
				return
			}
			ui.Icon(c, homeIconArrowUp).Size(th.Space(4), th.Space(4)).TextColor(glyph)
		})
		switch {
		case disabled:
		case button.Pressed():
			wrap = wrap.Background(t.AccentPressed)
		case button.Hovered():
			wrap = wrap.Background(t.AccentHover)
		}
		if button.Clicked() && !disabled && onClick != nil {
			onClick()
		}
	})
}
