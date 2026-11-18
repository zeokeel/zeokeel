package uikit

// The compact transcript tool row: the quiet anatomy shared by the registered
// tool-card renderers (internal/ui/toolcards) and the session screen's fallback
// for tools without a renderer. A transcript is mostly prose; a tool call is one
// muted disclosure row, not a panel.
//
// The row's anatomy is fixed (B-spec-v2 §3.3.1): a 32 DIP line of
// [status glyph 16][tool name 13/500][input summary 12 tertiary, ellipsized]
// [state word][duration 11][chevron]. The leading glyph is the call's *state*,
// not its category: a running call shows the official spinner in the accent
// role, a failure the alert glyph in DangerText, a finished call a success
// check, and a stopped call a neutral dot (a user stop is never drawn as a
// failure).
//
// DELIBERATE CONVENTION (do not "fix" it without reading this): a *successful*
// row shows no state word. Success is the state a transcript is mostly made of,
// so it is the one quiet state: the check glyph carries it in the visual channel
// and the row's accessible name ("<tool> · 完成") carries it for a screen reader,
// which keeps the two channels the status contract requires (DESIGN.md §5.2)
// without printing 完成 on every finished call. Running, failed and cancelled
// rows do state their word — those are the states a reader must notice now.
// TestToolRowSuccessKeepsTheWordOutOfTheRow pins it.
//
// The chevron follows the row's text rather than the column's far end
// (ToolRowOptions.InlineChevron), because a process timeline stacks reasoning
// rows and tool rows: see uikit.ReasoningTrace's marker column, and note the
// registered tool cards set the same option (internal/ui/toolcards).
//
// A tool row paints no border of its own: the row is hover tint plus type, and
// only the expanded panel draws a hairline above its body (BorderL1), exactly
// one border between the trigger and the content (B-spec-v2 §2).

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
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
	// Icon is the tool's category glyph, drawn in the activity band's marker
	// column. The wide row leads with the call's state glyph instead, so a
	// finished call's category is read from its name.
	Icon *ui.SVG
	// Name is the friendly, localized tool name shown in the row.
	Name string
	// RawName is the protocol tool id. It is not a tooltip — a tooltip is an
	// overlay that takes Escape from the conversation (A4-I-01) — it travels as
	// the row's accessible description and as a muted line in the expanded panel.
	RawName string
	// Status is the localized status label ("完成"/"Completed"). It is drawn as
	// the row's state word for every state except success: a finished call keeps
	// the check glyph and the accessible name and stays wordless, so a transcript
	// of finished calls reads as prose (the deliberate convention documented at
	// the top of this file).
	Status string
	// StatusColor is the WCAG-AA status text color (uikit.StatusColor).
	StatusColor ui.Color
	// Running shows the official indeterminate-work spinner in place of the
	// state glyph while the call runs.
	Running bool
	// State is the call's place in the shared status vocabulary: it picks the
	// leading glyph and its color. Running is the accent role, never warning
	// (F-06: warning is reserved for a gate that needs the user). The zero value
	// with Running keeps the running spinner.
	State StatusKind
	// Preview is the collapsed one-line input summary (already normalized to
	// one line); empty keeps the state word at the row's right edge.
	Preview string
	// Duration is the call's formatted elapsed time ("1.2s", "3m 04s"), drawn in
	// the micro step before the chevron. Empty draws nothing.
	Duration string
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
	// row.
	InlineChevron bool
	// MarkerColumn puts the category glyph (or the official spinner while the
	// call runs) in a fixed 16 DIP column at the start of the line, so every row
	// of a transcript activity band starts its copy on the same x and the reader
	// can tell a reasoning row from a tool row at a glance.
	MarkerColumn bool
}

// toolCallHeadOptions is the head of one tool-call line: the state glyph, the
// friendly name and the status word, as the compact ToolRow line and the
// ToolCallCard header both draw it. The caller adds its own tail.
type toolCallHeadOptions struct {
	// Icon is the category glyph of the activity band's marker column.
	Icon *ui.SVG
	// Name is the friendly, localized tool name.
	Name string
	// Status is the localized status label; empty hides the status.
	Status string
	// StatusColor is the WCAG-AA status text color (uikit.StatusColor).
	StatusColor ui.Color
	// Kind is the status vocabulary kind: it picks the state glyph.
	Kind StatusKind
	// Running shows the official indeterminate spinner in place of the glyph.
	Running bool
	// Badge draws the status as the shared status badge (the complete card); the
	// compact row writes it as the state word instead.
	Badge bool
	// ShowWord draws the state word beside the head even for a settled status
	// (the complete card's header states its status in words).
	ShowWord bool
}

