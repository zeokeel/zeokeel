package shell

import (
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"

	"github.com/zeokeel/zeokeel/internal/design"
)

// markdown.go is the thin ZeoKeel wrapper over mygo's official Markdown
// renderer (ui/markdown): GFM tables, task lists, strikethrough, autolinks,
// footnotes, math, syntax-highlighted code blocks with a language label and
// a copy button, selectable text, and incremental streaming all come from the
// library instead of the deleted hand-written parser (task-17, ADR 0001).
//
// The renderer keeps the product's own contracts:
//   - finished messages and artifact previews use markdown.View;
//   - the assistant message being written uses markdown.Stream, so only the
//     newly appended text is parsed and half-written syntax is healed;
//   - the conversation font size setting and the brand line rhythm are applied
//     through one cached markdown.Options per App, so the library does not
//     reparse or restyle on every frame.

// monoFont stays the code font of the shell's plain-text tool details
// (toolcards.go). Markdown code blocks set the same stack through their theme.
const monoFont = "SF Mono, JetBrains Mono, Fira Code, Consolas, Menlo, monospace"

// streamReveal is how Stream shows text as it arrives. The session transcript
// already fades and lifts a growing assistant message as a whole
// (liveMessage/streamShown on the motion scale), so the library adds no second
// per-word reveal: text shows as it comes and the shell owns the one fade.
// This keeps streaming to one animation driver and matches the pre-migration
// motion behavior.
var streamReveal = &markdown.Reveal{Unit: markdown.AtOnce, Fade: -1}

// markdownStyle is one App's cached Markdown configuration. md/ui/markdown
// keeps its parse state per view root, and Options says how that state is
// styled; it must stay stable across frames or every frame would restyle from
// scratch. The struct records the inputs the options were derived from so a
// theme or conversation-size change rebuilds them.
type markdownStyle struct {
	options *markdown.Options
	dark    bool
	size    float32
	line    float32
	// root is the workspace directory image paths resolve against; a
	// workspace switch rebuilds the options so images load from the right
	// files.
	root string
}

// markdownStyles holds each App's Markdown configuration. It lives here, in
// the Markdown wrapper, because the screens' shared App/renderState types
// belong to other files; the map is keyed by App like the other UI state maps
// (pluginUIStates, sessionRenderStates).
var markdownStyles sync.Map // map[*App]*markdownStyle

// markdownOptions returns the App's Markdown configuration for the current
// appearance and conversation font size. It is rebuilt only when one of those
// inputs changes.
func (a *App) markdownOptions(t *design.Tokens) *markdown.Options {
	style := &markdownStyle{}
	if v, ok := markdownStyles.Load(a); ok {
		style = v.(*markdownStyle)
	} else {
		actual, _ := markdownStyles.LoadOrStore(a, style)
		style = actual.(*markdownStyle)
	}

	size := a.ConversationFontSize()
	line := a.ConversationLineHeight() / size
	root := a.workspaceRoot()
	if root == "" {
		root = "."
	}
	if style.options == nil || style.dark != t.Dark || style.size != size ||
		style.line != line || style.root != root {
		theme := markdown.DefaultTheme(t.Theme())
		// The conversation text setting scales chat text only; the markdown
		// line box follows it (24 DIPs at 14, 28 at 16), the way the deleted
		// renderer did.
		theme.FontSize = size
		theme.LineHeight = line
		theme.CodeFont = monoFont
		style.options = &markdown.Options{
			Theme:      theme,
			Extensions: parser.Default,
			Reveal:     streamReveal,
			// Images in messages and tool results load from the workspace
			// (ui/markdown shows the alt text until then, and forever when
			// the file is missing).
			LoadImage: markdown.FileImages(root),
		}
		style.dark, style.size, style.line, style.root = t.Dark, size, line, root
	}
	return style.options
}

// markdownBlocks renders finished Markdown (a settled assistant message, a
// plan or artifact preview, any other static Markdown body) with
// markdown.View, mygo's complete-document renderer.
func (a *App) markdownBlocks(c *ui.Context, t *design.Tokens, src string) {
	markdown.View(c, src, a.markdownOptions(t))
}

// markdownMessage renders one assistant message: markdown.Stream while the
// turn is writing it (incremental parse, healed syntax), markdown.View once
// it is complete. Both are called in the same place in the message column so
// the library's view state carries the parse over from stream to final view.
func (a *App) markdownMessage(c *ui.Context, t *design.Tokens, src string, streaming bool) {
	if streaming {
		markdown.Stream(c, src, a.markdownOptions(t))
		return
	}
	markdown.View(c, src, a.markdownOptions(t))
}
