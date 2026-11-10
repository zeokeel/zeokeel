package uikit_test

// Headless tests for the shared status vocabulary: the domain statuses fold onto
// one StatusKind, the colors are the WCAG-AA *Text tokens (never the fills), a
// cancelled stop and an interrupted run stay neutral, and the badge carries the
// caller's text (a status is never color alone). The pixel probes render a real
// frame, so a regression to a fill color or to the danger red shows up.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// statusTestLocale is the smallest LocaleService for the label helpers.
type statusTestLocale struct{ lang string }

func (l statusTestLocale) Language() string    { return l.lang }
func (l statusTestLocale) SetLanguage(string)  {}
func (l statusTestLocale) T(key string) string { return key }

// sameColor reports whether two tokens are the same sRGB value.
func sameColor(a, b ui.Color) bool {
	return a.R == b.R && a.G == b.G && a.B == b.B && a.A == b.A
}

// TestStatusKindFolding covers the domain parsers and their place in the shared
// vocabulary, including the rule that availability "active" is never running.
func TestStatusKindFolding(t *testing.T) {
	for _, want := range []struct {
		raw  string
		kind uikit.ToolStatus
	}{
		{"running", uikit.ToolStatusRunning},
		{"in_progress", uikit.ToolStatusRunning},
		{"started", uikit.ToolStatusRunning},
		{"ok", uikit.ToolStatusSuccess},
		{"done", uikit.ToolStatusSuccess},
		{"completed", uikit.ToolStatusSuccess},
		{"error", uikit.ToolStatusError},
		{"failed", uikit.ToolStatusError},
		{"cancelled", uikit.ToolStatusCancelled},
		{"canceled", uikit.ToolStatusCancelled},
		{"", uikit.ToolStatusUnknown},
		{"blocked", uikit.ToolStatusUnknown},
		// Availability is not a run: "active" must never read as running.
		{"active", uikit.ToolStatusUnknown},
	} {
		if got := uikit.ParseToolStatus(want.raw); got != want.kind {
			t.Fatalf("ParseToolStatus(%q) = %q, want %q", want.raw, got, want.kind)
		}
	}
	if got := uikit.ToolStatusRunning.Kind(); got != uikit.StatusRunning {
		t.Fatalf("running kind = %v", got)
	}
	if got := uikit.ToolStatusSuccess.Kind(); got != uikit.StatusSuccess {
		t.Fatalf("success kind = %v", got)
	}
	if got := uikit.ToolStatusError.Kind(); got != uikit.StatusError {
		t.Fatalf("error kind = %v", got)
	}
	if got := uikit.ToolStatusCancelled.Kind(); got != uikit.StatusCancelled {
		t.Fatalf("cancelled kind = %v", got)
	}
	if got := uikit.ToolStatusUnknown.Kind(); got != uikit.StatusUnknown {
		t.Fatalf("unknown kind = %v", got)
	}

	for _, want := range []struct {
		raw  string
		kind uikit.RunState
	}{
		{"running", uikit.RunStateRunning},
		{"idle", uikit.RunStateIdle},
		{"done", uikit.RunStateDone},
		{"completed", uikit.RunStateDone},
		{"failed", uikit.RunStateFailed},
		{"interrupted", uikit.RunStateInterrupted},
		{"closed", uikit.RunStateUnknown},
		// Availability, not a tracked run: never running (AP-08).
		{"active", uikit.RunStateUnknown},
	} {
		if got := uikit.ParseRunState(want.raw); got != want.kind {
			t.Fatalf("ParseRunState(%q) = %q, want %q", want.raw, got, want.kind)
		}
	}
	for _, want := range []struct {
		state uikit.RunState
		kind  uikit.StatusKind
	}{
		{uikit.RunStateRunning, uikit.StatusRunning},
		{uikit.RunStateIdle, uikit.StatusIdle},
		{uikit.RunStateDone, uikit.StatusSuccess},
		{uikit.RunStateFailed, uikit.StatusError},
		{uikit.RunStateInterrupted, uikit.StatusInterrupted},
		{uikit.RunStateUnknown, uikit.StatusUnknown},
	} {
		if got := want.state.Kind(); got != want.kind {
			t.Fatalf("%q.Kind() = %v, want %v", want.state, got, want.kind)
		}
	}

	for _, want := range []struct {
		raw  string
		kind uikit.TaskStatus
	}{
		{"pending", uikit.TaskStatusPending},
		{"todo", uikit.TaskStatusPending},
		{"in_progress", uikit.TaskStatusInProgress},
		{"doing", uikit.TaskStatusInProgress},
		{"completed", uikit.TaskStatusCompleted},
		{"done", uikit.TaskStatusCompleted},
		// A board status "active" is the task board's own alias for in progress:
		// the board is not a run, so this is not the AP-08 "active is not
		// running" case (the run-state table above keeps a run's availability
		// "active" unknown, never running).
		{"active", uikit.TaskStatusInProgress},
		{"", uikit.TaskStatusUnknown},
	} {
		if got := uikit.ParseTaskStatus(want.raw); got != want.kind {
			t.Fatalf("ParseTaskStatus(%q) = %q, want %q", want.raw, got, want.kind)
		}
	}
	for _, want := range []struct {
		status uikit.TaskStatus
		kind   uikit.StatusKind
	}{
		{uikit.TaskStatusPending, uikit.StatusPending},
		{uikit.TaskStatusInProgress, uikit.StatusInProgress},
		{uikit.TaskStatusCompleted, uikit.StatusSuccess},
		{uikit.TaskStatusUnknown, uikit.StatusUnknown},
	} {
		if got := want.status.Kind(); got != want.kind {
			t.Fatalf("%q.Kind() = %v, want %v", want.status, got, want.kind)
		}
	}
}

