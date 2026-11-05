package shell

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
)

// markdown.go renders the Markdown subset DSH shows in assistant messages
// into mygo elements. It never builds HTML and never drops source text: a
// construct the parser does not understand stays a paragraph carrying its
// source, so a table or raw HTML is still readable.

// monoFont is the markdown code stack: DSH's SF Mono/Consolas/Menlo list,
// with mygo's generic monospace as the last resort.
const monoFont = "SF Mono, JetBrains Mono, Fira Code, Consolas, Menlo, monospace"

// mdBlockKind is the kind of one parsed Markdown block.
type mdBlockKind uint8

const (
	mdParagraph mdBlockKind = iota
	mdHeadingBlock
	mdCodeBlock
	mdListBlock
	mdQuoteBlock
	mdRuleBlock
)

// mdBlock is one top-level Markdown block. Lists keep their items; code keeps
// its body; headings keep their level.
type mdBlock struct {
	kind    mdBlockKind
	level   int
	text    string
	lang    string
	ordered bool
	items   []mdListItem
}

// mdListItem is one list row: the marker ("•" or "3.") and its inline text.
type mdListItem struct {
	marker string
	text   string
}

// markdownBlocks parses src and adds one element per block to the current
// container. The container's gap spaces the blocks.
func (a *App) markdownBlocks(c *ui.Context, t *design.Tokens, src string) {
	for _, b := range parseMarkdown(src) {
		switch b.kind {
		case mdHeadingBlock:
			a.markdownHeading(c, t, b)
		case mdCodeBlock:
			a.markdownCode(c, t, b)
		case mdListBlock:
			a.markdownList(c, t, b)
		case mdQuoteBlock:
			a.markdownQuote(c, t, b)
		case mdRuleBlock:
			ui.Box(c).Height(1).FillWidth().Background(t.BorderL2)
		default:
			a.markdownParagraph(c, t, b.text)
		}
	}
}

// markdownParagraph is one paragraph of inline Markdown with DSH's 14/24 body
// metrics (or the user's chosen conversation size).
func (a *App) markdownParagraph(c *ui.Context, t *design.Tokens, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	spans := a.inlineSpans(t, text, a.ConversationFontSize(), 400, false)
	if len(spans) == 0 {
		// Defensive: never drop a paragraph whose text produced no spans.
		ui.Text(c, text).FontSize(a.ConversationFontSize()).
			FixedLineHeight(a.ConversationLineHeight()).TextColor(t.LabelPrimary)
		return
	}
	ui.RichText(c, spans...).FontSize(a.ConversationFontSize()).
		FixedLineHeight(a.ConversationLineHeight()).TextColor(t.LabelPrimary)
}

// markdownHeading is an ATX heading. Sizes follow DSH's rendered Markdown:
// h1 24/32, h2 19/28, h3 16/24, and h4-h6 in the body size at 600 weight.
func (a *App) markdownHeading(c *ui.Context, t *design.Tokens, b mdBlock) {
	size, line, weight := float32(24), float32(32), 700
	switch b.level {
	case 1:
		size, line, weight = 24, 32, 700
	case 2:
		size, line, weight = 19, 28, 700
	case 3:
		size, line, weight = 16, 24, 600
	default:
		size, line, weight = 14, 24, 600
	}
	spans := a.inlineSpans(t, b.text, size, weight, false)
	if len(spans) == 0 {
		return
	}
	ui.RichText(c, spans...).FontSize(size).FixedLineHeight(line).
		FontWeight(weight).TextColor(t.LabelPrimary)
}

// markdownCode is a fenced code block: a panel with monospace text, as DSH's
// pre/code surface.
func (a *App) markdownCode(c *ui.Context, t *design.Tokens, b mdBlock) {
	ui.Column(c).FillWidth().Background(t.BgSidebar).
		Border(1, t.BorderL2).Radius(design.RadiusSM).
		Padding(design.Space3, design.Space3).
		Children(func() {
			if b.text == "" {
				ui.Box(c).Height(20)
				return
			}
			ui.Text(c, b.text).Font(monoFont).FontSize(13).
				FixedLineHeight(20).TextColor(t.LabelPrimary)
		})
}

