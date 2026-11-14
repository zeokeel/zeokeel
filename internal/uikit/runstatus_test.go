package uikit_test

// Headless tests for the shared run-status vocabulary (uikit.RunStatus): the
// generating line never fakes a progress value, the queue badge opens the queue
// popover with its cancel/clear/resume actions, a failed delivery retries, a
// failed turn's retry is a labeled button, the usage footer folds, and the stop
// control is the official danger button.

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// runStatusView draws one RunStatus on a page background.
func runStatusView(o uikit.RunStatusOptions) func(*ui.Context) {
	return func(c *ui.Context) {
		t := design.Of(c)
		ui.Column(c).FillWidth().FillHeight().Background(t.BgBase).
			Padding(design.Space4).Children(func() {
			uikit.RunStatus(c, o)
		})
	}
}

// TestRunStatusRunningDoesNotFakeProgress covers the streaming state: the
// official spinner with its localized status copy, marked as a status region,
// and an indeterminate indicator (never a made-up percentage).
func TestRunStatusRunningDoesNotFakeProgress(t *testing.T) {
	tt := ui.NewTester(runStatusView(uikit.RunStatusOptions{
		Running:     true,
		StatusLabel: "生成中",
	}), 480, 200)
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleStatus, "生成中"); !ok {
		t.Fatalf("running status is not a RoleStatus named 生成中; texts = %v", tt.Texts())
	}
	if !tt.HasText("生成中") {
		t.Fatalf("running status copy missing; texts = %v", tt.Texts())
	}
	progress, ok := tt.AccessByRole(ui.RoleProgress, "")
	if !ok {
		t.Fatalf("running status has no spinner; texts = %v", tt.Texts())
	}
	// A spinner announces itself as indeterminate (Now -1); a progress bar with a
	// made-up value would report a number here.
	if progress.Now >= 0 {
		t.Fatalf("run status fakes progress: %+v", progress)
	}
}

// TestRunStatusRetryAndDismiss covers the failure row: the alert copy, the
// labeled retry action (a button with text, not an icon-only control) and the
// optional dismiss action.
func TestRunStatusRetryAndDismiss(t *testing.T) {
	retried, dismissed := 0, 0
	tt := ui.NewTester(runStatusView(uikit.RunStatusOptions{
		Error:     "生成失败：网络错误",
		Retryable: true,
		OnRetry:   func() { retried++ },
		OnDismiss: func() { dismissed++ },
	}), 480, 240)
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleAlert, ""); !ok {
		t.Fatalf("failed run has no alert row; texts = %v", tt.Texts())
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "重试"); !ok {
		t.Fatalf("failed run has no labeled retry button; texts = %v", tt.Texts())
	}
	if err := tt.Click("重试"); err != nil {
		t.Fatalf("click retry: %v", err)
	}
	tt.Frame()
	if retried != 1 {
		t.Fatalf("retry calls = %d, want 1", retried)
	}
	if err := tt.Click("关闭"); err != nil {
		t.Fatalf("click dismiss: %v", err)
	}
	tt.Frame()
	if dismissed != 1 {
		t.Fatalf("dismiss calls = %d, want 1", dismissed)
	}
}

// TestRunStatusQueuePopover covers the queue: the count badge is a button, the
// popover lists the queued prompts and offers cancel, clear and (after a paused
// stop) resume actions.
func TestRunStatusQueuePopover(t *testing.T) {
	queueOpen := false
	var queued = []string{"第一条排队消息", "第二条排队消息"}
	var cancels []int
	clears, resumes := 0, 0
	tt := ui.NewTester(runStatusView(uikit.RunStatusOptions{
		Queued:      queued,
		QueueOpen:   &queueOpen,
		QueueList:   &ui.ListState{},
		QueuePaused: true,
		OnQueueCancel: func(index int) {
			cancels = append(cancels, index)
		},
		OnQueueClear:  func() { clears++ },
		OnQueueResume: func() { resumes++ },
	}), 480, 320)
	tt.Frame()
	if !tt.HasText("已排队 2 条") {
		t.Fatalf("queue badge missing; texts = %v", tt.Texts())
	}
	if tt.HasText("第一条排队消息") {
		t.Fatalf("queue rows leak before the badge opens; texts = %v", tt.Texts())
	}
	if err := tt.Click("已排队 2 条"); err != nil {
		t.Fatalf("click queue badge: %v", err)
	}
	tt.Frame()
	if !queueOpen {
		t.Fatal("clicking the queue badge did not open the popover")
	}
	if _, ok := tt.AccessByRole(ui.RoleList, "排队消息"); !ok {
		t.Fatalf("queue popover has no named list; texts = %v", tt.Texts())
	}
	if !tt.HasText("第一条排队消息") || !tt.HasText("第二条排队消息") {
		t.Fatalf("queue rows missing; texts = %v", tt.Texts())
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, "取消排队"); !ok {
		t.Fatalf("queue rows have no cancel control; texts = %v", tt.Texts())
	}
	if err := tt.Click("取消排队"); err != nil {
		t.Fatalf("cancel queued prompt: %v", err)
	}
	tt.Frame()
	if len(cancels) != 1 || cancels[0] != 0 {
		t.Fatalf("cancel calls = %v, want [0]", cancels)
	}
	if err := tt.Click("清空全部"); err != nil {
		t.Fatalf("clear queue: %v", err)
	}
	tt.Frame()
	if clears != 1 {
		t.Fatalf("clear calls = %d, want 1", clears)
	}
	if err := tt.Click("发送排队消息"); err != nil {
		t.Fatalf("resume queue: %v", err)
	}
	tt.Frame()
	if resumes != 1 {
		t.Fatalf("resume calls = %d, want 1", resumes)
	}
}

