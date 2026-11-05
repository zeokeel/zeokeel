package shell

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/pixeltest"
)

// darkMasks are the regions TestScreensBaseline excludes for the light
// screenshots, reused unchanged for every dark and narrow comparison:
//   - the brand row (ZeoKeel wordmark replaces DSH's),
//   - the sidebar workspace/session lists (dynamic, captured differently),
//   - the hover tooltip DSH captured in some screenshots.
var darkMasks = []image.Rectangle{
	// Only the wordmark/logo; the collapse control at x=240..268, y=22..50
	// stays compared in both themes.
	image.Rect(0, 0, 236, 64),
	image.Rect(236, 0, 280, 18),
	image.Rect(236, 52, 280, 64),
	// Only the dynamic row labels/timestamps are masked; the sidebar geometry
	// and fills stay compared.
	image.Rect(38, 250, 232, 334),
	image.Rect(38, 350, 232, 386),
	image.Rect(276, 210, 520, 410),
}

// darkCase is one static screen's dark and narrow coverage.
//
// Budgets are maximum masked-diff ratios (0..1), tolerance 12. They gate
// structural regressions, not DSH pixel parity. The dark captures were taken
// with the 720px right sidebar open and a different session state, which the
// static renderer (Ctrl == nil) never paints; the per-case panel masks remove
// that region rather than spending the budget on it. Only the dynamic sidebar
// row interiors are masked elsewhere. Derivation, measured after the masks:
//
//	budget = measured dark diff + 3pp
//
//	dark  (1600x1000): home 5.96%, session 19.83%, plugins 3.13%,
//	                   settings 4.69%, automation no baseline
//	narrow (1200x800), panel masked at its real 680px edge:
//	                   home 5.60%, session 18.68%, others no baseline
//
// The narrow capture has DSH's 520px right panel open, while the static
// renderer paints the full 920px content column; masking only the actual panel
// (680..1200) therefore still compares DSH's 400px main column with the static
// render's wider layout, so the session budget is derived from that measured
// state. The +3pp headroom lets text antialiasing and that known column-width
// difference drift, but fails a moved pane, lost theme, or broken layout,
// which move far more pixels. A missing dark baseline is a test failure, not a
// silent skip.
type darkCase struct {
	name   string
	screen Screen
	// darkBase is the 1600x1000 DSH dark screenshot, "" when DSH has none.
	darkBase string
	// narrowBase is the 1200x800 DSH dark screenshot, "" when DSH has none.
	narrowBase string
	// scrim is the alpha of the full-window backdrop a screen paints over
	// itself. Settings is a modal: its black scrim dims the sidebar and main
	// background, so the token samples are blended through it. The light
	// backdrop is 0.24, DSH's dark one 0.5 (baseline 104-settings-dark).
	// 0 means the screen paints its token colors directly.
	scrim float64
	// darkBudget and narrowBudget are the maximum masked diff ratios.
	darkBudget   float64
	narrowBudget float64
	// narrowSample is a background pixel that stays outside every control
	// at 1200x800; each screen has different content there.
	narrowSample [2]int
	// extraMasks are regions the DSH capture contains but the static fallback
	// never paints. The dark home/session captures were taken with the 720px
	// right sidebar open; the static renderer (Ctrl == nil) has none.
	extraMasks []image.Rectangle
	// darkPanelMask/narrowPanelMask are per-kind right-panel regions, empty
	// when the capture has no panel.
	darkPanelMask   image.Rectangle
	narrowPanelMask image.Rectangle
}

