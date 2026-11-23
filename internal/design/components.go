package design

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/brand"
)

// Brand spacing steps on the 4 DIP grid whose unit is brand.Spacing.
const (
	// Space0 is the explicit zero of the spacing scale: a call that must pass
	// one side of a padding or margin and leave another side alone names the
	// grid's zero instead of a bare 0 literal (DESIGN-LINT DS-03).
	Space0 float32 = 0
	Space1 float32 = brand.Spacing
	Space2 float32 = Space1 * 2
	Space3 float32 = Space1 * 3
	Space4 float32 = Space1 * 4
	Space5 float32 = Space1 * 5
	Space6 float32 = Space1 * 6
	Space8 float32 = Space1 * 8
)

// Font weights of the type scale. The scale uses three steps only: regular for
// body, medium for controls and emphasized rows, semibold for headings. Name
// them here so a new heading does not have to repeat a raw 600 (DS-03).
const (
	WeightRegular  int = 400
	WeightMedium   int = 500
	WeightSemibold int = 600
)

// Control heights stay on the 4 DIP grid. The scale is intentionally small:
// compact (28) for chips and icon buttons, compact row (32) for trees and
// menus, standard (36) for list rows, form controls and dialog buttons, page
// action (40) for a page's single primary action, and double row (44) for a
// list row that carries metadata. The retired 38 DIP primary sat off the grid.
const (
	ControlCompactHeight float32 = 28
	RowCompactHeight     float32 = 32
	ControlHeight        float32 = 36
	PageActionHeight     float32 = 40
	RowDoubleHeight      float32 = 44
)

// Brand layout measures. They are the single source of the shell geometry so
// the host and the modules cannot drift: window chrome (sidebar, rail,
// inspector), the readable content column and the settings surface.
const (
	// BandHeight is the window's top band row (screen title + actions): 48,
	// one step above the 40 it used to be, so a 14 DIP semibold title, a 32 DIP
	// chip and a 28 DIP icon button all breathe in one row.
	BandHeight float32 = 48
	// BrandRowHeight is the sidebar's brand row (mark + wordmark + collapse):
	// 56, tighter than the old 64 so the navigation starts higher.
	BrandRowHeight float32 = 56
	// PaletteWidth is the command palette's card width: wide enough for a
	// command, its section and its shortcut on one row, narrower than a form
	// dialog.
	PaletteWidth float32 = 640
	// FooterHeight is a surface's fixed action bar (settings page, long form):
	// 56 DIP with a hairline on top, holding 32 DIP buttons in its right corner.
	FooterHeight float32 = 56
	// ToastWidth bounds a toast card (max 320, min 240): a two-line notice,
	// never a banner across the reading column.
	ToastWidth    float32 = 320
	ToastMinWidth float32 = 240

	// SidebarWidth is the expanded source column.
	SidebarWidth float32 = 280
	// SidebarRailWidth is the collapsed icon rail.
	SidebarRailWidth float32 = 64
	// InspectorWidth is the right inspector's preferred width.
	InspectorWidth float32 = 320
	// InspectorMinWidth is the narrowest usable inspector.
	InspectorMinWidth float32 = 260
	// ContentMinWidth is the narrowest the main column may become before the
	// inspector collapses to an overlay.
	ContentMinWidth float32 = 520
	// SettingsWidth/SettingsHeight are the settings surface's preferred size;
	// it never grows past the window minus SettingsMargin.
	SettingsWidth  float32 = 880
	SettingsHeight float32 = 760
	// SettingsMargin is the free space kept around the settings surface; the
	// surface grows with the window so a long page list stays visible rather
	// than scrolling the last pages out of reach.
	SettingsMargin float32 = 24
	// TranscriptWidth is the conversation column: the width the transcript,
	// composer and composer dock share, so a change keeps them aligned; narrower
	// windows still fall back to 92% of the content column. It bounds blocks
	// (code fences, tables, tool cards, approval cards) rather than paragraphs.
	TranscriptWidth float32 = 960
	// ProseMeasure is the reading measure of a text block inside that column:
	// paragraphs, lists and quotes. At 14 DIP it holds roughly 45 Chinese
	// characters or 90 Latin characters per line, the comfortable range for
	// both scripts; a fence or a table keeps the wider column so it does not
	// squeeze into two narrow columns.
	ProseMeasure float32 = 640

	// DialogWidthS/M/L are the widths of the modal forms, in the order of the
	// content they hold: a single prompt, a form, a long form or preview. They
	// replaced the bare 360/460/520/560/620 values scattered over the modules.
	DialogWidthS float32 = 460
	DialogWidthM float32 = 560
	DialogWidthL float32 = 620
	// DialogWidthXS is the narrowest modal: a single input row such as the
	// rename-session prompt. DialogWidthCompact holds one small form and
	// DialogWidthXL the widest editor, with fields side by side.
	DialogWidthXS      float32 = 360
	DialogWidthCompact float32 = 520
	DialogWidthXL      float32 = 680
	// OverlayWidthS bounds an overlay's secondary line (a menu's model note and
	// context window), so a long label cannot stretch the popup.
	OverlayWidthS float32 = 380
	// DialogBodyMax bounds a scrollable body inside a dialog (a list or a long
	// preview), so the footer and the title stay on screen.
	DialogBodyMax float32 = 480
)

