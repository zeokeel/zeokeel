package sidebar_test

// Regression test for the unified selection language (V1): the chosen session
// row is the quiet persistent selection — the BgSelected 12% tint with a 2px
// Accent left stripe and LabelPrimary text — not mygo's saturated accent fill.
// The row's content, its badges and the stripe must all stay readable on that
// tint in both themes.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// colorDistance reports whether two sampled colors are within tolerance per
// channel (the painter composites in 8-bit channels, so a rounding step is
// expected between the token math and the frame).
func colorNear(got [3]uint32, want [4]uint8) bool {
	channels := [3]uint32{uint32(want[0]), uint32(want[1]), uint32(want[2])}
	for i := range got {
		if got[i]+2 < channels[i] || got[i] > channels[i]+2 {
			return false
		}
	}
	return true
}

// TestSelectedSessionRowUsesQuietSelection covers the V1 selection model in both
// themes: the chosen row is the selection tint, the left stripe is the accent,
// and the running badge keeps the resting neutral fill (the old on-accent fill
// was for the retired full-accent row).
func TestSelectedSessionRowUsesQuietSelection(t *testing.T) {
	for _, tc := range []struct {
		name string
		dark bool
	}{
		{name: "light"},
		{name: "dark", dark: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chat := &fakeChat{
				sessions: []contracts.ChatSessionInfo{
					{ID: "s1", Title: "运行中的会话", Running: true},
					{ID: "s2", Title: "空闲会话"},
				},
				active: "s1",
			}
			h := newHarness(t, chat, sidebar.Config{})
			tester := ui.NewTester(h.uiRoot.View, 1200, 800)
			tester.Frame()
			if tc.dark {
				// The product theme (brand tokens) is the host's, not the
				// desktop appearance the Tester starts with.
				h.uiRoot.Theme().SetDark(true)
				tester.Frame()
			}
			tokens := design.Light()
			if tc.dark {
				tokens = design.Dark()
			}

			title, ok := tester.Find("运行中的会话")
			if !ok {
				t.Fatalf("chosen session title is not in the frame; texts = %v", tester.Texts())
			}
			badge, ok := tester.Find("运行中")
			if !ok {
				t.Fatalf("running badge is not in the frame; texts = %v", tester.Texts())
			}
			img := tester.Image()

			// The chosen row's content starts at the found label's left edge:
			// the 2 DIP accent stripe is its first child, then the row gap and
			// the title's start padding (the message glyph and its text follow).
			barX := int(title.X) + 1
			barY := int(title.Y + title.H/2)
			br, bg, bb, _ := img.At(barX, barY).RGBA()
			bar := [3]uint32{br >> 8, bg >> 8, bb >> 8}
			if !colorNear(bar, rgbOf(tokens.Accent)) {
				t.Fatalf("selection stripe at (%d,%d) = #%02x%02x%02x, want the accent",
					barX, barY, bar[0], bar[1], bar[2])
			}

			// The row fill: the BgSelected tint over the sidebar, not the accent.
			rowX := int(title.X) + 9
			rr, rg, rb, _ := img.At(rowX, barY).RGBA()
			row := [3]uint32{rr >> 8, rg >> 8, rb >> 8}
			rowTint := tokens.BgSelected.Over(tokens.BgSidebar)
			if !colorNear(row, rgbOf(rowTint)) {
				t.Fatalf("chosen row fill at (%d,%d) = #%02x%02x%02x, want the BgSelected tint",
					rowX, barY, row[0], row[1], row[2])
			}

			// The badge keeps the resting neutral fill over the selected tint.
			bx := int(badge.X) - 3
			by := int(badge.Y + badge.H/2)
			xr, xg, xb, _ := img.At(bx, by).RGBA()
			fill := [3]uint32{xr >> 8, xg >> 8, xb >> 8}
			bgFill := tokens.BgHover.Over(rowTint)
			if !colorNear(fill, rgbOf(bgFill)) {
				t.Fatalf("chosen running badge fill = #%02x%02x%02x at (%d,%d), want the neutral badge fill",
					fill[0], fill[1], fill[2], bx, by)
			}
		})
	}
}

// rgbOf turns a token color into its opaque 8-bit channels.
func rgbOf(c ui.Color) [4]uint8 {
	return [4]uint8{c.R, c.G, c.B, c.A}
}
