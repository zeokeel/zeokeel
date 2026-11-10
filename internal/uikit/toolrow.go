package uikit

// The compact transcript tool row: the quiet anatomy shared by the registered
// tool-card renderers (internal/ui/toolcards) and the session screen's fallback
// for tools without a renderer. A transcript is mostly prose; a tool call is one
// muted disclosure row (icon, friendly name, status, one-line preview, chevron),
// not a panel. Expanded, the panel shows the tool's input and output with a
// copy action per section and an optional cross-navigation footer.

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// ToolPanelSection is one labeled block of an expanded tool row's panel: the
// tool's input, its output, or another bounded detail. The caller localizes
// Label, CopyLabel, Hint and Empty; the panel renders it with the product's
// typography so the transcript row and the registered cards keep one anatomy.
type ToolPanelSection struct {
	// Label is the section heading ("输入"/"输出"/"命令"), already localized.
	Label string
	// Text is the body; multi-line text keeps its line breaks.
	Text string
	// Mono renders the body in the brand code face (commands, diffs, JSON).
	Mono bool
	// Tone colors the body: neutral text, a removed (red) or added (green) diff
	// side. Zero is neutral.
	Tone ToolSectionTone
	// CopyLabel is the copy action's accessible name (localized). Empty hides
	// the action; CopyValue is what it copies (Text when empty).
	CopyLabel string
	CopyValue string
	// Bounded keeps a long body in a fixed-height scroll box so a long output
	// cannot push the transcript away.
	Bounded bool
	// Hint is a muted note under the body (e.g. the truncation notice).
	Hint string
	// Empty replaces the body when Text is empty (localized); empty hides it.
	Empty string
}

// ToolSectionTone is the color intent of one panel section body.
type ToolSectionTone int

const (
	// ToolSectionNeutral is the quiet body color (default).
	ToolSectionNeutral ToolSectionTone = iota
	// ToolSectionRemoved is the removed side of an edit diff.
	ToolSectionRemoved
	// ToolSectionAdded is the added side of an edit diff.
	ToolSectionAdded
)

// toolSectionMaxHeight bounds one expandable panel section (about eight lines).
const toolSectionMaxHeight float32 = design.Space8 * 5

// ToolRowOptions is one compact transcript tool row.
type ToolRowOptions struct {
	// Key is the row's stable identity (one per tool call).
	Key string
	// Icon is the tool's category glyph; nil falls back to the message glyph.
	Icon *ui.SVG
	// Name is the friendly, localized tool name shown in the row.
	Name string
	// RawName is the protocol tool id: it is the row's tooltip and accessible
	// description only, never visible copy. Empty (or equal to Name) adds none.
	RawName string
	// Status is the localized status label ("完成"/"Completed"); empty hides it.
	Status string
	// StatusColor is the WCAG-AA status text color (success/warning/danger).
	StatusColor ui.Color
	// Running shows the official indeterminate-work spinner before the status
	// text for a call that is still running.
	Running bool
	// Preview is the collapsed one-line summary excerpt (already normalized to
	// one line); empty keeps the status at the row end.
	Preview string
	// Open is the current disclosure state; Toggle receives the next one (the
	// caller stores it and requests a frame).
	Open   bool
	Toggle func(open bool)
	// Sections is the expanded panel's content: the tool's input and output.
	// Empty keeps the row a plain line with no disclosure.
	Sections []ToolPanelSection
	// Footer builds the panel's closing actions (cross-navigation to the team
	// panel or the task board); nil adds none.
	Footer func(c *ui.Context)
	// InlineChevron puts the disclosure chevron right after the preview (one
	// Space2 gap) instead of the row's far end, and stops the preview from
	// growing, so a transcript activity band reads as one compact left-aligned
	// row. The default false keeps the wide row's far-end chevron and growing
	// preview for other callers.
	InlineChevron bool
	// MarkerColumn puts the category glyph (or the official spinner while the
	// call runs) in a fixed 16 DIP column at the start of the line, so every row
	// of a transcript activity band starts its copy on the same x and the reader
	// can tell a reasoning row from a tool row at a glance. The default false
	// keeps the old leading glyph inside the line.
	MarkerColumn bool
}

// toolCallHeadOptions is the head of one tool-call line: the glyph, the friendly
// name and the status, as the compact ToolRow line and the ToolCallCard header
// both draw it. The caller adds its own tail (a preview, step metadata, the
// disclosure chevron, extra header content).
type toolCallHeadOptions struct {
	// Icon is the category glyph; nil falls back to the message glyph.
	Icon *ui.SVG
	// Name is the friendly, localized tool name.
	Name string
	// Status is the localized status label; empty hides the status.
	Status string
	// StatusColor is the WCAG-AA status text color (uikit.StatusColor).
	StatusColor ui.Color
	// Kind is the status vocabulary kind, for the badge anatomy.
	Kind StatusKind
	// Running shows the official indeterminate spinner before the status text.
	Running bool
	// Badge draws the status as the shared status badge (the complete card); the
	// compact row writes it as plain text beside the spinner instead.
	Badge bool
}

