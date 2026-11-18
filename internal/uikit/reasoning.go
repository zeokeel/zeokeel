package uikit

// The model's reasoning, as one secondary disclosure: the product's only
// reasoning look (S2 in docs/AGENT-SURFACES.md). While the model thinks the
// trace is open, the trigger shows the official spinner and 思考中…, and the
// panel streams the reasoning Markdown in the muted theme. Once the turn ends it
// folds to one quiet line (reasoning glyph, 思考, the line count), the reader's
// toggle is persisted by the caller, and a toggle keeps the transcript's place
// through the caller's HoldPlace flag. The reasoning is never repeated in the
// reply's prose and never carries the error of the turn: errors belong to the
// error line, the finished reasoning stays foldable.

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// reasoningRuleWidth is the 2 DIP rule down the reasoning body's left edge: the
// same rule weight as a quote bar and the approval card's status rail, derived
// from the 4 DIP grid instead of a bare literal.
const reasoningRuleWidth float32 = design.Space1 / 2

// reasoningMetadata draws one metadata item of the folded summary (the trace's
// duration, its line count): a middle dot and the value in the micro step, so
// the items read as one muted tail behind the 思考 label.
func reasoningMetadata(c *ui.Context, t *design.Tokens, text string) {
	design.Apply(ui.Text(c, "·"), t.Font(design.TypeXXXS)).
		TextColor(t.LabelTertiary).NoWrap().Shrink(0)
	design.Apply(ui.Text(c, text), t.Font(design.TypeXXXS)).
		TextColor(t.LabelTertiary).NoWrap().Shrink(0)
}

// ReasoningTraceOptions is one reasoning disclosure.
type ReasoningTraceOptions struct {
	// Key is the trace's stable identity (the message ID), so the fold survives
	// a virtualized transcript List rebuilding its rows.
	Key string
	// Reasoning is the reasoning Markdown; empty renders nothing (no
	// placeholder row for a turn without reasoning).
	Reasoning string
	// Streaming starts the trace open and streams the Markdown; a finished
	// trace defaults to its folded summary line (the caller's Open state wins).
	Streaming bool
	// Open is the caller-persisted fold state: the disclosure flips the bound
	// pointer and OnToggle receives the reader's new state.
	Open *bool
	// OnToggle receives the new fold state after the reader toggles the trace.
	OnToggle func(open bool)
	// HoldPlace is the transcript's one-shot fold flag: a toggle sets it, and
	// the surrounding SessionThread turns it into ListState.HoldPlace on the
	// next layout so the toggled row does not jump.
	HoldPlace *bool
	// Locale localizes the trigger copy and its accessible name; nil falls back
	// to the product's Chinese copy.
	Locale contracts.LocaleService
	// Markdown overrides the muted reasoning theme: a host that follows the
	// desktop's chat-font-size setting caches one (see ReasoningMarkdown). Nil
	// builds the entry's muted default from the current tokens.
	Markdown *markdown.Options
	// InlineChevron puts the disclosure chevron right after the trigger's label
	// and line count (one Space2 gap) instead of the line's far end, so a
	// transcript activity band reads as one compact left-aligned row. The
	// default false keeps the wide row's far-end chevron for other callers.
	InlineChevron bool
	// MarkerColumn puts the type glyph (the spinner while thinking) in a fixed
	// 16 DIP column at the start of the trigger line, so every row of a
	// transcript activity band starts its copy on the same x. It is the band's
	// marker column; the default false keeps the old inline glyph.
	MarkerColumn bool
	// Duration is the trace's formatted elapsed time ("2.5s"), drawn in the
	// folded summary between a middle dot and the chevron. Empty omits it: a
	// multi-segment trace has no per-segment time to report, and an approximate
	// number would misstate what the reader is judging.
	Duration string
}

