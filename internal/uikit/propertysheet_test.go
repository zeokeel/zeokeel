package uikit_test

// Headless tests for the shared property sheet (uikit.PropertySheet): the fixed
// 120 DIP label column, the left-aligned value on a 32 DIP row, the divider
// between rows, the selectable value and the empty-value placeholder. The
// pages that migrated to the sheet (About/Diagnostics/Network/Update/Context)
// keep their own frame tests; this file pins the shared geometry and states.

import (
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// propertySheetView draws one sheet in a page column.
func propertySheetView(rows []uikit.PropertyRow, maxWidth float32) func(*ui.Context) {
	return func(c *ui.Context) {
		ui.Column(c).FillWidth().Children(func() {
			uikit.PropertySheet(c, uikit.PropertySheetOptions{Rows: rows, MaxWidth: maxWidth})
		})
	}
}

func TestPropertySheetLabelAndValueGeometry(t *testing.T) {
	tt := ui.NewTester(propertySheetView([]uikit.PropertyRow{
		{Label: "版本", Value: "v0.9.0"},
		{Label: "平台", Value: "linux"},
	}, 0), 1000, 400)

	label, ok := tt.Find("版本")
	if !ok {
		t.Fatalf("label missing; texts = %v", tt.Texts())
	}
	value, ok := tt.Find("v0.9.0")
	if !ok {
		t.Fatalf("value missing; texts = %v", tt.Texts())
	}
	if math.Abs(float64(label.W-uikit.PropertySheetLabelWidth)) > 1 {
		t.Fatalf("label column width = %v, want the fixed %v", label.W, uikit.PropertySheetLabelWidth)
	}
	// The value starts right after the fixed label column and the row gap, and
	// stays in the sheet's bounded measure instead of the page's right edge.
	wantValueLeft := label.X + uikit.PropertySheetLabelWidth + design.Space3
	if math.Abs(float64(value.X-wantValueLeft)) > 1 {
		t.Fatalf("value left = %v, want %v (left-aligned after the label column)", value.X, wantValueLeft)
	}
	wantValueWidth := uikit.PropertySheetDefaultMaxWidth - uikit.PropertySheetLabelWidth - design.Space3
	if math.Abs(float64(value.W-wantValueWidth)) > 1 {
		t.Fatalf("value width = %v, want %v (the sheet's bounded measure)", value.W, wantValueWidth)
	}
	if value.X <= label.X {
		t.Fatalf("value is not to the right of its label: label=%v value=%v", label.X, value.X)
	}

	// Rows are 32 DIP, with a hairline divider between them.
	next, ok := tt.Find("平台")
	if !ok {
		t.Fatalf("second label missing; texts = %v", tt.Texts())
	}
	rowStep := next.Y - label.Y
	if rowStep < design.RowCompactHeight || rowStep > design.RowCompactHeight+design.Space1 {
		t.Fatalf("row step = %v, want the 32 DIP row plus at most a 1 DIP divider", rowStep)
	}
}

func TestPropertySheetHonorsMaxWidth(t *testing.T) {
	tt := ui.NewTester(propertySheetView([]uikit.PropertyRow{
		{Label: "路径", Value: "/tmp/zeokeel/data"},
	}, 400), 1000, 200)
	value, ok := tt.Find("/tmp/zeokeel/data")
	if !ok {
		t.Fatalf("value missing; texts = %v", tt.Texts())
	}
	want := 400 - uikit.PropertySheetLabelWidth - design.Space3
	if math.Abs(float64(value.W-want)) > 1 {
		t.Fatalf("value width = %v, want %v for MaxWidth(400)", value.W, want)
	}
}

// TestPropertySheetFullWidthSpansParent covers the Context page's kv dividers
// (2026-11 UI review B4): with FullWidth the sheet (and its row dividers) spans
// the parent's full width like the enclosing section divider, while the value
// stays left-aligned right after the fixed label column.
func TestPropertySheetFullWidthSpansParent(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().Children(func() {
			uikit.PropertySheet(c, uikit.PropertySheetOptions{
				Rows:      []uikit.PropertyRow{{Label: "窗口", Value: "128K tokens"}},
				FullWidth: true,
			})
		})
	}, 1000, 200)
	label, ok := tt.Find("窗口")
	if !ok {
		t.Fatalf("label missing; texts = %v", tt.Texts())
	}
	value, ok := tt.Find("128K tokens")
	if !ok {
		t.Fatalf("value missing; texts = %v", tt.Texts())
	}
	// The value box fills the parent width (1000) minus the label column and gap,
	// not the bounded default measure — so the row divider runs full width.
	want := 1000 - uikit.PropertySheetLabelWidth - design.Space3
	if math.Abs(float64(value.W-want)) > 1 {
		t.Fatalf("FullWidth value width = %v, want %v (the parent's full measure)", value.W, want)
	}
	if value.W <= uikit.PropertySheetDefaultMaxWidth-uikit.PropertySheetLabelWidth {
		t.Fatalf("FullWidth sheet stayed bounded (value.W=%v)", value.W)
	}
	// The value still sits left-aligned right after the label column.
	wantLeft := label.X + uikit.PropertySheetLabelWidth + design.Space3
	if math.Abs(float64(value.X-wantLeft)) > 1 {
		t.Fatalf("FullWidth value left = %v, want %v (still left-aligned)", value.X, wantLeft)
	}
}

