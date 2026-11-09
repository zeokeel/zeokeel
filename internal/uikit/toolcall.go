package uikit

// The complete card anatomy of one tool call: the entry ToolCallCard replaces
// the hand-built full cards (the session transcript's decision card, the
// registered plan card and the demo probe card) so a tool call has exactly two
// looks — the compact ToolRow line and this card. Both share the same trigger
// head (toolCallHead in toolrow.go): the category glyph, the friendly localized
// name and the status — the official spinner with its warning/status text while
// running, the shared status badge otherwise (DESIGN.md §5.2 semantics, owned by
// status.go). The card adds the optional extra header content (the plan card's
// reviewer state), the optional step metadata, one official Collapsible detail
// panel with the copyable ToolPanelSections and the team footer, or a rich
// always-visible Content (the plan Markdown, the demo summary).
//
// A call with nothing to disclose degrades to the compact line instead of
// opening an empty panel; a decision card keeps its panel because it is the
// decision's only visible record in the transcript.

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// ToolCallCardVariant is where a call card appears, and whether it stays a card
// when it has no detail body.
type ToolCallCardVariant int

const (
	// ToolCallCardNormal is a complete card whose call has a detail body; one
	// without a body renders as the compact line instead of an empty panel.
	ToolCallCardNormal ToolCallCardVariant = iota
	// ToolCallCardDecision is the transcript card of a decision tool
	// (plan_review, exit_plan_mode, ask_user): it keeps its card panel even
	// without a detail body, because the card is the decision's visible record.
	ToolCallCardDecision
)

// ToolCallCardOptions is one complete tool-call card.
type ToolCallCardOptions struct {
	// Key is the card's stable identity (the call's row key).
	Key string
	// Call is the tool call: its name selects the category glyph, its status
	// selects the status semantics.
	Call contracts.ToolCall
	// Locale localizes the friendly name, the status label and the disclosure
	// label; nil falls back to the product's Chinese copy.
	Locale contracts.LocaleService
	// Title overrides the friendly name in the header (the plan card's
	// "计划审阅", the demo probe's historical title).
	Title string
	// StatusLabel overrides the localized status label; the semantic color still
	// comes from Call.Status (the demo probe shows its raw "ok").
	StatusLabel string
	// HideStatus hides the mapped status, for a card that carries its own
	// lifecycle badge in Header (the plan reviewer state) and must not stack two.
	HideStatus bool
	// Header builds extra header content before the spacer (the plan card's
	// reviewer state badge); nil adds none.
	Header func(*ui.Context)
	// Sections are the disclosure's copyable segments: the tool's input, its
	// output or another bounded detail.
	Sections []ToolPanelSection
	// Content builds rich always-visible card content in place of the
	// disclosure: the plan Markdown or the demo probe's summary.
	Content func(*ui.Context)
	// Footer builds the disclosure's closing actions (the team/task
	// cross-navigation); nil adds none.
	Footer func(*ui.Context)
	// Open is the disclosure state the caller persists; nil starts folded. The
	// card flips the bound pointer and reports the new state through OnToggle.
	Open *bool
	// OnToggle receives the disclosure's new state after the reader toggles it.
	OnToggle func(open bool)
	// Steps is the optional step number shown at the header's end (the
	// trajectory timeline); 0 hides it.
	Steps int
	// Variant decides whether a card without a detail body keeps its panel.
	Variant ToolCallCardVariant
}

// ToolCallCard draws one complete tool-call card: the shared tool line as the
// header, then either its rich Content or the official Collapsible detail panel
// with the sections and the footer. A call without a body degrades to a single
// quiet line (unless it is a decision card), so an expanded card always has
// something to read.
func ToolCallCard(c *ui.Context, o ToolCallCardOptions) {
	t := design.Of(c)
	kind := ParseToolStatus(o.Call.Status).Kind()
	name := o.Title
	if name == "" {
		name = ToolDisplayName(o.Locale, o.Call.Name)
	}
	status := o.StatusLabel
	if status == "" {
		status = ToolStatusLabel(o.Locale, o.Call.Status)
	}
	stepLabel := ""
	if o.Steps > 0 {
		stepLabel = T(o.Locale, fmt.Sprintf("步骤 %d", o.Steps), fmt.Sprintf("Step %d", o.Steps))
	}
	head := func(cc *ui.Context) {
		toolCallHead(cc, t, toolCallHeadOptions{
			Icon:        ToolIcon(o.Call.Name),
			Name:        name,
			Status:      status,
			StatusColor: StatusColor(t, kind),
			Kind:        kind,
			Running:     !o.HideStatus && kind == StatusRunning,
			// A running call shows the official spinner; only a settled status
			// becomes the badge.
			Badge: !o.HideStatus && kind != StatusRunning,
		})
	}
	hasBody := o.Content != nil || len(o.Sections) > 0 || o.Footer != nil
	if !hasBody && o.Header == nil && o.Variant != ToolCallCardDecision {
		var line ui.Element
		if o.Key != "" {
			line = ui.Row(c.Key(o.Key))
		} else {
			line = ui.Row(c)
		}
		line.FillWidth().AlignItems(ui.Center).
			Gap(design.Space2).PaddingY(design.Space1).Children(func() {
			head(c)
			ui.Spacer(c).Grow(1)
			if stepLabel != "" {
				design.Apply(ui.Text(c, stepLabel), t.Font(design.TypeXXXS)).
					TextColor(t.LabelTertiary).NoWrap()
			}
		})
		return
	}

	var card ui.Element
	if o.Key != "" {
		card = design.Apply(ui.Column(c.Key(o.Key)), t.Panel())
	} else {
		card = design.Apply(ui.Column(c), t.Panel())
	}
	card = card.FillWidth().Padding(design.Space3).Gap(design.Space2)
	card.Children(func() {
		ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			head(c)
			if o.Header != nil {
				o.Header(c)
			}
			ui.Spacer(c).Grow(1)
			if stepLabel != "" {
				design.Apply(ui.Text(c, stepLabel), t.Font(design.TypeXXXS)).
					TextColor(t.LabelTertiary).NoWrap()
			}
		})
		if o.Content != nil {
			o.Content(c)
			return
		}
		if len(o.Sections) == 0 && o.Footer == nil {
			return
		}
		open := o.Open
		if open == nil {
			// No caller-owned fold state: the disclosure stays folded. A caller
			// that wants the reader's fold remembered passes Open and OnToggle
			// (the transcript cards and the plan card do).
			open = new(bool)
		}
		details := ui.Collapsible(c, T(o.Locale, "详情", "Details"), open, func() {
			ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
				for _, section := range o.Sections {
					ToolPanelSectionView(c, t, section)
				}
				if o.Footer != nil {
					// The footer draws its own separator once it knows its action
					// is available, so a host without the command leaves no line.
					o.Footer(c)
				}
			})
		})
		if details.Changed() && o.OnToggle != nil {
			o.OnToggle(*open)
		}
	})
}

// SummaryDefaultOpen reports whether a tool call's detail starts expanded (H15):
// failures first, and very short results are cheap to show. The compact row and
// the complete card share the rule, so one call never gets two disclosure
// defaults.
func SummaryDefaultOpen(status, summary string) bool {
	if ParseToolStatus(status).Kind() == StatusError {
		return true
	}
	return len([]rune(strings.TrimSpace(summary))) <= 160
}