// Brand icon sizes. A quiet inline glyph, a control glyph and a prominent
// glyph; as everywhere else the shell and the modules share these instead of
// writing bare Size values.
const (
	IconSizeS float32 = 16
	IconSizeM float32 = 20
	IconSizeL float32 = 24
)

// Brand radii. The ladder is 4 / 6 / 8 / 12 / 16: an inline marker, a chip,
// a control (RadiusMD is the theme's brand.Radius), a card or dialog, and the
// composer. Rounder steps read as consumer software, so the product stops at
// 16; RadiusMax is only for an avatar, never for a status label (those are
// RadiusXS chips, not pills).
const (
	RadiusXS  float32 = 4
	RadiusSM  float32 = 6
	RadiusMD  float32 = brand.Radius
	RadiusLG  float32 = 12
	RadiusXL  float32 = 16
	RadiusMax float32 = 999
)

// Semantic spacing aliases. They add no values (each is an existing Space
// step); they name the rhythm so a reviewer can check one rule: inline gaps
// use Inline, rows use Row, blocks inside a card use Group, cards within a
// section use Section, page regions use Screen, and the top of a page uses
// Page. Using the next step up (not 10 or 14) is what keeps the 4 DIP grid.
const (
	SpaceInline  = Space1 // 4: icon to text, kbd to label
	SpaceRow     = Space2 // 8: row padding, gap between rows
	SpaceGroup   = Space3 // 12: blocks inside a card
	SpaceSection = Space4 // 16: card to card, panel sections
	SpaceScreen  = Space6 // 24: page regions, settings sections
	SpacePage    = Space8 // 32: page top
)

// SectionLabel is a small uppercase sidebar section title.
func (t *Tokens) SectionLabel() Style {
	return func(el ui.Element) ui.Element {
		return Apply(el, t.Font(TypeXXXS), func(e ui.Element) ui.Element {
			return e.TextColor(t.LabelTertiary).PaddingX(Space2)
		})
	}
}

// SurfaceSunken is the surface one step below a floating overlay (S4): the
// settings dialog's navigation column. In the light ramp that step is the
// window's own sidebar (S1), one shade under the white panel. In the dark ramp
// the window sidebar is DARKER than the page, so inside a floating panel it
// would read as a hole; the raised surface (S2) is the step below the overlay
// there. One helper so both themes keep the ramp's direction instead of the
// same token name meaning two different depths.
func (t *Tokens) SurfaceSunken() ui.Color {
	if t.Dark {
		return t.BgRaised
	}
	return t.BgSidebar
}

// Panel is a bordered surface (independent cards, tool cards, dialogs). Use it
// for objects, not for every section: sections are spacing and a title. The
// border is BorderL3 — the edge of something the pointer can enter — and the
// card carries no shadow: elevation is reserved for floating layers.
func (t *Tokens) Panel() Style {
	return func(el ui.Element) ui.Element {
		return el.Background(t.BgRaised).Border(1, t.BorderL3).Radius(RadiusLG)
	}
}

// ShadowComposer is kept for callers that still want a lifted input surface;
// the composer preset itself is flat (see Composer).
func (t *Tokens) ShadowComposer() Style {
	return func(el ui.Element) ui.Element {
		return el.Shadow(0, 2, 4, 0, ui.RGBA(0, 0, 0, 0.05))
	}
}

// Composer is the message input box: a raised card with a hairline border and
// the largest corner in the product (RadiusXL), flat on the page. The soft
// drop shadow it used to carry made it read as a floating dialog over the
// transcript; the shell now treats it as the docked bottom surface, separated
// by its edge alone. The width is left to the content column so the composer
// can never be wider than the surface it lives in.
func (t *Tokens) Composer() Style {
	return func(el ui.Element) ui.Element {
		return Apply(el, t.Panel(), func(e ui.Element) ui.Element {
			return e.Radius(RadiusXL)
		})
	}
}

// PrimaryButton is the brand's main action: accent fill, OnPrimary text, 36
// DIP tall, the theme's control radius. Apply it to a ui.PrimaryButton so
// mygo keeps the native hover/pressed/disabled state machine; overriding the
// background here would disable those states (mygo styleButton only tints a
// button whose background is the theme's base).
func (t *Tokens) PrimaryButton() Style {
	return func(el ui.Element) ui.Element {
		return el.Height(ControlHeight).PaddingX(Space4).Radius(RadiusMD).FontWeight(WeightMedium)
	}
}

