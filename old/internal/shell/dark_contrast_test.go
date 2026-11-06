package shell

import (
	"context"
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/design"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/tools"
)

// darkPixelsInRect counts pixels darker than limit inside r. It is how these
// tests check that a dark label or icon is actually painted on a light pill,
// rather than trusting the style function.
func darkPixelsInRect(img *image.RGBA, r ui.Rect, limit uint8) int {
	n := 0
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
				continue
			}
			c := img.RGBAAt(x, y)
			if c.R < limit && c.G < limit && c.B < limit {
				n++
			}
		}
	}
	return n
}

// renderDarkComponent renders one component under the dark theme and returns
// its tester so the test can look up a control's box.
func renderDarkComponent(t *testing.T, build func(a *App, c *ui.Context, tokens *design.Tokens)) *ui.Tester {
	t.Helper()
	tokens := design.Dark()
	app := &App{}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(tokens.Theme())
		build(app, c, tokens)
	}, 640, 320)
	return tt
}

// TestDarkPrimaryActionContrast guards the invisible dark primary actions:
// "+ Add plugin", "Add model" and Automation's "New" pill render a dark
// foreground on the light primary surface in the dark theme.
func TestDarkPrimaryActionContrast(t *testing.T) {
	t.Run("add plugin", func(t *testing.T) {
		tt := renderDarkComponent(t, func(a *App, c *ui.Context, tokens *design.Tokens) {
			ui.Row(c).Padding(20).Children(func() { a.addPluginButton(c, tokens) })
		})
		r, ok := tt.Find("Add plugin")
		if !ok {
			t.Fatalf("Add plugin not found: %q", tt.Texts())
		}
		if n := darkPixelsInRect(tt.Image(), r, 0x50); n < 10 {
			t.Fatalf("Add plugin label has %d dark pixels in %v; dark mode label is invisible", n, r)
		}
	})

	t.Run("add model", func(t *testing.T) {
		tt := renderDarkComponent(t, func(a *App, c *ui.Context, tokens *design.Tokens) {
			ui.Row(c).Padding(20).Children(func() { a.modelsAddButton(c, tokens) })
		})
		r, ok := tt.Find("Add model")
		if !ok {
			t.Fatalf("Add model not found: %q", tt.Texts())
		}
		if n := darkPixelsInRect(tt.Image(), r, 0x50); n < 10 {
			t.Fatalf("Add model label has %d dark pixels in %v", n, r)
		}
	})

	t.Run("automation new", func(t *testing.T) {
		tt := renderDarkComponent(t, func(a *App, c *ui.Context, tokens *design.Tokens) {
			ui.Row(c).Padding(20).Children(func() { a.automationHeadingLive(c, tokens) })
		})
		r, ok := tt.Find("New")
		if !ok {
			t.Fatalf("New not found: %q", tt.Texts())
		}
		if n := darkPixelsInRect(tt.Image(), r, 0x50); n < 10 {
			t.Fatalf("Automation plus/label has %d dark pixels in %v", n, r)
		}
	})
}

// TestDarkContrastRatios checks the inverse token itself: on-primary must
// contrast strongly with the primary fill in both themes.
func TestDarkContrastRatios(t *testing.T) {
	for _, tc := range []struct {
		name string
		t    *design.Tokens
	}{{"light", design.Light()}, {"dark", design.Dark()}} {
		if got := contrastRatio(tc.t.OnPrimary, tc.t.LabelPrimary); got < 7 {
			t.Errorf("%s: OnPrimary/LabelPrimary contrast = %.1f, want >= 7", tc.name, got)
		}
		if got := contrastRatio(tc.t.AccentText, tc.t.Accent); got < 3 {
			t.Errorf("%s: AccentText/Accent contrast = %.1f, want >= 3", tc.name, got)
		}
	}
}

