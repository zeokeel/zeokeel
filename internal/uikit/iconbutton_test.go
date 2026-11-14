package uikit_test

// Headless tests for the shared icon button: mygo v0.9.0 has no official icon
// button, so uikit.IconButton is a ButtonBase with the product's one look. The
// tests drive the real interaction (pointer, Tab, Enter/Space, disabled) and
// sample the rendered frame, because the hover/selected fills and the danger
// glyph are the component's contract and the tester has no state getter for
// them.

import (
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// iconButtonView draws one IconButton as a page-level action: a page-colored
// box (so the button's own fill is visible in the frame's pixels) with the
// button at its start.
func iconButtonView(o uikit.IconButtonOptions) func(*ui.Context) {
	return func(c *ui.Context) {
		t := design.Of(c)
		ui.Column(c).FillWidth().FillHeight().Background(t.BgBase).
			Padding(design.Space4).Children(func() {
			uikit.IconButton(c, o)
		})
	}
}

// requireButton returns the accessible box and info of the named button role.
func requireButton(t *testing.T, tt *ui.Tester, role ui.Role, name string) ui.AccessInfo {
	t.Helper()
	info, ok := tt.AccessByRole(role, name)
	if !ok {
		t.Fatalf("frame has no %v named %q; texts = %v", role, name, tt.Texts())
	}
	return info
}

// requirePixel asserts the rendered frame paints want at (x, y).
func requirePixel(t *testing.T, tt *ui.Tester, x, y int, want ui.Color) {
	t.Helper()
	got := tt.Image().RGBAAt(x, y)
	if got != (color.RGBA{R: want.R, G: want.G, B: want.B, A: want.A}) {
		t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, got, want)
	}
}

// iconFillPixel is a point inside the button's own fill but clear of the glyph:
// the inset left of a centered design.IconSize* glyph.
func iconFillPixel(t *testing.T, tt *ui.Tester, role ui.Role, name string) (int, int) {
	t.Helper()
	info := requireButton(t, tt, role, name)
	return int(info.Bounds.X) + 2, int(info.Bounds.Y + info.Bounds.H/2)
}

// over composites fg over bg as the painter does, for the token fills that are
// translucent tints (brand.Hover/RowSelected are the accent at 6%/12%).
func over(fg, bg ui.Color) ui.Color {
	fa := float32(fg.A) / 255
	blend := func(f, b uint8) uint8 {
		return uint8(float32(f)*fa + float32(b)*(1-fa) + 0.5)
	}
	return ui.RGB(blend(fg.R, bg.R), blend(fg.G, bg.G), blend(fg.B, bg.B))
}

// TestIconButtonIsTheNamedButton covers the a11y contract: a glyph-only control
// is a button named by Label, described by Tooltip, clicked by the pointer, and
// its hit target is never below design.IconSizeL.
func TestIconButtonIsTheNamedButton(t *testing.T) {
	clicks := 0
	tt := ui.NewTester(iconButtonView(uikit.IconButtonOptions{
		Key:     "test.close",
		Label:   "关闭右侧栏",
		Tooltip: "关闭右侧栏",
		Icon:    icons.Close(),
		OnClick: func() { clicks++ },
	}), 240, 160)
	tt.Frame()

	info := requireButton(t, tt, ui.RoleButton, "关闭右侧栏")
	if info.Description != "关闭右侧栏" {
		t.Fatalf("tooltip is not the button's description: %+v", info)
	}
	if info.Bounds.W < design.IconSizeL || info.Bounds.H < design.IconSizeL {
		t.Fatalf("hit target = %vx%v, want at least %v", info.Bounds.W, info.Bounds.H, design.IconSizeL)
	}
	if err := tt.Click("关闭右侧栏"); err != nil {
		t.Fatalf("click: %v", err)
	}
	tt.Frame()
	if clicks != 1 {
		t.Fatalf("click calls = %d, want 1", clicks)
	}
}

// TestIconButtonWithoutTooltipAddsNoDescription pins the shell's drawer close:
// a keyboard-raised tooltip is an overlay with its own Escape handler, so a
// button that must not eat the first Escape passes no Tooltip and gets no
// description.
func TestIconButtonWithoutTooltipAddsNoDescription(t *testing.T) {
	tt := ui.NewTester(iconButtonView(uikit.IconButtonOptions{
		Label: "关闭右侧栏",
		Icon:  icons.Close(),
	}), 240, 160)
	tt.Frame()
	info := requireButton(t, tt, ui.RoleButton, "关闭右侧栏")
	if info.Description != "" {
		t.Fatalf("button without a Tooltip describes itself as %q", info.Description)
	}
}