// markdownList is an unordered or ordered list. Markers sit in a fixed
// gutter so the item text lines up, as DSH's list inset does.
func (a *App) markdownList(c *ui.Context, t *design.Tokens, b mdBlock) {
	ui.Column(c).FillWidth().Children(func() {
		for _, item := range b.items {
			item := item
			ui.Row(c).FillWidth().AlignItems(ui.Start).
				Padding(0, 0, 0, 0).Children(func() {
				ui.Text(c, item.marker).FontSize(14).
					FixedLineHeight(a.ConversationLineHeight()).
					TextColor(t.LabelPrimary).Width(18).NoWrap()
				spans := a.inlineSpans(t, item.text, a.ConversationFontSize(), 400, false)
				if len(spans) == 0 {
					ui.Text(c, item.text).FontSize(a.ConversationFontSize()).
						FixedLineHeight(a.ConversationLineHeight()).
						TextColor(t.LabelPrimary).Grow(1)
					return
				}
				ui.RichText(c, spans...).FontSize(a.ConversationFontSize()).
					FixedLineHeight(a.ConversationLineHeight()).
					TextColor(t.LabelPrimary).Grow(1)
			})
		}
	})
}

// markdownQuote is a blockquote: DSH's 2px rule and 14px inset, with the
// quoted lines kept as their own lines.
func (a *App) markdownQuote(c *ui.Context, t *design.Tokens, b mdBlock) {
	ui.Row(c).FillWidth().AlignItems(ui.Start).
		BorderWidth(0, 0, 0, 2).BorderColor(t.BorderL3).
		Padding(0, 0, 0, design.Space3+2).Children(func() {
		spans := a.inlineSpans(t, b.text, a.ConversationFontSize(), 400, false)
		if len(spans) == 0 {
			return
		}
		ui.RichText(c, spans...).FontSize(a.ConversationFontSize()).
			FixedLineHeight(a.ConversationLineHeight()).
			TextColor(t.LabelPrimary).Grow(1)
	})
}

// inlineSpans turns inline Markdown (bold, italic, inline code, links, and
// backslash escapes) into styled text spans. Unmatched markers are literal,
// so malformed input keeps all of its characters.
func (a *App) inlineSpans(t *design.Tokens, src string, baseSize float32, weight int, italic bool) []ui.Span {
	var spans []ui.Span
	a.parseInline(t, src, baseSize, weight, italic, &spans)
	return mergeSpans(spans)
}

