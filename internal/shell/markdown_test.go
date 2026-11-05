package shell

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/state"
)

// newLiveSnapshotApp builds the live controller fixture and injects snap as
// the snapshot the live renderer draws. Pixel parity retired the snapshot-only
// static renderer, so a synthetic snapshot must ride the real controller.
// The assertions in this group use English copy, so the language is pinned.
func newLiveSnapshotApp(t *testing.T, screen Screen, snap *state.Snapshot) *liveApp {
	t.Helper()
	h := newLiveAppWithSnapshot(t, screen, snap)
	h.app.Language = LangEN
	// The renderers keep scroll/motion/menu state in package maps keyed by
	// *App; clear them in case the allocator reuses a dead App's address.
	sessionRenderStates.Delete(h.app)
	markdownStyles.Delete(h.app)
	liveStates.Delete(h.app)
	liveUIStates.Delete(h.app)
	liveMenuStates.Delete(h.app)
	shellUIStates.Delete(h.app)
	modeUIStates.Delete(h.app)
	pluginUIStates.Delete(h.app)
	startTabStates.Delete(h.app)
	return h
}

// m6MarkdownApp renders one assistant message through the live transcript.
func m6MarkdownApp(t *testing.T, text string) (*ui.Tester, *App) {
	t.Helper()
	snap := state.NewSnapshot()
	snap.Messages = []state.Message{{MsgID: "a1", Role: state.RoleAssistant, Text: text}}
	snap.Phase = state.PhaseIdle
	h := newLiveSnapshotApp(t, Session, snap)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	return tt, h.app
}

func TestMarkdownRendersHeadingsEmphasisAndLists(t *testing.T) {
	text := strings.Join([]string{
		"# Heading one",
		"",
		"Plain **bold** and *italic* and `inline code` and [the docs](https://example.com/guide).",
		"",
		"## Heading two",
		"",
		"- alpha item",
		"- beta item",
		"",
		"1. first step",
		"2. second step",
		"",
		"> quoted guidance",
		"",
		"---",
		"",
		"```go",
		"func main() {",
		"\tprintln(\"hi\")",
		"}",
		"```",
	}, "\n")

	tt, _ := m6MarkdownApp(t, text)
	for _, want := range []string{
		"Heading one", "Heading two",
		"Plain ", "bold", "italic", "inline code",
		"the docs", "alpha item", "beta item",
		"1.", "2.", "first step", "second step",
		"quoted guidance",
		`func main() {`, `println("hi")`,
		// ui/markdown renders fenced code as a code block with a language
		// label and a copy button instead of plain monospace text.
		"Code, go", "Copy code",
	} {
		if !tt.HasText(want) {
			t.Fatalf("markdown output missing %q; texts: %v", want, tt.Texts())
		}
	}
	// Link labels show; destinations and emphasis markers do not.
	if tt.HasText("https://example.com/guide") {
		t.Fatalf("link destination leaked into the message: %v", tt.Texts())
	}
	for _, marker := range []string{"**", "```", "](", "> quoted"} {
		if tt.HasText(marker) {
			t.Fatalf("markdown marker %q rendered literally: %v", marker, tt.Texts())
		}
	}
}

// TestMarkdownTableRendersCells covers the GFM table extension the migration
// adopts: a pipe table shows as a table whose cells are real text labels, and
// the source pipes/delimiter row no longer leak into the message.
func TestMarkdownTableRendersCells(t *testing.T) {
	table := "| name | value |\n|------|-------|\n| a    | 1     |"
	tt, _ := m6MarkdownApp(t, table)
	for _, want := range []string{"name", "value", "a", "1"} {
		if !tt.HasText(want) {
			t.Fatalf("table cell %q missing; texts: %v", want, tt.Texts())
		}
	}
	for _, marker := range []string{"| name", "|------", "| a "} {
		if tt.HasText(marker) {
			t.Fatalf("table source %q rendered literally; texts: %v", marker, tt.Texts())
		}
	}
}

// TestMarkdownTaskList covers the GFM task list extension: the items' text is
// shown (the checkboxes are controls, not text).
func TestMarkdownTaskList(t *testing.T) {
	tt, _ := m6MarkdownApp(t, "- [x] done task\n- [ ] todo task")
	for _, want := range []string{"done task", "todo task"} {
		if !tt.HasText(want) {
			t.Fatalf("task list item %q missing; texts: %v", want, tt.Texts())
		}
	}
}

// TestMarkdownRawHTMLKeptAsSource covers the fallback for raw HTML: without an
// HTML hook the library shows an HTML block as its source, so content is not
// dropped.
func TestMarkdownRawHTMLKeptAsSource(t *testing.T) {
	html := "<div class=\"raw\">kept as text</div>"
	tt, _ := m6MarkdownApp(t, html)
	if !tt.HasText(html) {
		t.Fatalf("raw HTML lost its source %q; texts: %v", html, tt.Texts())
	}
}

