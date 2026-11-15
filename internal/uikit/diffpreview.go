package uikit

// The shared "what will change" preview: the block an approval card, a tool card
// or a trajectory row shows between a call's summary and its raw arguments.
//
// It draws the official diff viewer (docs/ui/diff-viewer.md) over the old and
// new texts the caller already holds — the component itself never reads a file,
// because it is a frame builder and a frame build must not do I/O. The
// arguments of a write/edit call carry everything needed: write_file's content
// is the whole new file, edit_file's old_string/new_string are the replaced
// region, so the block can show the change without touching the disk.
//
// Contract:
//   - the block header names the file path and counts the lines the official
//     diff adds and deletes (+N −M), as git counts them;
//   - content the component cannot show as an honest diff becomes a bounded
//     mono preview with a one-line explanation — never a blank box and never a
//     fabricated diff: a caller-declared fallback (arguments it could not
//     parse), binary text, and text beyond DiffPreviewMaxLines /
//     DiffPreviewMaxBytes all take that path;
//   - equal texts render the quiet "no visible changes" empty state instead of
//     an empty viewer;
//   - the viewer's state (scroll, folds) is kept across frames per block key,
//     and the block never grows past its height bound: a short diff takes its
//     natural height, a long one scrolls inside the viewer. Only a caller that
//     sets FitContent (a surface scrolling around the block) takes over the
//     height budget.
//
// It is the single entry for this surface so approval, tool cards and the
// trajectory cannot drift into three different diff renditions.

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

const (
	// DiffPreviewMaxLines bounds the texts one preview compares: a tool call
	// this large is summarized instead of diffed, so the frame build stays
	// cheap.
	DiffPreviewMaxLines = 2000
	// DiffPreviewMaxBytes bounds the compared texts in bytes (256 KiB), for
	// the same reason.
	DiffPreviewMaxBytes = 256 * 1024
	// DiffPreviewMaxHeight is the default height bound of the block (224 DIP);
	// a taller diff scrolls inside the official viewer.
	DiffPreviewMaxHeight float32 = design.Space8 * 7
	// diffPreviewFitLines is how many lines a diff may show at its natural
	// height. A card has no height budget of its own, so a longer diff is
	// bounded at DiffPreviewMaxHeight rather than stretching the surface.
	diffPreviewFitLines = 8
	// diffPreviewFallbackRunes bounds the mono preview of content that cannot
	// be diffed.
	diffPreviewFallbackRunes = 2000
)

// DiffPreviewOptions is one change preview. Old and New are the texts to
// compare; Path names the file (the header copy and the syntax language);
// FallbackText/FallbackNote describe a change the caller could not turn into a
// diff (unparseable arguments, an insertion whose position lives in the file).
type DiffPreviewOptions struct {
	// Key scopes the viewer state across frames (scroll, folds). Build two
	// blocks under one parent with distinct keys.
	Key string
	// Path is the file path shown in the header and used to pick the syntax
	// language by extension.
	Path string
	// Old and New are the compared texts: Old empty for a new file, New empty
	// for a deletion.
	Old, New string
	// FallbackText is a bounded mono preview shown instead of the diff when the
	// caller could not build one; FallbackNote is the honest one-line reason
	// shown above it. Setting either one selects the fallback: the component
	// does not guess a diff from texts the caller already rejected.
	FallbackText string
	FallbackNote string
	// EmptyMessage overrides the quiet "no visible changes" copy shown when the
	// texts compare equal (default 无可显示改动 / No visible changes).
	EmptyMessage string
	// MaxHeight bounds the block's body when the diff does not fit its natural
	// height; 0 uses DiffPreviewMaxHeight.
	MaxHeight float32
	// FitContent lets the viewer take its natural height for any length. Leave
	// it false on a surface without a height budget (a card): the component
	// then fits short diffs and bounds long ones at MaxHeight. Set it on a
	// surface that scrolls around the block; the caller then owns the height.
	FitContent bool
	// Locale localizes the component's own copy (empty state, fallback notes);
	// nil falls back to the product's Chinese copy.
	Locale contracts.LocaleService
}

// diffPreviewMode is which body one preview draws.
type diffPreviewMode uint8

const (
	// diffPreviewDiff draws the official viewer over the two texts.
	diffPreviewDiff diffPreviewMode = iota
	// diffPreviewEmpty draws the "no visible changes" line.
	diffPreviewEmpty
	// diffPreviewFallback draws a bounded mono preview and its explanation.
	diffPreviewFallback
)

