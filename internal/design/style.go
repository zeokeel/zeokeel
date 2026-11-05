package design

import "github.com/egoist/mygo/ui"

// Style is one styling step: it takes an element and returns it with more
// properties set. Styles compose with Apply, the Go equivalent of CSS
// declarations; every screen builds with these and never with raw values.
type Style func(ui.Element) ui.Element

// Apply applies styles in order.
func Apply(el ui.Element, styles ...Style) ui.Element {
	for _, s := range styles {
		el = s(el)
	}
	return el
}

// Atoms: one property each, the equivalents of CSS utilities.
func (t *Tokens) Pad(v float32) Style {
	return func(el ui.Element) ui.Element { return el.Padding(v) }
}
func (t *Tokens) PadX(v float32) Style {
	return func(el ui.Element) ui.Element { return el.PaddingX(v) }
}
func (t *Tokens) PadY(v float32) Style {
	return func(el ui.Element) ui.Element { return el.PaddingY(v) }
}
func (t *Tokens) Gap(v float32) Style {
	return func(el ui.Element) ui.Element { return el.Gap(v) }
}
func (t *Tokens) Fill(c ui.Color) Style {
	return func(el ui.Element) ui.Element { return el.Background(c) }
}
func (t *Tokens) Stroke(w float32, c ui.Color) Style {
	return func(el ui.Element) ui.Element { return el.Border(w, c) }
}
func (t *Tokens) Radius(r float32) Style {
	return func(el ui.Element) ui.Element { return el.Radius(r) }
}

// Font sets a baseline type role and the DSH font stack. Applying the
// family here, at the design-system level, makes every styled text run in
// the baseline face without per-call edits; raw FontSize calls inherit the
// same stack from Tokens.Theme().Font.
func (t *Tokens) Font(role TypeRole) Style {
	size, line, weight := TypeMetrics(role)
	return func(el ui.Element) ui.Element {
		return el.Font(FontStack()).FontSize(size).FixedLineHeight(line).FontWeight(weight)
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