// toolCallHead draws the icon, the friendly tool name and the status of one
// tool call. The status travels as text, not color alone: the official spinner
// while running, the shared status badge on a complete card, plain status text on
// the compact row.
func toolCallHead(c *ui.Context, t *design.Tokens, o toolCallHeadOptions) {
	icon := o.Icon
	if icon == nil {
		icon = icons.Message()
	}
	ui.Icon(c, icon).Size(design.Space3, design.Space3).TextColor(t.LabelTertiary)
	design.Apply(ui.Text(c, o.Name), t.Font(design.TypeXXS)).
		TextColor(t.LabelSecondary).MaxLines(1).Ellipsis("…")
	switch {
	case o.Badge:
		if o.Status != "" {
			StatusBadge(c, t, o.Kind, o.Status)
		}
	case o.Running:
		ui.Spinner(c).Label(o.Status).Size(design.Space3, design.Space3).
			TextColor(o.StatusColor)
		if o.Status != "" {
			design.Apply(ui.Text(c, o.Status), t.Font(design.TypeXXXS)).
				TextColor(o.StatusColor).NoWrap()
		}
	case o.Status != "":
		design.Apply(ui.Text(c, o.Status), t.Font(design.TypeXXXS)).
			TextColor(o.StatusColor).NoWrap()
	}
}

// ToolRow draws one compact transcript tool call as one official mygo
// disclosure (ui.CollapsibleBase): the whole row is the trigger, so assistive
// technology sees expanded/collapsed, Enter and Space work, and the panel grows
// into view. It has no panel background or card: a muted category icon, the
// friendly tool name, the status (with the official spinner while running), the
// one-line preview and the chevron share one muted line; the input/output
// sections read below it when expanded.
func ToolRow(c *ui.Context, o ToolRowOptions) {
	t := design.Of(c)
	buildLine := func(c *ui.Context, progress float32, disclosure bool) {
		if o.MarkerColumn {
			buildActivityLine(c, t, o, progress, disclosure)
			return
		}
		toolCallHead(c, t, toolCallHeadOptions{
			Icon:        o.Icon,
			Name:        o.Name,
			Status:      o.Status,
			StatusColor: o.StatusColor,
			Running:     o.Running,
		})
		// While the row is open the panel already shows the text: repeating the
		// preview in the trigger rendered a short result twice (H15 opens short
		// results by default). The trigger keeps only icon, name, status and
		// chevron, and the panel is the one place the result reads.
		if o.Preview != "" && !o.Open {
			preview := design.Apply(ui.Text(c, o.Preview), t.Font(design.TypeXXXS)).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
			if !o.InlineChevron {
				preview.Grow(1)
			}
		} else if !o.InlineChevron {
			ui.Spacer(c).Grow(1)
		}
		if disclosure {
			ui.Icon(c, icons.ChevronRight()).Size(design.Space3, design.Space3).
				TextColor(t.LabelTertiary).Rotate(90 * progress)
		}
		if o.InlineChevron {
			// After the inline chevron the spacer takes the rest of the line, so
			// a long preview stays one left-aligned string instead of stretching.
			ui.Spacer(c).Grow(1)
		}
	}

	// A call without a panel body has nothing to disclose: build one quiet line
	// instead of a trigger that opens an empty panel.
	hasPanel := len(o.Sections) > 0 || o.Footer != nil
	if !hasPanel {
		ui.Row(c.Key(o.Key)).FillWidth().AlignItems(ui.Center).
			Gap(design.Space2).PaddingY(design.Space1).Children(func() {
			buildLine(c, 0, false)
		})
		return
	}

	open := o.Open
	parts := ui.CollapsibleBase(c.Key(o.Key), &open)
	// The disclosure is named by the friendly tool name and its status: the
	// status text and the spinner inside a disclosure button are not nodes of
	// their own for assistive technology, so the state travels in the name.
	label := o.Name
	if o.Status != "" {
		label = o.Name + " · " + o.Status
	}
	trigger := parts.Trigger.FillWidth().AlignItems(ui.Center).
		Gap(design.Space2).PaddingY(design.Space1).Radius(design.RadiusSM).
		Label(label)
	if o.RawName != "" && o.RawName != o.Name {
		trigger = trigger.Tooltip(o.RawName)
	}
	if parts.Trigger.Hovered() || parts.Trigger.Focused() {
		trigger.Background(t.BgHover)
	}
	trigger.Children(func() {
		buildLine(c, parts.Progress(), true)
	})
	parts.Panel(func() {
		design.Apply(ui.Column(c), t.Gap(design.Space2)).
			PaddingStart(design.Space3 + design.Space2).PaddingEnd(design.Space2).
			PaddingY(design.Space1).Children(func() {
			for _, section := range o.Sections {
				ToolPanelSectionView(c, t, section)
			}
			if o.Footer != nil {
				// The footer draws its own separator once it knows its action is
				// available, so a host without the command leaves no dangling line.
				o.Footer(c)
			}
		})
	})
	// The framework already flipped the bound open state before Toggle runs:
	// the caller only persists it (and keeps the transcript's place).
	if parts.Trigger.Changed() && o.Toggle != nil {
		o.Toggle(open)
	}
}

