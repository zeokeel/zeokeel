package uiroot

import (
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/brand"
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

// currentTheme returns the mygo theme applied at the root of each frame: the
// brand theme (palette + radius/spacing/font metrics) for the current
// appearance. Modules build with the frame's theme (ui.Context); they must not
// call Context.SetTheme at the root.
func (t *themeService) currentTheme() *ui.Theme {
	return brand.ThemeFor(t.Dark())
}

// LightTheme returns ZeoKeel's brand light theme (the palette in internal/brand
// applied to mygo's light defaults).
func LightTheme() *ui.Theme { return brand.LightTheme() }

// DarkTheme returns ZeoKeel's brand dark theme.
func DarkTheme() *ui.Theme { return brand.DarkTheme() }
