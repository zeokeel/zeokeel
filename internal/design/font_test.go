package design

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/brand"
)

// TestFontStack checks that the design system carries the brand system font
// stack: the platform families and the Chinese fallbacks in a fixed order,
// with sans-serif last so an unknown system still resolves.
func TestFontStack(t *testing.T) {
	stack := FontStack()
	if stack != brand.SystemFont {
		t.Errorf("FontStack = %q, want the brand stack %q", stack, brand.SystemFont)
	}
	for _, want := range []string{
		"-apple-system",
		"BlinkMacSystemFont",
		`"Segoe UI"`,
		`"PingFang SC"`,
		`"Hiragino Sans GB"`,
		`"Microsoft YaHei"`,
		`"Helvetica Neue"`,
		"Arial",
		"sans-serif",
	} {
		if !strings.Contains(stack, want) {
			t.Errorf("FontStack missing %q: %s", want, stack)
		}
	}
	// Segoe UI is the Windows family and must come before the Chinese
	// fallbacks, which are only reached on an install without it.
	if strings.Index(stack, "Segoe UI") > strings.Index(stack, "PingFang SC") {
		t.Errorf("Segoe UI must precede PingFang SC: %s", stack)
	}
	if !strings.HasSuffix(stack, "sans-serif") {
		t.Errorf("FontStack must end on sans-serif: %s", stack)
	}
	// The bare "Helvetica" alias is dropped on purpose: on Linux it maps to
	// Nimbus Sans and replaces the host fallback (see FontStack's comment).
	if strings.Contains(stack, ", Helvetica,") {
		t.Errorf("FontStack should not carry the bare Helvetica alias: %s", stack)
	}
}

// TestMonoFontStack checks the brand code face for terminal and command
// text.
func TestMonoFontStack(t *testing.T) {
	stack := MonoFontStack()
	if stack != brand.MonoFont {
		t.Errorf("MonoFontStack = %q, want the brand mono stack %q", stack, brand.MonoFont)
	}
	for _, want := range []string{"SF Mono", "JetBrains Mono", "Fira Code", "Consolas", "Menlo", "PingFang SC"} {
		if !strings.Contains(stack, want) {
			t.Errorf("MonoFontStack missing %q: %s", want, stack)
		}
	}
}

// TestThemeCarriesFontStack pins the global application point: Tokens.Theme
// is what App.View installs with SetTheme, so its Font reaches every widget
// and every raw text element that does not name a family itself.
func TestThemeCarriesFontStack(t *testing.T) {
	for _, tokens := range []*Tokens{Light(), Dark()} {
		if got := tokens.Theme().Font; got != FontStack() {
			t.Errorf("Theme().Font = %q, want %q", got, FontStack())
		}
	}
}

// TestFontStyleAppliesInRender builds both font helpers on a headless
// context, which fails if Font/FontFamily cannot set a family on a real
// element (for example if mygo dropped Element.Font).
func TestFontStyleAppliesInRender(t *testing.T) {
	tokens := Light()
	img := ui.Render(func(c *ui.Context) {
		ui.Column(c).Children(func() {
			Apply(ui.Text(c, "Hello"), tokens.Font(TypeBody))
			Apply(ui.Text(c, "code"), tokens.FontFamily())
			Apply(ui.Text(c, "mono"), tokens.MonoFamily())
		})
	}, 240, 80, 1)
	if img == nil {
		t.Fatal("render returned nil with the font helpers applied")
	}
}