// TestPrimaryButtonLabelContrastInCards renders the real approval and
// ask-user cards and measures their primary labels against the fill those
// buttons actually paint (BgStrong), in both themes. The old PrimaryButton
// used OnPrimary, which is white on the white light fill and #151517 on
// #43454A in dark: invisible in light and only 1.9:1 in dark.
func TestPrimaryButtonLabelContrastInCards(t *testing.T) {
	for _, tc := range []struct {
		name   string
		dark   bool
		tokens *design.Tokens
	}{{"light", false, design.Light()}, {"dark", true, design.Dark()}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("approval", func(t *testing.T) {
				h := newApprovalApp(t, &alwaysAllowProvider{})
				if err := h.ctrl.Submit(context.Background(), h.ctrl.CurrentSessionID(), "turn"); err != nil {
					t.Fatalf("Submit: %v", err)
				}
				waitApprovalPending(t, h.ctrl)
				tt := ui.NewTester(h.app.View, 1600, 1000)
				if tc.dark {
					tt.SetDark(true)
				}
				tt.Frame()
				for _, label := range []string{"Allow", "Always allow"} {
					assertLabelContrast(t, tt, label, tc.tokens.BgStrong, 4.5)
				}
			})

			t.Run("ask user", func(t *testing.T) {
				provider := &m6Provider{steps: []m6Step{
					{tool: "ask_user", args: `{"question":"Which database should I use?","options":["sqlite","postgres"]}`},
					{text: "waiting for your answer"},
				}}
				app := m6LiveApp(t, provider, tools.NewAskUserTool())
				m6Submit(t, app, "choose a database")
				tt := ui.NewTester(app.View, 1600, 1000)
				if tc.dark {
					tt.SetDark(true)
				}
				tt.Frame()
				assertLabelContrast(t, tt, "Send answer", tc.tokens.BgStrong, 4.5)
			})
		})
	}
}

// assertLabelContrast finds label's box and checks its strongest rendered
// pixel against fill. Text antialiasing blends toward the fill, so the glyph
// core is the pixel with the highest contrast.
func assertLabelContrast(t *testing.T, tt *ui.Tester, label string, fill ui.Color, want float64) {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("%q not found: %q", label, tt.Texts())
	}
	img := tt.Image()
	best := 0.0
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
				continue
			}
			c := img.RGBAAt(x, y)
			if got := contrastRatio(ui.RGB(c.R, c.G, c.B), fill); got > best {
				best = got
			}
		}
	}
	if best < want {
		t.Fatalf("%q glyph contrast against fill %v = %.2f:1, want >= %.1f:1", label, fill, best, want)
	}
}