// toolStateGlyph draws the call's state in one 16 DIP slot: the official
// spinner while the call runs, the semantic status glyph when it settled, and a
// muted dot for a state the vocabulary does not name. It is the row's leading
// mark, so a reader sees "running / failed / finished / stopped" before reading
// anything.
func toolStateGlyph(c *ui.Context, t *design.Tokens, o toolCallHeadOptions) {
	state := o.Kind
	if state == StatusUnknown && o.Running {
		state = StatusRunning
	}
	if state == StatusRunning && o.Running {
		ui.Spinner(c).Label(o.Status).Size(design.Space3, design.Space3).
			TextColor(t.Accent)
		return
	}
	switch state {
	case StatusRunning, StatusInProgress:
		ui.Spinner(c).Label(o.Status).Size(design.Space3, design.Space3).
			TextColor(t.Accent)
	case StatusSuccess:
		// The quiet state still reports itself through the glyph slot's status role
		// (see toolCallHead): that is how a successful row states "完成" without
		// printing it on every finished call.
		ui.Icon(c, icons.Check()).Size(design.Space3, design.Space3).
			TextColor(t.SuccessText)
	case StatusError:
		ui.Icon(c, icons.AlertTriangle()).Size(design.Space3, design.Space3).
			TextColor(t.DangerText)
	case StatusCancelled:
		// A user stop is neutral: a muted dot, never the failure glyph.
		design.Apply(ui.Box(c).Size(design.Space2, design.Space2), func(el ui.Element) ui.Element {
			return el.Radius(design.Space1).Background(t.LabelTertiary)
		})
	default:
		design.Apply(ui.Box(c).Size(design.Space2, design.Space2), func(el ui.Element) ui.Element {
			return el.Radius(design.Space1).Background(t.LabelTertiary)
		})
	}
}

// toolStatusWord is the state word a row shows: every state except success
// states itself in words (two channels, DESIGN.md §5.2). Success stays quiet —
// its glyph and accessible name already carry it.
func toolStatusWord(o toolCallHeadOptions) bool {
	if o.Status == "" {
		return false
	}
	if o.ShowWord || o.Badge {
		return true
	}
	switch o.Kind {
	case StatusSuccess:
		return false
	default:
		return true
	}
}

// toolCallHead draws the state glyph, the friendly tool name and the status of
// one tool call. The status travels as text, not color alone: the official
// spinner while running, the shared status badge on a complete card, the state
// word on the compact row.
func toolCallHead(c *ui.Context, t *design.Tokens, o toolCallHeadOptions) {
	// The glyph sits in a fixed 16 DIP slot. It is not an access node of its own:
	// the row's trigger is a disclosure button, and a button's children fold into
	// its name — which is why the trigger's Label carries the status word (see
	// ToolRow) for the state a reader must hear, including the success row that
	// draws no word.
	ui.Row(c).Width(design.Space3).Shrink(0).Justify(ui.Center).Children(func() {
		toolStateGlyph(c, t, o)
	})
	// The tool name is the row's one medium-weight label (13/500): the type
	// scale bakes weights into roles, and no role is 13 at medium, so the weight
	// is named from the token here (UD-10).
	design.Apply(ui.Text(c, o.Name), t.Font(design.TypeXS)).
		FontWeight(design.WeightMedium).TextColor(t.LabelSecondary).
		MaxLines(1).Ellipsis("…").Shrink(0)
	if o.Badge {
		if o.Status != "" {
			StatusBadge(c, t, o.Kind, o.Status)
		}
		return
	}
	if o.ShowWord {
		toolStatusText(c, t, o)
	}
}

// toolStatusText draws the call's state word: the second channel beside the
// glyph, in the status's AA text color. It recedes behind the name and the
// summary, so a transcript never reads as a status bar.
func toolStatusText(c *ui.Context, t *design.Tokens, o toolCallHeadOptions) {
	if !toolStatusWord(o) {
		return
	}
	design.Apply(ui.Text(c, o.Status), t.Font(design.TypeXXS)).
		TextColor(o.StatusColor).NoWrap().Shrink(0)
}

