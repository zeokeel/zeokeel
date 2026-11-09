package brand

import "testing"

// TestThemesKeepInactiveAccent covers the brand's explicit inactive-window
// decision: mygo v0.8 fades a window's accent to a gray unless the theme sets
// InactiveAccent, while the brand's hand-drawn accent surfaces do not fade.
// Keeping InactiveAccent equal to Accent makes a window that loses focus look
// the same (docs/ui/windows.md documents both behaviors).
func TestThemesKeepInactiveAccent(t *testing.T) {
	light, dark := LightTheme(), DarkTheme()
	if light.InactiveAccent != light.Accent {
		t.Fatalf("light InactiveAccent = %v, want Accent %v", light.InactiveAccent, light.Accent)
	}
	if dark.InactiveAccent != dark.Accent {
		t.Fatalf("dark InactiveAccent = %v, want Accent %v", dark.InactiveAccent, dark.Accent)
	}
}
