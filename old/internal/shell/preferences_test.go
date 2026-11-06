package shell

import (
	"image"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
	"github.com/zeokeel/zeokeel/old/internal/session"
)

// prefLuminance and prefContrast are WCAG's relative luminance and contrast
// ratio for the preference tests (the shell's other contrast helpers live in
// the DSH-era tests and are not reused here).
func prefLuminance(c ui.Color) float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

func prefContrast(a, b ui.Color) float64 {
	la, lb := prefLuminance(a), prefLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TestTextScaleScalesThemeAndConversationText covers the merge rule between
// the desktop's TextScale and the app's conversation font size: the theme's
// base size and the conversation size both scale, the app's 14/16 choice stays
// the relative baseline (16 × 1.5 = 24), and the keys of a 1.5x frame stay
// inside the window.
func TestTextScaleScalesThemeAndConversationText(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	h.app.Language = LangEN

	// The tester the assertions use: preferences come from the desktop.
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.SetPreferences(ui.Preferences{TextScale: 1.5})
	tt.Frame()
	if got := h.app.shellTextScaleOf(); got != 1.5 {
		t.Fatalf("cached text scale = %v, want 1.5", got)
	}
	// Conversation text: the 14px setting scales to 21, its line box to 36.
	if got := h.app.ConversationFontSize(); got != 21 {
		t.Fatalf("ConversationFontSize at 1.5x = %v, want 21", got)
	}
	if got := h.app.ConversationLineHeight(); got != 36 {
		t.Fatalf("ConversationLineHeight at 1.5x = %v, want 36", got)
	}
	// The user's 16px choice is relative to the desktop scale too.
	h.app.settingsLoaded = true
	h.app.settingsValue = session.DefaultSettings()
	h.app.settingsValue.FontSize = session.FontSizeLarge
	tt.Frame()
	if got := h.app.ConversationFontSize(); got != 24 {
		t.Fatalf("ConversationFontSize at 16px/1.5x = %v, want 24", got)
	}
	if got := h.app.ConversationLineHeight(); got != 42 {
		t.Fatalf("ConversationLineHeight at 16px/1.5x = %v, want 42", got)
	}

	// The frame's theme base size is the brand's 14 × 1.5 = 21, and the key
	// controls stay inside the window at that scale.
	var themeFont float32
	tt2 := ui.NewTester(func(c *ui.Context) {
		h.app.View(c)
		themeFont = c.Theme().FontSize
	}, 1600, 1000)
	tt2.SetPreferences(ui.Preferences{TextScale: 1.5})
	tt2.Frame()
	if themeFont != 21 {
		t.Fatalf("theme FontSize at 1.5x = %v, want brand 14 × 1.5 = 21", themeFont)
	}
	assertPrefControlsInside(t, tt2, "Composer input", "Send message")

	// The session screen keeps its header, transcript and composer too.
	h.app.Screen = Session
	tt.Frame()
	assertPrefControlsInside(t, tt, "Session transcript", "Composer input")
	if !tt.HasText("live") {
		t.Fatalf("session header title missing at 1.5x: %q", tt.Texts())
	}
}

// TestHighContrastStrengthensThemeAndTokens covers the HighContrast rule:
// with no mygo API to re-apply Theme.follow to a custom theme, the shell
// mirrors the same adjustments on the brand theme and on the design tokens:
// stronger borders and secondary text, an opaque focus ring and a wider,
// stronger scrollbar.
func TestHighContrastStrengthensThemeAndTokens(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	h.app.Language = LangEN

	var tokens *design.Tokens
	var theme *ui.Theme
	tt := ui.NewTester(func(c *ui.Context) {
		h.app.View(c)
		tokens = h.app.themeTokens(c)
		theme = c.Theme()
	}, 1600, 1000)
	tt.SetPreferences(ui.Preferences{HighContrast: true})
	tt.Frame()

	base := design.Light()
	if tokens.BorderL2 == base.BorderL2 {
		t.Fatal("HighContrast left the regular border at the brand value")
	}
	// Borders are translucent: compare them as painted over the page.
	if got, want := prefContrast(tokens.BorderL2.Over(tokens.BgBase), tokens.BgBase),
		prefContrast(base.BorderL2.Over(base.BgBase), base.BgBase); got <= want {
		t.Fatalf("HighContrast border contrast = %.2f, want > the brand's %.2f", got, want)
	}
	if got, want := prefContrast(tokens.LabelSecondary, tokens.BgBase), prefContrast(base.LabelSecondary, base.BgBase); got <= want {
		t.Fatalf("HighContrast secondary text contrast = %.2f, want > the brand's %.2f", got, want)
	}
	if theme.Focus.A != 255 {
		t.Fatalf("HighContrast focus ring alpha = %d, want opaque 255", theme.Focus.A)
	}
	if want := scrollbarPolicyForPreferences(ui.Preferences{HighContrast: true}).Width; theme.ScrollbarWidth != want {
		t.Fatalf("HighContrast scrollbar width = %v, want %v", theme.ScrollbarWidth, want)
	}
	if got := prefContrast(theme.Border.Over(theme.Background), theme.Background); got <=
		prefContrast(base.Theme().Border.Over(base.Theme().Background), base.Theme().Background) {
		t.Fatalf("HighContrast theme border contrast = %.2f, want stronger than the brand theme", got)
	}
	// The frame still renders the regular controls.
	assertPrefControlsInside(t, tt, "Composer input", "Send message")
}

// TestPreferencesReduceMotionDisablesAnimations covers the global
// ReduceMotion integration: View turns the frame's animations off before
// building, so transitions and Animate values settle at once instead of
// animating; the default desktop keeps them.
func TestPreferencesReduceMotionDisablesAnimations(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	h.app.Language = LangEN

	instant := false
	tt := ui.NewTester(func(c *ui.Context) {
		h.app.View(c)
		instant = c.Instant()
	}, 1600, 1000)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	if !instant {
		t.Fatal("ReduceMotion did not disable the frame's animations")
	}
	if h.app.prefs.ReduceMotion != true {
		t.Fatal("ReduceMotion was not cached on the App")
	}

	instant = true
	tt.SetPreferences(ui.Preferences{})
	tt.Frame()
	if instant {
		t.Fatal("a normal desktop disabled the frame's animations")
	}
	if h.app.prefs.ReduceMotion {
		t.Fatal("ReduceMotion stayed cached after the desktop turned it off")
	}
}

// TestPreferencesScrollbarPolicy pins the research outcome: mygo draws overlay
// scroll bars shown on hover, sized by Theme.ScrollbarWidth and colored by
// Theme.Scrollbar, with no desktop-wide preference to force them always on.
// The shell keeps the native overlay behavior and only widens/strengthens the
// bars under HighContrast.
func TestPreferencesScrollbarPolicy(t *testing.T) {
	normal := scrollbarPolicyForPreferences(ui.Preferences{})
	contrast := scrollbarPolicyForPreferences(ui.Preferences{HighContrast: true})
	if !normal.Overlay || !contrast.Overlay {
		t.Fatalf("scrollbar policy dropped the native overlay bars: %+v / %+v", normal, contrast)
	}
	if normal.Width != shellScrollbarWidth {
		t.Fatalf("default scrollbar width = %v, want %v", normal.Width, shellScrollbarWidth)
	}
	if contrast.Width <= normal.Width {
		t.Fatalf("HighContrast scrollbar width = %v, want > %v", contrast.Width, normal.Width)
	}
	if normal.Contrasted || !contrast.Contrasted {
		t.Fatalf("Contrasted flags = %v/%v", normal.Contrasted, contrast.Contrasted)
	}
	if th := shellTheme(design.Light(), ui.Preferences{}); th.ScrollbarWidth != 0 {
		t.Fatalf("normal frame overrode the theme scrollbar width: %v", th.ScrollbarWidth)
	}
	if th := shellTheme(design.Light(), ui.Preferences{HighContrast: true}); th.ScrollbarWidth != contrast.Width {
		t.Fatalf("HighContrast frame scrollbar width = %v, want %v", th.ScrollbarWidth, contrast.Width)
	}
}

// TestPreferencesKeepBrandAccent covers the documented accent rule: the
// desktop accent is intentionally not taken, so the brand identity survives,
// and the brand's text-on-accent pairs keep WCAG AA contrast.
func TestPreferencesKeepBrandAccent(t *testing.T) {
	systemAccent := ui.Hex("#FF2200")
	for _, tc := range []struct {
		name   string
		tokens *design.Tokens
	}{
		{"light", design.Light()},
		{"dark", design.Dark()},
	} {
		th := shellTheme(tc.tokens, ui.Preferences{Accent: systemAccent})
		if th.Accent != tc.tokens.Accent {
			t.Errorf("%s: theme accent = %v, want the brand accent %v", tc.name, th.Accent, tc.tokens.Accent)
		}
		if got := prefContrast(th.AccentText, th.Accent); got < 4.5 {
			t.Errorf("%s: text on accent contrast = %.2f, want >= 4.5", tc.name, got)
		}
	}
	// The text scale clamps what a desktop may ask for.
	if got := shellTextScale(ui.Preferences{}); got != 1 {
		t.Errorf("no desktop text scale = %v, want 1", got)
	}
	if got := shellTextScale(ui.Preferences{TextScale: 2.5}); got != 2 {
		t.Errorf("2.5x text scale = %v, want the 2x clamp", got)
	}
}

// TestHighDPIRenderSmoke is the 2x DPI regression: rendering Home and Session
// at 1600x1000 DIPs with scale 2 gives a 3200x2000 physical frame with real
// content, and the key controls are still found in the window.
func TestHighDPIRenderSmoke(t *testing.T) {
	for _, tc := range []struct {
		name   string
		screen Screen
		keys   []string
	}{
		{"home", Home, []string{"Composer input", "Send message"}},
		{"session", Session, []string{"Composer input", "Chat"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newLiveApp(t, &shellProvider{})
			h.app.Language = LangEN
			h.app.Screen = tc.screen

			img := ui.Render(h.app.View, 1600, 1000, 2)
			if got := img.Bounds(); got.Dx() != 3200 || got.Dy() != 2000 {
				t.Fatalf("2x render bounds = %v, want 3200x2000", got)
			}
			if colors := prefDistinctColors(img); colors < 16 {
				t.Fatalf("2x render has only %d distinct sampled colors; want real content", colors)
			}

			// The same window through the tester: the controls are laid out in
			// the window's DIPs even though the frame is painted at 2 device
			// pixels per DIP.
			tt := ui.NewTester(h.app.View, 1600, 1000)
			tt.SetScale(2)
			tt.Frame()
			if got := tt.Image().Bounds(); got.Dx() != 3200 || got.Dy() != 2000 {
				t.Fatalf("tester 2x image bounds = %v, want 3200x2000", got)
			}
			assertPrefControlsInside(t, tt, tc.keys...)
		})
	}
}

// prefDistinctColors samples a grid of the physical frame and counts the
// distinct colors it shows, so a blank or single-color 2x frame fails.
func prefDistinctColors(img *image.RGBA) int {
	seen := make(map[uint32]struct{}, 256)
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 4 {
		for x := b.Min.X; x < b.Max.X; x += 4 {
			c := img.RGBAAt(x, y)
			seen[uint32(c.R)<<16|uint32(c.G)<<8|uint32(c.B)] = struct{}{}
		}
	}
	return len(seen)
}

// assertPrefControlsInside finds each control and checks its DIP rect is
// inside a 1600x1000 window: the preference changes must not push a key
// control off the frame.
func assertPrefControlsInside(t *testing.T, tt *ui.Tester, labels ...string) {
	t.Helper()
	for _, label := range labels {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("control %q missing: %q", label, tt.Texts())
		}
		if r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 1600 || r.Y+r.H > 1000 {
			t.Fatalf("control %q = %+v, want it inside the 1600x1000 window", label, r)
		}
	}
}

