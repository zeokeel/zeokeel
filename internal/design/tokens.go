// Package design is ZeoKeel's design system: the DSH baseline tokens
// (docs/baseline/css.md) and the style functions every screen builds with.
package design

import "github.com/egoist/mygo/ui"

// TypeRole names a step of the baseline type scale. Values come from the
// computed styles captured in baseline/css/summary.json.
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

// TypeMetrics returns the font size, line height and weight of a role.
// The numbers are the DSH baseline's typography tokens (docs/baseline/css.md).
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

// Tokens is one theme's slice of the baseline design tokens.
type Tokens struct {
	Dark bool

	BgBase    ui.Color // page background
	BgSidebar ui.Color // sidebar background
	BgRaised  ui.Color // raised surface: cards, composer, panels
	BgStrong  ui.Color // strong raised surface: primary buttons
	BgHover   ui.Color // hover/selected row tint

	LabelPrimary   ui.Color
	LabelSecondary ui.Color
	LabelTertiary  ui.Color

	BorderL2 ui.Color // regular divider, rgba(0,0,0,.10) in the baseline
	BorderL3 ui.Color // stronger divider

	Accent        ui.Color
	AccentHover   ui.Color
	AccentPressed ui.Color
	AccentText    ui.Color
	// OnPrimary is the foreground for a primary/accent filled control. In
	// the light theme that is white; in the dark theme DSH's near-black
	// label on its light primary pill.
	OnPrimary ui.Color

	Success ui.Color
	Danger  ui.Color
	// Warning is the baseline status color used for badges and state dots
	// (#DD8629). WarningText is its WCAG-AA foreground for warning message
	// text on the page background; the status color itself is too light for
	// 11-13px text on white.
	Warning     ui.Color
	WarningText ui.Color

	White ui.Color
}

// Light returns the light theme tokens.
func Light() *Tokens {
	return &Tokens{
		BgBase:         ui.Hex("#FFFFFF"),
		BgSidebar:      ui.Hex("#F9FAFB"),
		BgRaised:       ui.Hex("#FFFFFF"),
		BgStrong:       ui.Hex("#FFFFFF"),
		BgHover:        ui.RGBA(38, 49, 72, 0.06),
		LabelPrimary:   ui.Hex("#0F1115"),
		LabelSecondary: ui.Hex("#61666B"),
		LabelTertiary:  ui.Hex("#81858C"),
		BorderL2:       ui.RGBA(0, 0, 0, 0.10),
		BorderL3:       ui.RGBA(0, 0, 0, 0.12),
		Accent:         ui.Hex("#4176E6"),
		AccentHover:    ui.Hex("#3468CF"),
		AccentPressed:  ui.Hex("#2A57B8"),
		AccentText:     ui.Hex("#FFFFFF"),
		OnPrimary:      ui.Hex("#FFFFFF"),
		Success:        ui.Hex("#22C55E"),
		Danger:         ui.Hex("#EC1313"),
		Warning:        ui.Hex("#DD8629"),
		WarningText:    ui.Hex("#8A4B0F"),
		White:          ui.Hex("#FFFFFF"),
	}
}

// Dark returns the dark theme tokens.
func Dark() *Tokens {
	return &Tokens{
		Dark:           true,
		BgBase:         ui.Hex("#151517"),
		BgSidebar:      ui.Hex("#1B1B1C"),
		BgRaised:       ui.Hex("#2C2C2E"),
		BgStrong:       ui.Hex("#43454A"),
		BgHover:        ui.RGBA(255, 255, 255, 0.08),
		LabelPrimary:   ui.Hex("#F9FAFB"),
		LabelSecondary: ui.Hex("#CFD3D6"),
		LabelTertiary:  ui.Hex("#ADB2B8"),
		BorderL2:       ui.RGBA(255, 255, 255, 0.12),
		BorderL3:       ui.RGBA(255, 255, 255, 0.16),
		Accent:         ui.Hex("#7AAAFF"),
		AccentHover:    ui.Hex("#93BAFF"),
		AccentPressed:  ui.Hex("#6398F0"),
		AccentText:     ui.Hex("#151517"),
		OnPrimary:      ui.Hex("#151517"),
		Success:        ui.Hex("#22C55E"),
		Danger:         ui.Hex("#F25A5A"),
		Warning:        ui.Hex("#DD8629"),
		WarningText:    ui.Hex("#E9A24C"),
		White:          ui.Hex("#FFFFFF"),
	}
}

// Scrim is the full-window backdrop a modal paints over the screen behind
// it: DSH's 0.24 black in the light theme and 0.5 black in the dark one.
func (t *Tokens) Scrim() ui.Color {
	if t.Dark {
		return ui.RGBA(0, 0, 0, 0.5)
	}
	return ui.RGBA(0, 0, 0, 0.24)
}

// Of picks the tokens for the context's current theme (light or dark).
func Of(c *ui.Context) *Tokens {
	if c.Theme().Dark {
		return Dark()
	}
	return Light()
}

// Theme maps the tokens onto the colors and metrics mygo's own widgets use,
// so built-in controls match the baseline too.
func (t *Tokens) Theme() *ui.Theme {
	return &ui.Theme{
		Dark:           t.Dark,
		Background:     t.BgBase,
		Surface:        t.BgRaised,
		SurfaceHover:   t.BgHover,
		SurfacePressed: t.BgHover,
		Border:         t.BorderL2,
		Text:           t.LabelPrimary,
		TextMuted:      t.LabelSecondary,
		Accent:         t.Accent,
		AccentHover:    t.AccentHover,
		AccentPressed:  t.AccentPressed,
		AccentText:     t.AccentText,
		Danger:         t.Danger,
		Warning:        t.Warning,
		Success:        t.Success,
		Selection:      t.BgHover,
		Focus:          t.Accent,
		Inverse:        t.LabelPrimary,
		InverseText:    t.BgBase,
		Radius:         8,
		Spacing:        4,
		FontSize:       14,
		// The DSH content stack, so built-in widgets (buttons, inputs,
		// menus, dialogs) get the same families as the shell's own text.
		Font: FontStack(),
	}
}
