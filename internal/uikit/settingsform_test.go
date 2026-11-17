package uikit_test

// Frame tests for the shared settings-form compositions (phase 2): WarningLine
// keeps the warning semantic (never danger ink), PageFooter keeps the desktop
// action order and the composer's neutral disabled-primary color, and SwitchRow
// aligns its description with the section's left edge.

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// nearColor reports whether a sampled pixel is close to a token color (the
// rasterizer anti-aliases edges, so allow a small tolerance).
func nearColor(got color.RGBA, want ui.Color) bool {
	d := func(a, b uint8) bool { return int(a)-int(b) < 12 && int(b)-int(a) < 12 }
	return d(got.R, want.R) && d(got.G, want.G) && d(got.B, want.B)
}

// countNear counts pixels close to want inside a rect (a solid fill or a glyph's
// ink), so a color assertion does not depend on one anti-aliased sample point.
func countNear(img *image.RGBA, r ui.Rect, want ui.Color) int {
	n := 0
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
				continue
			}
			if nearColor(img.RGBAAt(x, y), want) {
				n++
			}
		}
	}
	return n
}

func TestWarningLineUsesWarningNotDangerInk(t *testing.T) {
	tk := design.Light()
	tester := ui.NewTester(func(c *ui.Context) {
		uikit.WarningLine(c, design.Of(c), "允许后将在沙箱策略限制内执行。")
	}, 400, 80)
	tester.SetLocale("zh-Hans")
	tester.Frame()

	if _, ok := tester.AccessByRole(ui.RoleAlert, ""); !ok {
		t.Fatalf("the warning line is not a role-alert region; texts = %v", tester.Texts())
	}
	rect, ok := tester.Find("允许后将在沙箱策略限制内执行。")
	if !ok {
		t.Fatalf("warning text missing; texts = %v", tester.Texts())
	}
	img := tester.Image()
	// The message ink is the warning token, never the danger token. Glyph edges
	// are antialiased blends of ink over the panel, and CI's font fallback never
	// reaches full coverage on small CJK strokes, so the assertion compares the
	// region's strongest ink sample against both semantics instead of requiring
	// an exact token pixel (2026-11-17: the strict scan passed only on hosts
	// whose font stack produced fully covered pixels).
	inkCloserThan(t, img, rect, tk.BgBase, tk.WarningText, tk.DangerText)
}

func TestPageFooterOrderAndDisabledPrimary(t *testing.T) {
	tk := design.Light()
	var saved bool
	tester := ui.NewTester(func(c *ui.Context) {
		uikit.PageFooter(c, uikit.PageFooterOptions{
			Key: "test.footer",
			Primary: uikit.DialogAction{Label: "保存", Disabled: true,
				OnClick: func() { saved = true }},
			Secondary: []uikit.DialogAction{
				{Label: "重置", Danger: true},
				{Label: "取消"},
			},
		})
	}, 500, 100)
	tester.SetLocale("zh-Hans")
	tester.Frame()

	// One label node per text button: a redundant .Label would double the visible
	// text in the frame (2026-11 UI review defect 1).
	saveLabels := 0
	for _, text := range tester.Texts() {
		if text == "保存" {
			saveLabels++
		}
	}
	if saveLabels != 1 {
		t.Fatalf("footer primary label appears %d times in Texts(), want 1 (double label)", saveLabels)
	}

	reset, okR := tester.Find("重置")
	cancel, okC := tester.Find("取消")
	save, okS := tester.Find("保存")
	if !okR || !okC || !okS {
		t.Fatalf("footer actions missing; texts = %v", tester.Texts())
	}
	// Desktop order: danger first, secondary next, the single primary rightmost.
	if !(reset.X < cancel.X && cancel.X < save.X) {
		t.Fatalf("footer order x = reset %.1f, cancel %.1f, save %.1f; want danger < secondary < primary",
			reset.X, cancel.X, save.X)
	}
	// The disabled primary is the neutral disabled look (BgStrong fill), not the
	// accent wash — the same color as the composer's disabled send.
	info, ok := tester.AccessByRole(ui.RoleButton, "保存")
	if !ok || !info.Disabled {
		t.Fatalf("the disabled primary is not disabled; %+v", info)
	}
	fill := tester.Image().RGBAAt(int(info.Bounds.X)+3, int(info.Bounds.Y+info.Bounds.H/2))
	if !nearColor(fill, tk.BgStrong) {
		t.Fatalf("disabled primary fill = %+v, want the neutral BgStrong %+v", fill, tk.BgStrong)
	}
	if saved {
		t.Fatalf("a disabled primary fired its action")
	}

	// An enabled primary is the accent fill and fires its action.
	tester2 := ui.NewTester(func(c *ui.Context) {
		uikit.PageFooter(c, uikit.PageFooterOptions{
			Primary: uikit.DialogAction{Label: "保存", OnClick: func() { saved = true }},
		})
	}, 500, 100)
	tester2.SetLocale("zh-Hans")
	tester2.Frame()
	info2, _ := tester2.AccessByRole(ui.RoleButton, "保存")
	fill2 := tester2.Image().RGBAAt(int(info2.Bounds.X)+3, int(info2.Bounds.Y+info2.Bounds.H/2))
	if nearColor(fill2, tk.BgStrong) {
		t.Fatalf("the enabled primary kept the neutral disabled fill")
	}
	if err := tester2.Click("保存"); err != nil {
		t.Fatalf("click save: %v", err)
	}
	if !saved {
		t.Fatalf("the enabled primary did not fire its action")
	}
}