// TestIconButtonKeyboardActivates covers the base's keyboard: Tab focuses the
// button and Enter and Space press it, as they press a native button.
func TestIconButtonKeyboardActivates(t *testing.T) {
	clicks := 0
	tt := ui.NewTester(iconButtonView(uikit.IconButtonOptions{
		Label:   "关闭右侧栏",
		Icon:    icons.Close(),
		OnClick: func() { clicks++ },
	}), 240, 160)
	tt.Frame()

	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if !tt.Focused("关闭右侧栏") {
		t.Fatalf("Tab did not focus the icon button; texts = %v", tt.Texts())
	}
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	if clicks != 1 {
		t.Fatalf("Space calls = %d, want 1", clicks)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if clicks != 2 {
		t.Fatalf("Enter calls = %d, want 2", clicks)
	}
}

// TestIconButtonTargetSizes pins the token-only sizing rule: the glyph is a
// design.IconSize* step (M when unset) and the target adds one 4 DIP inset,
// never below IconSizeL.
func TestIconButtonTargetSizes(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().FillHeight().Padding(design.Space4).Children(func() {
			uikit.IconButton(c, uikit.IconButtonOptions{Key: "small", Label: "小", Icon: icons.Close(), Size: design.IconSizeS})
			uikit.IconButton(c, uikit.IconButtonOptions{Key: "default", Label: "默认", Icon: icons.Close()})
			uikit.IconButton(c, uikit.IconButtonOptions{Key: "large", Label: "大", Icon: icons.Close(), Size: design.IconSizeL})
			uikit.IconButton(c, uikit.IconButtonOptions{Key: "tiny", Label: "极小", Icon: icons.Close(), Size: design.IconSizeS / 2})
		})
	}, 240, 240)
	tt.Frame()

	for _, want := range []struct {
		label string
		size  float32
	}{
		{"小", design.IconSizeS + design.Space2},
		{"默认", design.IconSizeM + design.Space2},
		{"大", design.IconSizeL + design.Space2},
		// A glyph below the tokens still gets the 24 DIP minimum target.
		{"极小", design.IconSizeL},
	} {
		info := requireButton(t, tt, ui.RoleButton, want.label)
		if info.Bounds.W != want.size || info.Bounds.H != want.size {
			t.Fatalf("%s target = %vx%v, want %v", want.label, info.Bounds.W, info.Bounds.H, want.size)
		}
	}
}

// TestIconButtonSelectedIsPressed covers the selected state: the toggle role
// reports it as pressed (a plain button role would drop it) and the button
// paints the selection tint.
func TestIconButtonSelectedIsPressed(t *testing.T) {
	tokens := design.Light()
	tt := ui.NewTester(iconButtonView(uikit.IconButtonOptions{
		Key:      "test.rail.home",
		Label:    "首页",
		Tooltip:  "首页",
		Icon:     icons.NewChat(),
		Toggle:   true,
		Selected: true,
	}), 240, 160)
	tt.Frame()

	info := requireButton(t, tt, ui.RoleToggleButton, "首页")
	if !info.Checked {
		t.Fatalf("selected rail destination does not read as pressed: %+v", info)
	}
	x, y := iconFillPixel(t, tt, ui.RoleToggleButton, "首页")
	requirePixel(t, tt, x, y, over(tokens.BgSelected, tokens.BgBase))

	// An unselected destination reads as not pressed and keeps the page's own
	// fill (the ghost look).
	other := ui.NewTester(iconButtonView(uikit.IconButtonOptions{
		Key:    "test.rail.session",
		Label:  "对话",
		Icon:   icons.Message(),
		Toggle: true,
	}), 240, 160)
	other.Frame()
	if got := requireButton(t, other, ui.RoleToggleButton, "对话"); got.Checked {
		t.Fatalf("unselected rail destination reads as pressed: %+v", got)
	}
	x, y = iconFillPixel(t, other, ui.RoleToggleButton, "对话")
	requirePixel(t, other, x, y, tokens.BgBase)
}

