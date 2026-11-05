package shell

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// TestSidebarDividerUsesBorderL3 pins the 1px sidebar edge to BorderL3:
// rgba(0,0,0,0.12) in the light theme (pixel 218/219 on #F9FAFB) and
// rgba(255,255,255,0.16) in dark (63 on #1B1B1C). BorderL2 left the light
// edge one to two steps too pale and the dark edge too dark.
func TestSidebarDividerUsesBorderL3(t *testing.T) {
	check := func(t *testing.T, tokens *design.Tokens, img *ui.Tester) {
		t.Helper()
		want := rgbaOf(tokens.BorderL3.Over(tokens.BgSidebar))
		got := img.Image().RGBAAt(279, 450)
		if got != want {
			t.Fatalf("sidebar divider at (279,450) = %v, want %v (BorderL3 over BgSidebar)", got, want)
		}
		// The main content starts at x=280; the divider must not spill into it.
		wantMain := rgbaOf(tokens.BgBase)
		if got := img.Image().RGBAAt(280, 450); got != wantMain {
			t.Fatalf("main content at (280,450) = %v, want BgBase %v", got, wantMain)
		}
	}

	t.Run("light", func(t *testing.T) {
		app := &App{Screen: Home}
		tt := ui.NewTester(app.View, 1600, 1000)
		tt.Frame()
		check(t, design.Light(), tt)
	})
	t.Run("dark", func(t *testing.T) {
		app := &App{Screen: Home}
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(design.Dark().Theme())
			app.View(c)
		}, 1600, 1000)
		tt.Frame()
		check(t, design.Dark(), tt)
	})
}