// parseInline appends the spans of src to out. It recurses for emphasis and
// links so nested styling stays inline.
func (a *App) parseInline(t *design.Tokens, src string, baseSize float32, weight int, italic bool, out *[]ui.Span) {
	plain := func(text string) ui.Span {
		return ui.Span{Text: text, Weight: weight, Italic: italic, Color: t.LabelPrimary}
	}
	code := func(text string) ui.Span {
		return ui.Span{
			Text:       text,
			Font:       monoFont,
			Size:       baseSize * 0.875,
			Weight:     weight,
			Italic:     italic,
			Color:      t.LabelPrimary,
			Background: t.BgSidebar,
			Radius:     5,
		}
	}
	link := func(text string) []ui.Span {
		var inner []ui.Span
		a.parseInline(t, text, baseSize, weight, italic, &inner)
		for i := range inner {
			inner[i].Color = t.Accent
			inner[i].Underline = true
			inner[i].Background = ui.Color{}
		}
		if len(inner) == 0 {
			inner = []ui.Span{{Text: text, Weight: weight, Italic: italic, Color: t.Accent, Underline: true}}
		}
		return inner
	}

	var buf strings.Builder
	flush := func() {
		if buf.Len() == 0 {
			return
		}
		*out = append(*out, plain(buf.String()))
		buf.Reset()
	}

	for i := 0; i < len(src); {
		c := src[i]
		// Backslash escapes: the escaped character is literal.
		if c == '\\' && i+1 < len(src) && isEscapable(src[i+1]) {
			buf.WriteByte(src[i+1])
			i += 2
			continue
		}
		// Inline code: everything between two backticks is literal.
		if c == '`' {
			if end := strings.IndexByte(src[i+1:], '`'); end >= 0 {
				flush()
				text := src[i+1 : i+1+end]
				// CommonMark drops one space at each end when both exist.
				if strings.HasPrefix(text, " ") && strings.HasSuffix(text, " ") && strings.TrimSpace(text) != "" {
					text = text[1 : len(text)-1]
				}
				*out = append(*out, code(text))
				i += end + 2
				continue
			}
			buf.WriteByte(c)
			i++
			continue
		}
		// Emphasis: ***bold italic***, **bold**, *italic*; the underscore
		// forms work too, but an underscore inside a word stays literal.
		if c == '*' || c == '_' {
			if c == '_' && i > 0 && i+1 < len(src) && isWordByte(src[i-1]) && isWordByte(src[i+1]) {
				buf.WriteByte(c)
				i++
				continue
			}
			marker := src[i : i+1]
			if i+2 < len(src) && src[i+1] == c && src[i+2] == c {
				if end := strings.Index(src[i+3:], strings.Repeat(marker, 3)); end > 0 {
					flush()
					inner := src[i+3 : i+3+end]
					boldWeight := weight
					if boldWeight < 600 {
						boldWeight = 600
					}
					a.parseInline(t, inner, baseSize, boldWeight, true, out)
					i += 3 + end + 3
					continue
				}
				// No closing run: keep all three markers literally.
				buf.WriteString(strings.Repeat(marker, 3))
				i += 3
				continue
			}
			if i+1 < len(src) && src[i+1] == c {
				if end := strings.Index(src[i+2:], strings.Repeat(marker, 2)); end > 0 {
					flush()
					inner := src[i+2 : i+2+end]
					boldWeight := weight
					if boldWeight < 600 {
						boldWeight = 600
					}
					a.parseInline(t, inner, baseSize, boldWeight, italic, out)
					i += 2 + end + 2
					continue
				}
				// A lone "**" is not emphasis: keep both markers so no
				// source character is lost.
				buf.WriteString(strings.Repeat(marker, 2))
				i += 2
				continue
			}
			if end := strings.IndexByte(src[i+1:], c); end > 0 {
				closing := i + 1 + end
				// An underscore that ends a word is not emphasis either.
				if c == '_' && closing+1 < len(src) && isWordByte(src[closing+1]) {
					buf.WriteByte(c)
					i++
					continue
				}
				flush()
				a.parseInline(t, src[i+1:closing], baseSize, weight, !italic, out)
				i = closing + 1
				continue
			}
			buf.WriteByte(c)
			i++
			continue
		}
		// Links: [label](url). Only the label is shown, colored like DSH's.
		if c == '[' {
			if close := strings.IndexByte(src[i+1:], ']'); close >= 0 {
				labelEnd := i + 1 + close
				if labelEnd+1 < len(src) && src[labelEnd+1] == '(' {
					if paren := strings.IndexByte(src[labelEnd+2:], ')'); paren >= 0 {
						flush()
						label := src[i+1 : labelEnd]
						*out = append(*out, link(label)...)
						i = labelEnd + 2 + paren + 1
						continue
					}
				}
			}
			buf.WriteByte(c)
			i++
			continue
		}
		buf.WriteByte(c)
		i++
	}
	flush()
}

// mergeSpans joins adjacent spans that differ only in their text, so a
// paragraph's plain runs stay one label for accessibility and wide
// Unicode segments keep one run.
func mergeSpans(spans []ui.Span) []ui.Span {
	if len(spans) < 2 {
		return spans
	}
	out := spans[:1]
	for _, s := range spans[1:] {
		last := &out[len(out)-1]
		a, b := *last, s
		a.Text, b.Text = "", ""
		if a == b {
			last.Text += s.Text
			continue
		}
		out = append(out, s)
	}
	return out
}

