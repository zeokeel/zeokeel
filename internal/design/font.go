package design

import "github.com/egoist/mygo/ui"

// FontStack is the DSH baseline's content font stack, copied from the
// computed styles in baseline/css (font-family of the body and of every
// piece of content text):
//
//	-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC",
//	"Hiragino Sans GB", "Microsoft YaHei", "Helvetica Neue", Arial,
//	sans-serif
//
// mygo's Element.Font takes a comma-separated family list and keeps the
// first family the system has, silently skipping the others ("Segoe UI" on
// Windows, "PingFang SC" on macOS and "Microsoft YaHei" on a Chinese
// Windows install). The list is therefore used as-is: no per-platform
// branch is needed, and a platform that has none of the named families
// still ends on its own sans-serif.
//
// One baseline name is intentionally left out: the bare "Helvetica" that
// DSH lists between "Helvetica Neue" and Arial. On Linux, mygo resolves
// families through fontconfig, where "Helvetica" is a strong alias for
// Nimbus Sans and therefore replaces the host's own sans-serif for every
// Latin glyph: the five screen baselines regress (settings 2.19% -> 2.72%,
// plugins 2.80% -> 3.55%) with no Windows/macOS benefit, since both
// platforms stop at -apple-system, "Segoe UI" or "PingFang SC" first.
// "Helvetica Neue" and Arial stay: on a system that has them they still
// win over sans-serif, and on this Linux host they resolve to the default
// sans-serif, matching the baseline renderer.
func FontStack() string { return fontStack }

// MonoFontStack is the DSH code face, for terminal output, commands and
// other monospaced text.
func MonoFontStack() string { return monoFontStack }

// The stacks are DSH's computed values. Quotes are kept: mygo strips them
// when it splits the list (internal/text.familyList), and they make the
// names containing spaces unambiguous for readers of this file.
const (
	fontStack     = `-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Helvetica Neue", Arial, sans-serif`
	monoFontStack = `"SF Mono", "JetBrains Mono", "Fira Code", Consolas, "Liberation Mono", Menlo, Courier, "PingFang SC", "Microsoft YaHei"`
)

// FontFamily is a style atom that sets the DSH content family while leaving
// size and weight alone. Most call sites should use Tokens.Font(role), which
// applies the family together with the type scale; this exists for raw
// ui.Text calls that only need the family.
func (t *Tokens) FontFamily() Style {
	return func(el ui.Element) ui.Element { return el.Font(FontStack()) }
}

// MonoFamily is FontFamily for code and terminal text.
func (t *Tokens) MonoFamily() Style {
	return func(el ui.Element) ui.Element { return el.Font(MonoFontStack()) }
}