// diffPreviewState is what one block keeps across frames: the texts last
// compared and the official viewer's own state. It lives on the block's root
// element (ui.Local), so the next request's block starts fresh.
type diffPreviewState struct {
	old, new string
	view     editor.DiffState
}

// diffPreviewPlan is one frame's resolved body: the mode, the official viewer
// and its stats, or the fallback copy.
type diffPreviewPlan struct {
	mode    diffPreviewMode
	viewer  *editor.DiffState
	added   int
	deleted int
	// fit tells that the viewer takes its natural height this frame.
	fit bool
	// fallback is the bounded mono preview of the fallback mode, and note the
	// honest explanation above it (either may be empty; not both).
	fallback string
	note     string
}

// DiffPreview renders one change preview and returns its block element. It is a
// plain frame builder: it creates controls only and keeps no state outside the
// element (see DiffPreviewOptions).
func DiffPreview(c *ui.Context, o DiffPreviewOptions) ui.Element {
	t := design.Of(c)
	maxHeight := o.MaxHeight
	if maxHeight <= 0 {
		maxHeight = DiffPreviewMaxHeight
	}
	key := strings.TrimSpace(o.Key)
	if key == "" {
		key = "uikit.diffpreview"
	}
	// The block frame: the same quiet code-block surface the approval summary
	// uses, so a diff reads as content to judge rather than as chrome.
	root := design.Apply(ui.Column(c.Key(key)).FillWidth().Gap(design.Space1),
		func(el ui.Element) ui.Element {
			return el.Background(t.BgBase).Border(1, t.BorderL2).
				Radius(design.RadiusSM).Padding(design.Space2)
		})
	// Resolve the body before building the header and children: the header's
	// +N −M counts come from the official diff, which is computed (and cached)
	// here.
	plan := diffPreviewPlanFor(root, o)
	root.Children(func() {
		diffPreviewHeader(c, t, strings.TrimSpace(o.Path), plan)
		diffPreviewBody(c, t, plan, o, maxHeight)
	})
	return root
}

// diffPreviewPlanFor resolves one frame's body: the caller's fallback first, the
// size and binary guards, the empty state, and finally the official viewer with
// its cached state.
func diffPreviewPlanFor(root ui.Element, o DiffPreviewOptions) diffPreviewPlan {
	fallbackText := strings.TrimSpace(o.FallbackText)
	fallbackNote := strings.TrimSpace(o.FallbackNote)
	if fallbackText != "" || fallbackNote != "" {
		// The caller could not build a diff (unparseable arguments, an
		// insertion whose position lives in the file): show its bounded
		// preview and its reason, never a guessed diff.
		return diffPreviewPlan{
			mode:     diffPreviewFallback,
			fallback: diffPreviewBound(o.FallbackText),
			note:     o.FallbackNote,
		}
	}
	if o.Old == o.New {
		return diffPreviewPlan{mode: diffPreviewEmpty}
	}
	if diffPreviewBinary(o.Old) || diffPreviewBinary(o.New) {
		// Binary content has no lines to show: say what it is and how big,
		// instead of painting control bytes as a fake diff.
		size := len(o.New)
		if size == 0 {
			size = len(o.Old)
		}
		return diffPreviewPlan{
			mode: diffPreviewFallback,
			note: fmt.Sprintf(T(o.Locale,
				"二进制内容（%d 字节），无法生成 diff。",
				"Binary content (%d bytes) cannot be diffed."), size),
		}
	}
	lines := diffPreviewLines(o.Old) + diffPreviewLines(o.New)
	if lines > DiffPreviewMaxLines || len(o.Old)+len(o.New) > DiffPreviewMaxBytes {
		// A very large change is summarized, honestly: the first lines of the
		// new text (the old one for a pure deletion) and the reason.
		preview := o.New
		if strings.TrimSpace(preview) == "" {
			preview = o.Old
		}
		return diffPreviewPlan{
			mode:     diffPreviewFallback,
			fallback: diffPreviewBound(preview),
			note: fmt.Sprintf(T(o.Locale,
				"改动过大（%d 行），无法生成 diff；以下为有界预览。",
				"The change is too large to diff (%d lines); showing a bounded preview."), lines),
		}
	}
	st := ui.Local(root, "diffpreview", func() diffPreviewState { return diffPreviewState{} })
	if st.old != o.Old || st.new != o.New || st.view.Old == nil {
		oldBuffer, newBuffer := editor.NewBuffer(o.Old), editor.NewBuffer(o.New)
		language := editor.LanguageFor(o.Path)
		oldBuffer.SetLanguage(language)
		newBuffer.SetLanguage(language)
		st.old, st.new = o.Old, o.New
		st.view.Old, st.view.New = oldBuffer, newBuffer
	}
	st.view.Layout = editor.DiffUnified
	st.view.Editable = false
	// A short diff takes its natural height (no empty viewer-sized hole); a
	// long one is bounded below, so FitContent stays on only for the short
	// ones unless the caller takes the height budget.
	fit := o.FitContent || lines <= diffPreviewFitLines
	st.view.FitContent = fit
	added, deleted := st.view.Stats()
	return diffPreviewPlan{
		mode:    diffPreviewDiff,
		viewer:  &st.view,
		added:   added,
		deleted: deleted,
		fit:     fit,
	}
}

