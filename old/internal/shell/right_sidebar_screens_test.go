package shell

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestRightSidebarWorksOnEveryHostScreen is the live regression for the panel
// being docked on Plugins and Automation: contentWidth must reserve its width,
// and New tab/Split/Fullscreen/Collapse must have real boxes inside the window
// at both the 1600px design size and the 900px minimum. Fullscreen must cover
// the whole content column and stop building the screen behind it.
func TestRightSidebarWorksOnEveryHostScreen(t *testing.T) {
	cases := []struct {
		screen Screen
		name   string
		// behind is a string only the screen behind the panel builds, so
		// fullscreen can prove it is not built at all.
		behind string
	}{
		// Home's behind-the-panel text is the brand tagline now; the retired
		// DSH hero copy was removed with the native Home redesign.
		{Home, "home", "What will you build today?"},
		{Session, "session", "Composer input"},
		{Plugins, "plugins", "Install, enable, and configure plugins"},
		{Automation, "automation", "No tasks yet. Tasks created in your sessions appear here."},
	}
	for _, c := range cases {
		for _, size := range [][2]int{{1600, 1000}, {900, 600}} {
			t.Run(fmt.Sprintf("%s-%dx%d", c.name, size[0], size[1]), func(t *testing.T) {
				h := newLiveApp(t, &shellProvider{})
				h.app.Language = LangEN
				h.app.Screen = c.screen
				tt := ui.NewTester(h.app.View, size[0], size[1])
				tt.Frame()
				if !tt.HasText(c.behind) {
					t.Fatalf("%s: setup lost the screen content %q: %q", c.name, c.behind, tt.Texts())
				}

				// The advertised Cmd/Ctrl+Alt+B shortcut opens the panel on
				// every screen the panel is docked on.
				tt.Key(ui.Cmd|ui.Alt, ui.KeyB)
				tt.Frame()
				if !h.app.rightOpen {
					t.Fatalf("%s %dx%d: Cmd/Ctrl+Alt+B did not open the panel", c.name, size[0], size[1])
				}
				for _, label := range []string{"New tab", "Split", "Fullscreen", "Collapse right sidebar"} {
					assertRectInside(t, tt, label, size[0], size[1])
				}

				// The docked panel is a real column, not an overlay spilling
				// past the window.
				panel := assertRectInside(t, tt, "Right sidebar", size[0], size[1])
				if panel.W < minRightPanelW-1 {
					t.Fatalf("%s %dx%d: panel width = %v, want at least %v", c.name, size[0], size[1], panel.W, minRightPanelW)
				}
				if panel.X < sidebarW-1 {
					t.Fatalf("%s %dx%d: panel starts at x=%v, inside the sidebar", c.name, size[0], size[1], panel.X)
				}
				if panel.X+panel.W > float32(size[0])+1 {
					t.Fatalf("%s %dx%d: panel right edge = %v, outside the window", c.name, size[0], size[1], panel.X+panel.W)
				}

				if c.screen == Plugins {
					for _, label := range []string{"Refresh", "Add plugin"} {
						r := assertRectInside(t, tt, label, size[0], size[1])
						if r.X+r.W > panel.X+1 {
							t.Fatalf("%s %dx%d: %q right edge = %v overlaps the panel at %v",
								c.name, size[0], size[1], label, r.X+r.W, panel.X)
						}
					}
				}

				// Fullscreen takes the whole content column and hides the
				// screen behind it.
				if err := tt.Click("Fullscreen"); err != nil {
					t.Fatalf("%s %dx%d: click Fullscreen: %v", c.name, size[0], size[1], err)
				}
				tt.Frame()
				if !h.app.rightSidebarMaximized() {
					t.Fatalf("%s %dx%d: Fullscreen did not maximize the panel", c.name, size[0], size[1])
				}
				panel = assertRectInside(t, tt, "Right sidebar", size[0], size[1])
				if panel.X > sidebarW+1 || panel.X+panel.W < float32(size[0])-1 {
					t.Fatalf("%s %dx%d: fullscreen panel = %+v, want the content column [%v,%d]",
						c.name, size[0], size[1], panel, sidebarW, size[0])
				}
				if tt.HasText(c.behind) {
					t.Fatalf("%s %dx%d: Fullscreen still built the screen behind the panel (%q)", c.name, size[0], size[1], c.behind)
				}
				if err := tt.Click("Exit fullscreen"); err != nil {
					t.Fatalf("%s %dx%d: exit fullscreen: %v", c.name, size[0], size[1], err)
				}
				tt.Frame()

				// Split is a real half/half pane pair, so the panel gets half
				// the content column instead of its docked width.
				if err := tt.Click("Split"); err != nil {
					t.Fatalf("%s %dx%d: click Split: %v", c.name, size[0], size[1], err)
				}
				tt.Frame()
				if !h.app.rightSidebarSplit() {
					t.Fatalf("%s %dx%d: Split did not enable split mode", c.name, size[0], size[1])
				}
				panel = assertRectInside(t, tt, "Right sidebar", size[0], size[1])
				half := (float32(size[0]) - sidebarW) / 2
				if panel.W < half-3 || panel.W > half+3 {
					t.Fatalf("%s %dx%d: split panel width = %v, want about %v", c.name, size[0], size[1], panel.W, half)
				}
				if err := tt.Click("Split"); err != nil {
					t.Fatalf("%s %dx%d: toggle Split: %v", c.name, size[0], size[1], err)
				}
				tt.Frame()

				if err := tt.Click("Collapse right sidebar"); err != nil {
					t.Fatalf("%s %dx%d: collapse: %v", c.name, size[0], size[1], err)
				}
				tt.Frame()
				if h.app.rightOpen {
					t.Fatalf("%s %dx%d: Collapse left the panel open", c.name, size[0], size[1])
				}
			})
		}
	}
}

