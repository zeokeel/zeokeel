// Motion tokens and helpers: the DSH baseline's durations, easing curves and
// transition presets, in the terms MyGo animates with.
//
// The values come from two places, both captured from the DSH frontend build
// at /usr/local/lib/node_modules/@deepseek-ai/dsh/node_modules/
// @deepseek-ai/dsh-web-frontend:
//
//   - the stylesheet's transition/animation declarations and @keyframes
//     (dist/assets/index-BxpfOGf3.css), and
//   - the computed transition-duration / transition-timing-function of every
//     element on every baseline screen (baseline/css/*.json).
//
// Each token's comment names the CSS selector or computed property it came
// from. docs/baseline/motion.md has the full table.
package design

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

// Durations, from DSH's transition and animation declarations.
const (
	// DurationTick is .1s: the session's activity rows, the "Ran command"
	// disclosure title, its icon and chevron, the status label, and the
	// composer's primary button. Computed as "0.1s" on
	// ._row_k7ery_26 (baseline/css/08-session-chat.json) and
	// ._iconIdle_k7ery_78, which transition color/opacity with it.
	DurationTick = 100 * time.Millisecond

	// DurationFast is .12s: file cards, the header search slot's input,
	// tab colors and chevrons. Computed as "0.12s" on
	// .nyYjTG_file and ._tab_d62eq_37.
	DurationFast = 120 * time.Millisecond

	// DurationEnter is .15s: the working indicator's entrance
	// (_indicator_1gwo3_1 animation: _indicator-enter_1gwo3_1 .15s
	// ease-out) and the tooltip's (_tooltip-in_12mhf_1 .15s).
	DurationEnter = 150 * time.Millisecond

	// DurationBase is .16s: the working indicator's color and opacity
	// transitions (transition: ... .16s ease-out on _indicator_1gwo3_1),
	// the toast's entrance (_dsh-toast-in_l6cvn_1 .16s ease-out) and the
	// dock indicator's transform (._indicator_d62eq_22 .16s ease).
	DurationBase = 160 * time.Millisecond

	// DurationSlow is .2s, DSH's --ds-transition-duration. It is the
	// duration of the modal entrance (_modalEnter_o6lrb_1 var(
	// --ds-transition-duration)) and of the file-link box-shadow
	// transition (._markdown_1ypvv_5 a), whose computed
	// transition-duration is "0.2s" and whose timing function is
	// cubic-bezier(0.4, 0, 0.2, 1) (baseline/css/11-right-sidebar.json).
	// The variable itself is defined outside the captured stylesheets, so
	// the computed value is the evidence.
	DurationSlow = 200 * time.Millisecond

	// DurationTab is .3s: the right sidebar's tab cell and empty-tab host
	// slide in with cubic-bezier(0.4, 0, 0.2, 1) (computed "0.3s" on
	// ._tabCell_6nhg2_142 and ._emptyTabHost_6nhg2_143).
	DurationTab = 300 * time.Millisecond

	// DurationSpin is .8s: one turn of DSH's ring spinner
	// (_spinner_u7vgf_45: animation: _spin_u7vgf_45 .8s linear infinite).
	DurationSpin = 800 * time.Millisecond

	// DurationPulse is 1.5s: the state-dot spinner's turn
	// (_dsh-state-dot-spin_1i3xo_1 1.5s linear infinite), its dash
	// (_dsh-state-dot-dash_1i3xo_1 1.5s ease-in-out infinite) and the
	// working indicator's three-dot reveal (_reveal-second-dot_1gwo3_1 and
	// _reveal-third-dot_1gwo3_1 1.5s step-end infinite).
	DurationPulse = 1500 * time.Millisecond

	// DurationShimmer is 1.5s: the session row's working shimmer sweep
	// (._sweep_1rdzk_34, animation-duration: 1.5s, delay .3s,
	// steps(48, end), infinite). The sweep keyframe reaches its end at
	// 66.6667% of the period (_dsh-row-shimmer-sweep_1rdzk_1) and holds.
	DurationShimmer = 1500 * time.Millisecond

	// ShimmerDelay is the .3s before the row shimmer starts.
	ShimmerDelay = 300 * time.Millisecond

	// ShimmerSteps is the 48 steps of the row shimmer's stepped sweep.
	ShimmerSteps = 48
)

