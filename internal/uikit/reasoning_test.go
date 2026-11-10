package uikit_test

// Headless tests for the shared reasoning disclosure (uikit.ReasoningTrace): the
// model's thinking is one official disclosure, open while it streams and folded
// to a quiet line with the line count once finished; a toggle reports through
// OnToggle, asks the transcript to keep its place, and the panel renders the
// muted Markdown. The theme helper is checked against the design tokens too.

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// reasoningView draws one reasoning trace on a page background.
func reasoningView(o uikit.ReasoningTraceOptions) func(*ui.Context) {
	return func(c *ui.Context) {
		t := design.Of(c)
		ui.Column(c).FillWidth().FillHeight().Background(t.BgBase).
			Padding(design.Space4).Children(func() {
			uikit.ReasoningTrace(c, o)
		})
	}
}

// TestReasoningTraceEmptyDrawsNothing covers the empty state: a turn without
// reasoning renders no placeholder row.
func TestReasoningTraceEmptyDrawsNothing(t *testing.T) {
	tt := ui.NewTester(reasoningView(uikit.ReasoningTraceOptions{
		Key:       "test.reasoning.empty",
		Reasoning: " \n\t ",
	}), 480, 200)
	tt.Frame()
	for _, text := range tt.Texts() {
		if strings.Contains(text, "思考") {
			t.Fatalf("empty reasoning rendered %q; texts = %v", text, tt.Texts())
		}
	}
	if _, ok := tt.AccessByRole(ui.RoleDisclosure, "思考"); ok {
		t.Fatal("empty reasoning rendered a disclosure")
	}
}

// TestReasoningTraceStreaming covers the streaming state: the trace is open, the
// trigger shows the official spinner and 思考中…, the reasoning Markdown streams
// in, and a click folds it, reports the new state and asks for HoldPlace.
func TestReasoningTraceStreaming(t *testing.T) {
	open := true
	holdPlace := false
	toggles := make([]bool, 0, 1)
	tt := ui.NewTester(reasoningView(uikit.ReasoningTraceOptions{
		Key:       "test.reasoning.streaming",
		Reasoning: "先看认证模块，再检查配置。",
		Streaming: true,
		Open:      &open,
		OnToggle:  func(next bool) { toggles = append(toggles, next) },
		HoldPlace: &holdPlace,
	}), 480, 240)
	tt.Frame()

	info, ok := tt.AccessByRole(ui.RoleDisclosure, "思考中…")
	if !ok || !info.Expanded {
		t.Fatalf("streaming trace is not an open disclosure: %+v found %v; texts = %v", info, ok, tt.Texts())
	}
	// The spinner's label shows up as a frame label; as inside the compact tool
	// row's disclosure, the disclosure name is what assistive technology reads.
	if !tt.HasText("思考中") {
		t.Fatalf("streaming trace has no spinner label; texts = %v", tt.Texts())
	}
	if !tt.HasText("先看认证模块") {
		t.Fatalf("streaming reasoning text is not visible; texts = %v", tt.Texts())
	}

	if err := tt.Click("思考中…"); err != nil {
		t.Fatalf("click streaming trace: %v", err)
	}
	tt.Advance(300 * time.Millisecond) // the disclosure's close animation
	tt.Frame()
	if info, _ := tt.AccessByRole(ui.RoleDisclosure, "思考中…"); info.Expanded {
		t.Fatalf("click did not fold the streaming trace: %+v", info)
	}
	if len(toggles) != 1 || toggles[0] {
		t.Fatalf("OnToggle calls = %v, want [false]", toggles)
	}
	if !holdPlace {
		t.Fatal("toggling the trace did not ask the transcript for HoldPlace")
	}
}

// TestReasoningTraceFinished covers the finished state: folded by default to
// 思考 plus the line count, the reasoning hidden until the reader opens it, and a
// single-line reasoning omits the count.
func TestReasoningTraceFinished(t *testing.T) {
	reasoning := "第一行结论\n第二行依据\n第三行备注"
	open := false
	tt := ui.NewTester(reasoningView(uikit.ReasoningTraceOptions{
		Key:       "test.reasoning.finished",
		Reasoning: reasoning,
		Open:      &open,
	}), 480, 240)
	tt.Frame()
	info, ok := tt.AccessByRole(ui.RoleDisclosure, "思考")
	if !ok || info.Expanded {
		t.Fatalf("finished trace is not a folded disclosure: %+v found %v; texts = %v", info, ok, tt.Texts())
	}
	if !tt.HasText("3 行") {
		t.Fatalf("finished trace has no line count; texts = %v", tt.Texts())
	}
	if tt.HasText("第一行结论") {
		t.Fatalf("folded trace leaks its reasoning; texts = %v", tt.Texts())
	}
	if err := tt.Click("思考"); err != nil {
		t.Fatalf("click finished trace: %v", err)
	}
	tt.Advance(300 * time.Millisecond) // the disclosure's open animation
	tt.Frame()
	if !tt.HasText("第一行结论") {
		t.Fatalf("opened trace does not show the reasoning; texts = %v", tt.Texts())
	}

	single := ui.NewTester(reasoningView(uikit.ReasoningTraceOptions{
		Key:       "test.reasoning.single",
		Reasoning: "只有一行",
	}), 480, 200)
	single.Frame()
	for _, text := range single.Texts() {
		if text == "1 行" {
			t.Fatalf("a single-line reasoning shows a line count; texts = %v", single.Texts())
		}
	}
}