// PrimaryButtonLarge is the page's single primary action: a hero or empty-state
// button that may sit on its own, one step taller than a dialog's control.
// A page must not show more than one filled accent button (DESIGN.md §5.2).
func (t *Tokens) PrimaryButtonLarge() Style {
	return func(el ui.Element) ui.Element {
		return el.Height(PageActionHeight).PaddingX(Space5).Radius(RadiusMD).FontWeight(WeightMedium)
	}
}

// DisabledPrimary is the look of a primary action that cannot run yet: a
// neutral fill with tertiary text instead of mygo's 50% accent wash, which
// still read as a clickable brand-blue button (white on it is only 2.47:1).
// Apply it to a plain ui.Button so the native disabled state stays.
func (t *Tokens) DisabledPrimary() Style {
	return func(el ui.Element) ui.Element {
		return el.Height(ControlHeight).PaddingX(Space4).Radius(RadiusMD).FontWeight(WeightMedium).
			Background(t.BgStrong).TextColor(t.LabelTertiary).
			// The composer's disabled send keeps its full-strength neutral fill
			// and a hairline; the framework's automatic dimming would halve it
			// into an invisible ghost (VD-20). The footer's disabled primary is
			// the same control in another place, so it uses the same tokens.
			Border(1, t.BorderL2).DimDisabled(false)
	}
}

// ControlChip is a compact outlined chip for staged attachments and the
// composer's/band's quiet choices: 28 DIP tall, RadiusSM, a hairline border and
// 8 DIP horizontal padding. It replaces the r16 Panel chip that was the only
// chip using a card radius (V17).
func (t *Tokens) ControlChip() Style {
	return func(el ui.Element) ui.Element {
		return el.Height(ControlCompactHeight).PaddingX(Space2).Gap(Space1).
			Background(t.BgRaised).Border(1, t.BorderL2).Radius(RadiusSM)
	}
}

// SelectedRow is a persistent navigation item's chosen state: the quiet 12%
// selection tint, not an accent fill. Transient choices (a command palette row,
// a menu item under the keyboard) keep the accent fill; persistent navigation
// stays quiet so one screen never shows two loud accent blocks (DESIGN.md
// §5.2 selection model).
func (t *Tokens) SelectedRow() Style {
	return func(el ui.Element) ui.Element {
		return el.Background(t.BgSelected).Radius(RadiusSM)
	}
}

// SecondaryButton is a neutral outlined action for dialogs and forms. Apply
// it to a plain ui.Button: mygo keeps the native surface, border, hover and
// pressed states, the brand only fixes the control metrics.
func (t *Tokens) SecondaryButton() Style {
	return func(el ui.Element) ui.Element {
		return el.Height(36).PaddingX(Space3).Radius(RadiusMD).FontWeight(WeightMedium)
	}
}

// DangerButton is a destructive action: danger text on the neutral control.
// Apply it to a plain ui.Button; callers must confirm irreversible actions.
func (t *Tokens) DangerButton() Style {
	return func(el ui.Element) ui.Element {
		return el.Height(36).PaddingX(Space3).Radius(RadiusMD).
			TextColor(t.DangerText).FontWeight(WeightMedium)
	}
}

// Badge is a small label for status, type, permission and counts — not for
// ordinary metadata. It is a crisp 4 DIP chip rather than a pill: a status
// label is information, not decoration, and the pill shape read as consumer
// software next to 32 DIP rows.
func (t *Tokens) Badge() Style {
	return func(el ui.Element) ui.Element {
		return Apply(el, t.Font(TypeXXXS), func(e ui.Element) ui.Element {
			return e.PaddingX(Space1).PaddingY(1).Radius(RadiusXS).
				Background(t.BgHover).TextColor(t.LabelSecondary)
		})
	}
}

// Kbd is the keyboard key cap: a 16 DIP tall chip with the short key label,
// used in menus, the command palette, tooltips and the composer hint. It is
// deliberately smaller than a badge so it recedes into the hint line.
func (t *Tokens) Kbd() Style {
	return func(el ui.Element) ui.Element {
		return Apply(el, t.Font(TypeXXXS), func(e ui.Element) ui.Element {
			return e.Height(16).PaddingX(Space1).MinWidth(16).
				Radius(RadiusXS).Border(1, t.BorderL2).
				Background(t.BgStrong).TextColor(t.LabelSecondary)
		})
	}
}

// ShadowOverlay is the elevation of floating surfaces above the page: menus,
// popovers, palettes, dialogs and toasts (not cards, which are flat in this
// product). It is two stacked shadows — a tight contact shadow and a wide
// ambient one — so the layer reads as raised without a dark halo, and it is
// stronger in the dark theme, where a soft shadow reads less.
func (t *Tokens) ShadowOverlay() Style {
	contact := float32(0.06)
	ambient := float32(0.10)
	if t.Dark {
		contact, ambient = 0.20, 0.34
	}
	return func(el ui.Element) ui.Element {
		return el.Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, contact)).
			Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, ambient))
	}
}