// ReasoningTrace draws one reasoning disclosure (ui.CollapsibleBase): the whole
// trigger line toggles it (Enter/Space included), assistive technology reads the
// trace's name (思考中… while thinking, 思考 once finished), the finished line
// carries the reasoning line count when there is more than one, and the panel
// shows the reasoning Markdown in the muted theme. It returns without building
// anything for an empty reasoning.
func ReasoningTrace(c *ui.Context, o ReasoningTraceOptions) {
	if strings.TrimSpace(o.Reasoning) == "" {
		return
	}
	t := design.Of(c)
	open := o.Open
	if open == nil {
		open = new(bool)
	}
	label := T(o.Locale, "思考", "Thinking")
	if o.Streaming {
		label = T(o.Locale, "思考中…", "Thinking…")
	}
	var parts ui.CollapsibleParts
	if o.Key != "" {
		parts = ui.CollapsibleBase(c.Key(o.Key), open)
	} else {
		parts = ui.CollapsibleBase(c, open)
	}
	// The trigger names itself for assistive technology: 思考中… while the model
	// thinks, 思考 once it finished. The duration and the line count are visual
	// metadata only.
	trigger := parts.Trigger.FillWidth().Height(design.RowCompactHeight).
		AlignItems(ui.Center).Gap(design.Space2).PaddingX(design.Space2).
		Radius(design.RadiusSM).Label(label).Transition(design.TransitionColors(c))
	// The marker column and the standalone glyph are the same 16 DIP slot with the
	// same gap, so a reasoning row and a tool row start their copy on one x in the
	// activity band as well as outside it.
	if parts.Trigger.Hovered() || parts.Trigger.Focused() {
		trigger.Background(t.BgHover)
	}
	trigger.Children(func() {
		// The glyph always owns the same 16 DIP slot: in the activity band (where
		// the tool rows put their state glyph in that slot) and on its own, so a
		// reasoning row and a tool row start their copy on one x. The official
		// spinner stands in while the model thinks, in the accent role.
		ui.Row(c).Width(design.Space4).Shrink(0).Justify(ui.Center).Children(func() {
			if o.Streaming {
				ui.Spinner(c).Label(T(o.Locale, "思考中", "Thinking")).
					Size(design.Space3, design.Space3).TextColor(t.Accent)
				return
			}
			ui.Icon(c, icons.Sparkles()).Size(design.Space4, design.Space4).
				TextColor(t.LabelTertiary)
		})
		// The folded summary is one quiet line: 思考 13/500, then the trace's
		// metadata — how long it took and how many lines it holds — so the
		// reader can decide whether it is worth opening (A2 §6.6).
		design.Apply(ui.Text(c, label), t.Font(design.TypeXS)).
			FontWeight(design.WeightMedium).TextColor(t.LabelSecondary).NoWrap()
		if o.Streaming {
			return
		}
		// The tail is pinned: the spacer takes the free room, the metadata (the
		// measured duration and the line count) grows leftwards into it, and the
		// chevron stays at the row's end. Appending the metadata when a segment
		// finishes therefore cannot move the chevron — only the row's text, and only
		// sideways (the trigger is a fixed 28 DIP line, so no height moves either).
		ui.Spacer(c).Grow(1)
		if o.Duration != "" {
			reasoningMetadata(c, t, o.Duration)
		}
		if lines := ReasoningLineCount(o.Reasoning); lines > 1 {
			reasoningMetadata(c, t, fmt.Sprintf(T(o.Locale, "%d 行", "%d lines"), lines))
		}
		ui.Icon(c, icons.ChevronRight()).Size(design.Space3, design.Space3).
			TextColor(t.LabelTertiary).Rotate(90 * parts.Progress())
	})
	parts.Panel(func() {
		// The thinking body sits under the trigger's text (past the glyph), as a
		// nested block; the muted Markdown theme keeps it subordinate to prose.
		options := o.Markdown
		if options == nil {
			options = ReasoningMarkdown(t, 0)
		}
		// The reasoning body is secondary content and says so: a 2 DIP BorderL2
		// rule down its left edge (the product's quote/indent rule), one Space3
		// of room inside, and the 13/20 XS step through the muted Markdown theme.
		design.Apply(ui.Column(c).FillWidth(), func(el ui.Element) ui.Element {
			return el.BorderWidth(design.Space0, design.Space0, design.Space0, reasoningRuleWidth).
				BorderColor(t.BorderL2).Transition(design.TransitionHeight(c))
		}).PaddingStart(design.Space3).PaddingEnd(design.Space2).
			PaddingY(design.Space1).Gap(design.Space1).Children(func() {
			if o.Streaming {
				markdown.Stream(c, o.Reasoning, options)
				return
			}
			markdown.View(c, o.Reasoning, options)
		})
	})
	if parts.Trigger.Changed() {
		if o.OnToggle != nil {
			o.OnToggle(*open)
		}
		if o.HoldPlace != nil {
			*o.HoldPlace = true
		}
	}
}

