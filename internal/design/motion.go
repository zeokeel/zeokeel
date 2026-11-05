// Motion tokens and helpers: the durations, easing curves and transition
// presets ZeoKeel screens animate with, in the terms mygo's animation API
// uses.
//
// The scale is small on purpose. A handful of durations covers UI feedback,
// two longer ones drive panels and looping indicators, and each named preset
// pairs a duration with an easing so screens do not invent new pairs. Motion
// here is feedback, not decoration: it tells the user where a row moved,
// that a panel appeared, or that the assistant is still working.

package design

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

// Durations: the motion scale.
const (
	// DurationTick is .1s: color and opacity feedback on rows and status
	// labels, short enough to feel immediate.
	DurationTick = 100 * time.Millisecond

	// DurationFast is .12s: content arriving in place, such as streaming
	// text or a caret swap.
	DurationFast = 120 * time.Millisecond

	// DurationEnter is .15s: overlays entering the screen, such as the
	// working indicator or a tooltip.
	DurationEnter = 150 * time.Millisecond

	// DurationBase is .16s: color/opacity transitions and the toast
	// entrance, one step above DurationEnter.
	DurationBase = 160 * time.Millisecond

	// DurationSlow is .2s: the standard duration of a modal entrance, an
	// expansion or an element moving to a new place.
	DurationSlow = 200 * time.Millisecond

	// DurationTab is .3s: a panel or tab sliding into its place.
	DurationTab = 300 * time.Millisecond

	// DurationSpin is .8s: one turn of a ring spinner.
	DurationSpin = 800 * time.Millisecond

	// DurationPulse is 1.5s: one cycle of a looping status indicator, the
	// state-dot spinner and the three-dot working indicator.
	DurationPulse = 1500 * time.Millisecond

	// DurationShimmer is 1.5s: the period of the working row's shimmer
	// sweep. The sweep reaches its end at two thirds of the period and
	// holds until the next cycle.
	DurationShimmer = 1500 * time.Millisecond

	// ShimmerDelay is the .3s before the row shimmer starts.
	ShimmerDelay = 300 * time.Millisecond

	// ShimmerSteps is the 48 steps of the row shimmer's stepped sweep.
	ShimmerSteps = 48
)

// Easing curves, as CSS timing functions.
var (
	// EaseStandard is cubic-bezier(0.4, 0, 0.2, 1): the all-purpose curve
	// for entrances, expansions and moves.
	EaseStandard = ui.CubicBezier(0.4, 0, 0.2, 1)

	// EaseCSS is CSS's `ease` keyword, cubic-bezier(0.25, 0.1, 0.25, 1):
	// the curve of short color/opacity feedback.
	EaseCSS = ui.CubicBezier(0.25, 0.1, 0.25, 1)

	// EaseOut is CSS's `ease-out`, cubic-bezier(0, 0, 0.58, 1): entrances
	// that should settle quickly.
	EaseOut = ui.CubicBezier(0, 0, 0.58, 1)

	// EaseInOut is CSS's `ease-in-out`, cubic-bezier(0.42, 0, 0.58, 1),
	// the curve of the pulsing status dot.
	EaseInOut = ui.CubicBezier(0.42, 0, 0.58, 1)

	// Pulse turns a loop's 0..1 progress into a 0..1..0 pulse, from which
	// PulseOpacity makes the running status dot's fade.
	Pulse = ui.Bounce(EaseInOut)
)

// Animations: duration/easing pairs in the form mygo animates with.
var (
	// AnimFast is the .12s ease-out used for content arriving in place:
	// the streaming fade and the caret/content swaps.
	AnimFast = ui.Animation{Duration: DurationFast, Ease: EaseOut}

	// AnimEnter is the .15s ease-out of an entering overlay, such as the
	// working indicator.
	AnimEnter = ui.Animation{Duration: DurationEnter, Ease: EaseOut}
)

// Fractions and distances of the "appearing" motions: opacity 0 to 1, a
// small rise, a .98 scale.
const (
	// StreamFrom is how opaque streaming text dips when new tokens arrive,
	// before it settles back to 1. It is a subtle "appear", not a full
	// cross-fade, and stays above 0.5 because tokens often arrive faster
	// than the fade, so the text should not look dim while it streams.
	StreamFrom float32 = 0.65

	// StreamRise is the DIPs streaming text rises from as it fades in.
	StreamRise float32 = 2

	// MessageRise is the DIPs a new message rises from as it fades in.
	MessageRise float32 = 6

	// IndicatorRise is the DIPs the working indicator rises from as it
	// enters; the fade carries most of the motion, the rise stays tiny.
	IndicatorRise float32 = 2

	// ScaleFrom is the .98 scale a dock hint starts at before it settles
	// at 1.
	ScaleFrom float32 = 0.98

	// PulseMin is the dimmest a running status dot gets in its pulse.
	PulseMin float32 = 0.4
)

