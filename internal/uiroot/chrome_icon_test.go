package uiroot

// Frame test for the chrome icon buttons' resting look (Phase 2a F-1): the
// collapsed rail's brand/expand control is the one subtle icon button of the
// chrome, so it keeps its light plate at rest instead of blending into the
// rail; the pointer deepens that plate rather than introducing the only fill
// the button ever shows.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// over composites fg over bg as the painter does, for the token fills that are
// translucent tints (brand.Hover/RowSelected are the accent at 6%/12%).
func over(fg, bg ui.Color) ui.Color {
	fa := float32(fg.A) / 255
	blend := func(f, b uint8) uint8 {
		return uint8(float32(f)*fa + float32(b)*(1-fa) + 0.5)
	}
	return ui.RGB(blend(fg.R, bg.R), blend(fg.G, bg.G), blend(fg.B, bg.B))
}

// requirePixelNear asserts the rendered frame paints approximately want at
// (x, y): the painter's 8-bit alpha blend may round one channel by one step.
func requirePixelNear(t *testing.T, tt *ui.Tester, x, y int, want ui.Color) {
	t.Helper()
	pixel := tt.Image().RGBAAt(x, y)
	got := ui.RGB(pixel.R, pixel.G, pixel.B)
	diff := func(a, b uint8) int {
		if a > b {
			return int(a - b)
		}
		return int(b - a)
	}
	if diff(got.R, want.R) > 1 || diff(got.G, want.G) > 1 || diff(got.B, want.B) > 1 {
		t.Fatalf("pixel (%d,%d) = %v, want %v (±1)", x, y, got, want)
	}
}

// TestRailBrandButtonKeepsItsRestingPlate covers F-1: the collapsed 64 DIP
// rail's brand mark is the expand affordance, and it must keep the light plate
// at rest (no pointer) — the unified IconButton ghost look had dropped it.
func TestRailBrandButtonKeepsItsRestingPlate(t *testing.T) {
	r := newTestRoot(t)
	mustScreen(t, r, textScreen("home", "首页", "body-home", 10))
	tt := newTestTester(t, r, 1280, 800)
	tt.Frame()
	r.SetSidebarCollapsed(true)
	tt.Frame()
	tt.Frame()

	info, ok := tt.AccessByRole(ui.RoleButton, "展开侧边栏")
	if !ok {
		t.Fatalf("rail brand/expand control missing; texts: %v", tt.Texts())
	}
	if info.Bounds.W < design.IconSizeL || info.Bounds.H < design.IconSizeL {
		t.Fatalf("rail brand target = %vx%v, want at least %v", info.Bounds.W, info.Bounds.H, design.IconSizeL)
	}
	tokens := design.Light()
	// A point inside the plate but clear of the 24 DIP logo (the target is 32
	// DIP: 4 DIP of fill on each side).
	x, y := int(info.Bounds.X)+2, int(info.Bounds.Y+info.Bounds.H/2)
	requirePixelNear(t, tt, x, y, over(tokens.BgHover, tokens.BgSidebar))
	restPixel := tt.Image().RGBAAt(x, y)
	rest := ui.RGB(restPixel.R, restPixel.G, restPixel.B)
	if rest.R == tokens.BgSidebar.R && rest.G == tokens.BgSidebar.G && rest.B == tokens.BgSidebar.B {
		t.Fatalf("rail brand has no resting plate: pixel (%d,%d) = %v, rail = %v", x, y, rest, tokens.BgSidebar)
	}

	// Hovering deepens the plate (the subtle variant) instead of being the one
	// state that shows it.
	tt.Move(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
	tt.Frame()
	hoverPixel := tt.Image().RGBAAt(x, y)
	hovered := ui.RGB(hoverPixel.R, hoverPixel.G, hoverPixel.B)
	t.Logf("rail brand plate rest=%v hovered=%v (rail=%v)", rest, hovered, tokens.BgSidebar)
	if !(hovered.R < rest.R || hovered.G < rest.G || hovered.B < rest.B) {
		t.Fatalf("hover does not deepen the rail brand plate: rest %v, hovered %v", rest, hovered)
	}
}
