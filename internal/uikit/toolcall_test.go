package uikit_test

// Headless tests for the shared complete tool-call card (uikit.ToolCallCard):
// it is the one card behind the transcript's decision card, the trajectory
// timeline, the registered plan card and the demo probe card. The tests drive
// the real interaction (the official detail disclosure and its copy actions, the
// header slots, the team footer) and sample the rendered frame, because the
// status colors and the panel/line degradation are the card's contract.

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// toolCardView draws one complete tool card on a page background, so the card
// panel, its status badge and its status text are the frame's own pixels.
func toolCardView(o uikit.ToolCallCardOptions) func(*ui.Context) {
	return func(c *ui.Context) {
		t := design.Of(c)
		ui.Column(c).FillWidth().FillHeight().Background(t.BgBase).
			Padding(design.Space4).Children(func() {
			uikit.ToolCallCard(c, o)
		})
	}
}

// bashCall is one tool call for the card tests.
func bashCall(status, summary string) contracts.ToolCall {
	return contracts.ToolCall{ID: "t1", Name: "bash", Status: status, Summary: summary}
}

// framesDiffer reports whether two rendered frames disagree on any pixel.
func framesDiffer(a, b *ui.Tester) bool {
	ia, ib := a.Image(), b.Image()
	if ia.Rect != ib.Rect {
		return true
	}
	for y := ia.Rect.Min.Y; y < ia.Rect.Max.Y; y++ {
		for x := ia.Rect.Min.X; x < ia.Rect.Max.X; x++ {
			if ia.RGBAAt(x, y) != ib.RGBAAt(x, y) {
				return true
			}
		}
	}
	return false
}

// TestToolCallCardStates covers the five statuses on the complete card: the
// friendly name and the status text are always visible, a running call shows the
// official spinner (never a settled badge), an unknown backend status keeps its
// own word, and only a failure paints the danger red.
func TestToolCallCardStates(t *testing.T) {
	for _, tc := range []struct {
		status  string
		want    string
		spinner bool
	}{
		{"running", "运行中", true},
		{"ok", "完成", false},
		{"error", "失败", false},
		{"cancelled", "已取消", false},
		{"blocked", "blocked", false},
	} {
		open := false
		tt := ui.NewTester(toolCardView(uikit.ToolCallCardOptions{
			Key:  "test.card." + tc.status,
			Call: bashCall(tc.status, "stdout: done"),
			Open: &open,
			Sections: []uikit.ToolPanelSection{{
				Label: "输出", Text: "stdout: done",
			}},
		}), 480, 300)
		tt.Frame()
		if !tt.HasText("运行命令") {
			t.Fatalf("%s card has no friendly name; texts = %v", tc.status, tt.Texts())
		}
		if !tt.HasText(tc.want) {
			t.Fatalf("%s card has no %q; texts = %v", tc.status, tc.want, tt.Texts())
		}
		if tc.spinner {
			if _, ok := tt.AccessByRole(ui.RoleProgress, "运行中"); !ok {
				t.Fatalf("running card has no labeled spinner; texts = %v", tt.Texts())
			}
		}
		// The card's status chip uses the one semantic vocabulary: only error is
		// the danger kind, cancelled stays neutral. Asserting the mapping (not
		// sampled raster pixels) keeps the test deterministic across host fonts;
		// the rendered colors are covered by StatusColor's frame test.
		kind := uikit.ParseToolStatus(tc.status).Kind()
		if tc.status == "error" && kind != uikit.StatusError {
			t.Fatalf("error status folds to kind %v, want StatusError", kind)
		}
		if tc.status == "cancelled" && kind != uikit.StatusCancelled {
			t.Fatalf("cancelled status folds to kind %v, want StatusCancelled", kind)
		}
	}
}

