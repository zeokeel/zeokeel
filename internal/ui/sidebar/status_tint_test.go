package sidebar_test

// Regression test for AP-21 in the sidebar: the add-workspace success notice is
// a status, so it uses uikit.StatusBadge's anatomy — the Success tint behind the
// Success text — instead of a neutral badge with a status-colored word.

import (
	"image"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/ui/sidebar"
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
	pill := statusPixel(t, img, int(rect.X)-4, int(rect.Y+rect.H/2))
	backdrop := statusPixel(t, img, int(rect.X+rect.W)+12, int(rect.Y+rect.H/2))
	want := fill.Alpha(alpha).Over(backdrop)
	if !statusColorNear(pill, want, 3) {
		t.Fatalf("%q badge fill = %+v, want the status tint %+v over the row %+v (AP-21: neutral badge + status-colored text)",
			label, pill, want, backdrop)
	}
}

// TestWorkspaceAddedNoticeUsesStatusTint covers the header's add-workspace
// success status (shown when the host has no notifier to toast through).
func TestWorkspaceAddedNoticeUsesStatusTint(t *testing.T) {
	workspace := &recordingWorkspace{fakeWorkspace: newFakeWorkspace()}
	h := newHarnessWithWorkspace(t, &fakeChat{}, sidebar.Config{}, workspace)
	overrideSidebarPicker(t, func(string) (string, error) { return "/work/myproj", nil })
	pickFolderFromDialog(t, h)
	tester := h.tester()

	h.assertTexts(tester, "已添加工作区")
	assertStatusBadgeTint(t, tester, "已添加工作区", design.Light().Success, 0.08)
}