// isEscapable reports whether backslash escapes c, per CommonMark's ASCII
// punctuation set.
func isEscapable(c byte) bool {
	return strings.ContainsRune(`\`+"`*_{}[]()#+-.!|>~", rune(c))
}

// isWordByte reports whether c is part of a word, for the intraword
// underscore rule.
func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

// parseMarkdown splits src into the block subset the renderer supports. All
// other lines stay paragraphs, so no source is lost.
func parseMarkdown(src string) []mdBlock {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	lines := strings.Split(src, "\n")
	blocks := make([]mdBlock, 0, 8)
	for i := 0; i < len(lines); {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			i++
			continue
		}
		if fence, ok := mdFence(line); ok {
			body := make([]string, 0, 8)
			j := i + 1
			closed := false
			for ; j < len(lines); j++ {
				if mdFenceClose(lines[j], fence) {
					closed = true
					break
				}
				body = append(body, lines[j])
			}
			blocks = append(blocks, mdBlock{kind: mdCodeBlock, lang: fence.info, text: strings.Join(body, "\n")})
			if closed {
				i = j + 1
			} else {
				i = j
			}
			continue
		}
		if level, text, ok := mdHeading(line); ok {
			blocks = append(blocks, mdBlock{kind: mdHeadingBlock, level: level, text: text})
			i++
			continue
		}
		if mdRule(line) {
			blocks = append(blocks, mdBlock{kind: mdRuleBlock})
			i++
			continue
		}
		if strings.HasPrefix(strings.TrimLeft(line, " "), ">") {
			quoted := make([]string, 0, 4)
			for i < len(lines) {
				l := strings.TrimLeft(lines[i], " ")
				if !strings.HasPrefix(l, ">") {
					break
				}
				l = strings.TrimPrefix(l, ">")
				l = strings.TrimPrefix(l, " ")
				quoted = append(quoted, l)
				i++
			}
			blocks = append(blocks, mdBlock{kind: mdQuoteBlock, text: strings.Join(quoted, "\n")})
			continue
		}
		if indent, ordered, marker, content, ok := mdListMarker(line); ok && indent <= 3 {
			block := mdBlock{kind: mdListBlock, ordered: ordered}
			item := mdListItem{marker: marker, text: content}
			j := i + 1
			for j < len(lines) {
				l := lines[j]
				if strings.TrimSpace(l) == "" {
					break
				}
				nindent, nordered, nmarker, ncontent, nok := mdListMarker(l)
				if nok && nindent == indent && nordered == ordered {
					block.items = append(block.items, item)
					item = mdListItem{marker: nmarker, text: ncontent}
					j++
					continue
				}
				if nok && nindent > indent {
					// A nested list is not rendered recursively; keep its
					// source on its own line inside the parent item.
					item.text += "\n" + strings.TrimRight(l, " ")
					j++
					continue
				}
				if !nok && !mdStartsBlock(l) {
					item.text += "\n" + strings.TrimSpace(l)
					j++
					continue
				}
				break
			}
			block.items = append(block.items, item)
			blocks = append(blocks, block)
			i = j
			continue
		}
		// Plain paragraph: join consecutive plain lines, keeping their line
		// breaks, until a blank line or the start of another block.
		par := []string{strings.TrimRight(line, " ")}
		j := i + 1
		for j < len(lines) {
			if strings.TrimSpace(lines[j]) == "" || mdStartsBlock(lines[j]) {
				break
			}
			par = append(par, strings.TrimRight(lines[j], " "))
			j++
		}
		blocks = append(blocks, mdBlock{kind: mdParagraph, text: strings.Join(par, "\n")})
		i = j
	}
	return blocks
}