// TestToolCallCardSectionsCopy covers the expanded detail: the copyable
// segments render their copy action and it writes the segment to the clipboard.
func TestToolCallCardSectionsCopy(t *testing.T) {
	open := true
	tt := ui.NewTester(toolCardView(uikit.ToolCallCardOptions{
		Key:  "test.copy",
		Call: bashCall("ok", "stdout: done"),
		Open: &open,
		Sections: []uikit.ToolPanelSection{{
			Label: "输出", Text: "stdout: done", CopyLabel: "复制输出",
		}},
	}), 480, 300)
	tt.Frame()
	if !tt.HasText("输出") || !tt.HasText("stdout: done") {
		t.Fatalf("expanded card does not show its section; texts = %v", tt.Texts())
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "复制输出"); !ok {
		t.Fatalf("section has no copy action; texts = %v", tt.Texts())
	}
	if err := tt.Click("复制输出"); err != nil {
		t.Fatalf("click copy: %v", err)
	}
	if got := tt.Clipboard(); got != "stdout: done" {
		t.Fatalf("clipboard = %q, want the section text", got)
	}
}

// TestToolCallCardDefaultOpenAndToggle covers the H15 default shared with the
// compact row (failures always open, short results open) and the official
// disclosure toggle reporting through OnToggle.
func TestToolCallCardDefaultOpenAndToggle(t *testing.T) {
	long := strings.Repeat("行结果 ", 80) // over the 160-rune H15 bound
	for _, tc := range []struct {
		status, summary string
		want            bool
	}{
		{"error", "boom", true},
		{"error", long, true},
		{"ok", "short", true},
		{"ok", long, false},
		{"cancelled", "short", true},
	} {
		if got := uikit.SummaryDefaultOpen(tc.status, tc.summary); got != tc.want {
			t.Fatalf("SummaryDefaultOpen(%q, %d runes) = %v, want %v",
				tc.status, len([]rune(tc.summary)), got, tc.want)
		}
	}

	open := uikit.SummaryDefaultOpen("error", "boom")
	toggles := make([]bool, 0, 1)
	tt := ui.NewTester(toolCardView(uikit.ToolCallCardOptions{
		Key:  "test.toggle",
		Call: bashCall("error", "boom"),
		Open: &open,
		OnToggle: func(next bool) {
			toggles = append(toggles, next)
		},
		Sections: []uikit.ToolPanelSection{{Label: "输出", Text: "boom"}},
	}), 480, 300)
	tt.Frame()
	info, ok := tt.AccessByRole(ui.RoleDisclosure, "详情")
	if !ok || !info.Expanded {
		t.Fatalf("H15: a failed card should start expanded: %+v found %v", info, ok)
	}
	if err := tt.Click("详情"); err != nil {
		t.Fatalf("click details: %v", err)
	}
	tt.Frame()
	if info, _ := tt.AccessByRole(ui.RoleDisclosure, "详情"); info.Expanded {
		t.Fatalf("click did not fold the card: %+v", info)
	}
	if len(toggles) != 1 || toggles[0] {
		t.Fatalf("OnToggle calls = %v, want [false]", toggles)
	}
	if open {
		t.Fatal("OnToggle did not reach the caller-owned open pointer")
	}
}

// TestToolCallCardWithoutBodyIsASingleLine covers the degradation rule: a normal
// card with nothing to disclose renders as the quiet line (no panel, no empty
// disclosure), while a decision card keeps its panel as the decision's record.
func TestToolCallCardWithoutBodyIsASingleLine(t *testing.T) {
	line := ui.NewTester(toolCardView(uikit.ToolCallCardOptions{
		Key:  "test.line",
		Call: bashCall("ok", ""),
	}), 480, 200)
	line.Frame()
	if !line.HasText("运行命令") || !line.HasText("完成") {
		t.Fatalf("degraded line lost the name or status; texts = %v", line.Texts())
	}
	if line.HasText("详情") {
		t.Fatalf("a card without a body shows an empty disclosure; texts = %v", line.Texts())
	}

	decision := ui.NewTester(toolCardView(uikit.ToolCallCardOptions{
		Key:     "test.line",
		Call:    bashCall("ok", ""),
		Variant: uikit.ToolCallCardDecision,
	}), 480, 200)
	decision.Frame()
	if decision.HasText("详情") {
		t.Fatalf("a decision card without a body shows an empty disclosure; texts = %v", decision.Texts())
	}
	if !framesDiffer(line, decision) {
		t.Fatal("a decision card without a detail body lost its panel")
	}
}

