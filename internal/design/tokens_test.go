package design

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestBaselineTokens(t *testing.T) {
	l := Light()
	if l.BgBase != ui.Hex("#FFFFFF") {
		t.Errorf("bg base: %v", l.BgBase)
	}
	if l.BgSidebar != ui.Hex("#F9FAFB") {
		t.Errorf("sidebar: %v", l.BgSidebar)
	}
	if l.LabelPrimary != ui.Hex("#0F1115") {
		t.Errorf("label primary: %v", l.LabelPrimary)
	}
	d := Dark()
	if !d.Dark || d.BgBase != ui.Hex("#151517") {
		t.Errorf("dark base: %+v", d)
	}
	if l.Accent != ui.Hex("#4176E6") {
		t.Errorf("light accent = %v, want #4176E6 (baseline rgb(65,118,230))", l.Accent)
	}
	if d.Accent != ui.Hex("#7AAAFF") {
		t.Errorf("dark accent = %v, want #7AAAFF (baseline rgb(122,170,255))", d.Accent)
	}
	if d.BgHover != ui.RGBA(255, 255, 255, 0.08) {
		t.Errorf("dark hover = %v, want white 0.08", d.BgHover)
	}
	if d.BorderL2 != ui.RGBA(255, 255, 255, 0.12) || d.BorderL3 != ui.RGBA(255, 255, 255, 0.16) {
		t.Errorf("dark borders = %v/%v, want 0.12/0.16", d.BorderL2, d.BorderL3)
	}
	if d.BgRaised != ui.Hex("#2C2C2E") || d.BgStrong != ui.Hex("#43454A") {
		t.Errorf("dark surfaces = %v/%v, want #2C2C2E/#43454A", d.BgRaised, d.BgStrong)
	}
	if d.OnPrimary != ui.Hex("#151517") || l.OnPrimary != ui.Hex("#FFFFFF") {
		t.Errorf("on-primary = %v/%v, want #151517/#FFFFFF", d.OnPrimary, l.OnPrimary)
	}
	// The modal scrim is theme-aware: DSH's light backdrop is 0.24 black and
	// its dark one is 0.5 (baseline 104-settings-dark).
	if l.Scrim() != ui.RGBA(0, 0, 0, 0.24) {
		t.Errorf("light scrim = %v, want black 0.24", l.Scrim())
	}
	if d.Scrim() != ui.RGBA(0, 0, 0, 0.5) {
		t.Errorf("dark scrim = %v, want black 0.5", d.Scrim())
	}
}

func TestTypeScale(t *testing.T) {
	cases := []struct {
		role   TypeRole
		size   float32
		line   float32
		weight int
	}{
		{TypeXL, 24, 32, 400},
		{TypeM, 16, 24, 400},
		{TypeBody, 14, 22, 400},
		{TypeXS, 13, 20, 400},
		{TypeXXS, 12, 18, 400},
	}
	for _, c := range cases {
		size, line, weight := TypeMetrics(c.role)
		if size != c.size || line != c.line || weight != c.weight {
			t.Errorf("role %d: got %v/%v/%d", c.role, size, line, weight)
		}
	}
}

func TestSpacingGrid(t *testing.T) {
	if Space1 != 4 || Space2 != 8 || Space4 != 16 || Space6 != 24 {
		t.Fatalf("space scale: %v %v %v %v", Space1, Space2, Space4, Space6)
	}
}
