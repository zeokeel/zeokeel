// Package design is ZeoKeel's design system: the brand theme built on mygo's
// native widgets and the style functions every screen builds with.
//
// History: the package began as a verbatim copy of the DSH baseline tokens,
// maintained for the retired pixel-parity effort (ADR 0001). Since ADR 0002
// it is a brand layer over mygo: Tokens carries the semantic colors of
// internal/brand, Theme returns the branded ui.Theme for the stock widgets,
// and the style, component and motion helpers stay as thin sugar over mygo's
// native Element API, to be replaced widget by widget in the native-control
// stage.
package design

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/brand"
)

// TypeRole names a step of the brand type scale: a 14px body with 16/20/24
// display steps above it and 13/12/11 metadata steps below.
type TypeRole int

const (
	TypeXL TypeRole = iota
	TypeL
	TypeM
	TypeBody
	TypeS
	TypeXS
	TypeXXS
	TypeXXXS
)

// TypeMetrics returns the font size, fixed line height and weight of a role.
// The numbers are the design system's own scale, tuned for a desktop UI at
// the brand's 14px base.
func TypeMetrics(r TypeRole) (size, line float32, weight int) {
	switch r {
	case TypeXL:
		return 24, 32, 400
	case TypeL:
		return 20, 28, 500
	case TypeM:
		return 16, 24, 400
	case TypeBody:
		return 14, 22, 400
	case TypeS:
		return 14, 24, 400
	case TypeXS:
		return 13, 20, 400
	case TypeXXS:
		return 12, 18, 400
	default:
		return 11, 14, 400
	}
}

// Tokens is one appearance's slice of the brand design system: the semantic
// colors and surfaces the screens build with. The colors come from
// internal/brand; this type names them the way screens use them and adds the
// surfaces that the mygo ui.Theme does not carry (the sidebar, the raised
// card fill, the modal scrim).
type Tokens struct {
	Dark bool
	// TextScale multiplies every type role rendered through Font, Text and
	// TextMuted (1.0 is the design system's own size), so the desktop's text
	// size preference reaches the brand type scale as well as the base theme
	// font size. It leaves colors, spacing and control metrics alone: the
	// screens scale a control's own height where its text must grow with it.
	TextScale float32

	BgBase    ui.Color // page background
	BgSidebar ui.Color // sidebar background
	BgRaised  ui.Color // raised surface: cards, composer, panels
	BgStrong  ui.Color // strong raised surface: filled neutral controls
	BgHover   ui.Color // hovered row tint
	// BgSelected is the stronger tint of the current row, menu item or tab;
	// keeping it apart from BgHover is what makes hover and selection
	// distinguishable.
	BgSelected ui.Color

	LabelPrimary   ui.Color
	LabelSecondary ui.Color
	LabelTertiary  ui.Color

	BorderL2 ui.Color // regular divider
	BorderL3 ui.Color // stronger divider

	Accent        ui.Color
	AccentHover   ui.Color
	AccentPressed ui.Color
	AccentText    ui.Color
	// OnPrimary is the foreground for a primary/accent filled control: white
	// on the brand blue in the light theme, brand ink on the sky fill in the
	// dark one.
	OnPrimary ui.Color

	Success ui.Color
	// SuccessText is the WCAG-AA foreground for success message text; Success
	// stays the fill/border/icon color.
	SuccessText ui.Color
	Danger      ui.Color
	// DangerText is the WCAG-AA foreground for danger message text on a
	// danger tint (Danger stays the fill/border/icon color).
	DangerText ui.Color
	// Warning is the status color for badges and state dots. WarningText is
	// its WCAG-AA foreground for warning message text on the page
	// background; the status color itself is too light for small text on
	// white.
	Warning     ui.Color
	WarningText ui.Color
}

// Light returns the light appearance's brand tokens.
func Light() *Tokens { return fromPalette(brand.LightPalette(), false) }

// Dark returns the dark appearance's brand tokens.
func Dark() *Tokens { return fromPalette(brand.DarkPalette(), true) }

// fromPalette maps a brand palette onto the semantic token names the screens
// use. The mapping is deliberately direct: one token is one palette role, so
// retuning the brand means editing internal/brand only.
func fromPalette(p brand.Palette, dark bool) *Tokens {
	return &Tokens{
		Dark:           dark,
		TextScale:      1,
		BgBase:         p.Background,
		BgSidebar:      p.Sidebar,
		BgRaised:       p.Surface,
		BgStrong:       p.SurfaceStrong,
		BgHover:        p.Hover,
		BgSelected:     p.RowSelected,
		LabelPrimary:   p.Text,
		LabelSecondary: p.TextSecondary,
		LabelTertiary:  p.TextTertiary,
		BorderL2:       p.Border,
		BorderL3:       p.BorderStrong,
		Accent:         p.Accent,
		AccentHover:    p.AccentHover,
		AccentPressed:  p.AccentPressed,
		AccentText:     p.AccentText,
		OnPrimary:      p.OnPrimary,
		Success:        p.Success,
		SuccessText:    p.SuccessText,
		Danger:         p.Danger,
		DangerText:     p.DangerText,
		Warning:        p.Warning,
		WarningText:    p.WarningText,
	}
}

// Scrim is the full-window backdrop a modal paints over the screen behind
// it: the brand ink scrim in the light theme and a deeper black in the dark
// one, so the dialog keeps its edge on either background.
func (t *Tokens) Scrim() ui.Color {
	return brand.PaletteOf(t.Dark).Scrim
}

// Of picks the tokens for the context's current theme (light or dark) and
// wires the desktop's text-size preference into the brand type scale. The
// brand theme is installed with c.SetTheme, so mygo does not scale it for us
// (a custom theme follows Preferences only if the app reads it); Tokens.Font
// sets explicit font sizes, so the scale must come from here for the desktop
// text-size setting to reach the brand type scale.
func Of(c *ui.Context) *Tokens {
	t := Light()
	if c.Theme().Dark {
		t = Dark()
	}
	if p := c.Preferences(); p.TextScale > 0 {
		t.TextScale = p.TextScale
	}
	return t
}

// Theme returns the brand theme the mygo widgets draw with: brand.LightTheme
// or brand.DarkTheme for the tokens' appearance. Screens install it with
// c.SetTheme(t.Theme()), so every stock mygo control (buttons, inputs, lists,
// menus, scroll bars) picks up the brand colors and metrics.
func (t *Tokens) Theme() *ui.Theme {
	if t.Dark {
		return brand.DarkTheme()
	}
	return brand.LightTheme()
}