// mdFence recognizes a fenced code block opener.
func mdFence(line string) (fence mdFenceInfo, ok bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return fence, false
	}
	if strings.HasPrefix(trimmed, "```") {
		n := 0
		for n < len(trimmed) && trimmed[n] == '`' {
			n++
		}
		return mdFenceInfo{char: '`', count: n, info: strings.TrimSpace(trimmed[n:])}, true
	}
	if strings.HasPrefix(trimmed, "~~~") {
		n := 0
		for n < len(trimmed) && trimmed[n] == '~' {
			n++
		}
		return mdFenceInfo{char: '~', count: n, info: strings.TrimSpace(trimmed[n:])}, true
	}
	return fence, false
}

// mdFenceInfo is a code fence opening: its character, its run length and its
// info string (the language).
type mdFenceInfo struct {
	char  byte
	count int
	info  string
}

// mdFenceClose reports whether line closes fence.
func mdFenceClose(line string, f mdFenceInfo) bool {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return false
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == f.char {
		n++
	}
	return n >= f.count && strings.TrimSpace(trimmed[n:]) == ""
}

// mdHeading recognizes an ATX heading (# through ######).
func mdHeading(line string) (level int, text string, ok bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || !strings.HasPrefix(trimmed, "#") {
		return 0, "", false
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == '#' {
		n++
	}
	if n == 0 || n > 6 {
		return 0, "", false
	}
	if n < len(trimmed) && trimmed[n] != ' ' && trimmed[n] != '\t' {
		return 0, "", false
	}
	text = strings.TrimSpace(trimmed[n:])
	// CommonMark closes a heading with a run of # only when a space
	// separates it from the text; "C#" keeps its #.
	if end := strings.TrimRight(text, " \t"); end != "" {
		i := len(end)
		for i > 0 && end[i-1] == '#' {
			i--
		}
		if i < len(end) && (i == 0 || end[i-1] == ' ' || end[i-1] == '\t') {
			text = strings.TrimSpace(end[:i])
		}
	}
	return n, text, true
}

// mdRule reports whether line is a thematic break.
func mdRule(line string) bool {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 3 {
		return false
	}
	var ch byte
	n := 0
	for i := 0; i < len(trimmed); i++ {
		c := trimmed[i]
		if c == ' ' || c == '\t' {
			continue
		}
		if c != '-' && c != '*' && c != '_' {
			return false
		}
		if ch == 0 {
			ch = c
		} else if c != ch {
			return false
		}
		n++
	}
	return n >= 3
}

// mdListMarker parses a list item's leading indentation, marker and content.
// Ordered markers are digits followed by "." or ")".
func mdListMarker(line string) (indent int, ordered bool, marker, content string, ok bool) {
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}
	if indent >= len(line) {
		return 0, false, "", "", false
	}
	c := line[indent]
	if c == '-' || c == '*' || c == '+' {
		rest := line[indent+1:]
		if rest == "" {
			return indent, false, "•", "", true
		}
		if rest[0] == ' ' || rest[0] == '\t' {
			return indent, false, "•", strings.TrimSpace(rest), true
		}
		return 0, false, "", "", false
	}
	if c >= '0' && c <= '9' {
		n := indent
		for n < len(line) && line[n] >= '0' && line[n] <= '9' {
			n++
		}
		if n >= len(line) || line[n] != '.' && line[n] != ')' {
			return 0, false, "", "", false
		}
		rest := line[n+1:]
		if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
			return 0, false, "", "", false
		}
		return indent, true, line[indent : n+1], strings.TrimSpace(rest), true
	}
	return 0, false, "", "", false
}

// mdStartsBlock reports whether line begins a non-paragraph block.
func mdStartsBlock(line string) bool {
	if strings.TrimSpace(line) == "" {
		return true
	}
	if _, ok := mdFence(line); ok {
		return true
	}
	if _, _, ok := mdHeading(line); ok {
		return true
	}
	if mdRule(line) {
		return true
	}
	if strings.HasPrefix(strings.TrimLeft(line, " "), ">") {
		return true
	}
	if indent, _, _, _, ok := mdListMarker(line); ok && indent <= 3 {
		return true
	}
	return false
}
