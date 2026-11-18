package uikit

// The context-usage ring: the small circular gauge in the composer's status strip
// that says how much of the model's context window the conversation has taken.
// mygo v0.9.2 has bar-shaped gauges only (ui.Meter, ui.Progress), and the product
// asked for the ring every modern agent client shows, so this is the one
// registered self-drawn widget of the design system (DESIGN.md §7.1,
// registry `uikit.usage-ring`).
//
// It draws with the framework's own vector primitives — ui.Path + Painter
// (StrokePath) — and takes every colour from internal/design: no hex, no ui.RGBA,
// no second palette. The ring is a graphic, not text: the state colour is the
// semantic *fill* token (BorderL1 for the track, Accent for usage, Warning when
// the window is nearly full), and the accessible label states the percentage in
// words while the description carries the exact token counts.
//
// Why Warning and not Danger at the top of the range: a nearly-full context is a
// capacity warning the user can act on (compact, start a new chat), not an error;
// the product's red budget stays for failures (AGENTS.md §3.10).

import (
	"fmt"
	"math"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

const (
	// usageRingWarnRatio is where the ring turns amber: 85% of the window is the
	// point where the next long turn may start compacting.
	usageRingWarnRatio = 0.85
	// usageRingDefaultSize is the ring's box: 16 DIP, the icon step, so it sits on
	// the status strip's baseline like the icons beside it.
	usageRingDefaultSize float32 = design.Space4
	// usageRingDefaultStroke is the ring's stroke: 2 DIP, the ring/rule weight the
	// product allows for a 2 DIP mark.
	usageRingDefaultStroke float32 = design.Space1 / 2
)

// UsageRingOptions is the appearance and the accessible copy of one context-usage
// ring.
type UsageRingOptions struct {
	// Ratio is the used share of the context window: 0..1. Negative and NaN read
	// as 0, more than 1 clamps to 1.
	Ratio float64
	// Size is the box, square, in DIPs; zero uses 16.
	Size float32
	// Stroke is the ring's line width in DIPs; zero uses 2.
	Stroke float32
	// Used and Limit are the token counts behind the ratio. They fill the
	// accessible description ("42K / 128K") so a screen reader reads the precise
	// numbers the arc only sketches; Exact overrides them, and with none of the
	// three the description is the percentage alone.
	Used, Limit int
	Exact       string
	// Locale localizes the label and the description; nil falls back to the
	// product's Chinese copy.
	Locale contracts.LocaleService
}

// UsageRing draws one context-usage ring: a track circle in BorderL1 and, from 12
// o'clock clockwise, the used share in Accent — or in Warning once 85% is spent.
// The arc is stroked with round caps, so even a two-percent share shows a round
// head instead of a hairline that reads as a broken graphic; at exactly zero the
// ring is the track alone, which is what zero usage looks like.
func UsageRing(c *ui.Context, o UsageRingOptions) {
	t := design.Of(c)
	size := o.Size
	if size <= 0 {
		size = usageRingDefaultSize
	}
	stroke := o.Stroke
	if stroke <= 0 {
		stroke = usageRingDefaultStroke
	}
	ratio := usageRingRatio(o.Ratio)
	pct := int(math.Round(ratio * 100))

	fill := t.Accent
	if ratio >= usageRingWarnRatio {
		fill = t.Warning
	}
	label := fmt.Sprintf(T(o.Locale, "上下文 %d%%", "Context %d%%"), pct)
	description := usageRingDescription(o, pct)

	ring := ui.Box(c).Size(size, size).Shrink(0).Role(ui.RoleMeter).Label(label)
	if description != "" {
		ring = ring.Description(description)
	}
	ring.Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		radius := (min32(r.W, r.H) - stroke) / 2
		if radius <= 0 {
			return
		}
		// One full track: the ring's shape when nothing is used, and the reference
		// the arc is read against.
		track := &ui.Path{}
		track.Circle(cx, cy, radius)
		p.StrokePath(track, stroke, t.BorderL1)
		if ratio <= 0 {
			return
		}
		// The arc starts at 12 o'clock (screen angle -90°) and runs clockwise.
		arc := &ui.Path{}
		usageArcPath(arc, cx, cy, radius, -90, float32(ratio)*360)
		p.StrokePath(arc, stroke, fill)
	})
}

// usageRingRatio folds the caller's share onto 0..1: a negative or not-a-number
// value reads as nothing used rather than as a broken gauge.
func usageRingRatio(ratio float64) float64 {
	switch {
	case math.IsNaN(ratio):
		return 0
	case ratio < 0:
		return 0
	case ratio > 1:
		return 1
	default:
		return ratio
	}
}

// usageRingDescription is what assistive technology reads after the percentage:
// the exact token counts when the caller has them, the percentage alone otherwise.
func usageRingDescription(o UsageRingOptions, pct int) string {
	if exact := strings.TrimSpace(o.Exact); exact != "" {
		return exact
	}
	if o.Limit > 0 {
		return FormatCompactCount(o.Used) + " / " + FormatCompactCount(o.Limit)
	}
	if o.Used > 0 {
		return FormatCompactCount(o.Used)
	}
	_ = pct
	return ""
}

// usageArcPath adds the arc that runs sweepDeg degrees clockwise from fromDeg, as
// one cubic per 90° — the segment size that keeps a 2 DIP stroke on a 16 DIP ring
// within a fraction of a pixel of the true circle.
func usageArcPath(path *ui.Path, cx, cy, radius, fromDeg, sweepDeg float32) {
	if sweepDeg <= 0 || radius <= 0 {
		return
	}
	segments := int(math.Ceil(float64(sweepDeg) / 90))
	step := sweepDeg / float32(segments)
	for index := 0; index < segments; index++ {
		a0 := (fromDeg + float32(index)*step) * math.Pi / 180
		a1 := (fromDeg + float32(index+1)*step) * math.Pi / 180
		x0, y0 := cx+radius*float32(math.Cos(float64(a0))), cy+radius*float32(math.Sin(float64(a0)))
		x1, y1 := cx+radius*float32(math.Cos(float64(a1))), cy+radius*float32(math.Sin(float64(a1)))
		if index == 0 {
			path.MoveTo(x0, y0)
		}
		// 4/3·tan(δ/4) is the control-point distance that makes one cubic the
		// quarter circle it replaces.
		tangent := radius * float32(4.0/3.0*math.Tan(float64(step)*math.Pi/180/4))
		c1x, c1y := x0-tangent*float32(math.Sin(float64(a0))), y0+tangent*float32(math.Cos(float64(a0)))
		c2x, c2y := x1+tangent*float32(math.Sin(float64(a1))), y1-tangent*float32(math.Cos(float64(a1)))
		path.CubeTo(c1x, c1y, c2x, c2y, x1, y1)
	}
}

// min32 is the smaller of two DIP measures.
func min32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}
