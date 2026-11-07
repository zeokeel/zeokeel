// Package brand is ZeoKeel's brand layer: the palette lifted from the
// logomark and the mygo ui.Theme built from it.
//
// Nothing here is copied from another product's design system. Every hex
// value either comes from the logomark (logo/logo.svg, logo/logo-dark.svg)
// or from the wordmark spec (docs/wordmark.md), or is a documented blend of
// those values. The semantic mapping from palette to product roles is the
// only part that is a design decision, and it is recorded in ADR 0002
// (docs/decisions/0002-brand-theme-foundation.md).
//
// Screens should use the theme (LightTheme/DarkTheme) and the semantic
// Palette colors; the raw Hex* constants exist to keep provenance and may be
// tuned here when the brand evolves.
package brand

import "github.com/egoist/mygo/ui"

// Logomark palette: the eight gradient stops of the mark, in the order they
// appear in logo/logo.svg. The comments name the file and line, so a future
// mark change can be traced without opening an image editor.
const (
	// HexCyan #37E7FE is the gradient id0 start (logo/logo.svg:4) and the
	// flat highlight stroke of the mark (logo/logo.svg:31).
	HexCyan = "#37E7FE"
	// HexBlue #0946EF is the gradient id0 end (logo/logo.svg:5).
	HexBlue = "#0946EF"
	// HexRoyal #0D44F6 is the gradient id2 start (logo/logo.svg:12); it is
	// the brand primary blue and the light theme's accent.
	HexRoyal = "#0D44F6"
	// HexSky #3CBBFA is the gradient id2 end (logo/logo.svg:13); it is the
	// dark theme's accent.
	HexSky = "#3CBBFA"
	// HexIndigo #1D32FD is the gradient id3 start (logo/logo.svg:16).
	HexIndigo = "#1D32FD"
	// HexIce #2ECCFB is the gradient id3 end (logo/logo.svg:17).
	HexIce = "#2ECCFB"
	// HexViolet #755BFD is the gradient id4 start (logo/logo.svg:20).
	HexViolet = "#755BFD"
	// HexUltra #3935FB is the gradient id4 end (logo/logo.svg:21).
	HexUltra = "#3935FB"
)

// Logomark tints: the light highlight stops the mark uses on top of the
// gradients. They are not UI fills; they document the light end of the
// brand's blue range for icons and decorative artwork.
const (
	// HexMist #CFE5FF is the gradient id1 start (logo/logo.svg:8).
	HexMist = "#CFE5FF"
	// HexPeriwinkle #9BB7FA is the gradient id1 end (logo/logo.svg:9).
	HexPeriwinkle = "#9BB7FA"
	// HexFrost #A2BBFE is the gradient id5 start (logo/logo.svg:24).
	HexFrost = "#A2BBFE"
	// HexGlow #F0F8FB is the gradient id5 end (logo/logo.svg:25).
	HexGlow = "#F0F8FB"
)

// Logo backdrops and wordmark ink.
const (
	// HexWhite #FFFFFF is the mark's light container (logo/logo.svg:28) and
	// the light theme's page background.
	HexWhite = "#FFFFFF"
	// HexBlack #000000 is the mark's dark container (logo/logo-dark.svg:28);
	// the dark theme lifts it to a brand night shade instead of using pure
	// black.
	HexBlack = "#000000"
	// HexInk #0F1115 is the wordmark text on light surfaces
	// (docs/wordmark.md, 颜色 row) and the light theme's primary text.
	HexInk = "#0F1115"
	// HexInkDark #F9FAFB is the wordmark text on dark surfaces
	// (docs/wordmark.md) and the dark theme's primary text.
	HexInkDark = "#F9FAFB"
)

// Palette is the semantic color set of one appearance. LightPalette and
// DarkPalette map the logo colors onto product roles; the neutral scale is
// built from the wordmark inks and leans a little toward the brand blue and
// violet, so neutral chrome reads as ZeoKeel instead of generic gray.
//
// A Palette is a value: callers get a fresh copy and may adjust it to probe
// alternatives without changing the brand defaults.
type Palette struct {
	// Surfaces, from the page up to the strongest control fill.
	Background     ui.Color
	Sidebar        ui.Color
	Surface        ui.Color
	SurfaceStrong  ui.Color
	SurfacePressed ui.Color

	// Hover is the tint of a hovered or selected row, menu item or tab;
	// SurfacePressed the fill of a pressed control.
	Hover ui.Color

	// Text, from primary ink down to tertiary labels.
	Text          ui.Color
	TextSecondary ui.Color
	TextTertiary  ui.Color

	// Border and BorderStrong are the regular and the emphasized dividers.
	Border       ui.Color
	BorderStrong ui.Color

	// Accent family: the brand blue in the light theme, the brand sky blue in
	// the dark one. AccentText is the text on an accent fill, OnPrimary the
	// text on a filled primary control.
	Accent        ui.Color
	AccentHover   ui.Color
	AccentPressed ui.Color
	AccentText    ui.Color
	OnPrimary     ui.Color

	// Selection and Focus stay translucent so the text underneath keeps its
	// contrast; Scrollbar is the scroll thumb; Scrim is the backdrop a modal
	// paints over the whole window.
	Selection ui.Color
	Focus     ui.Color
	Scrollbar ui.Color
	Scrim     ui.Color

	// Status colors are universal semantic hues, deliberately outside the
	// logo palette. WarningText is the WCAG-AA text companion of Warning,
	// which is too light for small text on the page background.
	Success ui.Color
	Danger  ui.Color
	// DangerText is the WCAG-AA foreground for danger message text on a
	// danger tint; Danger itself is the fill/border/icon color.
	DangerText  ui.Color
	Warning     ui.Color
	WarningText ui.Color
}

