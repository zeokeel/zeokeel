package rightpanel_test

// Regression test for AP-21 in the Files page: the deliverables section's
// success notice is a status, so it uses uikit.StatusBadge's anatomy — the
// Success tint behind the Success text — instead of a neutral badge with a
// status-colored word.

import (
	"image"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
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

// tintScore ranks how far a pixel sits from the neutral row background toward
// the status fill: a tinted pixel is greener than the row it sits on, an
// anti-aliased edge is only half way there.
func tintScore(c ui.Color) int {
	return int(c.G) - (int(c.R)+int(c.B))/2
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
	// The badge's horizontal padding is Space2; sample inside its left edge.
	// The canvas' text metrics differ between machines (the CI runner's font
	// stack is not this one), so the text rect—and with it the exact pixel—can
	// move a DIP or two and land on the pill's anti-aliased edge. Take the most
	// saturated of a short horizontal run inside the pill instead of one pixel:
	// the fill is the tinted extreme, the edges are blends toward the row.
	mid := int(rect.Y + rect.H/2)
	var pill ui.Color
	best := -1
	for dx := -7; dx <= -1; dx++ {
		candidate := statusPixel(t, img, int(rect.X)+dx, mid)
		if score := tintScore(candidate); score > best {
			pill, best = candidate, score
		}
	}
	// The 8 DIP gap to the next child is bare row background.
	backdrop := statusPixel(t, img, int(rect.X+rect.W)+12, mid)
	want := fill.Alpha(alpha).Over(backdrop)
	// Two independent checks, because a single-pixel blend is machine
	// dependent: the sampled fill must read as a status tint at all (a neutral
	// badge keeps the row's hue, so its green channel does not lead), and it
	// must sit within a small distance of the token blend. The first check is
	// what AP-21 is about; the second keeps the alpha honest.
	if tintScore(pill) < 5 || !statusColorNear(pill, want, 12) {
		t.Fatalf("%q badge fill = %+v, want the status tint %+v over the row %+v (AP-21: neutral badge + status-colored text)",
			label, pill, want, backdrop)
	}
}

// TestDeliverableSavedNoticeUsesStatusTint covers the delivered-file "已保存"
// notice: it is the download's success status, so it reads as a StatusBadge.
func TestDeliverableSavedNoticeUsesStatusTint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact.md")
	withArtifactSaver(t, func(mygo.SaveDialogOptions) (string, error) { return path, nil })
	history := []contracts.Artifact{
		artifactAt("a1", "巡检 报告", "# 内容", time.Unix(1700000000, 0)),
	}
	h := newArtifactsHarness(t, &fakeArtifactService{history: history})
	tester := h.tester()
	h.openPage(tester, "文件")

	h.click(tester, "下载")
	h.assertTexts(tester, "已保存")
	assertStatusBadgeTint(t, tester, "已保存", design.Light().Success, 0.08)
}
