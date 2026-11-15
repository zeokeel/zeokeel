package uikit_test

// Headless frame tests for the shared change preview (uikit.DiffPreview): the
// "what will change" block an approval card shows for write/edit calls and that
// tool cards and the trajectory can reuse. The tests pin the block's content
// (path and +N −M header, the official unified diff), the honest fallbacks for
// content no diff can show, the quiet empty state, and the height bound that
// keeps a long diff from stretching the surface.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/uikit"
)

// diffPreviewView renders one preview in the product's narrow card column.
func diffPreviewView(o uikit.DiffPreviewOptions) func(*ui.Context) {
	return func(c *ui.Context) {
		ui.Column(c).FillWidth().Children(func() {
			uikit.DiffPreview(c, o)
		})
	}
}

// diffPreviewHarness renders one preview and returns its tester.
func diffPreviewHarness(t *testing.T, o uikit.DiffPreviewOptions, width, height int) *ui.Tester {
	t.Helper()
	tester := ui.NewTester(diffPreviewView(o), width, height)
	tester.Frame()
	return tester
}

// diffPreviewNewText returns the accessibility value of the diff's new side, so
// a test reads the lines the official viewer paints on the canvas.
func diffPreviewNewText(t *testing.T, tester *ui.Tester) string {
	t.Helper()
	info, ok := tester.Access("New text")
	if !ok {
		t.Fatalf("the diff view is not exposed as the new text; texts = %v", tester.Texts())
	}
	return info.Value
}

// TestDiffPreviewWriteFileShowsAddedLines covers the write_file shape: an empty
// old text against the whole new file is an all-added diff, with the path and
// the +N −0 count in the header and the new content in the viewer.
func TestDiffPreviewWriteFileShowsAddedLines(t *testing.T) {
	newText := "package main\n\nfunc main() {}\n"
	tester := diffPreviewHarness(t, uikit.DiffPreviewOptions{
		Key:  "write",
		Path: "internal/app.go",
		New:  newText,
	}, 800, 600)
	if !tester.HasText("internal/app.go") {
		t.Fatalf("the block header does not name the path; texts = %v", tester.Texts())
	}
	if !tester.HasText("+3") || !tester.HasText("−0") {
		t.Fatalf("the header does not count +3 −0; texts = %v", tester.Texts())
	}
	if got := diffPreviewNewText(t, tester); !strings.Contains(got, "func main()") {
		t.Fatalf("the diff does not show the written content; a11y value = %q", got)
	}
}

// TestDiffPreviewEditShowsReplacedLine covers the edit_file shape: the old and
// new snippets differ by one replaced line, which the official diff counts as
// one addition and one deletion.
func TestDiffPreviewEditShowsReplacedLine(t *testing.T) {
	tester := diffPreviewHarness(t, uikit.DiffPreviewOptions{
		Key:  "edit",
		Path: "internal/app.go",
		Old:  "old line\n",
		New:  "new line\n",
	}, 800, 600)
	if !tester.HasText("internal/app.go") {
		t.Fatalf("the block header does not name the path; texts = %v", tester.Texts())
	}
	if !tester.HasText("+1") || !tester.HasText("−1") {
		t.Fatalf("the header does not count +1 −1; texts = %v", tester.Texts())
	}
	if got := diffPreviewNewText(t, tester); !strings.Contains(got, "new line") {
		t.Fatalf("the diff does not show the new line; a11y value = %q", got)
	}
}

// TestDiffPreviewEmptyState covers equal texts: the block says there is nothing
// to see instead of rendering an empty viewer.
func TestDiffPreviewEmptyState(t *testing.T) {
	tester := diffPreviewHarness(t, uikit.DiffPreviewOptions{
		Key:  "empty",
		Path: "notes.txt",
		Old:  "same text\n",
		New:  "same text\n",
	}, 800, 600)
	if !tester.HasText("无可显示改动") {
		t.Fatalf("the empty state copy is missing; texts = %v", tester.Texts())
	}
	if _, ok := tester.Access("New text"); ok {
		t.Fatalf("equal texts still rendered a diff viewer; texts = %v", tester.Texts())
	}
}