// TestTextScaleRoundsToWholeDips covers the 1.25x desktop scale: text sizes
// are rounded to whole DIPs (17.5 becomes 18) so glyphs stay crisp, and the
// conversation line box rounds with the same rule.
func TestTextScaleRoundsToWholeDips(t *testing.T) {
	if got := scaleShellText(14, 1.25); got != 18 {
		t.Fatalf("14 DIP at 1.25x = %v, want 18", got)
	}
	if got := scaleShellText(24, 1.25); got != 30 {
		t.Fatalf("24 DIP line at 1.25x = %v, want 30", got)
	}
	if got := scaleShellText(16, 1.25); got != 20 {
		t.Fatalf("16 DIP at 1.25x = %v, want 20", got)
	}
	// Unusual scales are clamped to a usable range, and no desktop means 1.
	if got := shellTextScale(ui.Preferences{TextScale: 0.2}); got != 0.5 {
		t.Fatalf("0.2x text scale = %v, want the 0.5 clamp", got)
	}
}

// TestTextScaleScalesDesignTypeRoles covers the v1.1 coverage: themeTokens
// writes the desktop scale onto design.Tokens, so every Font/Text/TextMuted
// type role scales too. At 1.0 the metrics equal the design system's own and
// key controls keep their baseline size; at 1.5x the type scale and the
// text-bearing controls grow while Home, Session and Settings stay inside the
// window.
func TestTextScaleScalesDesignTypeRoles(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Screen = Home
	h.app.Language = LangEN

	// 1.0 baseline: no desktop preference means the unscaled design system.
	var tokens *design.Tokens
	tt := ui.NewTester(func(c *ui.Context) {
		h.app.View(c)
		tokens = h.app.themeTokens(c)
	}, 1600, 1000)
	tt.Frame()
	if tokens.TextScale != 1 {
		t.Fatalf("tokens.TextScale at 1.0 = %v, want 1", tokens.TextScale)
	}
	if size, line, weight := tokens.Metrics(design.TypeBody); size != 14 || line != 22 || weight != 400 {
		t.Fatalf("TypeBody at 1.0 = %v/%v/%d, want the design system's 14/22/400", size, line, weight)
	}
	if chip, ok := tt.Find("Workspace, current: test"); !ok || chip.H != 28 {
		t.Fatalf("Home chip at 1.0 = %+v (ok=%v), want its baseline 28 DIP height", chip, ok)
	}

	// 1.5x: the design type scale follows the desktop text size.
	tt.SetPreferences(ui.Preferences{TextScale: 1.5})
	tt.Frame()
	if tokens.TextScale != 1.5 {
		t.Fatalf("tokens.TextScale at 1.5x = %v, want 1.5", tokens.TextScale)
	}
	if size, line, _ := tokens.Metrics(design.TypeBody); size != 21 || line != 33 {
		t.Fatalf("TypeBody at 1.5x = %v/%v, want 21/33", size, line)
	}
	if got := tokens.Scale(28); got != 42 {
		t.Fatalf("Scale(28) at 1.5x = %v, want 42", got)
	}
	chip, ok := tt.Find("Workspace, current: test")
	if !ok {
		t.Fatalf("Home chip missing at 1.5x: %q", tt.Texts())
	}
	if chip.H != 42 {
		t.Fatalf("Home chip at 1.5x = %v DIPs, want 42 (28 × 1.5)", chip.H)
	}
	assertPrefControlsInside(t, tt, "Composer input", "Send message", "Workspace, current: test")

	// Session keeps its header, transcript and composer inside the window.
	h.app.Screen = Session
	tt.Frame()
	assertPrefControlsInside(t, tt, "Session transcript", "Composer input")

	// Settings opens and its converted brand type grows with the scale.
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	title, ok := tt.Find("Settings")
	if !ok {
		t.Fatalf("Settings did not open at 1.5x: %q", tt.Texts())
	}
	if title.H <= 24 {
		t.Fatalf("Settings header title at 1.5x = %v DIPs, want taller than the 1.0x line", title.H)
	}
	assertPrefControlsInside(t, tt, "Settings")
}