// TestToolCallCardVariantsShareTheAnatomy covers the two variants with a body:
// the decision and normal cards draw the same header, step metadata and detail
// disclosure, so a call never has two different card anatomies.
func TestToolCallCardVariantsShareTheAnatomy(t *testing.T) {
	options := func(variant uikit.ToolCallCardVariant) uikit.ToolCallCardOptions {
		return uikit.ToolCallCardOptions{
			Key:  "test.variant",
			Call: bashCall("ok", "stdout: done"),
			Sections: []uikit.ToolPanelSection{{
				Label: "输出", Text: "stdout: done", CopyLabel: "复制输出",
			}},
			Steps:   2,
			Variant: variant,
		}
	}
	normal := ui.NewTester(toolCardView(options(uikit.ToolCallCardNormal)), 480, 300)
	normal.Frame()
	decision := ui.NewTester(toolCardView(options(uikit.ToolCallCardDecision)), 480, 300)
	decision.Frame()

	for _, tt := range []*ui.Tester{normal, decision} {
		if _, ok := tt.AccessByRole(ui.RoleDisclosure, "详情"); !ok {
			t.Fatalf("variant card has no detail disclosure; texts = %v", tt.Texts())
		}
		for _, want := range []string{"运行命令", "完成", "步骤 2"} {
			if !tt.HasText(want) {
				t.Fatalf("variant card does not show %q; texts = %v", want, tt.Texts())
			}
		}
	}
	if !slices.Equal(normal.Texts(), decision.Texts()) {
		t.Fatalf("variants render different copy: normal %v, decision %v",
			normal.Texts(), decision.Texts())
	}
}

// TestToolCallCardHeaderAndOverrides covers the plan/demo card compatibility
// paths: a custom title replaces the friendly name, a custom status label
// replaces the localized one, and Header adds extra header content (the plan
// reviewer badge) before the spacer.
func TestToolCallCardHeaderAndOverrides(t *testing.T) {
	tt := ui.NewTester(toolCardView(uikit.ToolCallCardOptions{
		Key:         "test.overrides",
		Call:        bashCall("ok", "the summary"),
		Title:       "工具调用: demo",
		StatusLabel: "ok",
		HideStatus:  false,
		Header: func(c *ui.Context) {
			ui.Text(c, "审阅中")
		},
		Content: func(c *ui.Context) {
			ui.Text(c, "the summary")
		},
	}), 480, 200)
	tt.Frame()
	for _, want := range []string{"工具调用: demo", "ok", "审阅中", "the summary"} {
		if !tt.HasText(want) {
			t.Fatalf("overridden card does not show %q; texts = %v", want, tt.Texts())
		}
	}
	if tt.HasText("运行命令") || tt.HasText("完成") {
		t.Fatalf("title/status overrides did not replace the defaults; texts = %v", tt.Texts())
	}
	if tt.HasText("详情") {
		t.Fatalf("rich Content should replace the disclosure; texts = %v", tt.Texts())
	}
}

// TestToolCallCardFooter covers the disclosure footer slot (the team and task
// cross-navigation): it renders inside the expanded detail, never while folded,
// and its action runs.
func TestToolCallCardFooter(t *testing.T) {
	clicks := 0
	open := false
	tt := ui.NewTester(toolCardView(uikit.ToolCallCardOptions{
		Key:      "test.footer",
		Call:     bashCall("ok", "out"),
		Open:     &open,
		Sections: []uikit.ToolPanelSection{{Label: "输出", Text: "out"}},
		Footer: func(c *ui.Context) {
			if ui.Button(c, "在团队中查看").Clicked() {
				clicks++
			}
		},
	}), 480, 300)
	tt.Frame()
	if tt.HasText("在团队中查看") {
		t.Fatalf("the footer shows while the detail is folded; texts = %v", tt.Texts())
	}
	if err := tt.Click("详情"); err != nil {
		t.Fatalf("click details: %v", err)
	}
	tt.Advance(300 * time.Millisecond) // the disclosure's open animation
	tt.Frame()
	if !tt.HasText("在团队中查看") {
		t.Fatalf("the expanded card has no footer action; texts = %v", tt.Texts())
	}
	if err := tt.Click("在团队中查看"); err != nil {
		t.Fatalf("click footer: %v", err)
	}
	tt.Frame()
	if clicks != 1 {
		t.Fatalf("footer action calls = %d, want 1", clicks)
	}
}