// diffPreviewHeader is the block's one header row: the file path (ellipsized in
// a narrow card) and the +N −M line counts of the official diff.
func diffPreviewHeader(c *ui.Context, t *design.Tokens, path string, plan diffPreviewPlan) {
	showStats := plan.mode == diffPreviewDiff
	if path == "" && !showStats {
		return
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		if path != "" {
			design.Apply(ui.Text(c, path), t.Font(design.TypeXXS), t.MonoFamily()).
				TextColor(t.LabelSecondary).Grow(1).MaxLines(1).Ellipsis("…")
		} else {
			ui.Spacer(c).Grow(1)
		}
		if !showStats {
			return
		}
		// The line counts are the review convention: additions read as
		// success text, deletions as danger text, both on the AA text colors
		// rather than the status fills (DESIGN.md §3 rule 7).
		design.Apply(ui.Text(c, "+"+strconv.Itoa(plan.added)),
			t.Font(design.TypeXXS), t.MonoFamily()).TextColor(t.SuccessText).Shrink(0)
		design.Apply(ui.Text(c, "−"+strconv.Itoa(plan.deleted)),
			t.Font(design.TypeXXS), t.MonoFamily()).TextColor(t.DangerText).Shrink(0)
	})
}

// diffPreviewBody draws the plan's body: the official viewer (bounded unless it
// fits its lines), the empty state, or the bounded fallback preview with its
// explanation.
func diffPreviewBody(c *ui.Context, t *design.Tokens, plan diffPreviewPlan,
	o DiffPreviewOptions, maxHeight float32) {
	switch plan.mode {
	case diffPreviewEmpty:
		message := strings.TrimSpace(o.EmptyMessage)
		if message == "" {
			message = T(o.Locale, "无可显示改动", "No visible changes")
		}
		design.Apply(ui.Text(c, message), t.Font(design.TypeXXS)).
			TextColor(t.LabelTertiary).MaxLines(2)
	case diffPreviewFallback:
		if plan.note != "" {
			design.Apply(ui.Text(c, plan.note), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).MaxLines(3)
		}
		if plan.fallback != "" {
			// A bounded, wrapping mono preview: no horizontal overflow in a
			// narrow card, no unbounded height on a huge payload.
			// The scroll sizes to its content up to the block's bound, so a
			// one-line fallback stays one line high and a huge one scrolls.
			scroll := ui.Scroll(c).MaxHeight(maxHeight).FillWidth()
			scroll.Children(func() {
				design.Apply(ui.Text(c, plan.fallback),
					t.Font(design.TypeXS), t.MonoFamily()).TextColor(t.LabelSecondary)
			})
		}
	case diffPreviewDiff:
		viewer := editor.DiffView(c, plan.viewer).FillWidth()
		if !plan.fit {
			// A long diff scrolls inside the official viewer rather than
			// stretching the card that contains it.
			viewer.Height(maxHeight)
		}
	}
}

// diffPreviewLines counts a text's lines as the editor does: an empty text has
// none, and a final newline ends the last line rather than opening an empty one.
func diffPreviewLines(text string) int {
	if text == "" {
		return 0
	}
	count := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		count++
	}
	return count
}

// diffPreviewBinary reports whether a text is not displayable text: it holds a
// NUL byte or is not valid UTF-8.
func diffPreviewBinary(text string) bool {
	return strings.IndexByte(text, 0) >= 0 || !utf8.ValidString(text)
}

// diffPreviewBound bounds a mono preview to diffPreviewFallbackRunes, marking
// the cut.
func diffPreviewBound(text string) string {
	if text == "" || utf8.RuneCountInString(text) <= diffPreviewFallbackRunes {
		return text
	}
	return string([]rune(text)[:diffPreviewFallbackRunes]) + "…"
}