// TestIconButtonHoverAndFocusTint covers the ghost look's interaction states
// with the pointer (Tester.Move) and the keyboard focus.
func TestIconButtonHoverAndFocusTint(t *testing.T) {
	tokens := design.Light()
	tt := ui.NewTester(iconButtonView(uikit.IconButtonOptions{
		Label: "刷新",
		Icon:  icons.Reload(),
	}), 240, 160)
	tt.Frame()

	x, y := iconFillPixel(t, tt, ui.RoleButton, "刷新")
	requirePixel(t, tt, x, y, tokens.BgBase)

	info := requireButton(t, tt, ui.RoleButton, "刷新")
	tt.Move(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
	tt.Frame()
	requirePixel(t, tt, x, y, over(tokens.BgHover, tokens.BgBase))

	// With the pointer away, the keyboard focus alone keeps the hover tint;
	// the theme's focus ring is the base's own (ButtonBase rings the focus).
	tt.Move(0, 0)
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if !tt.Focused("刷新") {
		t.Fatalf("Tab did not focus the icon button; texts = %v", tt.Texts())
	}
	requirePixel(t, tt, x, y, over(tokens.BgHover, tokens.BgBase))
}

// TestIconButtonSubtleKeepsAQuietFill covers the subtle variant: it shows a
// quiet fill at rest instead of the bare page, and the pointer deepens it
// (the collapsed rail's brand button relies on the resting fill).
func TestIconButtonSubtleKeepsAQuietFill(t *testing.T) {
	tokens := design.Light()
	tt := ui.NewTester(iconButtonView(uikit.IconButtonOptions{
		Label:   "复制",
		Icon:    icons.Copy(),
		Variant: uikit.IconButtonSubtle,
	}), 240, 160)
	tt.Frame()
	x, y := iconFillPixel(t, tt, ui.RoleButton, "复制")
	requirePixel(t, tt, x, y, over(tokens.BgHover, tokens.BgBase))

	info := requireButton(t, tt, ui.RoleButton, "复制")
	tt.Move(info.Bounds.X+info.Bounds.W/2, info.Bounds.Y+info.Bounds.H/2)
	tt.Frame()
	requirePixel(t, tt, x, y, over(tokens.BgSelected, tokens.BgBase))
}

// TestIconButtonDangerColorsTheGlyph covers the danger variant: it changes the
// glyph, not the interaction fills, so the danger color never becomes a wash.
func TestIconButtonDangerColorsTheGlyph(t *testing.T) {
	ghost := ui.NewTester(iconButtonView(uikit.IconButtonOptions{
		Label: "取消运行",
		Icon:  icons.Close(),
	}), 240, 160)
	ghost.Frame()
	danger := ui.NewTester(iconButtonView(uikit.IconButtonOptions{
		Label:   "取消运行",
		Icon:    icons.Close(),
		Variant: uikit.IconButtonDanger,
	}), 240, 160)
	danger.Frame()

	info := requireButton(t, danger, ui.RoleButton, "取消运行")
	// The difference lives inside the target's glyph area: count the pixels the
	// two variants disagree on.
	diff := 0
	ghostImage, dangerImage := ghost.Image(), danger.Image()
	for y := int(info.Bounds.Y); y < int(info.Bounds.Y+info.Bounds.H); y++ {
		for x := int(info.Bounds.X); x < int(info.Bounds.X+info.Bounds.W); x++ {
			if ghostImage.RGBAAt(x, y) != dangerImage.RGBAAt(x, y) {
				diff++
			}
		}
	}
	if diff == 0 {
		t.Fatal("the danger variant paints exactly the ghost glyph")
	}
	x, y := iconFillPixel(t, danger, ui.RoleButton, "取消运行")
	requirePixel(t, danger, x, y, design.Light().BgBase)
}

// TestIconButtonDisabledTakesNoInput covers the disabled state: no clicks and no
// keyboard focus.
func TestIconButtonDisabledTakesNoInput(t *testing.T) {
	clicks := 0
	tt := ui.NewTester(iconButtonView(uikit.IconButtonOptions{
		Label:    "刷新",
		Icon:     icons.Reload(),
		Disabled: true,
		OnClick:  func() { clicks++ },
	}), 240, 160)
	tt.Frame()

	info := requireButton(t, tt, ui.RoleButton, "刷新")
	if !info.Disabled {
		t.Fatalf("disabled icon button does not read as disabled: %+v", info)
	}
	// The base dims and ignores the press: the pointer target may still be the
	// button's box, but the button reports no click.
	if err := tt.Click("刷新"); err != nil {
		t.Fatalf("click: %v", err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if tt.Focused("刷新") {
		t.Fatal("a disabled icon button took the keyboard focus")
	}
	if clicks != 0 {
		t.Fatalf("disabled button ran its action %d times", clicks)
	}
}

// TestIconButtonContentBuildsTheInside covers the shell's brand button: the
// rail's mark is a bitmap logo, not an SVG icon, so Content draws the inside.
func TestIconButtonContentBuildsTheInside(t *testing.T) {
	tt := ui.NewTester(iconButtonView(uikit.IconButtonOptions{
		Key:     "test.brand",
		Label:   "展开侧边栏",
		Tooltip: "展开侧边栏",
		Content: func(c *ui.Context) {
			ui.Text(c, "mark")
		},
	}), 240, 160)
	tt.Frame()
	if !tt.HasText("mark") {
		t.Fatalf("the button's Content did not render; texts = %v", tt.Texts())
	}
	requireButton(t, tt, ui.RoleButton, "展开侧边栏")
}

// TestIconButtonInkColorOverridesTheGlyph covers the additive InkColor option
// (audit PN-01): a selected List row paints the accent fill, so an icon action
// on it passes Tokens.AccentText as its ink. The zero value must keep the
// variant's existing ink exactly (the option is additive), and a non-zero color
// must reach the glyph.
func TestIconButtonInkColorOverridesTheGlyph(t *testing.T) {
	ghost := ui.NewTester(iconButtonView(uikit.IconButtonOptions{
		Label: "任务操作",
		Icon:  icons.More(),
	}), 240, 160)
	ghost.Frame()
	same := ui.NewTester(iconButtonView(uikit.IconButtonOptions{
		Label:    "任务操作",
		Icon:     icons.More(),
		InkColor: ui.Color{}, // the zero color: no override
	}), 240, 160)
	same.Frame()
	ghostImage, sameImage := ghost.Image(), same.Image()
	for y := 0; y < 160; y++ {
		for x := 0; x < 240; x++ {
			if ghostImage.RGBAAt(x, y) != sameImage.RGBAAt(x, y) {
				t.Fatalf("the zero InkColor changed the glyph at (%d,%d): %v != %v",
					x, y, sameImage.RGBAAt(x, y), ghostImage.RGBAAt(x, y))
			}
		}
	}

	// The on-accent case: the button sits on the accent fill (a selected List
	// row), so the page background is the accent color.
	onAccent := func(o uikit.IconButtonOptions) func(*ui.Context) {
		return func(c *ui.Context) {
			t := design.Of(c)
			ui.Column(c).FillWidth().FillHeight().Background(t.Accent).
				Padding(design.Space4).Children(func() {
				uikit.IconButton(c, o)
			})
		}
	}
	ink := design.Light().AccentText
	colored := ui.NewTester(onAccent(uikit.IconButtonOptions{
		Label:    "任务操作",
		Icon:     icons.More(),
		InkColor: ink,
	}), 240, 160)
	colored.Frame()
	info := requireButton(t, colored, ui.RoleButton, "任务操作")
	found := 0
	for y := int(info.Bounds.Y); y < int(info.Bounds.Y+info.Bounds.H); y++ {
		for x := int(info.Bounds.X); x < int(info.Bounds.X+info.Bounds.W); x++ {
			got := colored.Image().RGBAAt(x, y)
			if got.R == ink.R && got.G == ink.G && got.B == ink.B && got.A == ink.A {
				found++
			}
		}
	}
	if found == 0 {
		t.Fatalf("the non-zero InkColor %v never painted inside the button %+v", ink, info.Bounds)
	}
	// The default (ghost) ink cannot paint that exact accent ink on the accent
	// fill; only the override does.
	resting := ui.NewTester(onAccent(uikit.IconButtonOptions{
		Label: "任务操作",
		Icon:  icons.More(),
	}), 240, 160)
	resting.Frame()
	restingInfo := requireButton(t, resting, ui.RoleButton, "任务操作")
	for y := int(restingInfo.Bounds.Y); y < int(restingInfo.Bounds.Y+restingInfo.Bounds.H); y++ {
		for x := int(restingInfo.Bounds.X); x < int(restingInfo.Bounds.X+restingInfo.Bounds.W); x++ {
			got := resting.Image().RGBAAt(x, y)
			if got.R == ink.R && got.G == ink.G && got.B == ink.B && got.A == ink.A {
				t.Fatalf("the resting glyph already paints the accent ink at (%d,%d); the test cannot tell the states apart", x, y)
			}
		}
	}
}