// ToolRow draws one compact transcript tool call as one official mygo
// disclosure (ui.CollapsibleBase): the whole row is the trigger, so assistive
// technology sees expanded/collapsed, Enter and Space work, and the panel grows
// into view. The row is 32 DIP of type and a hover tint — no border, no card —
// and the expanded panel is separated by one 1 DIP BorderL1 rule.
func ToolRow(c *ui.Context, o ToolRowOptions) {
	t := design.Of(c)
	kind := o.State
	if kind == StatusUnknown && o.Running {
		kind = StatusRunning
	}
	buildLine := func(c *ui.Context, progress float32, disclosure bool) {
		if o.MarkerColumn {
			buildActivityLine(c, t, o, kind, progress, disclosure)
			return
		}
		head := toolCallHeadOptions{
			Name:        o.Name,
			Status:      o.Status,
			StatusColor: o.StatusColor,
			Kind:        kind,
			Running:     o.Running,
		}
		toolCallHead(c, t, head)
		// While the row is open the panel already shows the text: repeating the
		// preview in the trigger rendered a short result twice (H15 opens short
		// results by default). The trigger keeps only the head, the preview and
		// the chevron.
		if o.Preview != "" && !o.Open {
			// The collapsed preview is the call's input summary (a command, a
			// path, a query): the mono face reads it as code rather than prose,
			// still one ellipsized line.
			preview := design.Apply(ui.Text(c, o.Preview), t.MonoText(design.TypeXXS)).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
			if !o.InlineChevron {
				preview.Grow(1)
			}
		} else if !o.InlineChevron {
			ui.Spacer(c).Grow(1)
		}
		// The tail order is the activity band's: duration, then the state word, then
		// the chevron. Both rows are one component seen at two widths, so the same
		// three pieces must not swap places between them.
		if o.Duration != "" {
			design.Apply(ui.Text(c, o.Duration), t.Font(design.TypeXXXS)).
				TextColor(t.LabelTertiary).NoWrap().Shrink(0)
		}
		toolStatusText(c, t, head)
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
		// A call with nothing to disclose is still one readable row: the group and
		// its name carry the state in words for assistive technology, which the
		// quiet success glyph alone would not.
		label := o.Name
		if o.Status != "" {
			label = o.Name + " · " + o.Status
		}
		row := ui.Row(c.Key(o.Key)).FillWidth().Height(design.RowCompactHeight).
			AlignItems(ui.Center).Gap(design.Space2).PaddingX(design.Space2).
			Role(ui.RoleGroup).Label(label)
		if o.RawName != "" && o.RawName != o.Name {
			row = row.Description(o.RawName)
		}
		row.Children(func() {
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
	trigger := parts.Trigger.FillWidth().Height(design.RowCompactHeight).
		AlignItems(ui.Center).Gap(design.Space2).PaddingX(design.Space2).
		Radius(design.RadiusSM).Label(label).Transition(design.TransitionColors(c))
	if o.RawName != "" && o.RawName != o.Name {
		// The protocol id is the row's accessible description, not a tooltip: a
		// tooltip is an overlay that would take Escape from the conversation
		// (A4-I-01).
		trigger = trigger.Description(o.RawName)
	}
	if parts.Trigger.Hovered() || parts.Trigger.Focused() {
		trigger.Background(t.BgHover)
	}
	trigger.Children(func() {
		buildLine(c, parts.Progress(), true)
	})
	parts.Panel(func() {
		// One hairline between the trigger and the panel's content: the panel's
		// top edge is the row's only border.
		design.Apply(ui.Column(c).FillWidth(), func(el ui.Element) ui.Element {
			return el.BorderWidth(1, design.Space0, design.Space0, design.Space0).
				BorderColor(t.BorderL1)
		}).PaddingStart(design.Space3 + design.Space2).PaddingEnd(design.Space2).
			PaddingY(design.Space2).Gap(design.Space2).Children(func() {
			for _, section := range o.Sections {
				ToolPanelSectionView(c, t, section)
			}
			if o.RawName != "" && o.RawName != o.Name {
				// The protocol id stays available where the details are read,
				// instead of in a tooltip over the row.
				design.Apply(ui.Text(c, o.RawName), t.MonoText(design.TypeXXXS)).
					TextColor(t.LabelTertiary)
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
// the call runs), the friendly action name, the state word, a muted middle dot
// and the one-line preview, then the inline chevron. The marker column lets a
// reader tell a reasoning row from a tool row at one x; the muted dot separates
// the action name from its preview without another label.
func buildActivityLine(c *ui.Context, t *design.Tokens, o ToolRowOptions, kind StatusKind,
	progress float32, disclosure bool) {
	ui.Row(c).Width(design.Space4).Shrink(0).Justify(ui.Center).Children(func() {
		if o.Running {
			ui.Spinner(c).Label(o.Status).Size(design.Space3, design.Space3).
				TextColor(t.Accent)
			return
		}
		icon := o.Icon
		if icon == nil {
			icon = icons.Message()
		}
		ui.Icon(c, icon).Size(design.Space4, design.Space4).TextColor(t.LabelTertiary)
	})
	design.Apply(ui.Text(c, o.Name), t.Font(design.TypeXS)).
		FontWeight(design.WeightMedium).TextColor(t.LabelSecondary).
		MaxLines(1).Ellipsis("…").Shrink(0)
	// The band's tail is the wide row's tail, in the same order: the preview takes
	// the room, then the measured duration, then the state word, then the chevron.
	// One timeline mixes band rows and wide rows, and two orders for the same three
	// pieces read as two widgets (2026-11 per-screen review).
	if o.Preview != "" && !o.Open {
		design.Apply(ui.Text(c, "·"), t.Font(design.TypeXXXS)).
			TextColor(t.LabelTertiary).NoWrap()
		// The band's collapsed preview is the same input summary as the wide
		// row's: mono face, one ellipsized line, and it absorbs the free room.
		design.Apply(ui.Text(c, o.Preview), t.MonoText(design.TypeXXS)).
			TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…").Grow(1)
	} else {
		// Nothing to summarize: the spacer holds the tail at the row's end.
		ui.Spacer(c).Grow(1)
	}
	if o.Duration != "" {
		design.Apply(ui.Text(c, o.Duration), t.Font(design.TypeXXXS)).
			TextColor(t.LabelTertiary).NoWrap().Shrink(0)
	}
	if o.Status != "" {
		design.Apply(ui.Text(c, o.Status), t.Font(design.TypeXXS)).
			TextColor(o.StatusColor).NoWrap().Shrink(0)
	}
	if disclosure {
		ui.Icon(c, icons.ChevronRight()).Size(design.Space3, design.Space3).
			TextColor(t.LabelTertiary).Rotate(90 * progress)
	}
}

// ToolPanelSectionView draws one expanded-panel section: a muted label with an
// optional one-click copy action, the bounded body in the brand type/face, and
// an optional muted hint (the truncation notice). The body is the panel's
// reading step (13/20): one step below prose, one step above the row's metadata.
func ToolPanelSectionView(c *ui.Context, t *design.Tokens, s ToolPanelSection) {
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		// A section without its own heading is just a block of text: draw no
		// label row when there is neither a label nor a copy action.
		if s.Label != "" || s.CopyLabel != "" {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				design.Apply(ui.Text(c, s.Label), t.Font(design.TypeXXXS)).
					TextColor(t.LabelTertiary).NoWrap()
				ui.Spacer(c).Grow(1)
				if s.CopyLabel != "" {
					value := s.CopyValue
					if value == "" {
						value = s.Text
					}
					IconButton(c, IconButtonOptions{
						Label:    s.CopyLabel,
						Icon:     icons.Copy(),
						Size:     design.IconSizeS,
						Variant:  IconButtonNative,
						InkColor: t.LabelTertiary,
						OnClick:  func() { c.WriteClipboard(value) },
					})
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
			element := design.Apply(ui.Text(cc, text), t.Font(design.TypeXS))
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

// toolShellTools are the tools whose result ends with the shell's own exit
// marker ("[exit code: N]") — the marker the agent's prompt tells the model to
// check on every command result. Only these can be re-classified by an exit
// code: a file read has no exit status to read.
var toolShellTools = map[string]bool{
	"bash": true, "pwsh": true, "powershell": true, "shell": true, "cmd": true, "cmd.exe": true,
}

// ToolExitCode reads the shell's exit marker out of a result text: the bracketed
// "[exit code: N]" the Rig tools append. The last marker wins — a command that
// prints its own marker cannot override the one the tool added, and a nonzero
// code is the failure. It reports false when the text carries no marker.
func ToolExitCode(result string) (int, bool) {
	const marker = "[exit code: "
	index := strings.LastIndex(result, marker)
	if index < 0 {
		return 0, false
	}
	rest := result[index+len(marker):]
	end := strings.IndexByte(rest, ']')
	if end < 0 {
		return 0, false
	}
	code, err := strconv.Atoi(strings.TrimSpace(rest[:end]))
	if err != nil {
		return 0, false
	}
	return code, true
}

// ToolCallStatusKind folds a call's status word *and* its shell exit marker onto
// the status vocabulary, so a command that exited nonzero is never drawn as a
// success. The background-job pane already classifies by exit code
// (ui/session's backgroundJobStatus); this is the transcript row's half of that
// contract, and it is the same rule: a shell result with a nonzero code failed,
// whatever word the backend put on the call.
//
// Running and cancelled calls keep their state: a live call has no exit code yet,
// and a user stop is neutral (its process may exit nonzero because it was stopped).
func ToolCallStatusKind(call contracts.ToolCall) StatusKind {
	kind := ParseToolStatus(call.Status).Kind()
	if kind == StatusRunning || kind == StatusCancelled || kind == StatusInterrupted {
		return kind
	}
	if !toolShellTools[strings.ToLower(strings.TrimSpace(call.Name))] {
		return kind
	}
	code, ok := ToolExitCode(call.Summary)
	if !ok {
		return kind
	}
	if code != 0 {
		return StatusError
	}
	if kind == StatusUnknown {
		return StatusSuccess
	}
	return kind
}

// ToolCallStatusLabel is the localized state word of a call, derived from the
// same decision as ToolCallStatusKind so the word and the mark can never
// disagree: a command re-classified by its exit code reads 失败/Failed even when
// the backend called the call a success.
func ToolCallStatusLabel(locale contracts.LocaleService, call contracts.ToolCall) string {
	kind := ToolCallStatusKind(call)
	declared := ParseToolStatus(call.Status).Kind()
	if kind != declared {
		switch kind {
		case StatusError:
			return T(locale, "失败", "Failed")
		case StatusSuccess:
			return T(locale, "完成", "Completed")
		}
	}
	return ToolStatusLabel(locale, call.Status)
}

// ToolCallElapsed is a tool call's measured interval in the row's micro step, or
// "" when either end of the interval is unknown (a restored call without
// timestamps, a call still running): a guessed duration would be a lie about
// the only thing the number is for.
func ToolCallElapsed(call contracts.ToolCall) string {
	if call.StartedAt.IsZero() || call.EndedAt.IsZero() || call.EndedAt.Before(call.StartedAt) {
		return ""
	}
	return FormatToolDuration(call.EndedAt.Sub(call.StartedAt))
}

// FormatToolDuration renders one measured interval at the precision a reader can
// use: whole milliseconds under a second, one decimal of seconds under a minute,
// and minutes with zero-padded seconds above that. A sub-millisecond call reads
// "<1ms" rather than a misleading 0ms.
func FormatToolDuration(duration time.Duration) string {
	switch {
	case duration <= 0:
		return ""
	case duration < time.Millisecond:
		return "<1ms"
	case duration < time.Second:
		return strconv.FormatInt(duration.Milliseconds(), 10) + "ms"
	case duration < time.Minute:
		return strconv.FormatFloat(duration.Seconds(), 'f', 1, 64) + "s"
	default:
		minutes := int(duration / time.Minute)
		seconds := int(duration.Seconds()) - minutes*60
		return fmt.Sprintf("%dm%02ds", minutes, seconds)
	}
}

// ToolPreview turns a tool summary into a single-line collapsed preview: line
// breaks and runs of whitespace collapse to one space, and the text is cut to
// max runes with an ellipsis. It is a data-side normalizer for callers that must
// hand a bounded string to something other than a Text element (a menu label, a
// clipboard payload); a row that displays the preview lets the layout ellipsize
// it instead (MaxLines(1).Ellipsis), so the full text stays readable.
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