func TestPropertySheetEmptyValueShowsPlaceholder(t *testing.T) {
	tt := ui.NewTester(propertySheetView([]uikit.PropertyRow{
		{Label: "提交", Value: ""},
		{Label: "构建时间", Value: "", Placeholder: "未知"},
	}, 0), 800, 200)
	if _, ok := tt.Find(uikit.PropertySheetPlaceholder); !ok {
		t.Fatalf("empty value did not show the placeholder; texts = %v", tt.Texts())
	}
	if !tt.HasText("未知") {
		t.Fatalf("custom placeholder missing; texts = %v", tt.Texts())
	}
}

func TestPropertySheetValueIsSelectable(t *testing.T) {
	const value = "/tmp/zeokeel/spill.txt"
	tt := ui.NewTester(propertySheetView([]uikit.PropertyRow{
		{Label: "路径", Value: value},
	}, 0), 800, 200)
	rect, ok := tt.Find(value)
	if !ok {
		t.Fatalf("value missing; texts = %v", tt.Texts())
	}
	y := rect.Y + rect.H/2
	tt.Press(rect.X+1, y)
	tt.Move(rect.X+rect.W-1, y)
	tt.Release(rect.X+rect.W-1, y)
	tt.Key(ui.Cmd, ui.KeyC)
	if got := tt.Clipboard(); !strings.Contains(got, value) {
		t.Fatalf("dragging the value did not copy it: clipboard = %q", got)
	}
}

func TestPropertySheetCopyControlWritesTheValue(t *testing.T) {
	tt := ui.NewTester(propertySheetView([]uikit.PropertyRow{
		{Label: "路径", Value: "/tmp/zeokeel/data"},
	}, 0), 800, 200)
	if _, ok := tt.Find("复制"); ok {
		t.Fatalf("plain row drew a copy control; texts = %v", tt.Texts())
	}
	withCopy := ui.NewTester(propertySheetView([]uikit.PropertyRow{
		{Label: "路径", Value: "/tmp/zeokeel/data", Copy: true},
	}, 0), 800, 200)
	if err := withCopy.Click("复制"); err != nil {
		t.Fatalf("click copy control: %v; texts = %v", err, withCopy.Texts())
	}
	if got := withCopy.Clipboard(); got != "/tmp/zeokeel/data" {
		t.Fatalf("copy control wrote %q, want the row value", got)
	}

	// A placeholder row has nothing to copy: no control is drawn.
	empty := ui.NewTester(propertySheetView([]uikit.PropertyRow{
		{Label: "提交", Value: "", Copy: true},
	}, 0), 800, 200)
	if _, ok := empty.Find("复制"); ok {
		t.Fatalf("placeholder row drew a copy control; texts = %v", empty.Texts())
	}
}
