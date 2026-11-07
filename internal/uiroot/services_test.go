package uiroot

import (
	"image/color"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/brand"
	"github.com/zeokeel/zeokeel/internal/contracts"
)

// requirePixelColor asserts the last rendered frame paints want at (x, y).
func requirePixelColor(t *testing.T, tt *ui.Tester, x, y int, want ui.Color) {
	t.Helper()
	got := tt.Image().RGBAAt(x, y)
	if got != (color.RGBA{R: want.R, G: want.G, B: want.B, A: want.A}) {
		t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, got, want)
	}
}

// TestThemeServiceDrivesFrameTheme covers the host theme: modules see the
// host's theme in their Build (accent, dark flag), the rendered window follows
// it (background/surface pixels), and SetDark/Toggle only ask for a frame when
// the value actually changes.
func TestThemeServiceDrivesFrameTheme(t *testing.T) {
	r := newTestRoot(t)
	var captured atomic.Pointer[ui.Theme]
	mustScreen(t, r, contracts.ScreenSpec{ID: "home", Title: "首页", Order: 10,
		Build: func(c *ui.Context) {
			captured.Store(c.Theme())
			ui.Text(c, "body-home")
		}})

	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()

	light := LightTheme()
	got := captured.Load()
	if got == nil {
		t.Fatal("the screen Build did not see a frame theme")
	}
	if got.Dark {
		t.Fatal("the frame theme is dark while the host theme is light")
	}
	if got.Accent != light.Accent || got.Background != light.Background || got.Surface != light.Surface {
		t.Fatalf("frame theme = {accent %v, background %v, surface %v}, want the host light theme",
			got.Accent, got.Background, got.Surface)
	}
	// The window paints the brand light sidebar and content backgrounds.
	lightPalette := brand.LightPalette()
	requirePixelColor(t, tt, 5, 5, lightPalette.Sidebar)
	requirePixelColor(t, tt, 600, 300, lightPalette.Background)

	// Dark theme: state, frame theme and painted pixels all follow.
	r.Theme().SetDark(true)
	if !r.Theme().Dark() {
		t.Fatal("Dark() = false after SetDark(true)")
	}
	baseFrames := r.Frames()
	tt.Frame()
	if got, want := r.Frames(), baseFrames+1; got != want {
		t.Fatalf("Frames() = %d after a theme frame, want %d", got, want)
	}
	dark := DarkTheme()
	got = captured.Load()
	if !got.Dark {
		t.Fatal("the frame theme is not dark after SetDark(true)")
	}
	if got.Accent != dark.Accent || got.Background != dark.Background || got.Surface != dark.Surface {
		t.Fatalf("frame theme = {accent %v, background %v, surface %v}, want the host dark theme",
			got.Accent, got.Background, got.Surface)
	}
	darkPalette := brand.DarkPalette()
	requirePixelColor(t, tt, 5, 5, darkPalette.Sidebar)
	requirePixelColor(t, tt, 600, 300, darkPalette.Background)
	requireHasText(t, tt, "body-home") // the frame still builds cleanly

	// Toggle returns the new state and flips the rendered frame back.
	if got := r.Theme().Toggle(); got {
		t.Fatal("Toggle() = true after toggling from dark to light")
	}
	if r.Theme().Dark() {
		t.Fatal("Dark() = true after Toggle to light")
	}
	tt.Frame()
	if got := captured.Load(); got.Dark {
		t.Fatal("the frame theme stayed dark after Toggle to light")
	}

	// requestFrame is only asked for when the value changes.
	var invalidations counter
	r.SetInvalidator(func() { invalidations.Inc() })
	r.Theme().SetDark(false) // already light: no request
	if got := invalidations.Load(); got != 0 {
		t.Fatalf("invalidations = %d after SetDark to the current value, want 0", got)
	}
	r.Theme().SetDark(true) // changed: one request
	if got := invalidations.Load(); got != 1 {
		t.Fatalf("invalidations = %d after SetDark to a new value, want 1", got)
	}
}

// TestLocaleServiceAndRenderedCopy covers the host locale: stable keys,
// language normalization, unknown-key fallback to the key, and the rendered
// copy switching languages on the next frame.
func TestLocaleServiceAndRenderedCopy(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	locale := r.Locale()

	if got, want := locale.Language(), "zh"; got != want {
		t.Fatalf("Language() = %q, want %q", got, want)
	}
	for key, want := range map[string]string{
		"app.name":      "ZeoKeel",
		"nav.screens":   "界面",
		"nav.settings":  "设置",
		"settings.none": "还没有注册任何设置页。",
	} {
		if got := locale.T(key); got != want {
			t.Fatalf("T(%q) = %q, want %q", key, got, want)
		}
	}
	if got, want := locale.T("no.such.key"), "no.such.key"; got != want {
		t.Fatalf("T(unknown key) = %q, want the key itself %q", got, want)
	}

	tt := newTestTester(t, r, 1400, 900)
	tt.Frame()

	locale.SetLanguage("en")
	if got, want := locale.Language(), "en"; got != want {
		t.Fatalf("Language() = %q, want %q", got, want)
	}
	if got, want := locale.T("nav.settings"), "Settings"; got != want {
		t.Fatalf("T(nav.settings) = %q, want %q", got, want)
	}
	if got, want := locale.T("no.such.key"), "no.such.key"; got != want {
		t.Fatalf("T(unknown key) with en = %q, want the key itself", got)
	}
	tt.Frame()
	requireHasText(t, tt, "body-home")

	// Language tags normalize; unknown languages fall back to zh.
	locale.SetLanguage("en-US")
	if got, want := locale.Language(), "en"; got != want {
		t.Fatalf("Language() after SetLanguage(en-US) = %q, want %q", got, want)
	}
	locale.SetLanguage("zh-CN")
	if got, want := locale.Language(), "zh"; got != want {
		t.Fatalf("Language() after SetLanguage(zh-CN) = %q, want %q", got, want)
	}
	locale.SetLanguage("fr")
	if got, want := locale.Language(), "zh"; got != want {
		t.Fatalf("Language() after an unknown language = %q, want the %q fallback", got, want)
	}
	tt.Frame()
	requireHasText(t, tt, "body-home")

	// requestFrame only on an actual language change.
	var invalidations counter
	r.SetInvalidator(func() { invalidations.Inc() })
	locale.SetLanguage("zh") // unchanged
	if got := invalidations.Load(); got != 0 {
		t.Fatalf("invalidations = %d after setting the current language, want 0", got)
	}
	locale.SetLanguage("en") // changed
	if got := invalidations.Load(); got != 1 {
		t.Fatalf("invalidations = %d after changing the language, want 1", got)
	}
}

// TestThemeLocaleConcurrentAccess checks the host services from worker
// goroutines while the UI thread builds frames: -race must stay clean and the
// final state must be deterministic.
func TestThemeLocaleConcurrentAccess(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		langs := []string{"en", "zh"}
		for i := 0; i < 50; i++ {
			r.Theme().Toggle()
			r.Locale().SetLanguage(langs[i%2])
		}
	}()

	for i := 0; i < 10; i++ {
		tt.Frame() // builds while the worker flips host state
	}
	wg.Wait()

	if r.Theme().Dark() {
		t.Fatal("50 Toggles from light did not return to light")
	}
	// 50 alternating SetLanguage calls end on "zh" (the last index is odd).
	if got, want := r.Locale().Language(), "zh"; got != want {
		t.Fatalf("Language() = %q after the concurrent run, want %q", got, want)
	}
	tt.Frame()
	requireHasText(t, tt, "body-home")
	requireHasText(t, tt, "body-home")
}
