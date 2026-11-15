package uikit

// The product's one segmented control: mygo's ui.SegmentedBase (a radio group
// whose radio buttons are the segments, with the arrow keys and Changed) and
// one explicit track/segment look, so the settings pages and the Insights range
// switch cannot drift apart again (docs/UI-UX-UPGRADE-2026-11-15.md P3-1).
//
// The stock ui.Segmented paints the chosen segment as a near-white surface with
// a 1px near-white border: against the track it measures about 1.3:1 and reads
// as "no selection" (audit V4). SegmentedControl keeps the chosen segment the
// product's persistent selection — Tokens.BgSelected tint, LabelPrimary text,
// 600 weight — and adds a 2 DIP Accent underline along its bottom edge. The
// underline is the boundary that separates the chosen segment from the track:
// it measures 6.0:1 (light) and 7.2:1 (dark) against the track fill, above the
// 3:1 the design requires, while a full accent outline would read as the
// keyboard focus ring the widget already draws. The look never paints a
// saturated accent fill, so the accent budget stays with the page's actions
// (DESIGN.md §5.5).

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// SegmentedOptions is one segmented control, as the settings pages' theme and
// work-details switches and the Insights range switch draw it.
type SegmentedOptions struct {
	// Key is the control's stable identity when it is built among changing
	// siblings; empty keeps the positional identity.
	Key string
	// Label names the radio group for assistive technology (the group's
	// accessible name: "Time range", "Work details"). Required: without it the
	// radio group has no name.
	Label string
	// Labels are the segments' visible and accessible names, in order. At
	// least one is required; an empty control renders nothing.
	Labels []string
	// Selected is the index of the chosen segment; mygo writes it on a click
	// or an arrow key. Required.
	Selected *int
}

// SegmentedControl builds one segmented control and reports a new choice.
//
// It keeps mygo's SegmentedBase behavior unchanged: the track is a radio group
// named by options.Label of radio buttons named by the segment labels, one Tab
// stop that goes to the chosen segment, and the arrows choose among the
// segments. The control owns only the look — a BgHover track on the
// ControlHeight metric, the chosen segment's BgSelected tint with LabelPrimary
// 600 text and the 2 DIP Accent boundary, the other segments in LabelSecondary
// — so every caller gets the same appearance in both themes.
//
// The return value is mygo's Changed notice: it reports the new choice in the
// build pass after the widget took it, once (the widget has already written
// *options.Selected by then). Callers that follow the value itself instead of
// the notice wait one pass, which is the pattern the Insights range switch
// uses so its data and its switch can never drift apart.
func SegmentedControl(c *ui.Context, options SegmentedOptions) bool {
	if c == nil || options.Selected == nil || len(options.Labels) == 0 {
		return false
	}
	t := design.Of(c)
	ctx := c
	if options.Key != "" {
		ctx = c.Key(options.Key)
	}
	parts := ui.SegmentedBase(ctx, options.Selected, len(options.Labels))
	parts.Track.Label(options.Label).Height(design.ControlHeight).
		Padding(design.Space1).Radius(design.RadiusMD).
		Background(t.BgHover).Children(func() {
		for index, label := range options.Labels {
			// The boundary width is reserved on every segment, so the chosen
			// segment's 2 DIP underline does not move its label by the border:
			// the labels of a control all sit on one baseline.
			segment := parts.Segment(index).Height(design.ControlHeight-design.Space2).
				PaddingX(design.Space4).Radius(design.RadiusSM).Center().
				BorderWidth(0, 0, 2, 0).Label(label)
			if index == *options.Selected {
				// The persistent selection look plus the 2 DIP Accent
				// underline that makes the choice visible against the track
				// (audit V4: the chosen segment used to read as unselected).
				segment.Background(t.BgSelected).BorderColor(t.Accent).
					FontWeight(design.WeightSemibold).TextColor(t.LabelPrimary)
			} else {
				segment.BorderColor(ui.Transparent).TextColor(t.LabelSecondary)
			}
			segment.Children(func() {
				design.Apply(ui.Text(c, label), t.Font(design.TypeS)).MaxLines(1)
			})
		}
	})
	return parts.Track.Changed()
}