// TestRightSidebarShortcutsOnPluginsAndAutomation covers Cmd/Ctrl+P and Ctrl+`
// on the screens the panel is now docked on; they used to be no-ops there
// although the shortcut reference advertises them.
func TestRightSidebarShortcutsOnPluginsAndAutomation(t *testing.T) {
	for _, screen := range []struct {
		screen Screen
		name   string
	}{{Plugins, "plugins"}, {Automation, "automation"}} {
		t.Run(screen.name, func(t *testing.T) {
			h := newLiveApp(t, &shellProvider{})
			h.app.Language = LangEN
			h.app.Screen = screen.screen
			tt := ui.NewTester(h.app.View, 1600, 1000)
			tt.Frame()

			tt.Key(ui.Cmd, ui.KeyP)
			tt.Frame()
			if !h.app.rightOpen || h.app.rightTab != filesTab {
				t.Fatalf("%s Cmd/Ctrl+P: open=%v tab=%v, want Files", screen.name, h.app.rightOpen, h.app.rightTab)
			}
			tt.Key(ui.Cmd|ui.Alt, ui.KeyB)
			tt.Frame()
			if h.app.rightOpen {
				t.Fatalf("%s Cmd/Ctrl+Alt+B did not close the panel", screen.name)
			}
			tt.Key(ui.Ctrl, ui.KeyBackquote)
			tt.Frame()
			if !h.app.rightOpen || h.app.rightTab != terminalTab {
				t.Fatalf("%s Ctrl+`: open=%v tab=%v, want Terminal", screen.name, h.app.rightOpen, h.app.rightTab)
			}
		})
	}
}

// TestSettingsOverlayKeepsRightSidebarTab covers the modal resetting the
// panel to Start: Settings is an overlay, so it must not count as a screen
// change for the right panel's per-screen page.
func TestSettingsOverlayKeepsRightSidebarTab(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Home
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(ui.Cmd|ui.Alt, ui.KeyB)
	tt.Frame()
	if !h.app.rightOpen {
		t.Fatal("right sidebar did not open")
	}
	if err := tt.Click("Files"); err != nil {
		t.Fatalf("click Files: %v", err)
	}
	if h.app.rightTab != filesTab {
		t.Fatalf("rightTab = %v, want Files", h.app.rightTab)
	}

	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if h.app.Screen != Settings {
		t.Fatalf("screen = %v, want Settings", h.app.Screen)
	}
	if h.app.rightTab != filesTab {
		t.Fatalf("opening Settings reset the panel tab to %v, want Files", h.app.rightTab)
	}

	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if h.app.Screen != Home {
		t.Fatalf("Esc returned to %v, want Home", h.app.Screen)
	}
	if h.app.rightTab != filesTab {
		t.Fatalf("closing Settings reset the panel tab to %v, want Files", h.app.rightTab)
	}
}

// TestFullscreenRightSidebarKeepsSettingsOverlay proves the maximized
// early-return does not skip the Settings modal: it is painted after the
// layout row, over the fullscreen panel.
func TestFullscreenRightSidebarKeepsSettingsOverlay(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	h.app.Screen = Plugins
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	tt.Key(ui.Cmd|ui.Alt, ui.KeyB)
	tt.Frame()
	if err := tt.Click("Fullscreen"); err != nil {
		t.Fatalf("click Fullscreen: %v", err)
	}
	tt.Frame()
	if !h.app.rightSidebarMaximized() {
		t.Fatal("Fullscreen did not maximize")
	}

	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if h.app.Screen != Settings {
		t.Fatalf("screen = %v, want Settings", h.app.Screen)
	}
	for _, label := range []string{"Settings", "General", "Close settings"} {
		assertRectInside(t, tt, label, 1600, 1000)
	}
	if !h.app.rightSidebarMaximized() {
		t.Fatal("opening Settings dropped fullscreen")
	}

	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if h.app.Screen != Plugins {
		t.Fatalf("Esc returned to %v, want Plugins", h.app.Screen)
	}
	if !h.app.rightSidebarMaximized() {
		t.Fatal("closing Settings dropped fullscreen")
	}
	for _, label := range []string{"New tab", "Exit fullscreen", "Collapse right sidebar"} {
		assertRectInside(t, tt, label, 1600, 1000)
	}
}