// TestDiffPreviewEmptyMessageOverride covers the caller's empty copy: another
// surface can name "no changes" in its own words.
func TestDiffPreviewEmptyMessageOverride(t *testing.T) {
	tester := diffPreviewHarness(t, uikit.DiffPreviewOptions{
		Key:          "empty-override",
		Old:          "",
		New:          "",
		EmptyMessage: "没有可显示的改动",
	}, 800, 600)
	if !tester.HasText("没有可显示的改动") {
		t.Fatalf("the overridden empty copy is missing; texts = %v", tester.Texts())
	}
}

// TestDiffPreviewOversizeFallsBack covers the size guard: a change beyond
// DiffPreviewMaxLines is summarized with a bounded mono preview and the honest
// reason, not diffed and not shown blank.
func TestDiffPreviewOversizeFallsBack(t *testing.T) {
	var newText strings.Builder
	for index := 0; index <= uikit.DiffPreviewMaxLines; index++ {
		newText.WriteString("generated line ")
		newText.WriteString("x")
		newText.WriteString("\n")
	}
	tester := diffPreviewHarness(t, uikit.DiffPreviewOptions{
		Key:  "oversize",
		Path: "generated.txt",
		New:  newText.String(),
	}, 800, 600)
	if _, ok := tester.Access("New text"); ok {
		t.Fatal("an oversized change was rendered as a diff instead of falling back")
	}
	if !tester.HasText("改动过大") || !tester.HasText("无法生成 diff") {
		t.Fatalf("the oversize fallback has no honest explanation; texts = %v", tester.Texts())
	}
	if !tester.HasText("generated line") {
		t.Fatalf("the oversize fallback has no bounded preview; texts = %v", tester.Texts())
	}
	// The preview is bounded: the whole payload never reaches the frame.
	if tester.HasText(strings.Repeat("generated line x\n", uikit.DiffPreviewMaxLines)) {
		t.Fatal("the oversize fallback rendered the whole payload instead of a bounded preview")
	}
}

// TestDiffPreviewOversizeByBytesFallsBack covers the byte guard: fewer than
// DiffPreviewMaxLines lines but more than DiffPreviewMaxBytes still falls back.
func TestDiffPreviewOversizeByBytesFallsBack(t *testing.T) {
	line := strings.Repeat("z", 1024) + "\n"
	newText := strings.Repeat(line, uikit.DiffPreviewMaxBytes/(len(line))+1)
	tester := diffPreviewHarness(t, uikit.DiffPreviewOptions{
		Key: "oversize-bytes",
		New: newText,
	}, 800, 600)
	if _, ok := tester.Access("New text"); ok {
		t.Fatal("a change over the byte bound was rendered as a diff")
	}
	if !tester.HasText("无法生成 diff") {
		t.Fatalf("the byte-bound fallback has no explanation; texts = %v", tester.Texts())
	}
}

// TestDiffPreviewBinaryFallsBack covers binary content: a NUL byte or invalid
// UTF-8 is explained, not painted as a fake diff.
func TestDiffPreviewBinaryFallsBack(t *testing.T) {
	tester := diffPreviewHarness(t, uikit.DiffPreviewOptions{
		Key:  "binary",
		Path: "logo.bin",
		New:  "PNG\x00\x01\x02binary",
	}, 800, 600)
	if _, ok := tester.Access("New text"); ok {
		t.Fatal("binary content was rendered as a text diff")
	}
	if !tester.HasText("二进制内容") || !tester.HasText("无法生成 diff") {
		t.Fatalf("the binary fallback has no honest explanation; texts = %v", tester.Texts())
	}
}

// TestDiffPreviewCallerFallback covers a caller-declared fallback: when the
// caller could not build a diff (unparseable arguments, an insertion), the
// component shows its bounded preview and its reason rather than guessing.
func TestDiffPreviewCallerFallback(t *testing.T) {
	tester := diffPreviewHarness(t, uikit.DiffPreviewOptions{
		Key:          "caller-fallback",
		Path:         "internal/app.go",
		FallbackText: "inserted text",
		FallbackNote: "无法从参数生成 diff",
	}, 800, 600)
	if !tester.HasText("无法从参数生成 diff") || !tester.HasText("inserted text") {
		t.Fatalf("the caller fallback is not shown; texts = %v", tester.Texts())
	}
	if _, ok := tester.Access("New text"); ok {
		t.Fatal("a caller-declared fallback still rendered a diff")
	}
}

