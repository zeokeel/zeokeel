package design

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/brand"
)

// FontStack is the brand text stack, shared by the shell and by every mygo
// widget through Tokens.Theme().Font. It is the platform's own UI face with
// the CJK fallbacks in one list: ZeoKeel bundles no font (docs/wordmark.md)
// and does not want a second visual identity from a downloaded typeface.
//
// mygo's Element.Font takes a comma-separated family list and keeps the
// first family the system has, silently skipping the others ("Segoe UI" on
// Windows, "PingFang SC" on macOS and "Microsoft YaHei" on a Chinese Windows
// install). The list is therefore used as-is: no per-platform branch is
// needed, and a platform that has none of the named families still ends on
// its own sans-serif.
//
// The bare "Helvetica" alias some stacks list between "Helvetica Neue" and
// Arial is intentionally left out: on Linux, mygo resolves families through
// fontconfig, where "Helvetica" is a strong alias for Nimbus Sans and would
// replace the host's own sans-serif for every Latin glyph, with no benefit
// on Windows or macOS.
func FontStack() string { return brand.SystemFont }

// MonoFontStack is the brand code face, for terminal output, commands and
// other monospaced text.
func MonoFontStack() string { return brand.MonoFont }

// FontFamily is a style atom that sets the brand content family while
// leaving size and weight alone. Most call sites should use Tokens.Font(role),
// which applies the family together with the type scale; this exists for raw
// ui.Text calls that only need the family.
func (t *Tokens) FontFamily() Style {
	return func(el ui.Element) ui.Element { return el.Font(FontStack()) }
}

// MonoFamily is FontFamily for code and terminal text.
func (t *Tokens) MonoFamily() Style {
	return func(el ui.Element) ui.Element { return el.Font(MonoFontStack()) }
}
