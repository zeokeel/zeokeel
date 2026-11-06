package shell

import (
	"math"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
)

// The shell sets a brand theme with Context.SetTheme, which replaces the
// theme the runtime derived from the desktop. mygo's own theme already
// follows the desktop preferences (Theme.follow: Accent, HighContrast,
// TextScale), and Animate/Transition honor ReduceMotion, so the shell
// re-applies the same adjustments to the brand theme here, once per frame,
// instead of losing them:
//
//   - TextScale multiplies the theme's base font size (brand.FontSize ×
//     scale, rounded to whole DIPs), the conversation text (user conversation
//     size × scale; line height × scale), and the design type scale:
//     themeTokens puts the scale on design.Tokens, so every Font/Text/
//     TextMuted role rendered through the tokens grows with the desktop text
//     size. The app's 14/16px setting stays a relative preference, so the
//     desktop's text size still applies to it. Only explicit FontSize calls
//     that carry their own line-height/icon layout remain fixed-by-design;
//     docs/design/known-limitations.md tracks the remainder.
//   - HighContrast strengthens the borders and secondary text and makes the
//     focus ring and scrollbar opaque, mirroring mygo's Theme.follow. The
//     design tokens the screens paint with get the same treatment.
//   - ReduceMotion turns every animation in the frame off (see
//     syncShellPreferences): mygo's transitions already honor it, and
//     DisableAnimations also settles the shell's own animated widgets at once.
//   - Accent is deliberately NOT taken from the desktop: ZeoKeel's brand
//     accent is part of its identity, and the contrast of text on it is
//     already checked by the brand palette (see docs/design/native-ui.md).

// shellTextScale returns the usable desktop text scale. mygo reports 0 when
// the desktop has no preference, which means usual size.
func shellTextScale(p ui.Preferences) float32 {
	s := p.TextScale
	if s <= 0 {
		return 1
	}
	// The brand layout is tested up to 2x; a wilder scale still lays out but
	// would make a desktop window unusable, so it is clamped.
	if s < 0.5 {
		return 0.5
	}
	if s > 2 {
		return 2
	}
	return s
}

// scaleShellText scales one DIP text size and rounds it to whole DIPs, as
// fractional text sizes blur on the rasterizer.
func scaleShellText(size, scale float32) float32 {
	if size <= 0 || scale == 1 {
		return size
	}
	return float32(math.Round(float64(size * scale)))
}

// syncShellPreferences reads the desktop preferences for the frame and turns
// its animations off when the desktop asks for less motion. The cached copy
// is what the frame's theme and conversation text scale read.
func (a *App) syncShellPreferences(c *ui.Context) {
	a.prefs = c.Preferences()
	if a.prefs.ReduceMotion {
		// DisableAnimations applies before anything is built, so transitions,
		// Animate values and the shell's animated widgets settle at once; the
		// desktop's reduce-motion request is thus global, not per screen.
		c.DisableAnimations()
	}
}

// shellTokensForPreferences applies the desktop preferences to brand tokens.
// HighContrast is the only one that changes the painted colors: borders and
// secondary text move toward the primary text, as mygo's Theme.follow does.
func shellTokensForPreferences(t *design.Tokens, p ui.Preferences) *design.Tokens {
	if !p.HighContrast {
		return t
	}
	hc := *t
	hc.BorderL2 = hc.BorderL2.Mix(hc.LabelPrimary, 0.45)
	hc.BorderL3 = hc.BorderL3.Mix(hc.LabelPrimary, 0.6)
	hc.LabelSecondary = hc.LabelSecondary.Mix(hc.LabelPrimary, 0.4)
	hc.LabelTertiary = hc.LabelTertiary.Mix(hc.LabelPrimary, 0.3)
	return &hc
}

// shellTheme derives the frame's ui.Theme from the brand tokens and the
// desktop preferences: TextScale multiplies the base font size, HighContrast
// strengthens the widget colors exactly as mygo's Theme.follow does. The
// returned theme is a copy, so the brand theme stays the source of truth.
func shellTheme(t *design.Tokens, p ui.Preferences) *ui.Theme {
	th := *t.Theme()
	if scale := shellTextScale(p); scale != 1 {
		th.FontSize = scaleShellText(th.FontSize, scale)
	}
	if p.HighContrast {
		th.Border = th.Border.Mix(th.Text, 0.45)
		th.TextMuted = th.TextMuted.Mix(th.Text, 0.4)
		th.Focus.A = 255
		th.Scrollbar = th.Scrollbar.Alpha(1.6)
		th.ScrollbarWidth = scrollbarPolicyForPreferences(p).Width
	}
	return &th
}

// shellScrollbarPolicy is the shell's scrollbar treatment. mygo v0.7.4 draws
// scroll bars as overlay indicators over the content (shown while the pointer
// is over the container), sized by Theme.ScrollbarWidth and colored by
// Theme.Scrollbar; it has no desktop "always show scroll bars" preference in
// ui.Preferences, and per-container hiding is Element.Scrollbars(false).
// The shell therefore keeps the native overlay behavior in both themes and,
// under HighContrast, widens and strengthens the bars so they stay findable.
// docs/design/native-ui.md records the research and the follow-up.
type shellScrollbarPolicy struct {
	// Width is the bar width in DIPs (Theme.ScrollbarWidth).
	Width float32
	// Overlay bars are the native hover-shown indicators (not a permanent
	// gutter); this stays true for the brand scroll containers.
	Overlay bool
	// Contrasted marks the HighContrast size, which also makes the thumb
	// opaque in shellTheme.
	Contrasted bool
}

const (
	// shellScrollbarWidth is mygo's default scroll bar width in DIPs.
	shellScrollbarWidth float32 = 6
	// shellScrollbarWidthHighContrast widens the bar under HighContrast so
	// the pointer has an easier target.
	shellScrollbarWidthHighContrast float32 = 8
)

// scrollbarPolicyForPreferences returns the scrollbar treatment for the
// desktop preferences.
func scrollbarPolicyForPreferences(p ui.Preferences) shellScrollbarPolicy {
	policy := shellScrollbarPolicy{Width: shellScrollbarWidth, Overlay: true}
	if p.HighContrast {
		policy.Width = shellScrollbarWidthHighContrast
		policy.Contrasted = true
	}
	return policy
}

// shellTextScaleOf is the cached text scale of the frame; direct calls outside
// a frame (tests, background helpers) see the usual size.
func (a *App) shellTextScaleOf() float32 { return shellTextScale(a.prefs) }
