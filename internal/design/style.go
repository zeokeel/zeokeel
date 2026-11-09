package design

import "github.com/egoist/mygo/ui"

// Style is one styling step: it takes an element and returns it with more
// properties set. Styles compose with Apply, the Go equivalent of CSS
// declarations; every screen builds with these, the brand colors of Tokens
// and the brand metrics of the theme.
type Style func(ui.Element) ui.Element

// Apply applies styles in order.
func Apply(el ui.Element, styles ...Style) ui.Element {
	for _, s := range styles {
		el = s(el)
	}
	return el
}

// Atoms: one property each, the equivalents of CSS utilities.
func (t *Tokens) Gap(v float32) Style {
	return func(el ui.Element) ui.Element { return el.Gap(v) }
}

// textScale is the usable TextScale: an unset or non-positive scale means the
// design system's own 1.0.
func (t *Tokens) textScale() float32 {
	if t == nil || t.TextScale <= 0 || t.TextScale == 1 {
		return 1
	}
	return t.TextScale
}

// Scale multiplies one DIP measure by the Tokens' TextScale. The spacing grid
// and control metrics stay fixed by design; a control whose text must grow
// with the system text size scales its own text-bearing measure with this.
func (t *Tokens) Scale(v float32) float32 {
	if s := t.textScale(); s != 1 {
		return v * s
	}
	return v
}

// Metrics returns a type role's size, fixed line height and weight at the
// Tokens' TextScale. Both the size and the line scale by the same factor and
// the weight never changes, so the hierarchy and line rhythm are preserved.
func (t *Tokens) Metrics(role TypeRole) (size, line float32, weight int) {
	size, line, weight = TypeMetrics(role)
	if s := t.textScale(); s != 1 {
		size *= s
		line *= s
	}
	return size, line, weight
}

// Font sets a brand type role and the brand system font family, at the
// Tokens' TextScale. Applying the family and the scaled metrics here, at the
// design-system level, makes every styled text run in the brand face and
// follow the desktop text size without per-call edits; raw FontSize calls
// inherit the same stack from Tokens.Theme().Font but not the scale.
func (t *Tokens) Font(role TypeRole) Style {
	size, line, weight := t.Metrics(role)
	return func(el ui.Element) ui.Element {
		return el.Font(FontStack()).FontSize(size).FixedLineHeight(line).FontWeight(weight)
	}
}

// MonoText sets a brand type role in the brand's monospaced code face
// (terminal output, commands, code and raw identifiers), at the Tokens'
// TextScale. It is the mono counterpart of Text: the role still brings the
// size, fixed line height and weight, so code text follows the desktop text
// size like every other string instead of freezing a raw FontSize.
func (t *Tokens) MonoText(role TypeRole) Style {
	return func(el ui.Element) ui.Element {
		return Apply(el, t.Font(role), t.MonoFamily())
	}
}

func (t *Tokens) Text(role TypeRole) Style {
	return func(el ui.Element) ui.Element {
		return Apply(el, t.Font(role), func(e ui.Element) ui.Element { return e.TextColor(t.LabelPrimary) })
	}
}
func (t *Tokens) TextMuted(role TypeRole) Style {
	return func(el ui.Element) ui.Element {
		return Apply(el, t.Font(role), func(e ui.Element) ui.Element { return e.TextColor(t.LabelSecondary) })
	}
}