// TestDiffPreviewLongDiffIsBounded covers the height bound: a long diff scrolls
// inside the viewer at DiffPreviewMaxHeight instead of stretching the card, and
// stays inside a narrow window's width.
func TestDiffPreviewLongDiffIsBounded(t *testing.T) {
	var newText strings.Builder
	for index := 0; index < 120; index++ {
		newText.WriteString("line ")
		newText.WriteString(strings.Repeat("x", 12))
		newText.WriteString("\n")
	}
	tester := diffPreviewHarness(t, uikit.DiffPreviewOptions{
		Key:  "long",
		Path: "long.txt",
		New:  newText.String(),
	}, 320, 600)
	if !tester.HasText("+120") {
		t.Fatalf("the long diff header does not count the added lines; texts = %v", tester.Texts())
	}
	info, ok := tester.Access("New text")
	if !ok {
		t.Fatalf("the long diff is not exposed as the new text; texts = %v", tester.Texts())
	}
	if info.Bounds.H > uikit.DiffPreviewMaxHeight+1 {
		t.Fatalf("the diff viewer is %g DIP high, want at most %g", info.Bounds.H, uikit.DiffPreviewMaxHeight)
	}
	if info.Bounds.H <= 0 {
		t.Fatalf("the diff viewer has no height (%g); texts = %v", info.Bounds.H, tester.Texts())
	}
	// A narrow card must not overflow horizontally: the block keeps the
	// viewer inside the window and lets long lines scroll sideways within it.
	if right := info.Bounds.X + info.Bounds.W; right > 320.5 || info.Bounds.X < -0.5 {
		t.Fatalf("the diff viewer spans x=%g..%g, want inside 0..320", info.Bounds.X, right)
	}
}

// TestDiffPreviewFitContentTakesNaturalHeight covers the FitContent option: on a
// surface that scrolls around the block, a long diff takes its natural height
// instead of the bound.
func TestDiffPreviewFitContentTakesNaturalHeight(t *testing.T) {
	var newText strings.Builder
	for index := 0; index < 40; index++ {
		newText.WriteString("line ")
		newText.WriteString(strings.Repeat("y", 8))
		newText.WriteString("\n")
	}
	tester := diffPreviewHarness(t, uikit.DiffPreviewOptions{
		Key:        "fit",
		Path:       "fit.txt",
		New:        newText.String(),
		FitContent: true,
	}, 800, 600)
	info, ok := tester.Access("New text")
	if !ok {
		t.Fatalf("the fitted diff is not exposed as the new text; texts = %v", tester.Texts())
	}
	if info.Bounds.H <= uikit.DiffPreviewMaxHeight {
		t.Fatalf("FitContent did not take the natural height: %g DIP, want more than %g",
			info.Bounds.H, uikit.DiffPreviewMaxHeight)
	}
}

// TestDiffPreviewShortDiffTakesNaturalHeight covers the default of a card
// surface: a short diff is only as high as its lines, so the block does not
// leave an empty viewer-sized hole.
func TestDiffPreviewShortDiffTakesNaturalHeight(t *testing.T) {
	tester := diffPreviewHarness(t, uikit.DiffPreviewOptions{
		Key: "short",
		New: "one\ntwo\n",
	}, 800, 600)
	info, ok := tester.Access("New text")
	if !ok {
		t.Fatalf("the short diff is not exposed as the new text; texts = %v", tester.Texts())
	}
	if info.Bounds.H <= 0 || info.Bounds.H >= uikit.DiffPreviewMaxHeight {
		t.Fatalf("a short diff is %g DIP high, want its natural height below %g",
			info.Bounds.H, uikit.DiffPreviewMaxHeight)
	}
}

// TestDiffPreviewKeepsStateAcrossFrames covers the cached viewer state: the same
// block key renders the same lines on the next frame instead of starting over.
func TestDiffPreviewKeepsStateAcrossFrames(t *testing.T) {
	tester := diffPreviewHarness(t, uikit.DiffPreviewOptions{
		Key:  "frames",
		Path: "a.go",
		Old:  "a\n",
		New:  "b\n",
	}, 800, 600)
	first := diffPreviewNewText(t, tester)
	tester.Frame()
	if got := diffPreviewNewText(t, tester); got != first {
		t.Fatalf("the diff changed across frames: %q then %q", first, got)
	}
}
