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
	pill := statusPixel(t, img, int(rect.X)-4, int(rect.Y+rect.H/2))
	// The 8 DIP gap to the next child is bare row background.
	backdrop := statusPixel(t, img, int(rect.X+rect.W)+12, int(rect.Y+rect.H/2))
	want := fill.Alpha(alpha).Over(backdrop)
	if !statusColorNear(pill, want, 3) {
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
