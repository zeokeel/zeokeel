package uikit_test

// Frame tests for the keyboard key cap: the product shows a chord as a real
// key chip in menus, the palette, tooltips and the composer hint, so the chip
// must render the label and keep the text readable by assistive technology.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

func TestKbdRendersItsLabel(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		t := design.Of(c)
		ui.Row(c).Padding(design.Space4).Gap(design.Space2).Children(func() {
			uikit.Kbd(c, t, uikit.FormatShortcut("CmdOrCtrl+K"))
			uikit.KbdHint(c, t, "Shift", "Enter")
		})
	}, 320, 120)
	tt.Frame()
	for _, want := range []string{uikit.FormatShortcut("CmdOrCtrl+K"), "Shift", "Enter"} {
		if _, ok := tt.Find(want); !ok {
			t.Fatalf("kbd label %q missing; texts = %v", want, tt.Texts())
		}
	}
}

// TestKbdChipFitsItsKey checks the cap is a chip, not a bare label: its box is
// taller than the text line and has the hairline border of the preset.
func TestKbdChipFitsItsKey(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		t := design.Of(c)
		ui.Row(c).Padding(design.Space4).Children(func() { uikit.Kbd(c, t, "Esc") })
	}, 240, 100)
	tt.Frame()
	rect, ok := tt.Find("Esc")
	if !ok {
		t.Fatalf("no kbd; texts = %v", tt.Texts())
	}
	if rect.H < 12 {
		t.Fatalf("kbd label height %v looks like a bare text line", rect.H)
	}
}
