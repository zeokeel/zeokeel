package uikit_test

// Frame and color tests for the product's one segmented control. They lock the
// look the settings pages and the Insights range switch share (audit V4: the
// chosen segment used to read as "no selection") in both themes: the chosen
// segment's BgSelected fill, LabelPrimary 600 text and its Accent boundary, the
// track's readability, the radio-group semantics and the arrow keys/Changed
// behavior mygo's SegmentedBase provides.

import (
	"image"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// segmentedTestView builds one bare SegmentedControl with the given tokens so a
// test can render it in either appearance; changed runs on mygo's Changed
// notice.
func segmentedTestView(tokens *design.Tokens, selected *int, changed func()) func(c *ui.Context) {
	return func(c *ui.Context) {
		c.SetTheme(tokens.Theme())
		if uikit.SegmentedControl(c, uikit.SegmentedOptions{
			Key:      "uikit.test.segmented",
			Label:    "视图",
			Labels:   []string{"列表", "网格"},
			Selected: selected,
		}) && changed != nil {
			changed()
		}
	}
}

// framePixel reads one pixel of the last frame as a token color.
func framePixel(t *testing.T, img *image.RGBA, x, y int) ui.Color {
	t.Helper()
	if x < 0 || y < 0 || x >= img.Rect.Dx() || y >= img.Rect.Dy() {
		t.Fatalf("pixel (%d,%d) is outside the %dx%d frame", x, y, img.Rect.Dx(), img.Rect.Dy())
	}
	c := img.RGBAAt(x, y)
	return ui.Color{R: c.R, G: c.G, B: c.B, A: c.A}
}

// colorNear reports whether two colors are within tolerance per channel: the
// painter composites in 8-bit channels, so a rounding step is expected between
// the token math and the frame.
func colorNear(got, want ui.Color, tolerance int) bool {
	close := func(a, b uint8) bool {
		d := int(a) - int(b)
		if d < 0 {
			d = -d
		}
		return d <= tolerance
	}
	return close(got.R, want.R) && close(got.G, want.G) && close(got.B, want.B)
}

// relativeLuminance is WCAG's relative luminance of an opaque color.
func relativeLuminance(c ui.Color) float64 {
	linear := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}

// contrastRatio is WCAG's ratio of two opaque colors.
func contrastRatio(a, b ui.Color) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TestSegmentedControlChosenSegmentLook covers the one look in both themes: the
// chosen segment paints the BgSelected tint with LabelPrimary text and the 2 DIP
// Accent boundary the track contrast needs (>= 3:1); the track stays readable
// for the unchosen segment (LabelSecondary >= 4.5:1); assistive technology sees
// a named radio group of radio buttons with the chosen one checked.
func TestSegmentedControlChosenSegmentLook(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tokens *design.Tokens
	}{
		{name: "light", tokens: design.Light()},
		{name: "dark", tokens: design.Dark()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected := 1
			tester := ui.NewTester(segmentedTestView(tc.tokens, &selected, nil), 420, 160)
			tester.Frame()

			group, ok := tester.AccessByRole(ui.RoleRadioGroup, "视图")
			if !ok {
				t.Fatalf("the segmented control is not a radio group named 视图; accessibility tree = %+v",
					tester.AccessAll())
			}
			chosen, ok := tester.AccessByRole(ui.RoleRadio, "网格")
			if !ok {
				t.Fatalf("the chosen segment is not a radio button; accessibility tree = %+v", tester.AccessAll())
			}
			if !chosen.Checked {
				t.Fatalf("the chosen segment is not checked; %+v", chosen)
			}
			if !chosen.Segment {
				t.Fatalf("the segment does not read as a segment of a segmented control; %+v", chosen)
			}
			unchosen, ok := tester.AccessByRole(ui.RoleRadio, "列表")
			if !ok || unchosen.Checked {
				t.Fatalf("the unchosen segment = %+v, %v; want a radio button that is not checked", unchosen, ok)
			}

			img := tester.Image()
			track := framePixel(t, img, int(group.Bounds.X)+1, int(group.Bounds.Y+group.Bounds.H/2))
			fill := framePixel(t, img, int(chosen.Bounds.X)+3, int(chosen.Bounds.Y+chosen.Bounds.H/2))
			underline := framePixel(t, img,
				int(chosen.Bounds.X+chosen.Bounds.W/2), int(chosen.Bounds.Y+chosen.Bounds.H-1))

			trackToken := tc.tokens.BgHover.Over(tc.tokens.BgBase)
			fillToken := tc.tokens.BgSelected.Over(trackToken)
			if !colorNear(track, trackToken, 3) {
				t.Fatalf("track pixel = %+v, want the BgHover tint %+v", track, trackToken)
			}
			if !colorNear(fill, fillToken, 3) {
				t.Fatalf("chosen segment fill = %+v, want the BgSelected tint %+v", fill, fillToken)
			}
			if colorNear(fill, track, 3) {
				t.Fatalf("the chosen segment fill (%+v) is indistinguishable from the track (%+v)", fill, track)
			}
			if !colorNear(underline, tc.tokens.Accent, 3) {
				t.Fatalf("the chosen segment boundary = %+v, want the Accent %+v", underline, tc.tokens.Accent)
			}

			// The boundary is what separates the chosen segment from the
			// track; it must clear the design's 3:1.
			if got := contrastRatio(tc.tokens.Accent, track); got < 3 {
				t.Fatalf("the chosen segment boundary is %.2f:1 against the track, want >= 3:1", got)
			}
			// The chosen segment's label is the persistent selection's ink.
			if got := contrastRatio(tc.tokens.LabelPrimary, fill); got < 4.5 {
				t.Fatalf("the chosen segment text is %.2f:1 on its fill, want >= 4.5:1", got)
			}
			// The unchosen segment stays readable on the track.
			if got := contrastRatio(tc.tokens.LabelSecondary, track); got < 4.5 {
				t.Fatalf("the unchosen segment text is %.2f:1 on the track, want >= 4.5:1", got)
			}
		})
	}
}