func TestScreensDarkNarrow(t *testing.T) {
	cases := []darkCase{
		{
			name: "home", screen: Home,
			darkBase:        "../../baseline/screenshots/102-home-dark.png",
			narrowBase:      "../../baseline/screenshots/105-home-dark-narrow.png",
			darkBudget:      0.090,
			narrowBudget:    0.090,
			narrowSample:    [2]int{1180, 50},
			darkPanelMask:   image.Rect(880, 0, 1600, 1000),
			narrowPanelMask: image.Rect(680, 0, 1200, 800),
		},
		{
			name: "session", screen: Session,
			darkBase:        "../../baseline/screenshots/69-session-dark.png",
			narrowBase:      "../../baseline/screenshots/70-session-dark-narrow.png",
			darkBudget:      0.230,
			narrowBudget:    0.220,
			narrowSample:    [2]int{1150, 450},
			darkPanelMask:   image.Rect(880, 0, 1600, 1000),
			narrowPanelMask: image.Rect(680, 0, 1200, 800),
		},
		{
			// The plugins dark capture has no narrow counterpart.
			name: "plugins", screen: Plugins,
			darkBase:     "../../baseline/screenshots/103-plugins-dark.png",
			darkBudget:   0.062,
			narrowSample: [2]int{300, 50},
		},
		{
			// Settings is a full-window modal, dimmed by the dark theme's
			// 0.5 scrim; it has no narrow dark capture.
			name: "settings", screen: Settings,
			darkBase:     "../../baseline/screenshots/104-settings-dark.png",
			scrim:        0.5,
			darkBudget:   0.077,
			narrowSample: [2]int{1180, 50},
		},
		{
			// DSH has no automation dark or narrow capture yet.
			name: "automation", screen: Automation,
			narrowSample: [2]int{300, 750},
		},
	}

	filter := os.Getenv("SCREEN")
	dir := os.Getenv("ARTIFACTS")
	if dir == "" {
		dir = "../../artifacts"
	}
	for _, sub := range []string{"screens-dark", "screens-narrow", "diff-dark", "diff-narrow"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// render draws a fresh static app in the dark theme. The wrapper is the
	// theme hook: App.View resolves the appearance from c.Theme().Dark when
	// the persisted setting is the default "system", so setting the theme
	// before it drives every screen through design.Dark().
	render := func(screen Screen, w, h int) *image.RGBA {
		app := &App{Screen: screen}
		return ui.Render(func(c *ui.Context) {
			c.SetTheme(design.Dark().Theme())
			app.View(c)
		}, w, h, 1)
	}

	tokens := design.Dark()
	wantSidebar := rgbaOf(tokens.BgSidebar)
	wantMain := rgbaOf(tokens.BgBase)

	// assertDarkFrame checks the frame size and that the dark tokens reached
	// the painted sidebar and main background. Coordinates are fixed and clear
	// of content: (10,450) is sidebar padding, the main sample is bottom-right
	// outside the composer, and (278,450)/(w-1,450) are the sidebar's last
	// interior column and the main area's last column. Settings dims all of
	// them through its modal scrim, computed from the same Dark() tokens.
	assertDarkFrame := func(t *testing.T, label string, img *image.RGBA, w, h int, scrim float64, checkBoundary bool, narrowAt [2]int) {
		t.Helper()
		if img == nil {
			t.Fatalf("%s: nil render", label)
		}
		if got := img.Bounds(); got.Dx() != w || got.Dy() != h {
			t.Fatalf("%s: render size %v, want %dx%d", label, got, w, h)
		}
		wantSide, wantBg := wantSidebar, wantMain
		if scrim > 0 {
			wantSide = scrimmed(wantSidebar, scrim)
			wantBg = scrimmed(wantMain, scrim)
		}
		if got := img.RGBAAt(10, 450); got != wantSide {
			t.Errorf("%s: sidebar bg at (10,450) = %v, want %v (design.Dark().BgSidebar)", label, got, wantSide)
		}
		mainX, mainY := 1500, 900
		if w == 1200 {
			mainX, mainY = narrowAt[0], narrowAt[1]
		}
		if got := img.RGBAAt(mainX, mainY); got != wantBg {
			t.Errorf("%s: main bg at (%d,%d) = %v, want %v (design.Dark().BgBase)", label, mainX, mainY, got, wantBg)
		}
		// The sidebar keeps its 280px width at 1200 and the main area still
		// reaches the right edge: content fits, nothing is clipped sideways.
		if got := img.RGBAAt(w-1, 450); got != wantBg {
			t.Errorf("%s: main bg at right edge (%d,450) = %v, want %v", label, w-1, got, wantBg)
		}
		if checkBoundary {
			if got := img.RGBAAt(278, 450); got != wantSide {
				t.Errorf("%s: sidebar bg at (278,450) = %v, want %v", label, got, wantSide)
			}
			if got := img.RGBAAt(280, 450); got != wantBg {
				t.Errorf("%s: main content starts at x=280: (280,450) = %v, want %v", label, got, wantBg)
			}
		}
	}

	compare := func(t *testing.T, name, kind string, img *image.RGBA, base string, budget float64, panel image.Rectangle) {
		t.Helper()
		if base == "" {
			// No DSH capture exists for this state (or for its narrow
			// variant). The render is still checked for frame size, dark
			// tokens and the control-geometry assertions in
			// TestNarrowLayoutKeepsControlsVisible / TestDarkPrimaryActionContrast;
			// do not claim 1:1 DSH parity for it.
			t.Logf("%s/%s: no DSH baseline; covered by structural and token assertions", name, kind)
			return
		}
		want, err := pixeltest.LoadPNG(base)
		if err != nil {
			t.Fatalf("%s/%s: baseline %s is required: %v", name, kind, base, err)
		}
		masks := append([]image.Rectangle(nil), darkMasks...)
		if panel != (image.Rectangle{}) {
			masks = append(masks, panel)
		}
		res := pixeltest.CompareExcluding(img, want, 12, masks...)
		t.Logf("%s/%s: masked diff %.2f%% (budget %.2f%%), baseline %s",
			name, kind, res.Ratio*100, budget*100, filepath.Base(base))
		if res.Ratio > budget {
			t.Errorf("%s/%s: structural regression: masked diff %.2f%% exceeds budget %.2f%% (baseline %s)",
				name, kind, res.Ratio*100, budget*100, base)
		}
		diff := pixeltest.DiffImage(img, want, 12)
		if err := pixeltest.SavePNG(filepath.Join(dir, "diff-"+kind, name+".png"), diff); err != nil {
			t.Errorf("%s/%s: save diff: %v", name, kind, err)
		}
	}

	for _, c := range cases {
		if filter != "" && c.name != filter {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			dark := render(c.screen, 1600, 1000)
			if dark == nil {
				t.Fatal("dark render: nil")
			}
			if err := pixeltest.SavePNG(filepath.Join(dir, "screens-dark", c.name+".png"), dark); err != nil {
				t.Fatalf("save dark render: %v", err)
			}
			assertDarkFrame(t, c.name+"/dark", dark, 1600, 1000, c.scrim, true, c.narrowSample)
			assertDarkSurfaces(t, c.name, dark)
			compare(t, c.name, "dark", dark, c.darkBase, c.darkBudget, c.darkPanelMask)

			narrow := render(c.screen, 1200, 800)
			if narrow == nil {
				t.Fatal("narrow render: nil")
			}
			if err := pixeltest.SavePNG(filepath.Join(dir, "screens-narrow", c.name+".png"), narrow); err != nil {
				t.Fatalf("save narrow render: %v", err)
			}
			// The Settings modal is 800x800 centered in 1200x800, so it covers
			// the sidebar boundary at x=280 on the narrow frame; the 1600
			// frame above checks the boundary. Every other screen exposes it.
			assertDarkFrame(t, c.name+"/narrow", narrow, 1200, 800, c.scrim, c.scrim == 0, c.narrowSample)
			compare(t, c.name, "narrow", narrow, c.narrowBase, c.narrowBudget, c.narrowPanelMask)
		})
	}
}