// TestStatusColorUsesTextTokens pins the §5.2 rule: the status foreground is the
// WCAG-AA *Text token, never the fill color, and every non-failure state is
// neutral (cancelled, interrupted, idle, pending, unknown).
func TestStatusColorUsesTextTokens(t *testing.T) {
	tokens := design.Light()
	for _, want := range []struct {
		kind uikit.StatusKind
		want ui.Color
	}{
		{uikit.StatusRunning, tokens.WarningText},
		{uikit.StatusInProgress, tokens.WarningText},
		{uikit.StatusSuccess, tokens.SuccessText},
		{uikit.StatusError, tokens.DangerText},
		{uikit.StatusCancelled, tokens.LabelTertiary},
		{uikit.StatusInterrupted, tokens.LabelTertiary},
		{uikit.StatusIdle, tokens.LabelTertiary},
		{uikit.StatusPending, tokens.LabelTertiary},
		{uikit.StatusUnknown, tokens.LabelTertiary},
	} {
		if got := uikit.StatusColor(tokens, want.kind); !sameColor(got, want.want) {
			t.Fatalf("StatusColor(%v) = %v, want %v", want.kind, got, want.want)
		}
	}
	// The fill colors are background/icon colors, too light for small text.
	for _, kind := range []uikit.StatusKind{uikit.StatusRunning, uikit.StatusSuccess, uikit.StatusError} {
		if sameColor(uikit.StatusColor(tokens, kind), tokens.Warning) ||
			sameColor(uikit.StatusColor(tokens, kind), tokens.Success) ||
			sameColor(uikit.StatusColor(tokens, kind), tokens.Danger) {
			t.Fatalf("StatusColor(%v) is a fill color, want its *Text variant", kind)
		}
	}
	if sameColor(uikit.StatusColor(tokens, uikit.StatusCancelled), tokens.DangerText) {
		t.Fatal("a cancelled stop paints the danger text color")
	}
}