// TestSegmentedControlArrowKeysChoose covers the radio-group keyboard behavior
// the look must not lose: Tab reaches the chosen segment, Right and Left choose
// their neighbor, and assistive technology follows the choice.
func TestSegmentedControlArrowKeysChoose(t *testing.T) {
	selected := 0
	tester := ui.NewTester(segmentedTestView(design.Light(), &selected, nil), 420, 160)
	tester.Frame()

	tester.Key(0, ui.KeyTab)
	tester.Frame()
	if !tester.Focused("列表") {
		t.Fatalf("Tab did not reach the chosen segment; texts = %v", tester.Texts())
	}

	tester.Key(0, ui.KeyRight)
	tester.Frame()
	if selected != 1 {
		t.Fatalf("Right chose %d, want 1", selected)
	}
	if info, ok := tester.AccessByRole(ui.RoleRadio, "网格"); !ok || !info.Checked {
		t.Fatalf("after Right the segment reads %+v, %v; want checked", info, ok)
	}
	if !tester.Focused("网格") {
		t.Fatalf("after Right the keyboard focus did not follow the choice")
	}

	tester.Key(0, ui.KeyLeft)
	tester.Frame()
	if selected != 0 {
		t.Fatalf("Left chose %d, want 0", selected)
	}
}

// TestSegmentedControlChangedNotice covers the Changed write-back the settings
// pages save on: a click writes the chosen index through Selected and reports
// one Changed notice in the following pass, and choosing the same segment again
// reports none.
func TestSegmentedControlChangedNotice(t *testing.T) {
	selected, changes := 0, 0
	tester := ui.NewTester(segmentedTestView(design.Light(), &selected, func() { changes++ }), 420, 160)
	tester.Frame()
	if changes != 0 {
		t.Fatalf("the first frame reported %d changes, want 0", changes)
	}

	if err := tester.Click("网格"); err != nil {
		t.Fatalf("click 网格: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if selected != 1 {
		t.Fatalf("a click on 网格 wrote selected = %d, want 1", selected)
	}
	for range 3 {
		tester.Frame()
	}
	if changes != 1 {
		t.Fatalf("the click reported %d Changed notices, want exactly one", changes)
	}

	if err := tester.Click("网格"); err != nil {
		t.Fatalf("click the chosen 网格: %v", err)
	}
	for range 3 {
		tester.Frame()
	}
	if changes != 1 {
		t.Fatalf("choosing the chosen segment again reported another change (%d total)", changes)
	}
}