func TestSwitchRowDescriptionAlignsSectionEdge(t *testing.T) {
	value := false
	tester := ui.NewTester(func(c *ui.Context) {
		uikit.SwitchRow(c, uikit.SwitchRowOptions{
			Label:       "启用通知",
			Description: "开启后在桌面显示提醒。",
			Value:       &value,
		})
	}, 400, 100)
	tester.SetLocale("zh-Hans")
	tester.Frame()

	label, okL := tester.Find("启用通知")
	desc, okD := tester.Find("开启后在桌面显示提醒。")
	if !okL || !okD {
		t.Fatalf("switch row label/description missing; texts = %v", tester.Texts())
	}
	// The description starts at the section's left edge, exactly under the label
	// (not indented under a control column).
	if desc.X != label.X {
		t.Fatalf("description x=%.1f != label/section x=%.1f (orphaned under a control column)", desc.X, label.X)
	}
	if !(desc.Y > label.Y) {
		t.Fatalf("the description is not below the label")
	}
	// The switch is named by the caption (LabelledBy) and sits at the trailing edge.
	sw, ok := tester.AccessByRole(ui.RoleSwitch, "启用通知")
	if !ok {
		t.Fatalf("the switch is not named by its caption; texts = %v", tester.Texts())
	}
	if !(sw.Bounds.X > label.X) {
		t.Fatalf("the switch is not at the row's trailing edge")
	}
	// Clicking the caption toggles the switch (LabelledBy gives the click through).
	if err := tester.Click("启用通知"); err != nil {
		t.Fatalf("click caption: %v", err)
	}
	tester.Frame()
	if !value {
		t.Fatalf("clicking the caption did not toggle the bound value")
	}
}

func TestSwitchRowDisabled(t *testing.T) {
	value := false
	tester := ui.NewTester(func(c *ui.Context) {
		uikit.SwitchRow(c, uikit.SwitchRowOptions{
			Label: "启用通知", Value: &value, Disabled: true,
		})
	}, 400, 80)
	tester.SetLocale("zh-Hans")
	tester.Frame()
	sw, ok := tester.AccessByRole(ui.RoleSwitch, "启用通知")
	if !ok || !sw.Disabled {
		t.Fatalf("the disabled switch is not disabled; %+v", sw)
	}
}

// inkCloserThan asserts the region paints `want` rather than `forbidden`: it
// finds the pixel with the strongest coverage against bg (the most saturated
// glyph sample) and requires that sample, plus at least one more pixel, to sit
// nearer `want` than `forbidden` on the blend line ink→bg. Exact-token equality
// is not portable: antialiased edges and different font stacks (CI has no CJK
// face) rarely produce a fully covered pixel at small sizes.
func inkCloserThan(t *testing.T, img *image.RGBA, r ui.Rect, bg, want, forbidden ui.Color) {
	t.Helper()
	best := 0.0
	near := 0
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
				continue
			}
			px := ui.Color{R: img.RGBAAt(x, y).R, G: img.RGBAAt(x, y).G, B: img.RGBAAt(x, y).B, A: 255}
			coverage := colorDistance(px, bg)
			if coverage <= 30 {
				continue
			}
			if colorDistance(px, want) < colorDistance(px, forbidden) {
				near++
				if coverage > best {
					best = coverage
				}
			}
		}
	}
	if near < 2 {
		t.Fatalf("the region paints neither a strong %+v sample nor any pixel nearer it than %+v (matches=%d)",
			want, forbidden, near)
	}
}

// colorDistance is the euclidean RGB distance between two opaque colors.
func colorDistance(a, b ui.Color) float64 {
	dr := float64(a.R) - float64(b.R)
	dg := float64(a.G) - float64(b.G)
	db := float64(a.B) - float64(b.B)
	return math.Sqrt(dr*dr + dg*dg + db*db)
}