// Easing curves, as CSS timing functions DSH names or computes.
var (
	// EaseStandard is DSH's --ds-ease-in-out: cubic-bezier(0.4, 0, 0.2, 1).
	// The stylesheet uses it for the modal entrance (the mask and the
	// dialog) and for the file link's box-shadow; the file link's computed
	// transition-timing-function is exactly this curve
	// (baseline/css/11-right-sidebar.json), as is that of every other
	// element captured with the variable's 0.2s transition.
	EaseStandard = ui.CubicBezier(0.4, 0, 0.2, 1)

	// EaseCSS is CSS's `ease` keyword, cubic-bezier(0.25, 0.1, 0.25, 1),
	// the computed timing function of the session's .1s activity rows.
	EaseCSS = ui.CubicBezier(0.25, 0.1, 0.25, 1)

	// EaseOut is CSS's `ease-out`, cubic-bezier(0, 0, 0.58, 1): the
	// working indicator's entrance, the toast's, the dock hint's, and the
	// indicator's opacity transition.
	EaseOut = ui.CubicBezier(0, 0, 0.58, 1)

	// EaseInOut is CSS's `ease-in-out`, cubic-bezier(0.42, 0, 0.58, 1),
	// the state-dot dash's curve.
	EaseInOut = ui.CubicBezier(0.42, 0, 0.58, 1)

	// Pulse turns a loop's 0..1 progress into a 0..1..0 pulse, from which
	// PulseOpacity makes the running status dot's fade.
	Pulse = ui.Bounce(EaseInOut)
)

// Animations: DSH's duration/easing pairs in the form MyGo animates with.
var (
	// AnimTick is the .1s ease of the session's activity rows and status.
	AnimTick = ui.Animation{Duration: DurationTick, Ease: EaseCSS}

	// AnimFast is the .12s ease-out used for content arriving in place:
	// the streaming fade and the caret/content swaps.
	AnimFast = ui.Animation{Duration: DurationFast, Ease: EaseOut}

	// AnimEnter is the .15s ease-out of the working indicator's entrance.
	AnimEnter = ui.Animation{Duration: DurationEnter, Ease: EaseOut}

	// AnimBase is the .16s ease-out of the indicator's color/opacity and
	// the toast's entrance.
	AnimBase = ui.Animation{Duration: DurationBase, Ease: EaseOut}

	// AnimSlow is the .2s --ds-transition-duration with the standard curve.
	AnimSlow = ui.Animation{Duration: DurationSlow, Ease: EaseStandard}
)

// Fractions and distances of the baseline's "appearing" motions, measured
// from the DSH @keyframes: opacity 0 to 1, a small rise, a .98 scale.
const (
	// StreamFrom is how opaque streaming text dips when new tokens arrive,
	// before it settles back to 1. DSH does not cross-fade a whole
	// message; this is the subtle "appear" that keeps new text from being
	// a hard cut. It stays above 0.5 because tokens often arrive faster
	// than the fade, and the text should not look dim while it streams.
	StreamFrom float32 = 0.65

	// StreamRise is the DIPs streaming text rises from as it fades in.
	StreamRise float32 = 2

	// MessageRise is the DIPs a new message rises from as it fades in.
	MessageRise float32 = 6

	// IndicatorRise is the DIPs the working indicator rises from
	// (the @keyframes only fade; the rise is ours, kept tiny).
	IndicatorRise float32 = 2

	// ScaleFrom is the .98 scale of DSH's dock hint entrance
	// (_dockHintIn_6nhg2_1: transform: scale(.98) to none).
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

// IndicatorTransition fades the working indicator in over DSH's .15s
// ease-out, and fades it out when the turn ends.
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

// ColorTransition fades a color change over DSH's .1s ease.
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
// elapsed, from 1 to 3: DSH reveals the second at 33.33% and the third at
// 66.66% of DurationPulse, step-end.
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

// ShimmerPhase returns how far DSH's row shimmer has swept after elapsed,
// in ShimmerSteps steps: 0 during ShimmerDelay and at the start of each
// DurationShimmer period, and 1 from two thirds through the period until
// it starts over.
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
