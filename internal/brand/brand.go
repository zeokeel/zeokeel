package brand

import "github.com/egoist/mygo/ui"

// Brand metrics. The radius is the brand's rounded-but-not-pill corner; the
// spacing unit and the body size are the density of the desktop UI. All
// three are set explicitly so every platform renders the same, instead of
// inheriting mygo's per-platform defaults (13px on macOS, 14px elsewhere)
// or its 6px stock corner.
const (
	// Radius rounds every control and panel: 10 DIPs.
	Radius float32 = 10
	// Spacing is the design-system unit for padding, gaps and control sizes:
	// 4 DIPs, the 4px grid the existing screens already follow.
	Spacing float32 = 4
	// FontSize is the base text size: 14 DIPs.
	FontSize float32 = 14
)

// SystemFont is the brand text stack. ZeoKeel bundles no font: the wordmark
// (docs/wordmark.md) and the whole UI run on the platform's own UI face,
// with the CJK families named in the same list so a Chinese Windows or macOS
// install keeps its native UI font.
//
// mygo's Element.Font takes a comma-separated list and keeps the first
// family the system has, so the list needs no per-platform branch: it stops
// at "Segoe UI" on Windows, "PingFang SC" on macOS, and falls through to
// the host's sans-serif on a system that has none of the named families.
//
// The bare "Helvetica" alias that some stacks place between "Helvetica Neue"
// and Arial is deliberately left out. On Linux, mygo resolves families
// through fontconfig, where "Helvetica" is a strong alias for Nimbus Sans;
// including it would replace the host's own UI sans-serif for every Latin
// glyph, with no benefit on Windows or macOS (which resolve earlier names
// first anyway).
const SystemFont = `-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Helvetica Neue", Arial, sans-serif`

// MonoFont is the brand code stack: terminal output, commands and other
// monospaced text. The CJK fallbacks keep command output readable on a
// Chinese system.
const MonoFont = `"SF Mono", "JetBrains Mono", "Fira Code", Consolas, "Liberation Mono", Menlo, Courier, "PingFang SC", "Microsoft YaHei"`

// LightTheme returns mygo's light theme with the brand applied: the light
// palette in the semantic color slots, the brand metrics (Radius 10,
// Spacing 4, FontSize 14) and the system font stack.
//
// It starts from ui.LightTheme, so everything the brand does not name keeps
// mygo's native value: SurfacePressed/SurfaceHover state colors are
// overridden by the palette, while Inverse/InverseText stay unset (mygo then
// derives them from Text/Background) and ScrollbarWidth keeps mygo's 6 DIP
// default.
func LightTheme() *ui.Theme {
	t := ui.LightTheme()
	applyPalette(t, LightPalette())
	applyMetrics(t)
	return t
}

// DarkTheme returns mygo's dark theme with the brand applied, the dark
// counterpart of LightTheme: the same overrides, starting from
// ui.DarkTheme's native dark defaults.
func DarkTheme() *ui.Theme {
	t := ui.DarkTheme()
	applyPalette(t, DarkPalette())
	applyMetrics(t)
	return t
}

// ThemeFor returns the brand theme for an appearance: true is dark, the same
// pick the app makes when it resolves the system appearance.
func ThemeFor(dark bool) *ui.Theme {
	if dark {
		return DarkTheme()
	}
	return LightTheme()
}

// applyPalette copies the palette's colors into the mygo theme slots.
func applyPalette(t *ui.Theme, p Palette) {
	t.Background = p.Background
	t.Surface = p.Surface
	t.SurfaceHover = p.Hover
	t.SurfacePressed = p.SurfacePressed
	t.Border = p.Border
	t.Text = p.Text
	t.TextMuted = p.TextSecondary
	t.Accent = p.Accent
	t.AccentHover = p.AccentHover
	t.AccentPressed = p.AccentPressed
	t.AccentText = p.AccentText
	t.Danger = p.Danger
	t.Warning = p.Warning
	t.Success = p.Success
	t.Selection = p.Selection
	t.Focus = p.Focus
	t.Scrollbar = p.Scrollbar
}

// applyMetrics sets the brand metrics on a theme.
func applyMetrics(t *ui.Theme) {
	t.Radius = Radius
	t.Spacing = Spacing
	t.FontSize = FontSize
	t.Font = SystemFont
}
