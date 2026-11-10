package uimodtest

// H-series keyboard chrome E2E: the right sidebar's page shortcuts
// (Cmd/Ctrl+Alt+P opens the 文件 page, Ctrl+` the 终端 page) and the command
// palette's shortcut column for the same commands.
//
// The retired 8-pane inspector picked its panes with a "Switch pane" menu and
// Cmd/Ctrl+Alt+←/→; the DSH-aligned sidebar keeps only the fixed pages (开始,
// 文件, 终端) plus opened-file tabs, so the discoverable keyboard paths are the
// two page commands.

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestInspectorPageShortcuts covers the two page shortcuts and their palette
// entries. With no workspace open the 文件 page shows the open-folder onboarding
// and the 终端 page shows the command runner.
func TestInspectorPageShortcuts(t *testing.T) {
	product := newInteractionProduct(t)
	tester := product.tester

	if err := uiRootPushSession(product.uiRoot); err != nil {
		t.Fatalf("push session: %v", err)
	}
	product.uiRoot.SetRightPanelOpen(false)
	tester.Frame()

	// Cmd/Ctrl+Alt+P opens the sidebar on the 文件 page.
	tester.Key(ui.Cmd|ui.Alt, ui.KeyP)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !(product.uiRoot.RightPanelOpen() && tester.HasText("打开文件夹")) {
		tester.Frame()
		time.Sleep(5 * time.Millisecond)
	}
	if !product.uiRoot.RightPanelOpen() {
		t.Fatalf("Cmd+Alt+P did not open the right sidebar; texts = %v", tester.Texts())
	}
	if !tester.HasText("打开文件夹") || !tester.HasText("还没有打开工作区") {
		t.Fatalf("Cmd+Alt+P did not show the 文件 page; texts = %v", tester.Texts())
	}
	if sidebarGuideVisible(tester) {
		t.Fatalf("the 开始 guide is still showing after the 文件 page shortcut; texts = %v", tester.Texts())
	}

	// Ctrl+` switches the open sidebar to the 终端 page.
	tester.Key(ui.Cmd, ui.KeyBackquote)
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !tester.HasText("运行") {
		tester.Frame()
		time.Sleep(5 * time.Millisecond)
	}
	if !tester.HasText("运行") || !tester.HasText("选择 shell") {
		t.Fatalf("Ctrl+` did not show the 终端 page; texts = %v", tester.Texts())
	}
	if tester.HasText("打开文件夹") {
		t.Fatalf("the 文件 page content is still showing after the 终端 shortcut; texts = %v", tester.Texts())
	}

	// The command palette advertises the same two chords on the page commands.
	tester.Key(ui.Cmd, ui.KeyK)
	if !frameUntil(tester, "命令面板", 2*time.Second) {
		t.Fatalf("command palette did not open; texts = %v", tester.Texts())
	}
	if err := tester.Click("搜索命令"); err != nil {
		t.Fatalf("click palette search: %v; texts = %v", err, tester.Texts())
	}
	tester.Type("文件页")
	tester.Frame()
	tester.Frame()
	if !tester.HasText("Ctrl+Alt+P") {
		t.Fatalf("files page command shortcut missing from the palette; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()

	tester.Key(ui.Cmd, ui.KeyK)
	if !frameUntil(tester, "命令面板", 2*time.Second) {
		t.Fatalf("command palette did not reopen; texts = %v", tester.Texts())
	}
	if err := tester.Click("搜索命令"); err != nil {
		t.Fatalf("click palette search: %v; texts = %v", err, tester.Texts())
	}
	tester.Type("命令页")
	tester.Frame()
	tester.Frame()
	if !tester.HasText("Ctrl+`") {
		t.Fatalf("terminal page command shortcut missing from the palette; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
}