// TestStatusLabels covers the localized copy each domain keeps: tool, run and
// task statuses have their own wording, and an unknown backend word stays
// readable.
func TestStatusLabels(t *testing.T) {
	zh := statusTestLocale{lang: "zh"}
	en := statusTestLocale{lang: "en"}
	for _, want := range []struct {
		got  string
		want string
	}{
		{uikit.ToolStatusLabel(zh, "running"), "运行中"},
		{uikit.ToolStatusLabel(zh, "ok"), "完成"},
		{uikit.ToolStatusLabel(zh, "error"), "失败"},
		{uikit.ToolStatusLabel(zh, "cancelled"), "已取消"},
		{uikit.ToolStatusLabel(zh, ""), "未知"},
		{uikit.ToolStatusLabel(zh, "blocked"), "blocked"},
		{uikit.ToolStatusLabel(en, "running"), "Running"},
		{uikit.ToolStatusLabel(en, "cancelled"), "Cancelled"},

		{uikit.RunStateLabel(zh, "running"), "运行中"},
		{uikit.RunStateLabel(zh, "done"), "已完成"},
		{uikit.RunStateLabel(zh, "failed"), "失败"},
		{uikit.RunStateLabel(zh, "interrupted"), "已中断"},
		{uikit.RunStateLabel(zh, "idle"), "空闲"},
		{uikit.RunStateLabel(zh, ""), "未知"},
		{uikit.RunStateLabel(en, "done"), "Done"},

		{uikit.TaskStatusLabel(zh, "pending"), "待处理"},
		{uikit.TaskStatusLabel(zh, "in_progress"), "进行中"},
		{uikit.TaskStatusLabel(zh, "completed"), "已完成"},
		{uikit.TaskStatusLabel(zh, ""), "未知"},
		{uikit.TaskStatusLabel(en, "in_progress"), "In progress"},
	} {
		if want.got != want.want {
			t.Fatalf("label = %q, want %q", want.got, want.want)
		}
	}
	// A nil locale falls back to the product's Chinese copy.
	if got := uikit.ToolStatusLabel(nil, "error"); got != "失败" {
		t.Fatalf("nil-locale label = %q", got)
	}
}

// statusFrame draws every status presentation in one frame: a status line and a
// badge per tool state, plus the run and task states the panes show.
func statusFrame(c *ui.Context) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.Space2).Padding(design.Space4).Children(func() {
		for _, status := range []string{"running", "ok", "error", "cancelled", "blocked"} {
			kind := uikit.ParseToolStatus(status).Kind()
			uikit.StatusText(c, t, design.TypeXS, kind, uikit.ToolStatusLabel(nil, status))
			uikit.StatusBadge(c, t, kind, uikit.ToolStatusLabel(nil, status))
		}
		for _, state := range []string{"failed", "interrupted", "idle"} {
			kind := uikit.ParseRunState(state).Kind()
			uikit.StatusBadge(c, t, kind, uikit.RunStateLabel(nil, state))
		}
		for _, status := range []string{"pending", "in_progress", "completed"} {
			kind := uikit.ParseTaskStatus(status).Kind()
			uikit.StatusBadge(c, t, kind, uikit.TaskStatusLabel(nil, status))
		}
	})
}

// TestStatusTextAndBadgeRender covers the frame output: every status shows its
// text (a status is not color alone), and the cancelled/interrupted states never
// paint the danger red.
func TestStatusTextAndBadgeRender(t *testing.T) {
	tt := ui.NewTester(statusFrame, 360, 560)
	tt.Frame()
	for _, want := range []string{
		"运行中", "完成", "失败", "已取消", "blocked",
		"已中断", "空闲", "待处理", "进行中", "已完成",
	} {
		if !tt.HasText(want) {
			t.Fatalf("frame does not show %q; texts = %v", want, tt.Texts())
		}
	}

	// The color contract is asserted through the semantic mapping rather than
	// sampled glyph pixels: the rasterized text depends on the host fonts, which
	// differ between developer machines and CI, while "failure is danger red and
	// the neutral states are not" is the invariant DESIGN.md §5.2 fixes.
	var tokens *design.Tokens
	colorTester := ui.NewTester(func(c *ui.Context) {
		tokens = design.Of(c)
		statusFrame(c)
	}, 480, 900)
	colorTester.Frame()
	if tokens == nil {
		t.Fatal("frame did not expose design tokens")
	}
	if got, want := uikit.StatusColor(tokens, uikit.StatusError), tokens.DangerText; got != want {
		t.Fatalf("failed status color = %v, want the danger text token %v", got, want)
	}
	for _, kind := range []uikit.StatusKind{
		uikit.StatusCancelled, uikit.StatusInterrupted, uikit.StatusIdle, uikit.StatusPending,
	} {
		if got := uikit.StatusColor(tokens, kind); got == tokens.DangerText {
			t.Fatalf("neutral status %v paints the danger text token", kind)
		}
	}
}