// assertDarkSurfaces checks the raised surfaces the dark capture shows: the
// New Session button is BgStrong (#43454A) and the session composer is
// BgRaised (#2C2C2E), not the page background.
func assertDarkSurfaces(t *testing.T, name string, img *image.RGBA) {
	t.Helper()
	tokens := design.Dark()
	switch name {
	case "home":
		want := rgbaOf(tokens.BgStrong)
		if got := img.RGBAAt(100, 89); got != want {
			t.Errorf("home/dark: New Session at (100,89) = %v, want BgStrong %v", got, want)
		}
	case "session":
		want := rgbaOf(tokens.BgRaised)
		if got := img.RGBAAt(700, 930); got != want {
			t.Errorf("session/dark: composer at (700,930) = %v, want BgRaised %v", got, want)
		}
	}
}

// rgbaOf converts a ui.Color to the 8-bit RGBA a render stores.
func rgbaOf(c ui.Color) color.RGBA {
	return color.RGBA{R: c.R, G: c.G, B: c.B, A: c.A}
}

// scrimmed darkens c the way a full-window black scrim of the given alpha
// does, through the same ui color compositing the renderer paints with, so
// the expected sample matches the artifact exactly.
func scrimmed(c color.RGBA, alpha float64) color.RGBA {
	return rgbaOf(ui.RGBA(0, 0, 0, float32(alpha)).Over(ui.Color{R: c.R, G: c.G, B: c.B, A: c.A}))
}
