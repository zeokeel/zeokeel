package sidebar_test

// Fix-round regression test for SH-03: a status badge inside a chosen session
// row must stay readable. The chosen row is painted in the accent fill, so the
// resting badge colors land dim on it in the dark theme (measured about 1.2:1);
// the chosen row now uses uikit.OnAccentBadge, whose fill is the on-accent
// foreground. This test pins the light theme: the pill behind the Running label
// must be the on-accent (near white) fill instead of the resting badge tint.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
)

// TestSelectedSessionBadgeUsesOnAccentFill covers SH-03 in both themes.
func TestSelectedSessionBadgeUsesOnAccentFill(t *testing.T) {
	for _, tc := range []struct {
		name string
		dark bool
		fill [3]uint32
	}{
		// Brand light AccentText (white) and dark AccentText (#0F1115 ink on
		// the sky accent): the on-accent foreground of the chosen row.
		{name: "light", fill: [3]uint32{255, 255, 255}},
		{name: "dark", dark: true, fill: [3]uint32{15, 17, 21}},
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

			badge, ok := tester.Find("运行中")
			if !ok {
				t.Fatalf("running badge is not in the frame; texts = %v", tester.Texts())
			}
			// The badge's fill sits just left of its label text (the pill's padding).
			x := int(badge.X) - 3
			y := int(badge.Y + badge.H/2)
			if x < 0 {
				t.Fatalf("badge %+v is at the frame edge; cannot sample its fill", badge)
			}
			img := tester.Image()
			r, g, b, _ := img.At(x, y).RGBA()
			got := [3]uint32{r >> 8, g >> 8, b >> 8}
			want := tc.fill
			if got != want {
				t.Fatalf("selected running badge fill = #%02x%02x%02x at (%d,%d), want the on-accent fill #%02x%02x%02x",
					got[0], got[1], got[2], x, y, want[0], want[1], want[2])
			}
			// And the chosen row itself is still an accent fill, so the sample above
			// really is inside a selected row (light: brand blue, dark: sky).
			rowX, rowY := 20, y
			rr, gg, bb, _ := img.At(rowX, rowY).RGBA()
			row := [3]uint32{rr >> 8, gg >> 8, bb >> 8}
			wantRow := [3]uint32{13, 68, 246}
			if tc.dark {
				wantRow = [3]uint32{60, 187, 250}
			}
			if row != wantRow {
				t.Fatalf("row fill at (%d,%d) is #%02x%02x%02x, want the accent selection fill", rowX, rowY, row[0], row[1], row[2])
			}
		})
	}
}
