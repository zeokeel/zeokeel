package uikit_test

// Headless tests for the shared transcript tool row: it is one official mygo
// disclosure (ui.CollapsibleBase), so a click, Enter and Space toggle it and
// assistive technology sees its expanded state and description.

import (
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// toolRowState is the caller-owned fold state of one row, as a transcript keeps
// it between frames.
type toolRowState struct {
	open    bool
	toggles []bool
}

func toolRowView(state *toolRowState) func(*ui.Context) {
	return func(c *ui.Context) {
		t := design.Of(c)
		uikit.ToolRow(c, uikit.ToolRowOptions{
			Key:         "test.tool." + "read_file",
			Icon:        icons.File(),
			Name:        "读取文件",
			RawName:     "read_file",
			Status:      "完成",
			StatusColor: t.SuccessText,
			Preview:     "auth.go",
			Open:        state.open,
			Toggle: func(next bool) {
				state.toggles = append(state.toggles, next)
				state.open = next
			},
			Sections: []uikit.ToolPanelSection{{
				Label: "完整结果标题",
				Text:  "完整结果",
				Mono:  true,
			}},
		})
	}
}

func TestToolRowIsOfficialDisclosure(t *testing.T) {
	state := &toolRowState{}
	tester := ui.NewTester(toolRowView(state), 800, 200)

	info, ok := tester.AccessByRole(ui.RoleDisclosure, "读取文件 · 完成")
	if !ok {
		t.Fatalf("tool row is not a disclosure; texts = %v", tester.Texts())
	}
	if info.Description != "read_file" {
		t.Fatalf("description = %q, want the raw tool id", info.Description)
	}
	if info.Expanded || tester.HasText("完整结果") {
		t.Fatalf("closed row: %+v shows detail %v", info, tester.HasText("完整结果"))
	}

	if err := tester.Click("读取文件"); err != nil {
		t.Fatalf("click: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if len(state.toggles) != 1 || state.toggles[0] != true {
		t.Fatalf("toggle calls = %v, want [true]", state.toggles)
	}
	if info, _ := tester.AccessByRole(ui.RoleDisclosure, "读取文件 · 完成"); !info.Expanded {
		t.Fatalf("click did not open the row: %+v", info)
	}
	if !tester.HasText("完整结果") {
		t.Fatalf("open row does not show its detail; texts = %v", tester.Texts())
	}

	tester.Key(0, ui.KeySpace)
	tester.Frame()
	if state.open || len(state.toggles) != 2 || state.toggles[1] != false {
		t.Fatalf("Space: open %v, toggles %v", state.open, state.toggles)
	}
	tester.Key(0, ui.KeyEnter)
	tester.Frame()
	if !state.open || len(state.toggles) != 3 || state.toggles[2] != true {
		t.Fatalf("Enter: open %v, toggles %v", state.open, state.toggles)
	}
}

func TestToolRowWithoutDetailIsAPlainLine(t *testing.T) {
	tester := ui.NewTester(func(c *ui.Context) {
		uikit.ToolRow(c, uikit.ToolRowOptions{
			Key:     "test.plain",
			Name:    "读取文件",
			RawName: "read_file",
			Status:  "完成",
		})
	}, 800, 200)
	if !tester.HasText("读取文件") || !tester.HasText("完成") {
		t.Fatalf("plain row missing its copy; texts = %v", tester.Texts())
	}
	if _, ok := tester.AccessByRole(ui.RoleDisclosure, "读取文件 · 完成"); ok {
		t.Fatal("a row without a detail body is still a disclosure")
	}
}

func TestToolRowRunningShowsLabeledSpinner(t *testing.T) {
	tester := ui.NewTester(func(c *ui.Context) {
		t := design.Of(c)
		uikit.ToolRow(c, uikit.ToolRowOptions{
			Key:         "test.running",
			Name:        "运行命令",
			RawName:     "bash",
			Status:      "运行中",
			StatusColor: t.WarningText,
			Running:     true,
			Sections:    []uikit.ToolPanelSection{{Label: "输出", Text: "结果"}},
		})
	}, 800, 200)
	running := 0
	for _, text := range tester.Texts() {
		if text == "运行中" {
			running++
		}
	}
	// The status text and the spinner's Label.
	if running != 2 {
		t.Fatalf("running row shows %d 运行中 labels, want the status and the spinner label; texts = %v", running, tester.Texts())
	}
	if _, ok := tester.AccessByRole(ui.RoleDisclosure, "运行命令 · 运行中"); !ok {
		t.Fatalf("running disclosure name missing; texts = %v", tester.Texts())
	}
}

// toolRowInlineView draws one tool row with InlineChevron, the transcript
// activity band's compact look (chevron right after the preview).
func toolRowInlineView(state *toolRowState) func(*ui.Context) {
	return func(c *ui.Context) {
		t := design.Of(c)
		uikit.ToolRow(c, uikit.ToolRowOptions{
			Key:           "test.tool.inline",
			Icon:          icons.File(),
			Name:          "读取文件",
			Status:        "完成",
			StatusColor:   t.SuccessText,
			Preview:       "auth.go",
			Open:          state.open,
			InlineChevron: true,
			Toggle: func(next bool) {
				state.toggles = append(state.toggles, next)
				state.open = next
			},
			Sections: []uikit.ToolPanelSection{{Label: "完整结果标题", Text: "完整结果"}},
		})
	}
}

// TestToolRowInlineChevron covers the activity-band variant: it is still an
// official disclosure with the same toggle semantics, and the preview stays a
// left-aligned string instead of growing to the row's far end.
func TestToolRowInlineChevron(t *testing.T) {
	inline := &toolRowState{}
	tester := ui.NewTester(toolRowInlineView(inline), 800, 200)
	info, ok := tester.AccessByRole(ui.RoleDisclosure, "读取文件 · 完成")
	if !ok || info.Expanded {
		t.Fatalf("inline row disclosure = %+v found %v, want a closed disclosure", info, ok)
	}
	inlinePreview, ok := tester.Find("auth.go")
	if !ok {
		t.Fatalf("inline preview missing; texts = %v", tester.Texts())
	}
	if err := tester.Click("读取文件"); err != nil {
		t.Fatalf("click inline row: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if len(inline.toggles) != 1 || inline.toggles[0] != true {
		t.Fatalf("inline toggle calls = %v, want [true]", inline.toggles)
	}
	if !tester.HasText("完整结果") {
		t.Fatalf("inline row panel missing; texts = %v", tester.Texts())
	}

	// The default row's growing preview is wider than the inline one, whose
	// preview keeps its intrinsic measure with the chevron right after it.
	wide := &toolRowState{}
	wideTester := ui.NewTester(toolRowView(wide), 800, 200)
	widePreview, ok := wideTester.Find("auth.go")
	if !ok {
		t.Fatalf("default preview missing; texts = %v", wideTester.Texts())
	}
	if inlinePreview.W >= widePreview.W {
		t.Fatalf("inline preview width = %.1f, default = %.1f: the inline preview should not grow",
			inlinePreview.W, widePreview.W)
	}
}

// TestToolRowMarkerColumn covers the activity band's fixed marker column: the
// category glyph takes a fixed 16 DIP column (or the official spinner while
// running), so the action name starts further right than the old inline glyph;
// the status and preview still read on the same line.
func TestToolRowMarkerColumn(t *testing.T) {
	state := &toolRowState{}
	view := func(c *ui.Context) {
		t := design.Of(c)
		uikit.ToolRow(c, uikit.ToolRowOptions{
			Key:           "test.tool.marker",
			Icon:          icons.File(),
			Name:          "读取文件",
			Status:        "完成",
			StatusColor:   t.SuccessText,
			Preview:       "auth.go",
			InlineChevron: true,
			MarkerColumn:  true,
			Open:          state.open,
			Toggle: func(next bool) {
				state.toggles = append(state.toggles, next)
				state.open = next
			},
			Sections: []uikit.ToolPanelSection{{Label: "完整结果标题", Text: "完整结果"}},
		})
	}
	tester := ui.NewTester(view, 800, 200)
	info, ok := tester.AccessByRole(ui.RoleDisclosure, "读取文件 · 完成")
	if !ok || info.Expanded {
		t.Fatalf("marker row disclosure = %+v found %v, want a closed disclosure", info, ok)
	}
	markerName, ok := tester.Find("读取文件")
	if !ok {
		t.Fatalf("marker row action name missing; texts = %v", tester.Texts())
	}
	if !tester.HasText("完成") || !tester.HasText("auth.go") {
		t.Fatalf("marker row lacks status/preview; texts = %v", tester.Texts())
	}
	// The fixed marker column is wider than the old inline glyph: the name
	// starts a little further right than in the default row.
	plainState := &toolRowState{}
	plainTester := ui.NewTester(toolRowView(plainState), 800, 200)
	plainName, ok := plainTester.Find("读取文件")
	if !ok {
		t.Fatalf("plain row action name missing; texts = %v", plainTester.Texts())
	}
	if markerName.X <= plainName.X {
		t.Fatalf("marker column name X=%.1f, plain X=%.1f: the marker column should shift the name",
			markerName.X, plainName.X)
	}
	if err := tester.Click("读取文件"); err != nil {
		t.Fatalf("click marker row: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if len(state.toggles) != 1 || state.toggles[0] != true {
		t.Fatalf("marker toggle calls = %v, want [true]", state.toggles)
	}
}

// toolRowGutterVariant selects one ToolRow shape: the plain line (a call with no
// panel body), the disclosure (a call with input/output sections) and the
// activity band's marker-column row.
type toolRowGutterVariant struct {
	name  string
	panel bool
	band  bool
}

// toolRowGutterView draws one tool row on the page background so the state
// gutter's fill is readable in the rendered pixels: the tester has no getter for
// a decorative box's paint, so the frame is the assertion surface (as the icon
// button's fill/danger tests do).
func toolRowGutterView(state uikit.StatusKind, running bool, v toolRowGutterVariant) func(*ui.Context) {
	return func(c *ui.Context) {
		t := design.Of(c)
		ui.Column(c).FillWidth().FillHeight().Background(t.BgBase).
			Padding(design.Space4).Children(func() {
			o := uikit.ToolRowOptions{
				Key:           "test.gutter",
				Icon:          icons.File(),
				Name:          "读取文件",
				Status:        "状态",
				StatusColor:   uikit.StatusColor(t, state),
				State:         state,
				Running:       running,
				Preview:       "auth.go",
				InlineChevron: v.band,
				MarkerColumn:  v.band,
			}
			if v.panel {
				o.Sections = []uikit.ToolPanelSection{{Label: "输出", Text: "完成"}}
			}
			uikit.ToolRow(c, o)
		})
	}
}

// gutterPixel is a point on the state gutter: the row starts at the page's
// design.Space4 padding and the gutter is its first child, so the row's first
// DIP is inside the 3 DIP rule while a quiet row leaves that DIP to the page
// (the glyph beside it starts a little further in); vertically the name's
// center is the row's center, where the centered rule lies.
func gutterPixel(t *testing.T, tt *ui.Tester) (int, int) {
	t.Helper()
	name, ok := tt.Find("读取文件")
	if !ok {
		t.Fatalf("gutter row name missing; texts = %v", tt.Texts())
	}
	return int(design.Space4), int(name.Y + name.H/2)
}

// TestToolRowStateDoesNotReflow pins the 32 DIP row's geometry: a call that goes
// from running to finished changes its glyph and (for a failure or a stop) its
// word, but the name and the summary keep their rectangles, so the line the
// reader is watching never jumps.
func TestToolRowStateDoesNotReflow(t *testing.T) {
	running := ui.NewTester(toolRowGutterView(uikit.StatusRunning, true, toolRowGutterVariant{}), 400, 120)
	running.Frame()
	settled := ui.NewTester(toolRowGutterView(uikit.StatusSuccess, false, toolRowGutterVariant{}), 400, 120)
	settled.Frame()
	runName, ok := running.Find("读取文件")
	if !ok {
		t.Fatalf("running row name missing; texts = %v", running.Texts())
	}
	doneName, ok := settled.Find("读取文件")
	if !ok {
		t.Fatalf("settled row name missing; texts = %v", settled.Texts())
	}
	if runName.X != doneName.X || runName.Y != doneName.Y {
		t.Fatalf("running name rect = %+v, settled = %+v: the state mark must only recolor, never reflow",
			runName, doneName)
	}
}

// TestToolRowStateGlyph covers the row's state-first mark (B-spec-v2 §3.3.1): the
// leading 16 DIP glyph is the call's state — the accent spinner while it runs, a
// success check, a danger alert, and a neutral dot for a user stop — and a
// finished call keeps its own quiet glyph instead of a warning/blank lane.
func TestToolRowStateGlyph(t *testing.T) {
	cases := []struct {
		name    string
		state   uikit.StatusKind
		running bool
		want    ui.Color
		// tolerance allows for the antialiasing of a thin arc (the spinner) or a
		// diagonal stroke (the check).
		tolerance int
	}{
		{"running", uikit.StatusRunning, true, design.Light().Accent, 90},
		{"error", uikit.StatusError, false, design.Light().DangerText, 40},
		{"success", uikit.StatusSuccess, false, design.Light().SuccessText, 40},
		{"cancelled", uikit.StatusCancelled, false, design.Light().LabelTertiary, 40},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tt := ui.NewTester(toolRowGutterView(tc.state, tc.running, toolRowGutterVariant{}), 400, 120)
			tt.Frame()
			// The glyph owns the row's first 16 DIP after the page padding: sample
			// the glyph's center row, two DIP in, where a filled marker paints.
			name, ok := tt.Find("读取文件")
			if !ok {
				t.Fatalf("row name missing; texts = %v", tt.Texts())
			}
			top := int(name.Y + name.H/2 - design.Space2)
			want := color.RGBA{R: tc.want.R, G: tc.want.G, B: tc.want.B, A: tc.want.A}
			if got := dominantInk(tt, int(design.Space4), top, int(design.Space4)); !closeColor(got, want, tc.tolerance) {
				t.Fatalf("state %q glyph ink = %v, want %v", tc.name, got, want)
			}
		})
	}
}

// dominantInk returns the darkest pixel of a square glyph slot: its stroke
// against the page background. A square (not one row) is what a round spinner
// arc or a diagonal check needs to show its ink.
func dominantInk(tt *ui.Tester, x, y, size int) color.RGBA {
	best := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	for dy := 0; dy < size; dy++ {
		for dx := 0; dx < size; dx++ {
			pixel := tt.Image().RGBAAt(x+dx, y+dy)
			if luminance(pixel) < luminance(best) {
				best = pixel
			}
		}
	}
	return best
}

// luminance is a rough brightness, for picking the darkest pixel of a glyph.
func luminance(c color.RGBA) int {
	return int(c.R)*299 + int(c.G)*587 + int(c.B)*114
}

// closeColor reports whether two inks match within the case's antialiasing
// margin.
func closeColor(got, want color.RGBA, tolerance int) bool {
	delta := func(a, b uint8) int {
		if a > b {
			return int(a - b)
		}
		return int(b - a)
	}
	return delta(got.R, want.R) <= tolerance && delta(got.G, want.G) <= tolerance &&
		delta(got.B, want.B) <= tolerance
}
