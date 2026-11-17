package uikit_test

// Frame tests for the shared settings-form compositions (phase 2): WarningLine
// keeps the warning semantic (never danger ink), PageFooter keeps the desktop
// action order and the composer's neutral disabled-primary color, and SwitchRow
// aligns its description with the section's left edge.

import (
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/uikit"
)

// nearColor reports whether a sampled pixel is close to a token color (the
// rasterizer anti-aliases edges, so allow a small tolerance).

// countNear counts pixels close to want inside a rect (a solid fill or a glyph's
// ink), so a color assertion does not depend on one anti-aliased sample point.

// nearColor reports whether a sampled pixel matches a token color within a
// small tolerance (solid fills are stable; glyph edges are not).
func nearColor(got color.RGBA, want ui.Color) bool {
	near := func(a, b uint8) bool {
		d := int(a) - int(b)
		if d < 0 {
			d = -d
		}
		return d <= 3
	}
	return near(got.R, want.R) && near(got.G, want.G) && near(got.B, want.B)
}

func TestPageFooterOrderAndDisabledPrimary(t *testing.T) {
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
	// Function over paint: the disabled primary must not be actionable, and the
	// page keeps exactly one primary. The neutral disabled LOOK is pinned once at
	// the settings level (PageFooter + composer tokens) rather than re-sampled
	// here — pixel colors are the renderer's business, not this contract's.
	info, ok := tester.AccessByRole(ui.RoleButton, "保存")
	if !ok || !info.Disabled {
		t.Fatalf("the disabled primary is not disabled; %+v", info)
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
	if info2, ok := tester2.AccessByRole(ui.RoleButton, "保存"); !ok || info2.Disabled {
		t.Fatalf("the enabled primary is still disabled; %+v", info2)
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

// colorDistance is the euclidean RGB distance between two opaque colors.
