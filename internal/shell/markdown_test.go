package shell

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/state"
)

// m6MarkdownApp renders one assistant message through the live transcript.
// It has no controller, as a snapshot built by hand needs none; the static
// App{Ctrl: nil} demo path is a different renderer and stays untouched.
func m6MarkdownApp(t *testing.T, text string) (*ui.Tester, *App) {
	t.Helper()
	snap := state.NewSnapshot()
	snap.Messages = []state.Message{{MsgID: "a1", Role: state.RoleAssistant, Text: text}}
	snap.Phase = state.PhaseIdle
	app := &App{Screen: Session}
	sessionRenderStates.Delete(app)
	liveStates.Delete(app)
	liveUIStates.Delete(app)
	app.SetSnapshot(snap)
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()
	return tt, app
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

func TestMarkdownPreservesUnsupportedConstructs(t *testing.T) {
	table := "| name | value |\n|------|-------|\n| a    | 1     |"
	html := "<div class=\"raw\">kept as text</div>"
	tt, _ := m6MarkdownApp(t, table+"\n\n"+html)
	for _, want := range []string{"| name | value |", "|------|-------|", "| a    | 1     |", html} {
		if !tt.HasText(want) {
			t.Fatalf("unsupported construct lost its source %q; texts: %v", want, tt.Texts())
		}
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
	app := &App{Screen: Session}
	if got, want := app.ConversationLineHeight(), float32(24); got != want {
		t.Fatalf("conversation line height = %v, want %v", got, want)
	}
	tt, _ := m6MarkdownApp(t, "first line\nsecond line")
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
	app := &App{Screen: Session}
	app.SetSnapshot(snap)
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Advance(2 * time.Second)

	idle := *snap
	idle.Phase = state.PhaseIdle
	app.SetSnapshot(&idle)
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
	app := &App{Screen: Session}
	sessionRenderStates.Delete(app)
	liveStates.Delete(app)
	liveUIStates.Delete(app)
	app.SetSnapshot(snap)
	tt := ui.NewTester(app.View, 1600, 1000)
	tt.Frame()

	if !tt.HasText("Back to bottom") {
		t.Fatalf("a scrolled-up transcript has no Back to bottom control: %v", tt.Texts())
	}
	st := app.renderState()
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
