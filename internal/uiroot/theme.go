package uiroot

import (
	"sync"

	"github.com/egoist/mygo/ui"
)

// themeService is the host theme state (contracts.ThemeService). The product
// design tokens are mygo's own Theme: ZeoKeel themes start from LightTheme /
// DarkTheme and are customized here (one place), not per module.
type themeService struct {
	root *Root
	mu   sync.Mutex
	dark bool
}

func (t *themeService) Dark() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dark
}

func (t *themeService) SetDark(dark bool) {
	t.mu.Lock()
	changed := t.dark != dark
	t.dark = dark
	t.mu.Unlock()
	if changed {
		t.root.requestFrame()
	}
}

func (t *themeService) Toggle() bool {
	t.mu.Lock()
	t.dark = !t.dark
	dark := t.dark
	t.mu.Unlock()
	t.root.requestFrame()
	return dark
}

// currentTheme returns the mygo theme applied at the root of each frame.
// Modules build with the frame's theme (ui.Context); they must not call
// Context.SetTheme at the root.
func (t *themeService) currentTheme() *ui.Theme {
	if t.Dark() {
		return DarkTheme()
	}
	return LightTheme()
}

// LightTheme returns ZeoKeel's light theme: mygo's native theme with the
// product accent applied in one place.
func LightTheme() *ui.Theme {
	theme := ui.LightTheme()
	theme.Accent = ui.RGB(0x3B, 0x82, 0xF6)
	theme.AccentHover = ui.RGB(0x2F, 0x6F, 0xDC)
	theme.AccentPressed = ui.RGB(0x25, 0x5C, 0xBC)
	return theme
}

// DarkTheme returns ZeoKeel's dark theme.
func DarkTheme() *ui.Theme {
	theme := ui.DarkTheme()
	theme.Accent = ui.RGB(0x6B, 0xA8, 0xFF)
	theme.AccentHover = ui.RGB(0x83, 0xB6, 0xFF)
	theme.AccentPressed = ui.RGB(0x4F, 0x8B, 0xE8)
	return theme
}
