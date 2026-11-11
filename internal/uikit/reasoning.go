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
	label := T(o.Locale, "思考", "Reasoning")
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
	// thinks, 思考 once it finished. The line-count hint stays visual only.
	trigger := parts.Trigger.FillWidth().AlignItems(ui.Center).
		Gap(design.Space2).PaddingY(design.Space1).Radius(design.RadiusSM).
		Label(label)
	if o.MarkerColumn {
		// The activity band's tool rows reserve the same 3 DIP state lane on
		// their left edge (toolRowGutterWidth), so reasoning and tool rows keep
		// their glyphs on one x even though only tool rows ever tint the lane.
		trigger = trigger.BorderWidth(design.Space0, design.Space0, design.Space0, toolRowGutterWidth).
			BorderColor(ui.Transparent)
	}
	if parts.Trigger.Hovered() || parts.Trigger.Focused() {
		trigger.Background(t.BgHover)
	}
	trigger.Children(func() {
		if o.MarkerColumn {
			// The band's fixed marker column: the type glyph (or the official
			// spinner while the model thinks) at a constant x, muted so it never
			// competes with the reply prose.
			ui.Row(c).Width(design.Space4).Shrink(0).Justify(ui.Center).Children(func() {
				if o.Streaming {
					ui.Spinner(c).Label(T(o.Locale, "思考中", "Thinking")).
						Size(design.Space3, design.Space3).TextColor(t.LabelTertiary)
					return
				}
				ui.Icon(c, icons.Sparkles()).Size(design.Space4, design.Space4).
					TextColor(t.LabelTertiary)
			})
			design.Apply(ui.Text(c, label), t.Font(design.TypeXXS)).
				TextColor(t.LabelSecondary)
			if !o.Streaming {
				if lines := ReasoningLineCount(o.Reasoning); lines > 1 {
					design.Apply(ui.Text(c, fmt.Sprintf(
						T(o.Locale, "%d 行", "%d lines"), lines)), t.Font(design.TypeXXXS)).
						TextColor(t.LabelTertiary)
				}
			}
		} else if o.Streaming {
			ui.Spinner(c).Label(T(o.Locale, "思考中", "Thinking")).
				Size(design.Space3, design.Space3).TextColor(t.LabelTertiary)
			design.Apply(ui.Text(c, label), t.Font(design.TypeXXS)).
				TextColor(t.LabelSecondary)
		} else {
			ui.Icon(c, icons.Sparkles()).Size(design.Space3, design.Space3).
				TextColor(t.LabelTertiary)
			design.Apply(ui.Text(c, label), t.Font(design.TypeXXS)).
				TextColor(t.LabelSecondary)
			if lines := ReasoningLineCount(o.Reasoning); lines > 1 {
				design.Apply(ui.Text(c, fmt.Sprintf(
					T(o.Locale, "%d 行", "%d lines"), lines)), t.Font(design.TypeXXXS)).
					TextColor(t.LabelTertiary)
			}
		}
		chevron := func() {
			ui.Icon(c, icons.ChevronRight()).Size(design.Space3, design.Space3).
				TextColor(t.LabelTertiary).Rotate(90 * parts.Progress())
		}
		if o.InlineChevron {
			// The chevron sits right after the label/line count (the row gap is
			// the 8 DIP spacing) and the spacer takes the rest of the line.
			chevron()
			ui.Spacer(c).Grow(1)
			return
		}
		ui.Spacer(c).Grow(1)
		chevron()
	})
	parts.Panel(func() {
		// The thinking body sits under the trigger's text (past the glyph), as a
		// nested block; the muted Markdown theme keeps it subordinate to prose.
		options := o.Markdown
		if options == nil {
			options = ReasoningMarkdown(t, 0)
		}
		design.Apply(ui.Column(c), t.Gap(design.Space1)).
			PaddingStart(design.Space3 + design.Space2).PaddingEnd(design.Space2).
			PaddingY(design.Space1).Children(func() {
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
		theme.Headings[index].Weight = 600
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
		theme.Headings[index].Weight = 600
		theme.Headings[index].Color = t.LabelSecondary
	}
	theme.Headings[0].Color = t.LabelPrimary
	theme.Headings[5].Color = t.LabelTertiary
	return &markdown.Options{
		Theme:      theme,
		Extensions: parser.Default,
		Reveal:     &markdown.Reveal{Unit: markdown.AtOnce, Fade: -1},
		CodeBlock:  MarkdownCodeBlock,
	}
}
