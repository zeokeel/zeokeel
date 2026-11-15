package about_test

// Frame tests for the About page's build information after its migration to the
// shared property sheet (uikit.PropertySheet): the metadata is a label/value
// table with one fixed 160 DIP label column and a bounded, left-aligned value,
// not the right-aligned rows or the page's own label column.

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/ui/about"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// TestAboutBuildInfoUsesPropertySheet pins the sheet's geometry on the real
// page: the value starts after the fixed label column instead of floating to
// the page's right edge, and the rows stay in the sheet's bounded measure.
func TestAboutBuildInfoUsesPropertySheet(t *testing.T) {
	uiRoot := newHarness(t, "zh", about.Config{}, contracts.AppInfo{
		Name: "ZeoKeel", Version: "1.2.3", Commit: "abc123",
		BuildTime: "2026-01-02", GoVersion: "go1.27.3",
	})
	tester := ui.NewTester(uiRoot.View, 1200, 800)
	tester.Frame()

	// The version row is unique on the page (the name "ZeoKeel" also appears in
	// the sidebar brand and the page heading).
	label, ok := tester.Find("版本")
	if !ok {
		t.Fatalf("About label missing; texts = %v", tester.Texts())
	}
	value, ok := tester.Find("1.2.3")
	if !ok {
		t.Fatalf("About version value missing; texts = %v", tester.Texts())
	}
	if math.Abs(float64(label.W-uikit.PropertySheetLabelWidth)) > 1 {
		t.Fatalf("label column width = %v, want the shared %v", label.W, uikit.PropertySheetLabelWidth)
	}
	wantLeft := label.X + uikit.PropertySheetLabelWidth + design.Space3
	if math.Abs(float64(value.X-wantLeft)) > 1 {
		t.Fatalf("value left = %v, want %v (left-aligned after the fixed label column)", value.X, wantLeft)
	}
	wantWidth := uikit.PropertySheetDefaultMaxWidth - uikit.PropertySheetLabelWidth - design.Space3
	if math.Abs(float64(value.W-wantWidth)) > 1 {
		t.Fatalf("value width = %v, want the bounded sheet measure %v", value.W, wantWidth)
	}
}
