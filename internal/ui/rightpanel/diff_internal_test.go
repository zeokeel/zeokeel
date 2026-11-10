package rightpanel

// Internal conformance tests for the file tab's diff fold: the official diff
// viewer cache (editor.ParsePatch/SetPatch, docs/ui/diff-viewer.md) shared with
// the retired Changes pane. They live in the package so they can drive the
// unexported state helpers directly.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/editor"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// sampleUnifiedDiff is a `git diff` style patch of one file: the changer emits
// this shape (a/ b/ headers, an index line, unified hunks).
const sampleUnifiedDiff = "diff --git a/main.go b/main.go\n" +
	"index 83b414d..1b1a2f0 100644\n" +
	"--- a/main.go\n" +
	"+++ b/main.go\n" +
	"@@ -1,3 +1,3 @@\n" +
	"-old line\n" +
	"+new line\n" +
	" context line\n" +
	" unchanged tail\n"

func TestDiffViewerParsesAndCachesUnifiedPatch(t *testing.T) {
	st := &state{diffs: make(map[string]*diffView)}
	change := contracts.FileChange{Path: "main.go", Status: "M", Diff: sampleUnifiedDiff}

	first, err := st.diffViewFor(change)
	if err != nil || first == nil {
		t.Fatalf("diffViewFor = %v, %v; want the official viewer", first, err)
	}
	if first.state.Layout != editor.DiffUnified {
		t.Fatalf("viewer layout = %v, want unified", first.state.Layout)
	}
	if !first.state.FitContent {
		t.Fatal("a short diff should take its natural height (FitContent)")
	}
	// SetPatch made the texts of the hunks: the official viewer, not a
	// hand-split string, decides what the lines are.
	if got := first.state.New.String(); !strings.Contains(got, "new line") || !strings.Contains(got, "context line") {
		t.Fatalf("viewer New text = %q, want the added and context lines", got)
	}
	if got := first.state.Old.String(); !strings.Contains(got, "old line") || !strings.Contains(got, "context line") {
		t.Fatalf("viewer Old text = %q, want the deleted and context lines", got)
	}
	if first.lines != 4 {
		t.Fatalf("viewer lines = %d, want the four hunk lines", first.lines)
	}

	// The viewer is cached per file: the same DiffState (scroll, folds, focus)
	// is reused across frames.
	second, err := st.diffViewFor(change)
	if err != nil || second != first {
		t.Fatalf("diffViewFor returned %p, %v; want the cached %p", second, err, first)
	}
}

func TestDiffViewerBoundsLongDiffs(t *testing.T) {
	var builder strings.Builder
	builder.WriteString("--- a/big.go\n+++ b/big.go\n@@ -0,0 +1,30 @@\n")
	for index := 0; index < 30; index++ {
		fmt.Fprintf(&builder, "+line%d\n", index)
	}
	st := &state{diffs: make(map[string]*diffView)}
	view, err := st.diffViewFor(contracts.FileChange{Path: "big.go", Status: "M", Diff: builder.String()})
	if err != nil || view == nil {
		t.Fatalf("diffViewFor = %v, %v", view, err)
	}
	if view.state.FitContent {
		t.Fatal("a long diff should scroll inside a bounded viewer, not stretch the pane")
	}
	if view.lines <= diffFitLines {
		t.Fatalf("viewer lines = %d, want more than diffFitLines (%d)", view.lines, diffFitLines)
	}
}

func TestDiffViewerFallsBackForUnparsablePatch(t *testing.T) {
	st := &state{}
	view, err := st.diffViewFor(contracts.FileChange{Path: "big.go", Status: "M",
		Diff: "+line1\n+line2\n+line3\n"})
	if view != nil {
		t.Fatalf("diffViewFor built %v from a headerless fragment, want the fallback", view)
	}
	if err == nil {
		t.Fatal("diffViewFor accepted a headerless fragment")
	}
}

func TestDiffLineStats(t *testing.T) {
	additions, deletions := diffLineStats(sampleUnifiedDiff)
	if additions != 1 || deletions != 1 {
		t.Fatalf("diffLineStats = +%d -%d, want +1 -1", additions, deletions)
	}
}