// TestReasoningMarkdownTheme covers the shared muted theme helper: it honors the
// requested body size, keeps text/muted/strong on the documented tokens and
// keeps the panel's headings below the reply's.
func TestReasoningMarkdownTheme(t *testing.T) {
	tokens := design.Light()
	options := uikit.ReasoningMarkdown(tokens, 13)
	theme := options.Theme
	if theme.FontSize != 13 {
		t.Fatalf("reasoning font size = %v, want 13", theme.FontSize)
	}
	if !sameColor(theme.Text, tokens.LabelSecondary) ||
		!sameColor(theme.Muted, tokens.LabelTertiary) ||
		!sameColor(theme.Strong, tokens.LabelPrimary) ||
		!sameColor(theme.Code, tokens.LabelSecondary) ||
		!sameColor(theme.QuoteText, tokens.LabelTertiary) {
		t.Fatalf("reasoning theme is not the muted token set: %+v", theme)
	}
	if h1 := theme.Headings[0].Size * theme.FontSize; h1 > 16.5 {
		t.Fatalf("reasoning H1 = %.2f DIP, want below the reply's H1", h1)
	}
	// size 0 uses the design's XS metric.
	def := uikit.ReasoningMarkdown(tokens, 0)
	wantSize, _, _ := tokens.Metrics(design.TypeXS)
	if def.Theme.FontSize != wantSize {
		t.Fatalf("default reasoning size = %v, want the XS metric %v", def.Theme.FontSize, wantSize)
	}
}

// TestReasoningTraceInlineChevron covers the activity-band variant: the trace is
// still the same disclosure (toggle, panel, OnToggle) with the chevron placed
// right after the trigger's label instead of the row's far end.
func TestReasoningTraceInlineChevron(t *testing.T) {
	open := false
	holdPlace := false
	toggles := make([]bool, 0, 1)
	tt := ui.NewTester(reasoningView(uikit.ReasoningTraceOptions{
		Key:           "test.reasoning.inline",
		Reasoning:     "先看认证模块。",
		Open:          &open,
		InlineChevron: true,
		OnToggle:      func(next bool) { toggles = append(toggles, next) },
		HoldPlace:     &holdPlace,
	}), 480, 240)
	tt.Frame()
	info, ok := tt.AccessByRole(ui.RoleDisclosure, "思考")
	if !ok || info.Expanded {
		t.Fatalf("inline trace disclosure = %+v found %v, want a closed disclosure", info, ok)
	}
	if err := tt.Click("思考"); err != nil {
		t.Fatalf("click inline trace: %v; texts = %v", err, tt.Texts())
	}
	tt.Frame()
	if len(toggles) != 1 || toggles[0] != true {
		t.Fatalf("inline toggle calls = %v, want [true]", toggles)
	}
	if !holdPlace {
		t.Fatal("inline toggle did not ask for HoldPlace")
	}
	if !tt.HasText("先看认证模块") {
		t.Fatalf("inline trace panel missing; texts = %v", tt.Texts())
	}
}

// TestReasoningTraceMarkerColumn covers the activity band's fixed marker column:
// the type glyph gets a fixed 16 DIP column, so the label copy starts a little
// further right than the old inline glyph, and the disclosure still works.
func TestReasoningTraceMarkerColumn(t *testing.T) {
	open := false
	toggles := make([]bool, 0, 1)
	options := uikit.ReasoningTraceOptions{
		Key:           "test.reasoning.marker",
		Reasoning:     "第一行\n第二行",
		Open:          &open,
		InlineChevron: true,
		MarkerColumn:  true,
		OnToggle:      func(next bool) { toggles = append(toggles, next) },
	}
	tt := ui.NewTester(reasoningView(options), 480, 240)
	tt.Frame()
	if info, ok := tt.AccessByRole(ui.RoleDisclosure, "思考"); !ok || info.Expanded {
		t.Fatalf("marker trace disclosure = %+v found %v, want a closed disclosure", info, ok)
	}
	markerLines, ok := tt.Find("2 行")
	if !ok {
		t.Fatalf("marker trace line count missing; texts = %v", tt.Texts())
	}
	plain := options
	plain.Key = "test.reasoning.plain"
	plain.MarkerColumn = false
	plainTT := ui.NewTester(reasoningView(plain), 480, 240)
	plainTT.Frame()
	plainLines, ok := plainTT.Find("2 行")
	if !ok {
		t.Fatalf("plain trace line count missing; texts = %v", plainTT.Texts())
	}
	if markerLines.X <= plainLines.X {
		t.Fatalf("marker column copy X=%.1f, plain X=%.1f: the fixed marker column should shift the label",
			markerLines.X, plainLines.X)
	}
	if err := tt.Click("思考"); err != nil {
		t.Fatalf("click marker trace: %v; texts = %v", err, tt.Texts())
	}
	tt.Frame()
	if len(toggles) != 1 || toggles[0] != true {
		t.Fatalf("marker toggle calls = %v, want [true]", toggles)
	}
}
