package settings_test

// Regression tests for AP-21 on the settings Search page: its backend
// configuration states are statuses, so they read as uikit.StatusBadge — the
// Success tint for a configured backend, the neutral status tint for the
// unconfigured one — instead of neutral badges with status-colored words.

import (
	"image"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// statusPixel reads one pixel of the last frame as a token color.
func statusPixel(t *testing.T, img *image.RGBA, x, y int) ui.Color {
	t.Helper()
	if x < 0 || y < 0 || x >= img.Rect.Dx() || y >= img.Rect.Dy() {
		t.Fatalf("pixel (%d,%d) is outside the %dx%d frame", x, y, img.Rect.Dx(), img.Rect.Dy())
	}
	c := img.RGBAAt(x, y)
	return ui.Color{R: c.R, G: c.G, B: c.B, A: c.A}
}

// statusColorNear reports whether two colors are within tolerance per channel.
func statusColorNear(got, want ui.Color, tolerance int) bool {
	close := func(a, b uint8) bool {
		d := int(a) - int(b)
		if d < 0 {
			d = -d
		}
		return d <= tolerance
	}
	return close(got.R, want.R) && close(got.G, want.G) && close(got.B, want.B)
}

// assertStatusBadgeTint asserts the pill showing label paints fill at alpha over
// the row's own background: the status tint a StatusBadge has, not the neutral
// badge fill AP-21 forbids.
func assertStatusBadgeTint(t *testing.T, tester *ui.Tester, label string, fill ui.Color, alpha float32) {
	t.Helper()
	rect, ok := tester.Find(label)
	if !ok {
		t.Fatalf("frame does not show the status %q; texts = %v", label, tester.Texts())
	}
	img := tester.Image()
	// The badge pads Space1 (4 DIP) horizontally in the v2 label, so the
	// pill's fill is visible 2 DIPs left of the text bounds. The backdrop is
	// probed on the same row 10 DIPs left of the text: outside the pill but
	// still inside the row, which avoids landing on a neighbouring status
	// badge when two sit side by side.
	pill := statusPixel(t, img, int(rect.X)-2, int(rect.Y+rect.H/2))
	backdrop := statusPixel(t, img, int(rect.X)-10, int(rect.Y+rect.H/2))
	want := fill.Alpha(alpha).Over(backdrop)
	if !statusColorNear(pill, want, 3) {
		t.Fatalf("%q badge fill = %+v, want the status tint %+v over the row %+v (AP-21: neutral badge + status-colored text)",
			label, pill, want, backdrop)
	}
}

// TestSearchConfiguredBadgesUseStatusTint covers the configured backend state
// and its stored API key: both are positive statuses of the search backend.
func TestSearchConfiguredBadgesUseStatusTint(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingWebSearchEndpoint: "https://search.example.com/search",
		contracts.SettingWebSearchAPIKey:   "sk-stored",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openSearchPage(t, h, tester)

	h.assertTexts(tester, "已配置", "API Key 已设置")
	tokens := design.Light()
	assertStatusBadgeTint(t, tester, "已配置", tokens.Success, 0.08)
	assertStatusBadgeTint(t, tester, "API Key 已设置", tokens.Success, 0.08)
}

// TestSearchUnconfiguredBadgeIsNeutralStatus covers the unconfigured backend:
// it is not a failure, so it gets the status vocabulary's neutral presentation
// (label tertiary tint and text), still as a status badge.
func TestSearchUnconfiguredBadgeIsNeutralStatus(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openSearchPage(t, h, tester)

	h.assertTexts(tester, "未配置")
	assertStatusBadgeTint(t, tester, "未配置", design.Light().LabelTertiary, 0.12)
}
