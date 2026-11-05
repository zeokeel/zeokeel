package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestStaticSidebarGeometry pins the baseline's 14px sidebar inset and row
// widths, and the vertical rhythm that the old trailing column padding added
// 16px to: New Session y=70, nav y=120, Workspaces y=206 (label 214),
// session rows y=282/316.
func TestStaticSidebarGeometry(t *testing.T) {
	app := &App{Screen: Home}
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()

	newSession, ok := tt.Find("New Session")
	if !ok {
		t.Fatalf("New Session button not found: %q", tt.Texts())
	}
	if newSession.X != 14 || newSession.Y != 70 || newSession.W != 252 || newSession.H != 38 {
		t.Errorf("New Session rect = %+v, want x=14 y=70 w=252 h=38", newSession)
	}
	row, ok := tt.Find("Untitled")
	if !ok {
		t.Fatalf("session row not found: %q", tt.Texts())
	}
	if row.X != 12 || row.Y != 316 || row.W != 256 || row.H != 32 {
		t.Errorf("session row rect = %+v, want x=12 y=316 w=256 h=32", row)
	}

	for label, wantY := range map[string]float32{
		"Plugins":          120,
		"Automation tasks": 160,
		"Workspaces":       214,
	} {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("%q not found: %q", label, tt.Texts())
		}
		if r.Y != wantY {
			t.Errorf("%q y = %v, want %v", label, r.Y, wantY)
		}
	}
	now, ok := tt.Find("now")
	if !ok {
		t.Fatalf("selected row timestamp missing: %q", tt.Texts())
	}
	if now.Y != 290 {
		t.Errorf("selected row timestamp y = %v, want 290 (row 282 + 8)", now.Y)
	}
	if now.X < 220 || now.X+now.W > 260.5 {
		t.Errorf("timestamp rect = %+v, want right-aligned before the 260px row edge", now)
	}

	// The nav pill is the baseline's 252x36 at x=14..266; the sidebar's 1px
	// right border must not eat a pixel from the symmetric 14px padding.
	for _, label := range []string{"Plugins", "Automation tasks"} {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("%q nav row missing", label)
		}
		if r.X != 14 || r.W != 252 {
			t.Errorf("%q nav row rect = %+v, want x=14 w=252", label, r)
		}
	}

	// The 16px leading status slot must push the title to x=40: the dot is
	// left of that, so scan the title area only.
	img := tt.Image()
	first := 1000
	for y := 320; y < 344; y++ {
		for x := 36; x < 80; x++ {
			c := img.RGBAAt(x, y)
			if c.R < 200 && c.G < 200 && c.B < 200 && x < first {
				first = x
			}
		}
	}
	if first < 39 || first > 42 {
		t.Errorf("session title first glyph x = %d, want 40 (16px leading slot)", first)
	}
}

// TestWorkspaceHeaderTargets checks the 28x28 hit areas and the baseline's
// right-aligned positions (176/208/240, ending at x=268), plus the y the
// header rows actually start at (nav 120, header 206, targets 210).
func TestWorkspaceHeaderTargets(t *testing.T) {
	app := &App{Screen: Home}
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	wantX := map[string]float32{"Search sessions": 176, "View options": 208, "Add workspace": 240}
	for label, x := range wantX {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("%q not found: %q", label, tt.Texts())
		}
		if r.W != 28 || r.H != 28 {
			t.Errorf("%q target = %vx%v, want 28x28", label, r.W, r.H)
		}
		if r.X != x {
			t.Errorf("%q x = %v, want %v", label, r.X, x)
		}
		if r.Y != 210 {
			t.Errorf("%q y = %v, want 210", label, r.Y)
		}
		if r.X+r.W > 268.5 {
			t.Errorf("%q right edge = %v, want <= 268", label, r.X+r.W)
		}
	}
}

// TestSelectedWorkspaceKeepsSidebarFill covers DSH's active state: the
// selected workspace row stays on the sidebar color and only its folder glyph
// turns accent; the selected session row carries the hover tint.
func TestSelectedWorkspaceKeepsSidebarFill(t *testing.T) {
	app := &App{Screen: Home}
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	img := tt.Image()
	if c := img.RGBAAt(150, 262); c.R != 0xF9 || c.G != 0xFA || c.B != 0xFB {
		t.Errorf("selected workspace row fill = %v, want sidebar #F9FAFB (no selected tint)", c)
	}
	accent := false
	for y := 248; y < 278 && !accent; y++ {
		for x := 16; x < 40; x++ {
			c := img.RGBAAt(x, y)
			if c.R < 120 && c.G < 170 && c.B > 180 {
				accent = true
				break
			}
		}
	}
	if !accent {
		t.Error("selected workspace folder is not drawn in the accent color")
	}
}

// TestStaticSessionRowsAreFlat checks the unselected row fill is the sidebar
// color, not the Button widget's white surface + border that the old baseline
// mask hid.
func TestStaticSessionRowsAreFlat(t *testing.T) {
	app := &App{Screen: Home}
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	selected, ok := tt.Find("now")
	if !ok {
		t.Fatalf("selected row timestamp missing: %q", tt.Texts())
	}
	unselected, ok := tt.Find("Untitled")
	if !ok {
		t.Fatalf("unselected row missing: %q", tt.Texts())
	}
	img := tt.Image()
	if c := img.RGBAAt(150, int(selected.Y+selected.H/2)); c.R < 230 || c.G < 232 || c.B < 234 {
		t.Errorf("selected row fill = %v, want the BgHover tint over the sidebar", c)
	}
	if c := img.RGBAAt(150, int(unselected.Y+unselected.H/2)); c.R != 0xF9 || c.G != 0xFA || c.B != 0xFB {
		t.Errorf("unselected row fill = %v, want the sidebar #F9FAFB", c)
	}
}

// TestNewSessionButtonTokens pins the baseline's primary-button edge and
// weight: BorderL3 renders #E0E0E0 on white (BorderL2 was #E5E5E5) and the
// 14px label is weight 500, dense enough that a 400 regression drops the
// dark-pixel count.
func TestNewSessionButtonTokens(t *testing.T) {
	app := &App{Screen: Home}
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	img := tt.Image()
	if c := img.RGBAAt(140, 70); c.R != 0xE0 || c.G != 0xE0 || c.B != 0xE0 {
		t.Errorf("New Session top border = %v, want #E0E0E0 (BorderL3)", c)
	}
	if c := img.RGBAAt(140, 71); c.R != 0xFF || c.G != 0xFF || c.B != 0xFF {
		t.Errorf("New Session fill = %v, want #FFFFFF (BgStrong)", c)
	}

	dark := 0
	for y := 78; y < 102; y++ {
		for x := 100; x < 220; x++ {
			c := img.RGBAAt(x, y)
			if c.R < 128 && c.G < 128 && c.B < 128 {
				dark++
			}
		}
	}
	if dark < 225 {
		t.Errorf("New Session label has %d dark pixels, want >= 225 for weight 500", dark)
	}
}
