package host

// Tests for the host-side activity chrome (P2-9): the window title and the
// system tray mirror the live run/approval state, in the product language. The
// title is ZeoKeel when nothing runs and appends "· 运行中 N"/"· 等待审批"
// otherwise; the tray tooltip and menu carry the same line and are localized.

import (
	"testing"

	"github.com/egoist/mygo"
)

// TestActivityStatusText pins the title/tooltip copy: the app name alone when
// idle, then the localized run count and approval state, both together when
// both are present.
func TestActivityStatusText(t *testing.T) {
	cases := []struct {
		name               string
		running, approvals int
		english            bool
		want               string
	}{
		{name: "idle zh", want: "ZeoKeel"},
		{name: "running zh", running: 2, want: "ZeoKeel · 运行中 2"},
		{name: "approval zh", approvals: 1, want: "ZeoKeel · 等待审批"},
		{name: "both zh", running: 3, approvals: 2, want: "ZeoKeel · 运行中 3 · 等待审批"},
		{name: "idle en", english: true, want: "ZeoKeel"},
		{name: "running en", running: 1, english: true, want: "ZeoKeel · Running 1"},
		{name: "approval en", approvals: 2, english: true, want: "ZeoKeel · Waiting for approval"},
		{name: "both en", running: 2, approvals: 1, english: true, want: "ZeoKeel · Running 2 · Waiting for approval"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := activityStatusText("ZeoKeel", tc.running, tc.approvals, tc.english); got != tc.want {
				t.Fatalf("activityStatusText(%d, %d, en=%v) = %q, want %q",
					tc.running, tc.approvals, tc.english, got, tc.want)
			}
		})
	}
}

// menuLabels returns the captured menu's labels.
func menuLabels(menu *mygo.Menu) []string {
	if menu == nil {
		return nil
	}
	items := menu.Items()
	labels := make([]string, len(items))
	for index, item := range items {
		if item.Type == mygo.MenuItemSeparator {
			labels[index] = ""
			continue
		}
		labels[index] = item.Label
	}
	return labels
}

// TestTrayMenuAndTooltipFollowActivityAndLocale covers the tray half: the
// localized labels, the status line (tooltip and a disabled menu line) and the
// live refresh the host calls when runs, approvals or the language change.
func TestTrayMenuAndTooltipFollowActivityAndLocale(t *testing.T) {
	running, approvals, english := 0, 0, false
	var newCalls int
	actions := trayActions{
		NewSession:   func() error { newCalls++; return nil },
		OpenSettings: func() {},
		English:      func() bool { return english },
		Status:       func() (int, int) { return running, approvals },
	}
	window := &fakeTrayWindow{}
	handle := &fakeTrayHandle{}
	factory := &fakeTrayFactory{handle: handle}
	_, refresh, err := setupTrayWithActivity(window, traySettings(t, ""), nil, actions, factory.create, nil)
	if err != nil {
		t.Fatalf("setupTrayWithActivity: %v", err)
	}
	if refresh == nil {
		t.Fatal("setupTrayWithActivity returned no refresh function")
	}

	// Idle: the tooltip stays the app name and no status line is added, exactly
	// as before this change.
	if got := factory.options.ToolTip; got != "ZeoKeel" {
		t.Fatalf("idle tooltip = %q, want ZeoKeel", got)
	}
	want := []string{"显示 ZeoKeel", "新建会话", "设置", "", "退出"}
	if got := menuLabels(factory.options.Menu); !sameLabels(got, want) {
		t.Fatalf("idle tray labels = %v, want %v", got, want)
	}

	// Running + waiting approvals: the tooltip and the menu show the state.
	running, approvals = 2, 1
	refresh()
	if got := handle.toolTip; got != "ZeoKeel · 运行中 2 · 等待审批" {
		t.Fatalf("tooltip = %q, want the activity line", got)
	}
	want = []string{"显示 ZeoKeel", "运行中 2 · 等待审批", "新建会话", "设置", "", "退出"}
	if got := menuLabels(handle.menu); !sameLabels(got, want) {
		t.Fatalf("tray labels = %v, want %v", got, want)
	}
	status := handle.menu.Items()[1]
	if !status.Disabled {
		t.Fatalf("status item %+v is not disabled (it is information, not an action)", status)
	}

	// A language switch re-localizes the tooltip and menu the next frame.
	english = true
	refresh()
	if got := handle.toolTip; got != "ZeoKeel · Running 2 · Waiting for approval" {
		t.Fatalf("localized tooltip = %q, want the English activity line", got)
	}
	want = []string{"Show ZeoKeel", "Running 2 · Waiting for approval", "New session", "Settings", "", "Quit"}
	if got := menuLabels(handle.menu); !sameLabels(got, want) {
		t.Fatalf("localized tray labels = %v, want %v", got, want)
	}

	// The actions still work after the relabeling (the refresh rebuilt the menu).
	items := handle.menu.Items()
	items[2].Click(items[2], nil)
	if newCalls != 1 {
		t.Fatalf("NewSession calls after the refreshed menu = %d, want 1", newCalls)
	}
	if window.shown != 1 || window.focused != 1 {
		t.Fatalf("window show/focus after the action = %d/%d, want 1/1", window.shown, window.focused)
	}
}

// sameLabels reports whether two label slices are equal.
func sameLabels(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

// TestTrayStatusRefreshWithoutStatusProvider covers a host that reports no
// activity: the refresh keeps the app-name tooltip and the plain menu instead
// of printing a stale count.
func TestTrayStatusRefreshWithoutStatusProvider(t *testing.T) {
	actions := trayActions{NewSession: func() error { return nil }, OpenSettings: func() {}}
	window := &fakeTrayWindow{}
	handle := &fakeTrayHandle{}
	factory := &fakeTrayFactory{handle: handle}
	_, refresh, err := setupTrayWithActivity(window, traySettings(t, ""), nil, actions, factory.create, nil)
	if err != nil {
		t.Fatalf("setupTrayWithActivity: %v", err)
	}
	refresh()
	if got := handle.toolTip; got != "ZeoKeel" {
		t.Fatalf("tooltip without a status provider = %q, want ZeoKeel", got)
	}
	if contract := len(handle.menu.Items()); contract != 5 {
		t.Fatalf("tray items without a status provider = %d, want 5", contract)
	}
}
