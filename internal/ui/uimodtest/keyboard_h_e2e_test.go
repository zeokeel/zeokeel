package uimodtest

// H-series keyboard chrome E2E: the inspector pane shortcuts (Cmd/Ctrl+Alt+←/→)
// and the command palette's shortcut column.

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// paneOf reports the inspector pane selector's current label, i.e. the pane
// whose content is shown.
func paneOf(tester *ui.Tester) string {
	for _, pane := range []string{"概览", "工具", "日志", "文件", "变更", "终端", "团队", "任务"} {
		if tester.HasText(pane) {
			return pane
		}
	}
	return ""
}

// TestInspectorPaneShortcuts covers Cmd/Ctrl+Alt+←/→ switching the inspector
// pane and the pane-switch commands advertised in the command palette.
func TestInspectorPaneShortcuts(t *testing.T) {
	product := newInteractionProduct(t)
	tester := product.tester

	if err := uiRootPushSession(product.uiRoot); err != nil {
		t.Fatalf("push session: %v", err)
	}
	product.uiRoot.SetRightPanelOpen(true)
	tester.Frame()
	tester.Frame()
	if !product.uiRoot.RightPanelOpen() {
		t.Fatalf("right inspector did not open")
	}
	if got := paneOf(tester); got != "概览" {
		t.Fatalf("inspector opens on pane %q, want 概览; texts = %v", got, tester.Texts())
	}
	// Next pane: the selector names the new pane (工具).
	tester.Key(ui.Cmd|ui.Alt, ui.KeyRight)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && paneOf(tester) != "工具" {
		tester.Frame()
		time.Sleep(5 * time.Millisecond)
	}
	if got := paneOf(tester); got != "工具" {
		t.Fatalf("Cmd+Alt+→ did not switch to the next pane (got %q); texts = %v", got, tester.Texts())
	}
	// Previous pane: back to 概览.
	tester.Key(ui.Cmd|ui.Alt, ui.KeyLeft)
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && paneOf(tester) != "概览" {
		tester.Frame()
		time.Sleep(5 * time.Millisecond)
	}
	if got := paneOf(tester); got != "概览" {
		t.Fatalf("Cmd+Alt+← did not switch back to the previous pane (got %q)", got)
	}

	// The command palette advertises the same chords on the pane commands.
	tester.Key(ui.Cmd, ui.KeyK)
	if !frameUntil(tester, "命令面板", 2*time.Second) {
		t.Fatalf("command palette did not open")
	}
	if err := tester.Click("搜索命令"); err != nil {
		t.Fatalf("click palette search: %v", err)
	}
	tester.Type("检查器面板")
	tester.Frame()
	tester.Frame()
	for _, want := range []string{"Cmd/Ctrl + Alt + ←", "Cmd/Ctrl + Alt + →"} {
		if !tester.HasText(want) {
			t.Fatalf("pane command shortcut %q missing from the palette; texts = %v", want, tester.Texts())
		}
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
}