// Element transitions, ready for ui.ElementTransition.
//
// MessageTransition fades and lifts a new message (Enter), and moves the
// rows around it smoothly as it appears (Position).
func MessageTransition() ui.ElementTransition {
	return ui.ElementTransition{
		Duration: DurationSlow,
		Ease:     EaseStandard,
		Position: true,
		Enter:    &ui.Motion{Y: MessageRise},
	}
}

// IndicatorTransition fades the working indicator in over the enter
// duration with ease-out, and fades it out when the turn ends.
func IndicatorTransition() ui.ElementTransition {
	return ui.ElementTransition{
		Duration: DurationEnter,
		Ease:     EaseOut,
		Enter:    &ui.Motion{Y: IndicatorRise},
		Exit:     &ui.Motion{},
	}
}

// CollapseTransition grows a tool card's detail into the layout and
// collapses it away again.
func CollapseTransition() ui.ElementTransition {
	return ui.ElementTransition{
		Duration: DurationSlow,
		Ease:     EaseStandard,
		Size:     true,
		Enter:    &ui.Motion{Collapse: true},
		Exit:     &ui.Motion{Collapse: true},
	}
}

// LayoutTransition moves an element to its new place smoothly, as rows below
// a growing message do.
func LayoutTransition() ui.ElementTransition {
	return ui.ElementTransition{
		Duration: DurationSlow,
		Ease:     EaseStandard,
		Position: true,
	}
}

// ColorTransition fades a color change over the tick duration.
func ColorTransition() ui.ElementTransition {
	return ui.ElementTransition{
		Duration: DurationTick,
		Ease:     EaseCSS,
		Colors:   true,
	}
}

// Progress returns how far a transition that started elapsed ago and lasts
// d has gone, eased along EaseStandard: 0 before it starts, 1 once it is
// over, and between in between. A caller that reads this in the view asks
// for a frame while it is between.
func Progress(elapsed, d time.Duration) float32 {
	if d <= 0 || elapsed >= d {
		return 1
	}
	if elapsed <= 0 {
		return 0
	}
	return EaseStandard(float32(elapsed) / float32(d))
}

// OpacityAt returns the opacity of an appearing element at eased progress p,
// from transparent at 0 to opaque at 1.
func OpacityAt(p float32) float32 { return clamp01(p) }

// ScaleAt returns the scale of an appearing element at progress p, from
// `from` to 1.
func ScaleAt(p, from float32) float32 {
	return from + (1-from)*clamp01(p)
}

// RiseAt returns the Y offset of an appearing element at progress p, from
// distance DIPs below its place to its place.
func RiseAt(p, distance float32) float32 {
	return (1 - clamp01(p)) * distance
}

// HeightAt returns the height of a collapsing element at progress p: full
// at 1, nothing at 0.
func HeightAt(p, full float32) float32 {
	return full * clamp01(p)
}

// StreamOpacity returns the opacity of streaming text that last grew
// elapsed ago, and whether it is still moving: it starts at StreamFrom,
// reaches exactly 1 at DurationFast, and stays there.
func StreamOpacity(elapsed time.Duration) (opacity float32, moving bool) {
	if elapsed <= 0 {
		return StreamFrom, true
	}
	if elapsed >= DurationFast {
		return 1, false
	}
	p := Progress(elapsed, DurationFast)
	return StreamFrom + (1-StreamFrom)*p, true
}

// DotsAt returns how many of the working indicator's dots show after
// elapsed, from 1 to 3: the second dot appears at a third of DurationPulse
// and the third at two thirds, step-end.
func DotsAt(elapsed time.Duration) int {
	if elapsed < 0 {
		return 1
	}
	step := DurationPulse / 3
	switch {
	case elapsed%DurationPulse < step:
		return 1
	case elapsed%DurationPulse < 2*step:
		return 2
	default:
		return 3
	}
}

// PulseOpacity returns the opacity of a pulsing status dot at loop progress
// p, from PulseMin to opaque.
func PulseOpacity(p float32) float32 {
	return PulseMin + (1-PulseMin)*clamp01(p)
}

// ShimmerPhase returns how far the row shimmer has swept after elapsed, in
// ShimmerSteps steps: 0 during ShimmerDelay and at the start of each
// DurationShimmer period, and 1 from two thirds through the period until it
// starts over.
func ShimmerPhase(elapsed time.Duration) float32 {
	start := elapsed - ShimmerDelay
	if start < 0 {
		return 0
	}
	at := start % DurationShimmer
	end := DurationShimmer * 2 / 3
	if at >= end {
		return 1
	}
	steps := float64(at) / float64(end) * ShimmerSteps
	return float32(math.Floor(steps)) / ShimmerSteps
}

func clamp01(p float32) float32 {
	return max(0, min(p, 1))
}
