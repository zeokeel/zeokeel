package uikit_test

// The compact tool row's anatomy (B-spec-v2 §3.3.1): a 32 DIP line of
// [state glyph][name 13/500][input summary 12 tertiary][duration 11][chevron].
// The glyph, the name and the summary keep one left edge across every state, and
// the state's word follows the summary at the row's right edge, so a call that
// changes state never reflows the line the reader is looking at.

import (
	"image/color"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

func toolRowStateTester(t *testing.T, status string, running bool, kind uikit.StatusKind, locale string) *ui.Tester {
	t.Helper()
	tester := ui.NewTester(func(c *ui.Context) {
		tk := design.Of(c)
		uikit.ToolRow(c, uikit.ToolRowOptions{
			Key: "t1", Icon: icons.Terminal(), Name: "运行命令", RawName: "bash",
			Status: status, StatusColor: uikit.StatusColor(tk, kind), Running: running,
			State: kind, Preview: "npm test -- --watch", Duration: "1.2s",
			Sections: []uikit.ToolPanelSection{{Label: "x", Text: "y"}},
		})
	}, 760, 300)
	tester.SetLocale(locale)
	tester.Frame()
	return tester
}

// TestToolRowAnatomyKeepsOneLeftEdge pins the row's fixed anatomy: the tool name
// and the input summary start at the same x in every state, and the preview is
// still on screen beside the state word.
func TestToolRowAnatomyKeepsOneLeftEdge(t *testing.T) {
	states := []struct {
		status  string
		running bool
		kind    uikit.StatusKind
	}{
		{"运行中", true, uikit.StatusRunning},
		{"完成", false, uikit.StatusSuccess},
		{"失败", false, uikit.StatusError},
		{"已停止", false, uikit.StatusCancelled},
	}
	nameX, previewX := float32(-1), float32(-1)
	for _, s := range states {
		tester := toolRowStateTester(t, s.status, s.running, s.kind, "zh-Hans")
		name, ok := tester.Find("运行命令")
		if !ok {
			t.Fatalf("state %q: name missing; texts = %v", s.status, tester.Texts())
		}
		preview, ok := tester.Find("npm test -- --watch")
		if !ok {
			t.Fatalf("state %q: preview missing; texts = %v", s.status, tester.Texts())
		}
		if nameX < 0 {
			nameX, previewX = name.X, preview.X
		}
		if name.X != nameX {
			t.Fatalf("state %q name x=%.1f, want %.1f (one left edge across states)",
				s.status, name.X, nameX)
		}
		if preview.X != previewX {
			t.Fatalf("state %q preview x=%.1f, want %.1f (one left edge across states)",
				s.status, preview.X, previewX)
		}
		// Success is the one quiet state: its glyph and accessible name carry it
		// and the row drops the word, so a transcript of finished calls stays quiet.
		if s.kind == uikit.StatusSuccess {
			continue
		}
		if !tester.HasText(s.status) {
			t.Fatalf("state %q: the state word is missing; texts = %v", s.status, tester.Texts())
		}
	}
}

// TestToolRowHeightAndDuration pins the row's 32 DIP measure and the duration's
// place: the micro step before the chevron, drawn only when the call measured an
// interval.
func TestToolRowHeightAndDuration(t *testing.T) {
	tester := toolRowStateTester(t, "完成", false, uikit.StatusSuccess, "zh-Hans")
	info, ok := tester.Access("运行命令 · 完成")
	if !ok {
		t.Fatalf("the row is not one named disclosure; texts = %v", tester.Texts())
	}
	if info.Bounds.H != design.RowCompactHeight {
		t.Fatalf("row height = %.1f, want %.1f (the compact row step)",
			info.Bounds.H, design.RowCompactHeight)
	}
	duration, ok := tester.Find("1.2s")
	if !ok {
		t.Fatalf("the duration is missing; texts = %v", tester.Texts())
	}
	preview, _ := tester.Find("npm test -- --watch")
	if duration.X <= preview.X {
		t.Fatalf("duration x=%.1f sits before the preview x=%.1f; want it at the row's tail",
			duration.X, preview.X)
	}

	// The tail order (preview → duration → state word → chevron) is pinned for
	// both widths by TestToolRowTailOrderMatchesTheActivityBand.
}

// TestToolRowWithoutMeasuredIntervalHidesDuration pins the honesty rule: a call
// whose interval is unknown shows no duration instead of a guessed one.
func TestToolRowWithoutMeasuredIntervalHidesDuration(t *testing.T) {
	tester := ui.NewTester(func(c *ui.Context) {
		tk := design.Of(c)
		uikit.ToolRow(c, uikit.ToolRowOptions{
			Key: "t2", Name: "运行命令", Status: "完成",
			StatusColor: uikit.StatusColor(tk, uikit.StatusSuccess),
			State:       uikit.StatusSuccess, Preview: "npm test",
		})
	}, 760, 120)
	tester.Frame()
	for _, text := range tester.Texts() {
		if text == "1.2s" {
			t.Fatalf("a duration appeared without a measured interval; texts = %v", tester.Texts())
		}
	}
}

// TestFormatToolDuration pins the duration vocabulary.
func TestFormatToolDuration(t *testing.T) {
	cases := map[string]string{
		"0s":    "",
		"500µs": "<1ms",
		"820ms": "820ms",
		"1.25s": "1.2s",
		"64s":   "1m04s",
	}
	for input, want := range cases {
		duration, err := time.ParseDuration(input)
		if err != nil {
			t.Fatalf("parse %q: %v", input, err)
		}
		if got := uikit.FormatToolDuration(duration); got != want {
			t.Fatalf("FormatToolDuration(%s) = %q, want %q", input, got, want)
		}
	}
}

// TestToolRowSuccessKeepsTheWordOutOfTheRow pins the deliberate convention
// documented at the top of toolrow.go: a finished call draws the success check
// and reports its status through the row's accessible name — it never prints the
// status word, so a transcript of finished calls stays quiet. Running, failed and
// cancelled rows do state their word.
//
// The accessible name is the row's only status channel: a disclosure trigger's
// children (the glyph, the word) are not access nodes of their own, so the name
// is where the state travels — which is why it includes the localized status.
func TestToolRowSuccessKeepsTheWordOutOfTheRow(t *testing.T) {
	success := toolRowStateTester(t, "完成", false, uikit.StatusSuccess, "zh-Hans")
	if _, ok := success.Access("运行命令 · 完成"); !ok {
		t.Fatalf("a finished row does not state its status to assistive technology; texts = %v",
			success.Texts())
	}
	// Exact match, not a substring: a bare "完成" node is the drawn word, and the
	// finished row must not have one (its name "运行命令 · 完成" is not a match).
	if _, drawn := success.Find("完成"); drawn {
		t.Fatalf("a finished row prints its status word; the convention is glyph + accessible name only; texts = %v",
			success.Texts())
	}
	for _, state := range []struct {
		status string
		kind   uikit.StatusKind
	}{
		{"运行中", uikit.StatusRunning},
		{"失败", uikit.StatusError},
		{"已停止", uikit.StatusCancelled},
	} {
		tester := toolRowStateTester(t, state.status, state.kind == uikit.StatusRunning, state.kind, "zh-Hans")
		if !tester.HasText(state.status) {
			t.Fatalf("the %q state does not state its word; texts = %v", state.status, tester.Texts())
		}
	}
}

// TestToolRowTailOrderMatchesTheActivityBand pins one order for the row's tail at
// both widths: preview, then duration, then the state word, then the chevron. The
// process timeline stacks band rows and wide rows, and the two must not show the
// same three pieces in two arrangements.
func TestToolRowTailOrderMatchesTheActivityBand(t *testing.T) {
	row := func(band bool) *ui.Tester {
		tester := ui.NewTester(func(c *ui.Context) {
			tk := design.Of(c)
			uikit.ToolRow(c, uikit.ToolRowOptions{
				Key: "tail", Icon: icons.Terminal(), Name: "运行命令", RawName: "bash",
				Status: "失败", StatusColor: uikit.StatusColor(tk, uikit.StatusError),
				State: uikit.StatusError, Preview: "npm test -- --watch", Duration: "8.4s",
				MarkerColumn: band, InlineChevron: band,
				Sections: []uikit.ToolPanelSection{{Label: "输出", Text: "failed"}},
			})
		}, 760, 200)
		tester.SetLocale("zh-Hans")
		tester.Frame()
		return tester
	}
	for _, band := range []bool{false, true} {
		tester := row(band)
		preview, okPreview := tester.Find("npm test -- --watch")
		duration, okDuration := tester.Find("8.4s")
		word, okWord := tester.Find("失败")
		if !okPreview || !okDuration || !okWord {
			t.Fatalf("band=%v: preview/duration/word missing (%v/%v/%v); texts = %v",
				band, okPreview, okDuration, okWord, tester.Texts())
		}
		if !(preview.X < duration.X && duration.X < word.X) {
			t.Fatalf("band=%v tail order x = preview %.1f, duration %.1f, word %.1f; want preview < duration < word",
				band, preview.X, duration.X, word.X)
		}
	}
}

// TestToolExitCode pins the shell exit marker's parser: the bracketed marker the
// Rig tools append, the last one winning, and no marker meaning "unknown".
func TestToolExitCode(t *testing.T) {
	cases := []struct {
		result string
		code   int
		found  bool
	}{
		{"ok\n", 0, false},
		{"go: no such file\n[exit code: 2]", 2, true},
		{"[exit code: 0]", 0, true},
		{"[exit code: 1]\n[exit code: 7]", 7, true},
		{"[exit code: notanumber]", 0, false},
		{"[exit code: 2", 0, false},
	}
	for _, test := range cases {
		got, found := uikit.ToolExitCode(test.result)
		if found != test.found || (found && got != test.code) {
			t.Fatalf("ToolExitCode(%q) = %d/%v, want %d/%v", test.result, got, found, test.code, test.found)
		}
	}
}

// TestToolRowShellExitCodeIsAFailure is the audit's bug, pinned: bash reports the
// call as "success" and appends "[exit code: 2]", and the row must draw the
// failure — the alert glyph and the 失败 word — not the success check. The same
// result with exit code 0 stays a quiet success, a canceled call stays neutral,
// and a non-shell tool's text is never re-classified.
func TestToolRowShellExitCodeIsAFailure(t *testing.T) {
	cases := []struct {
		name string
		call contracts.ToolCall
		kind uikit.StatusKind
		word string
	}{
		{
			name: "bash exit 2",
			call: contracts.ToolCall{Name: "bash", Status: "success", Summary: "ls: cannot access\n[exit code: 2]"},
			kind: uikit.StatusError, word: "失败",
		},
		{
			name: "pwsh exit 1",
			call: contracts.ToolCall{Name: "pwsh", Status: "success", Summary: "error\n[exit code: 1]"},
			kind: uikit.StatusError, word: "失败",
		},
		{
			name: "bash exit 0",
			call: contracts.ToolCall{Name: "bash", Status: "success", Summary: "all good\n[exit code: 0]"},
			kind: uikit.StatusSuccess, word: "完成",
		},
		{
			name: "canceled bash with a nonzero code",
			call: contracts.ToolCall{Name: "bash", Status: "cancelled", Summary: "[exit code: 130]"},
			kind: uikit.StatusCancelled, word: "已取消",
		},
		{
			name: "read_file text that mentions a code",
			call: contracts.ToolCall{Name: "read_file", Status: "success", Summary: "log line\n[exit code: 3]"},
			kind: uikit.StatusSuccess, word: "完成",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := uikit.ToolCallStatusKind(test.call); got != test.kind {
				t.Fatalf("status kind = %v, want %v", got, test.kind)
			}
			if got := uikit.ToolCallStatusLabel(nil, test.call); got != test.word {
				t.Fatalf("status label = %q, want %q", got, test.word)
			}
		})
	}

	// The row draws it: the alert glyph and the failure word, never the check.
	call := contracts.ToolCall{Name: "bash", Status: "success",
		Summary: "ls: cannot access 'x': No such file or directory\n[exit code: 2]"}
	kind := uikit.ToolCallStatusKind(call)
	tester := ui.NewTester(func(c *ui.Context) {
		tk := design.Of(c)
		uikit.ToolRow(c, uikit.ToolRowOptions{
			Key: "exit2", Icon: uikit.ToolIcon(call.Name), Name: "运行命令", RawName: "bash",
			Status: uikit.ToolCallStatusLabel(nil, call), StatusColor: uikit.StatusColor(tk, kind),
			State: kind, Preview: uikit.ToolCallPreview(nil, call),
			Sections: uikit.ToolPanelSections(nil, call),
		})
	}, 760, 240)
	tester.SetLocale("zh-Hans")
	tester.Frame()
	if _, ok := tester.Find("失败"); !ok {
		t.Fatalf("a nonzero exit code does not draw the failure word; texts = %v", tester.Texts())
	}
	if _, ok := tester.Access("运行命令 · 失败"); !ok {
		t.Fatalf("the failed row does not state its state to assistive technology; texts = %v", tester.Texts())
	}
	// The alert glyph is the row's leading mark, in the danger ink.
	row, _ := tester.Access("运行命令 · 失败")
	want := design.Light().DangerText
	ink := dominantInk(tester, int(row.Bounds.X+design.Space2), int(row.Bounds.Y), int(design.Space4))
	if !closeColor(ink, color.RGBA{R: want.R, G: want.G, B: want.B, A: want.A}, 90) {
		t.Fatalf("the failed row's leading glyph ink = %v, want the danger ink %v", ink, want)
	}
}