// contrastRatio is WCAG's ratio for two opaque colors.
func contrastRatio(a, b ui.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(c ui.Color) float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// TestNarrowLayoutKeepsControlsVisible covers the 900-1100px range the window
// allows: the Home send control and the Session composer controls stay inside
// the window, and the transcript cannot overlap the composer. It renders the
// live controller-backed screens (the static ones were retired with pixel
// parity).
func TestNarrowLayoutKeepsControlsVisible(t *testing.T) {
	for _, w := range []int{900, 1000, 1100, 1200} {
		w := w
		t.Run("home", func(t *testing.T) {
			h := newLiveAppAt(t, Home)
			h.app.Language = LangEN
			tt := ui.NewTester(h.app.View, w, 800)
			tt.Frame()
			r, ok := tt.Find("Send message")
			if !ok {
				t.Fatalf("%dpx: Send message missing: %q", w, tt.Texts())
			}
			if r.W < 30 || r.H < 30 || r.X < 0 || r.X+r.W > float32(w) {
				t.Fatalf("%dpx: Send message rect = %+v, want a visible 34x34 button", w, r)
			}
		})
		t.Run("session", func(t *testing.T) {
			h := newLiveAppAt(t, Session)
			h.app.Language = LangEN
			// The footer hides itself before any turn has run; this layout check
			// needs it visible.
			usageSnap := state.NewSnapshot()
			usageSnap.Usage = state.Usage{Turns: 1, Steps: 2, In: 100, Out: 20, CacheHit: 50}
			h.app.SetSnapshot(usageSnap)
			tt := ui.NewTester(h.app.View, w, 800)
			tt.Frame()
			transcript, ok := tt.Find("Session transcript")
			if !ok {
				t.Fatalf("%dpx: transcript missing: %q", w, tt.Texts())
			}
			composer, ok := tt.Find("Session composer")
			if !ok {
				t.Fatalf("%dpx: composer missing: %q", w, tt.Texts())
			}
			usage, ok := tt.Find("Session usage")
			if !ok {
				t.Fatalf("%dpx: usage missing: %q", w, tt.Texts())
			}
			if transcript.Y+transcript.H > composer.Y+1 {
				t.Errorf("%dpx: transcript %+v overlaps composer %+v", w, transcript, composer)
			}
			if composer.Y+composer.H > usage.Y+1 {
				t.Errorf("%dpx: composer %+v overlaps usage %+v", w, composer, usage)
			}
			if composer.W > float32(w)-280 || composer.W <= 0 {
				t.Errorf("%dpx: composer width %v does not fit the main column", w, composer.W)
			}
		})
	}
}

// TestNarrowPluginsHeaderFits covers the clipped "+ Add plugin" control at
// 1200px: the split button stays inside the window on the live Plugins screen.
func TestNarrowPluginsHeaderFits(t *testing.T) {
	h := newLiveAppAt(t, Plugins)
	h.app.Language = LangEN
	for _, w := range []int{1000, 1200, 1600} {
		tt := ui.NewTester(h.app.View, w, 800)
		tt.Frame()
		r, ok := tt.Find("Add plugin")
		if !ok {
			t.Fatalf("%dpx: Add plugin missing: %q", w, tt.Texts())
		}
		if r.W < 40 || r.X < 0 || r.X+r.W > float32(w) {
			t.Fatalf("%dpx: Add plugin rect = %+v, want it fully inside the window", w, r)
		}
	}
}

// TestSettingsLabelsDoNotTruncate guards the ellipsis regression: the full
// selector and action labels render as text, not "Workspace Wr…". It opens the
// live Settings overlay with Cmd+, instead of the retired static screen.
func TestSettingsLabelsDoNotTruncate(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if h.app.Screen != Settings {
		t.Fatalf("Cmd+, screen = %v, want Settings", h.app.Screen)
	}
	for _, want := range []string{"Workspace Write", "On completion", "Edit shortcuts"} {
		r, ok := tt.Find(want)
		if !ok {
			t.Fatalf("settings label %q missing: %q", want, tt.Texts())
		}
		if r.W <= 0 {
			t.Errorf("settings label %q has no width", want)
		}
	}
	if tt.HasText("…") {
		t.Errorf("settings still renders an ellipsis: %q", tt.Texts())
	}
}

// TestRightSidebarHeaderFitsPanel checks the docked panel and its narrow
// shrink: the four tabs and the New tab/Split/Fullscreen controls stay inside
// the panel with a non-zero box at both sizes. (The former DSH 720px width pin
// was retired with pixel parity.)
func TestRightSidebarHeaderFitsPanel(t *testing.T) {
	for _, w := range []int{1600, 1200} {
		h := newLiveApp(t, &shellProvider{})
		h.app.Screen = Session
		tt := ui.NewTester(h.app.View, w, 800)
		if err := tt.Click("Open right sidebar"); err != nil {
			t.Fatalf("%dpx: open right sidebar: %v", w, err)
		}
		panel, ok := tt.Find("Right sidebar")
		if !ok {
			t.Fatalf("%dpx: panel label missing", w)
		}
		if panel.W < 360 {
			t.Fatalf("%dpx: panel width %v, want >= 360 for the header controls", w, panel.W)
		}
		for _, label := range []string{"Start", "Files", "Terminal", "Log", "New tab", "Split", "Fullscreen"} {
			r, ok := tt.Find(label)
			if !ok {
				t.Errorf("%dpx: header control %q missing", w, label)
				continue
			}
			if r.W <= 0 || r.H <= 0 {
				t.Errorf("%dpx: %q has an empty rect %+v", w, label, r)
			}
			if r.X < panel.X-1 || r.X+r.W > panel.X+panel.W+1 {
				t.Errorf("%dpx: %q rect %+v is outside the panel %+v", w, label, r, panel)
			}
		}
	}
}

// rgbaOf converts a ui.Color to the 8-bit RGBA a render stores. It was moved
// here from the deleted baseline_dark_test.go, which also owned the DSH dark /
// narrow pixel baselines; scrim_test.go and verified_ui_test.go still use it.
func rgbaOf(c ui.Color) color.RGBA {
	return color.RGBA{R: c.R, G: c.G, B: c.B, A: c.A}
}