// TestMarkdownStreamsThenSettles covers the stream path: an assistant message
// is rendered with markdown.Stream while the turn runs and markdown.View once
// it is complete, in the same message position, so the text survives the
// switch and no Markdown marker leaks either way.
func TestMarkdownStreamsThenSettles(t *testing.T) {
	text := "# Streaming heading\n\nA **bold** sentence with `code`."
	snap := state.NewSnapshot()
	snap.Messages = []state.Message{{MsgID: "a1", Role: state.RoleAssistant, Text: text}}
	snap.Phase = state.PhaseRunning
	h := newLiveSnapshotApp(t, Session, snap)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	for _, want := range []string{"Streaming heading", "bold", "code"} {
		if !tt.HasText(want) {
			t.Fatalf("streamed Markdown missing %q; texts: %v", want, tt.Texts())
		}
	}
	if tt.HasText("**") || tt.HasText("# ") {
		t.Fatalf("streamed Markdown leaked markers: %v", tt.Texts())
	}

	// The same message, now complete: markdown.View takes over in place and
	// the finished text is still the same.
	idle := *snap
	idle.Phase = state.PhaseIdle
	h.app.SetSnapshot(&idle)
	tt.Frame()
	for _, want := range []string{"Streaming heading", "bold", "code"} {
		if !tt.HasText(want) {
			t.Fatalf("settled Markdown missing %q; texts: %v", want, tt.Texts())
		}
	}
	if tt.HasText("**") {
		t.Fatalf("settled Markdown leaked markers: %v", tt.Texts())
	}
}

func TestMarkdownMalformedInlineKeepsCharacters(t *testing.T) {
	text := "a **b and *c and `d and [open("
	tt, _ := m6MarkdownApp(t, text)
	if !tt.HasText(text) {
		t.Fatalf("malformed inline lost characters; want %q in %v", text, tt.Texts())
	}
}

func TestMarkdownUnclosedFenceKeepsBody(t *testing.T) {
	tt, _ := m6MarkdownApp(t, "```go\nstill code\n")
	if !tt.HasText("still code") {
		t.Fatalf("unclosed fence dropped its body: %v", tt.Texts())
	}
}

func TestMarkdownPlainParagraphLineHeight(t *testing.T) {
	tt, app := m6MarkdownApp(t, "first line\nsecond line")
	if got, want := app.ConversationLineHeight(), float32(24); got != want {
		t.Fatalf("conversation line height = %v, want %v", got, want)
	}
	r1, ok := tt.Find("first line\nsecond line")
	if !ok {
		// RichText may split the newline into separate labels; the model
		// still shows both lines.
		if !tt.HasText("first line") || !tt.HasText("second line") {
			t.Fatalf("paragraph lines missing: %v", tt.Texts())
		}
		return
	}
	if r1.H < 40 {
		t.Fatalf("two 24px lines occupy %v, want at least 40", r1.H)
	}
}

func TestUsagePillsSegments(t *testing.T) {
	snap := state.NewSnapshot()
	snap.Phase = state.PhaseRunning
	snap.Usage = state.Usage{Turns: 3, Steps: 5, In: 200, Out: 100, CacheHit: 50}
	h := newLiveSnapshotApp(t, Session, snap)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Advance(2 * time.Second)

	idle := *snap
	idle.Phase = state.PhaseIdle
	h.app.SetSnapshot(&idle)
	tt.Frame()

	for _, want := range []string{
		"3 turns", "5 steps", "100 out tok",
		"Completed in 2.0s", "50 tok/s", "cache 25%",
	} {
		if !tt.HasText(want) {
			t.Fatalf("usage pill %q missing; texts: %v", want, tt.Texts())
		}
	}
}

func TestBackToBottomAppearsAndScrolls(t *testing.T) {
	snap := state.NewSnapshot()
	snap.Messages = []state.Message{{
		MsgID: "a1",
		Role:  state.RoleAssistant,
		Text:  strings.Repeat("a long line of transcript text\n", 200),
	}}
	h := newLiveSnapshotApp(t, Session, snap)
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	if !tt.HasText("Back to bottom") {
		t.Fatalf("a scrolled-up transcript has no Back to bottom control: %v", tt.Texts())
	}
	st := h.app.renderState()
	if st.scroll.MaxY <= 1 {
		t.Fatalf("transcript reports no scroll range: %+v", st.scroll)
	}
	if err := tt.Click("Back to bottom"); err != nil {
		t.Fatalf("click Back to bottom: %v", err)
	}
	tt.Frame()
	if tt.HasText("Back to bottom") {
		t.Fatalf("Back to bottom stayed after scrolling to the end: %v", tt.Texts())
	}
	if st.scroll.Y < st.scroll.MaxY-1 {
		t.Fatalf("transcript did not scroll to the end: y=%v max=%v", st.scroll.Y, st.scroll.MaxY)
	}
}

func TestAssistantFooterTimeAndCompletionOnlyWhenCompleted(t *testing.T) {
	provider := &m6Provider{steps: []m6Step{{text: "final answer"}}}
	app := m6LiveApp(t, provider)
	m6Submit(t, app, "question")
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()

	clock := regexp.MustCompile(`^\d{2}:\d{2}$`)
	times := 0
	for _, text := range tt.Texts() {
		if clock.MatchString(text) {
			times++
		}
	}
	if times != 1 {
		t.Fatalf("assistant footer has %d time labels, want 1; texts: %v", times, tt.Texts())
	}
	if !tt.HasText("Completed") {
		t.Fatalf("assistant footer lacks its completion summary: %v", tt.Texts())
	}
	for label, want := range map[string]float32{"Copy": 28, "Retry": 28, "Good response": 28, "Bad response": 28} {
		rect, ok := tt.Find(label)
		if !ok {
			t.Fatalf("action %q missing from the footer", label)
		}
		if rect.H != want {
			t.Fatalf("%s button height = %v, want %v", label, rect.H, want)
		}
	}

	// While the assistant message streams, no footer is shown.
	running := *app.Snapshot()
	running.Phase = state.PhaseRunning
	app.SetSnapshot(&running)
	tt.Frame()
	if tt.HasText("Completed") {
		t.Fatalf("streaming message already shows a completion footer: %v", tt.Texts())
	}
	if _, ok := tt.Find("Copy"); ok {
		t.Fatal("streaming message shows Copy")
	}
}