// ReasoningLineCount counts the non-blank lines of a reasoning body: the folded
// summary shows it when the reasoning has more than one line, so the reader can
// tell a two-line note from a long chain of thought.
func ReasoningLineCount(text string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

// ReasoningMarkdown is the muted Markdown configuration of a reasoning panel:
// the given body size in the secondary text color (strong text stays primary so
// a bolded conclusion reads), with headings kept subordinate to the reply's own
// headings. size 0 uses the design's XS metric; a transcript that follows the
// desktop's chat-font-size setting passes the reply's body size minus one.
func ReasoningMarkdown(t *design.Tokens, size float32) *markdown.Options {
	if size <= 0 {
		size, _, _ = t.Metrics(design.TypeXS)
	}
	theme := markdown.DefaultTheme(t.Theme())
	theme.FontSize = size
	theme.Text = t.LabelSecondary
	theme.Muted = t.LabelTertiary
	theme.Strong = t.LabelPrimary
	theme.Code = t.LabelSecondary
	theme.QuoteText = t.LabelTertiary
	theme.BlockSpacing = 0.6
	// The panel's headings must never outrank the reply's: keep them small
	// multiples of the thinking size instead of the Markdown defaults, so even
	// at the large chat font size the panel's H1 stays below the reply's 17 DIP.
	sizes := [6]float32{1.1, 1.05, 1.0, 0.95, 0.9, 0.85}
	for index := range theme.Headings {
		theme.Headings[index].Size = sizes[index]
		theme.Headings[index].Weight = design.WeightSemibold
		theme.Headings[index].Color = t.LabelSecondary
	}
	theme.Headings[5].Color = t.LabelTertiary
	return &markdown.Options{
		Theme:      theme,
		Extensions: parser.Default,
		Reveal:     &markdown.Reveal{Unit: markdown.AtOnce, Fade: -1},
		CodeBlock:  MarkdownCodeBlock,
	}
}

// MarkdownDoc renders a document inside a narrow panel — the inspector's file
// and artifact previews. The chat's Markdown defaults put an H1 at ~22 DIP,
// larger than a settings page's 20 DIP title while the column is only 320 DIP
// wide (V16). This theme keeps the body on the TypeXS step (13 DIP) and scales
// the headings off it, so the document stays subordinate to the screen title.
func MarkdownDoc(t *design.Tokens, size float32) *markdown.Options {
	if size <= 0 {
		size, _, _ = t.Metrics(design.TypeXS)
	}
	theme := markdown.DefaultTheme(t.Theme())
	theme.FontSize = size
	theme.Text = t.LabelSecondary
	theme.Muted = t.LabelTertiary
	theme.Strong = t.LabelPrimary
	theme.Code = t.LabelSecondary
	theme.QuoteText = t.LabelTertiary
	theme.BlockSpacing = 0.6
	sizes := [6]float32{1.15, 1.08, 1.0, 0.95, 0.9, 0.85}
	for index := range theme.Headings {
		theme.Headings[index].Size = sizes[index]
		theme.Headings[index].Weight = design.WeightSemibold
		theme.Headings[index].Color = t.LabelSecondary
	}
	theme.Headings[0].Color = t.LabelPrimary
	theme.Headings[5].Color = t.LabelTertiary
	return withMarkdownImageFallback(&markdown.Options{
		Theme:      theme,
		Extensions: parser.Default,
		Reveal:     &markdown.Reveal{Unit: markdown.AtOnce, Fade: -1},
		CodeBlock:  MarkdownCodeBlock,
	})
}
