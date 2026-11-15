package uikit_test

// Headless tests for the shared reading column: the fixed transcript measure,
// the 92% narrow-window cap, no Grow, and the shared padding that keeps the
// transcript and the Context pane on one left edge.

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// readingColumnView draws one full-width probe inside the reading column, so the
// frame reports the column's content width and its left edge.
func readingColumnView(c *ui.Context) {
	ui.Column(c).FillWidth().FillHeight().Children(func() {
		uikit.ReadingColumn(c, func(body *ui.Context) {
			ui.Text(body, "列内容").FillWidth()
		})
	})
}

func TestReadingColumnCentersTheTranscriptMeasure(t *testing.T) {
	const window float32 = 1600
	tt := ui.NewTester(readingColumnView, int(window), 600)
	rect, ok := tt.Find("列内容")
	if !ok {
		t.Fatalf("reading column probe missing; texts = %v", tt.Texts())
	}
	// The 960 DIP measure is centered and carries its own horizontal padding;
	// the probe fills the column's content box.
	wantLeft := (window-design.TranscriptWidth)/2 + design.Space4
	wantWidth := design.TranscriptWidth - 2*design.Space4
	t.Logf("left=%v width=%v", rect.X, rect.W)
	if math.Abs(float64(rect.X-wantLeft)) > 1 {
		t.Fatalf("reading column left = %v, want the centered measure at %v", rect.X, wantLeft)
	}
	if math.Abs(float64(rect.W-wantWidth)) > 1 {
		t.Fatalf("reading column content width = %v, want %v (no Grow may widen it)", rect.W, wantWidth)
	}
}

func TestReadingColumnCapsAtNarrowWidth(t *testing.T) {
	const window float32 = 704
	tt := ui.NewTester(readingColumnView, int(window), 600)
	rect, ok := tt.Find("列内容")
	if !ok {
		t.Fatalf("reading column probe missing; texts = %v", tt.Texts())
	}
	// A narrow window falls back to 92% of the viewport instead of overflowing.
	column := window * 0.92
	wantLeft := (window-column)/2 + design.Space4
	wantWidth := column - 2*design.Space4
	t.Logf("left=%v width=%v wantLeft=%v wantWidth=%v", rect.X, rect.W, wantLeft, wantWidth)
	if math.Abs(float64(rect.X-wantLeft)) > 1 {
		t.Fatalf("narrow reading column left = %v, want %v", rect.X, wantLeft)
	}
	if math.Abs(float64(rect.W-wantWidth)) > 1 {
		t.Fatalf("narrow reading column width = %v, want %v", rect.W, wantWidth)
	}
}

// TestReadingColumnBodyBuildsInsideTheColumn pins the helper's contract: the
// elements built by body are the column's children (the helper calls body
// inside the column's Children, as the other uikit entries do).
func TestReadingColumnBodyBuildsInsideTheColumn(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		uikit.ReadingColumn(c, func(body *ui.Context) {
			ui.Row(body).FillWidth().Gap(design.Space1).Children(func() {
				ui.Text(body, "甲")
				ui.Text(body, "乙")
			})
		})
	}, 1200, 200)
	left, ok := tt.Find("甲")
	if !ok {
		t.Fatalf("body content missing; texts = %v", tt.Texts())
	}
	right, ok := tt.Find("乙")
	if !ok {
		t.Fatalf("body content missing; texts = %v", tt.Texts())
	}
	if right.X <= left.X {
		t.Fatalf("body row did not build horizontally inside the column: 甲=%v 乙=%v", left.X, right.X)
	}
}