// TestRunStatusQueueRetry covers a failed queued delivery: the error row plus the
// explicit retry (idle only, as the delivery waits for the turn to end).
func TestRunStatusQueueRetry(t *testing.T) {
	retried := 0
	tt := ui.NewTester(runStatusView(uikit.RunStatusOptions{
		QueueError:   "排队发送失败：超时",
		OnRetryQueue: func() { retried++ },
	}), 480, 200)
	tt.Frame()
	if !tt.HasText("排队发送失败：超时") {
		t.Fatalf("queue error row missing; texts = %v", tt.Texts())
	}
	if err := tt.Click("重试"); err != nil {
		t.Fatalf("retry queued delivery: %v", err)
	}
	tt.Frame()
	if retried != 1 {
		t.Fatalf("queue retry calls = %d, want 1", retried)
	}
}

// TestRunStatusUsageCollapsed covers the usage footer: the compact summary line
// folds the label/value rows; opening it reports the state and asks the
// transcript for HoldPlace so the footer does not jump the reading position.
func TestRunStatusUsageCollapsed(t *testing.T) {
	open := false
	holdPlace := false
	var toggles []bool
	summary := "用时 2.5s · 68 tokens"
	tt := ui.NewTester(runStatusView(uikit.RunStatusOptions{
		Usage: &uikit.Usage{
			Summary: summary,
			Rows: []uikit.UsageRow{
				{Label: "输出 tokens", Value: "42", Exact: "42"},
				{Label: "上下文预算", Value: "128k"},
			},
			Open: &open,
		},
		OnToggle:  func(next bool) { toggles = append(toggles, next) },
		HoldPlace: &holdPlace,
	}), 480, 240)
	tt.Frame()
	if !tt.HasText(summary) {
		t.Fatalf("usage summary missing; texts = %v", tt.Texts())
	}
	if tt.HasText("输出 tokens") || tt.HasText("上下文预算") {
		t.Fatalf("collapsed usage footer leaks its rows; texts = %v", tt.Texts())
	}
	if err := tt.Click(summary); err != nil {
		t.Fatalf("open usage footer: %v", err)
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if !open {
		t.Fatal("clicking the usage footer did not open it")
	}
	if !tt.HasText("输出 tokens") || !tt.HasText("上下文预算") {
		t.Fatalf("opened usage footer does not show its rows; texts = %v", tt.Texts())
	}
	if len(toggles) != 1 || !toggles[0] {
		t.Fatalf("usage toggles = %v, want [true]", toggles)
	}
	if !holdPlace {
		t.Fatal("usage toggle did not ask the transcript for HoldPlace")
	}
}

// TestRunStatusUsageSummaryLine covers the row-less usage form (the session
// statistics strip): one quiet line, no disclosure.
func TestRunStatusUsageSummaryLine(t *testing.T) {
	line := "3 轮 · 1 步 · 68 tok"
	tt := ui.NewTester(runStatusView(uikit.RunStatusOptions{
		Usage: &uikit.Usage{Summary: line, Exact: "exact numbers"},
	}), 480, 200)
	tt.Frame()
	if !tt.HasText(line) {
		t.Fatalf("usage summary line missing; texts = %v", tt.Texts())
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, ""); ok {
		t.Fatalf("a row-less usage summary rendered a disclosure control; texts = %v", tt.Texts())
	}
}

// TestRunStatusStop covers the official stop control: a labeled danger button
// that interrupts the turn once.
func TestRunStatusStop(t *testing.T) {
	stopped := 0
	tt := ui.NewTester(runStatusView(uikit.RunStatusOptions{
		Running: true,
		OnStop:  func() { stopped++ },
	}), 480, 200)
	tt.Frame()
	stop, ok := tt.AccessByRole(ui.RoleButton, "停止")
	if !ok {
		t.Fatalf("running status has no stop control; texts = %v", tt.Texts())
	}
	_ = stop
	if err := tt.Click("停止"); err != nil {
		t.Fatalf("click stop: %v", err)
	}
	tt.Frame()
	if stopped != 1 {
		t.Fatalf("stop calls = %d, want 1", stopped)
	}
}

// TestRunStatusStatusRowsHaveDistinctKeys: the generating row carries a stable
// Key (the v0.9.0 RoleStatus is read whole again when its text changes, and the
// key keeps that status on one element across frames). The key includes the
// caller's KeyPrefix, so several run-status areas in one scope (a message
// footer's per message) do not collide; the Tester builds in strict mode and
// panics on a duplicate key.
func TestRunStatusStatusRowsHaveDistinctKeys(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		tokens := design.Of(c)
		ui.Column(c).FillWidth().FillHeight().Background(tokens.BgBase).Children(func() {
			uikit.RunStatus(c, uikit.RunStatusOptions{Running: true, KeyPrefix: "message-a", StatusLabel: "甲"})
			uikit.RunStatus(c, uikit.RunStatusOptions{Running: true, KeyPrefix: "message-b", StatusLabel: "乙"})
		})
	}, 480, 200)
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleStatus, "甲"); !ok {
		t.Fatalf("first status row missing; texts = %v", tt.Texts())
	}
	if _, ok := tt.AccessByRole(ui.RoleStatus, "乙"); !ok {
		t.Fatalf("second status row missing; texts = %v", tt.Texts())
	}
	// The status text change keeps one element per area: the same two statuses
	// are still reachable after a rebuild.
	tt.Frame()
	if _, ok := tt.AccessByRole(ui.RoleStatus, "乙"); !ok {
		t.Fatalf("status row lost after a rebuild; texts = %v", tt.Texts())
	}
}
