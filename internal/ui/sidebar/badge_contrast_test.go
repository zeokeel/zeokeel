package sidebar_test

// Regression test for the unified selection language (V1): the chosen session
// row is the quiet persistent selection — the BgSelected 12% tint with a 2px
// Accent left stripe and LabelPrimary text — not mygo's saturated accent fill.
// The row's content and the stripe must stay readable on that tint in both
// themes; the live run detail (spinner + state label) sits at the row end
// without painting a saturated block.

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
// and the running state is carried by the row-end spinner + label rather than a
// saturated badge.
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
			if _, ok := tester.Find("运行中"); !ok {
				t.Fatalf("running state is not in the frame; texts = %v", tester.Texts())
			}
			img := tester.Image()
			barY := int(title.Y + title.H/2)

			// The chosen row's content starts at the row's left edge: the 2 DIP
			// accent stripe is its first child, then the row gap and the title's
			// start padding (the message glyph and its text follow). Scan from
			// the sidebar edge to the title for the first accent pixel, so the
			// assertion does not depend on the current glyph/gap metrics.
			barX := -1
			for x := 0; x < int(title.X); x++ {
				r, g, b, _ := img.At(x, barY).RGBA()
				if colorNear([3]uint32{r >> 8, g >> 8, b >> 8}, rgbOf(tokens.Accent)) {
					barX = x
					break
				}
			}
			if barX < 0 {
				t.Fatalf("no accent selection stripe on the chosen row at y=%d; texts = %v", barY, tester.Texts())
			}

			// The row fill: the BgSelected tint over the sidebar, not the accent.
			rowX := barX + 6
			rr, rg, rb, _ := img.At(rowX, barY).RGBA()
			fill := [3]uint32{rr >> 8, rg >> 8, rb >> 8}
			rowTint := tokens.BgSelected.Over(tokens.BgSidebar)
			if !colorNear(fill, rgbOf(rowTint)) {
				t.Fatalf("chosen row fill at (%d,%d) = #%02x%02x%02x, want the BgSelected tint",
					rowX, barY, fill[0], fill[1], fill[2])
			}
		})
	}
}

// rgbOf turns a token color into its opaque 8-bit channels.
func rgbOf(c ui.Color) [4]uint8 {
	return [4]uint8{c.R, c.G, c.B, c.A}
}