// buildActivityLine draws one tool row in the transcript's activity band: a
// fixed 16 DIP marker column (the category glyph, or the official spinner while
// the call runs), the friendly action name, the status, a muted middle dot and
// the one-line preview, then the inline chevron. The marker column lets a reader
// tell a reasoning row from a tool row at one x; the muted dot separates the
// action name from its preview without another label.
func buildActivityLine(c *ui.Context, t *design.Tokens, o ToolRowOptions, progress float32, disclosure bool) {
	ui.Row(c).Width(design.Space4).Shrink(0).Justify(ui.Center).Children(func() {
		if o.Running {
			ui.Spinner(c).Label(o.Status).Size(design.Space3, design.Space3).
				TextColor(o.StatusColor)
			return
		}
		icon := o.Icon
		if icon == nil {
			icon = icons.Message()
		}
		ui.Icon(c, icon).Size(design.Space4, design.Space4).TextColor(t.LabelTertiary)
	})
	design.Apply(ui.Text(c, o.Name), t.Font(design.TypeXXS)).
		TextColor(t.LabelSecondary).MaxLines(1).Ellipsis("…")
	if o.Status != "" {
		design.Apply(ui.Text(c, o.Status), t.Font(design.TypeXXXS)).
			TextColor(o.StatusColor).NoWrap()
	}
	if o.Preview != "" && !o.Open {
		design.Apply(ui.Text(c, "·"), t.Font(design.TypeXXXS)).
			TextColor(t.LabelTertiary).NoWrap()
		design.Apply(ui.Text(c, o.Preview), t.Font(design.TypeXXXS)).
			TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
	}
	if disclosure {
		ui.Icon(c, icons.ChevronRight()).Size(design.Space3, design.Space3).
			TextColor(t.LabelTertiary).Rotate(90 * progress)
	}
	ui.Spacer(c).Grow(1)
}

// ToolPanelSectionView draws one expanded-panel section: a muted uppercase-ish
// label with an optional one-click copy action, the bounded body in the brand
// type/face, and an optional muted hint (the truncation notice).
func ToolPanelSectionView(c *ui.Context, t *design.Tokens, s ToolPanelSection) {
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		// A section without its own heading is just a block of text: draw no
		// label row when there is neither a label nor a copy action.
		if s.Label != "" || s.CopyLabel != "" {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				design.Apply(ui.Text(c, s.Label), t.Font(design.TypeXXXS)).
					FontWeight(600).TextColor(t.LabelTertiary).NoWrap()
				ui.Spacer(c).Grow(1)
				if s.CopyLabel != "" {
					value := s.CopyValue
					if value == "" {
						value = s.Text
					}
					button := ui.Button(c, "").Label(s.CopyLabel).Tooltip(s.CopyLabel).
						Size(design.Space6, design.Space6).PaddingX(0).Radius(design.RadiusSM)
					button.Children(func() {
						ui.Icon(c, icons.Copy()).Size(design.Space3, design.Space3).
							TextColor(t.LabelTertiary)
					})
					if button.Clicked() {
						c.WriteClipboard(value)
					}
				}
			})
		}
		text := s.Text
		if text == "" {
			if s.Empty != "" {
				design.Apply(ui.Text(c, s.Empty), t.TextMuted(design.TypeXXS))
			}
			return
		}
		body := func(cc *ui.Context) {
			element := design.Apply(ui.Text(cc, text), t.Font(design.TypeXXS))
			if s.Mono {
				element = design.Apply(element, t.MonoFamily())
			}
			switch s.Tone {
			case ToolSectionRemoved:
				element = element.TextColor(t.DangerText)
			case ToolSectionAdded:
				element = element.TextColor(t.SuccessText)
			default:
				element = element.TextColor(t.LabelSecondary)
			}
			element.Selectable()
		}
		if s.Bounded {
			ui.Scroll(c).FillWidth().MaxHeight(t.Scale(toolSectionMaxHeight)).Children(func() {
				body(c)
			})
		} else {
			body(c)
		}
		if s.Hint != "" {
			design.Apply(ui.Text(c, s.Hint), t.TextMuted(design.TypeXXXS)).MaxLines(1).Ellipsis("…")
		}
	})
}

// ToolPreview turns a tool summary into a single-line collapsed preview: line
// breaks and runs of whitespace collapse to one space, and the text is cut to
// max runes with an ellipsis.
func ToolPreview(summary string, max int) string {
	trimmed := ""
	space := false
	for _, r := range summary {
		if r == '\n' || r == '\r' || r == '\t' || r == ' ' {
			space = trimmed != ""
			continue
		}
		if space {
			trimmed += " "
			space = false
		}
		trimmed += string(r)
	}
	runes := []rune(trimmed)
	if max <= 0 || len(runes) <= max {
		return trimmed
	}
	return string(runes[:max]) + "…"
}