// LightPalette returns the light appearance's palette. The comments name the
// provenance of every value that is not obvious: a logo line, the wordmark,
// or the blend it was derived from.
func LightPalette() Palette {
	return Palette{
		Background: ui.Hex(HexWhite), // logo/logo.svg:28
		// Sidebar and SurfaceStrong are the cool brand neutrals from the
		// wordmark preview (ADR 0001 §8.2): #F9FAFB and #F1F3F5.
		Sidebar:        ui.Hex("#F9FAFB"),
		Surface:        ui.Hex(HexWhite), // raised cards separate by their border
		SurfaceStrong:  ui.Hex("#F1F3F5"),
		SurfacePressed: ui.RGBA(13, 68, 246, 0.10), // brand royal blue at 10%
		Hover:          ui.RGBA(13, 68, 246, 0.06), // brand royal blue at 6%
		Text:           ui.Hex(HexInk),             // wordmark light ink, 18.9:1 on white
		TextSecondary:  ui.Hex("#5B6373"),          // ink cooled toward blue, 6.0:1 on white
		// Tertiary carries 11–13px explanations, timestamps and usage numbers,
		// so it must clear WCAG AA (4.5:1) on every light surface:
		// 5.29:1 on BgBase/BgRaised, 5.06:1 on BgSidebar, 4.76:1 on BgStrong.
		// It stays lighter than TextSecondary (6.04:1 on white) so the
		// Text > Secondary > Tertiary hierarchy survives (P0-2, review-round3).
		TextTertiary:  ui.Hex("#646C7A"),         // ink cooled, WCAG AA on all light surfaces
		Border:        ui.RGBA(15, 17, 21, 0.10), // ink at 10%
		BorderStrong:  ui.RGBA(15, 17, 21, 0.16), // ink at 16%
		Accent:        ui.Hex(HexRoyal),          // logo/logo.svg:12, 6.6:1 with white text
		AccentHover:   ui.Hex(HexBlue),           // logo/logo.svg:5, one step darker
		AccentPressed: ui.Hex("#0B38CA"),         // royal blue darkened 18% for the press
		AccentText:    ui.Hex(HexWhite),
		OnPrimary:     ui.Hex(HexWhite),
		Selection:     ui.RGBA(13, 68, 246, 0.22), // royal blue at 22%
		Focus:         ui.RGBA(13, 68, 246, 0.55), // royal blue at 55%
		Scrollbar:     ui.RGBA(15, 17, 21, 0.30),
		Scrim:         ui.RGBA(15, 17, 21, 0.30), // ink scrim, modal backdrop
		Success:       ui.Hex("#16A34A"),
		Danger:        ui.Hex("#DC2626"), // 4.8:1 on white
		DangerText:    ui.Hex("#B91C1C"), // 5.7:1 on the 8% danger tint
		Warning:       ui.Hex("#D97706"),
		WarningText:   ui.Hex("#8A4B0F"), // 6.8:1 on white
	}
}

// DarkPalette returns the dark appearance's palette. The dark surfaces lift
// the logomark's black container (logo-dark.svg:28) toward the brand violet,
// so the night chrome is a brand night rather than neutral black.
func DarkPalette() Palette {
	return Palette{
		Background:     ui.Hex("#0D0D14"), // black lifted ~8% toward brand violet
		Sidebar:        ui.Hex("#13131C"),
		Surface:        ui.Hex("#1B1B26"),
		SurfaceStrong:  ui.Hex("#272736"),
		SurfacePressed: ui.RGBA(55, 231, 254, 0.16), // logo cyan at 16%
		Hover:          ui.RGBA(55, 231, 254, 0.10), // logo cyan at 10%
		Text:           ui.Hex(HexInkDark),          // wordmark dark ink, 18.5:1 on the night
		TextSecondary:  ui.Hex("#C7CAD9"),           // 11.9:1
		TextTertiary:   ui.Hex("#9AA0B4"),           // 7.4:1
		Border:         ui.RGBA(255, 255, 255, 0.12),
		BorderStrong:   ui.RGBA(255, 255, 255, 0.18),
		Accent:         ui.Hex(HexSky),  // logo/logo.svg:13, 8.7:1 with ink text
		AccentHover:    ui.Hex(HexIce),  // logo/logo.svg:17
		AccentPressed:  ui.Hex(HexCyan), // logo/logo.svg:4: the press glows brighter in the dark
		AccentText:     ui.Hex(HexInk),  // #0F1115 ink on the sky fill
		OnPrimary:      ui.Hex(HexInk),
		Selection:      ui.RGBA(60, 187, 250, 0.30), // logo sky at 30%
		Focus:          ui.RGBA(55, 231, 254, 0.70), // logo cyan at 70%
		Scrollbar:      ui.RGBA(255, 255, 255, 0.35),
		Scrim:          ui.RGBA(0, 0, 0, 0.55),
		Success:        ui.Hex("#22C55E"),
		Danger:         ui.Hex("#F87171"),
		DangerText:     ui.Hex("#FCA5A5"), // AA on the dark danger tint
		Warning:        ui.Hex("#F59E0B"),
		WarningText:    ui.Hex("#E9A24C"), // 9.0:1 on the night
	}
}

// PaletteOf returns the palette for an appearance, the same pick
// LightTheme/DarkTheme make: true is dark.
func PaletteOf(dark bool) Palette {
	if dark {
		return DarkPalette()
	}
	return LightPalette()
}
