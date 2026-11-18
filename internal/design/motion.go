// Motion tokens: the product's one timing table. Chrome never animates for
// more than a fifth of a second, and every transition in the app names one of
// these values instead of a bare duration, so a reviewer can read the rhythm
// from one file and a reduced-motion user turns all of it off at once.
package design

import (
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	// MotionFast is a colour or focus change: hover, pressed, tint. Short
	// enough to feel immediate; long enough not to flicker.
	MotionFast = 80 * time.Millisecond
	// MotionBase is an expand or collapse: a reasoning block, a tool detail,
	// an inline form. Pair it with a held place so rows below do not jump.
	MotionBase = 120 * time.Millisecond
	// MotionEnter is a floating layer arriving: menu, popover, dialog,
	// palette. The exit is MotionBase (leaving is always quicker).
	MotionEnter = 160 * time.Millisecond
	// MotionList is a list following a streamed message or reordering rows.
	MotionList = 200 * time.Millisecond
)

// ReduceMotion reports the desktop's reduced-motion preference. When it is on,
// the timing helpers return negative durations, which mygo shows at once
// (ElementTransition: "no time at all when negative"), so the interface keeps
// every state change without the motion.
func ReduceMotion(c *ui.Context) bool {
	if c == nil {
		return false
	}
	return c.Preferences().ReduceMotion
}

// duration returns d, or the "show at once" value when motion is reduced.
func duration(c *ui.Context, d time.Duration) time.Duration {
	if ReduceMotion(c) {
		return -1
	}
	return d
}

// TransitionColors fades an element's background and border between states:
// the hover and pressed machine of a hand-drawn row. Official widgets carry
// their own state transitions, so this is only for the product's own rows.
func TransitionColors(c *ui.Context) ui.ElementTransition {
	return ui.ElementTransition{Colors: true, Duration: duration(c, MotionFast), Ease: ui.EaseOut}
}

// TransitionHeight animates an element whose height follows its content, as a
// collapsed section opening: the height alone, so the width keeps up with the
// layout and the rows below settle in step.
func TransitionHeight(c *ui.Context) ui.ElementTransition {
	return ui.ElementTransition{Height: true, Duration: duration(c, MotionBase), Ease: ui.EaseOut}
}

// TransitionOverlay is the entrance and exit of a floating layer: it comes in
// from 8 DIPs below, fading up, over MotionEnter, and leaves over MotionBase.
// Scale is deliberately not animated: a scaled native popup renders its text
// soft on the way.
func TransitionOverlay(c *ui.Context) ui.ElementTransition {
	if ReduceMotion(c) {
		return ui.ElementTransition{Duration: -1}
	}
	return ui.ElementTransition{
		Duration:     MotionEnter,
		ExitDuration: MotionBase,
		Ease:         ui.EaseOut,
		Enter:        &ui.Motion{Opacity: 0, Y: 8},
		Exit:         &ui.Motion{Opacity: 0, Y: 4},
	}
}

// TransitionSize animates a container whose size the layout changes, as a
// sidebar collapsing or a split pane being snapped: the elements that move
// with it carry the same transition to move in step.
func TransitionSize(c *ui.Context) ui.ElementTransition {
	return ui.ElementTransition{Size: true, Duration: duration(c, MotionBase), Ease: ui.EaseOut}
}

// ListFollowAnimation is the animation of a list that grows at its end while a
// reply streams in: the new row rises into place instead of popping. A nil
// result (reduced motion) leaves the list static.
func ListFollowAnimation(c *ui.Context) *ui.Animation {
	if ReduceMotion(c) {
		return nil
	}
	return &ui.Animation{Duration: MotionList, Ease: ui.EaseOut}
}

// ListMoveAnimation is the animation of rows that reorder or collapse, so the
// rest slide into their new places rather than jumping. It needs stable row
// keys (ListState.Key), which the product's lists already carry.
func ListMoveAnimation(c *ui.Context) *ui.Animation {
	if ReduceMotion(c) {
		return nil
	}
	return &ui.Animation{Duration: MotionList, Ease: ui.EaseOut}
}
