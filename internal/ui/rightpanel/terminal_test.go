package rightpanel_test

// Frame tests for the right sidebar's 命令 page: the reused command runner (no
// PTY) now rendered inside its tab, with output, stop, clear and the shell
// picker.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func terminalHarness(t *testing.T, terminal contracts.TerminalService) *harness {
	t.Helper()
	return newHarness(t, harnessOptions{Terminal: terminal})
}

func TestTerminalPageWithoutBackend(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tester := h.tester()
	h.openPage(tester, "命令")
	h.assertTexts(tester, "命令未启用")
}

func TestTerminalPageRunsCommand(t *testing.T) {
	terminal := newFakeTerminal()
	terminal.setRunOutput(contracts.TerminalOutput{Output: "probe terminal output\n", ExitCode: 0})
	h := terminalHarness(t, terminal)
	tester := h.tester()
	h.openPage(tester, "命令")
	h.assertTexts(tester, "运行")

	// Type a command into the native input and press Enter.
	input, ok := tester.Find("命令输入")
	if !ok {
		t.Fatalf("terminal command input not found; texts = %v", tester.Texts())
	}
	tester.ClickAt(input.X+input.W/2, input.Y+input.H/2)
	tester.Type("echo probe")
	tester.Key(0, ui.KeyEnter)
	if !pumpFrames(tester, func() bool { return tester.HasText("probe terminal output") }, 3*time.Second) {
		t.Fatalf("terminal output did not appear; texts = %v", tester.Texts())
	}
	if runs := terminal.runs(); len(runs) != 1 || runs[0] != "echo probe" {
		t.Fatalf("terminal runs = %v, want [echo probe]", runs)
	}

	// Copy-all appears with the output and writes it to the clipboard.
	if !pumpFrames(tester, func() bool { _, ok := tester.Find("复制全部输出"); return ok }, 3*time.Second) {
		t.Fatalf("terminal page has no copy-all action after output; texts = %v", tester.Texts())
	}
	h.click(tester, "复制全部输出")
	if got := tester.Clipboard(); !strings.Contains(got, "probe terminal output") {
		t.Fatalf("clipboard after copy-all = %q, want the terminal output", got)
	}
}

func TestTerminalPageClear(t *testing.T) {
	terminal := newFakeTerminal()
	terminal.setRunOutput(contracts.TerminalOutput{Output: "to be cleared\n"})
	h := terminalHarness(t, terminal)
	tester := h.tester()
	h.openPage(tester, "命令")
	input, _ := tester.Find("命令输入")
	tester.ClickAt(input.X+input.W/2, input.Y+input.H/2)
	tester.Type("echo clear")
	tester.Key(0, ui.KeyEnter)
	if !pumpFrames(tester, func() bool { return tester.HasText("to be cleared") }, 3*time.Second) {
		t.Fatalf("terminal output did not appear; texts = %v", tester.Texts())
	}
	h.click(tester, "清空")
	h.assertNoTexts(tester, "to be cleared")
}

func TestTerminalPageStopRunningCommand(t *testing.T) {
	terminal := newFakeTerminal()
	terminal.setRunBlock(true)
	h := terminalHarness(t, terminal)
	tester := h.tester()
	h.openPage(tester, "命令")
	input, _ := tester.Find("命令输入")
	tester.ClickAt(input.X+input.W/2, input.Y+input.H/2)
	tester.Type("sleep 100")
	tester.Key(0, ui.KeyEnter)
	if !pumpFrames(tester, func() bool {
		_, ok := tester.Find("停止")
		return ok
	}, 3*time.Second) {
		t.Fatalf("running terminal has no stop button; texts = %v", tester.Texts())
	}
	h.click(tester, "停止")
	if !pumpFrames(tester, func() bool { return tester.HasText("[已停止]") }, 3*time.Second) {
		t.Fatalf("stopped command was not reported; texts = %v", tester.Texts())
	}
}

func TestTerminalPageShellSelect(t *testing.T) {
	terminal := newFakeTerminal()
	h := terminalHarness(t, terminal)
	tester := h.tester()
	h.openPage(tester, "命令")
	selector, ok := tester.Find("bash")
	if !ok {
		t.Fatalf("shell select not found; texts = %v", tester.Texts())
	}
	tester.ClickAt(selector.X+selector.W/2, selector.Y+selector.H/2)
	tester.Frame()
	if !hasTextPrefix(tester.Texts(), "zsh") {
		t.Fatalf("shell menu does not list zsh; texts = %v", tester.Texts())
	}
	if err := tester.Click("zsh"); err != nil {
		t.Fatalf("choose shell: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if calls := terminal.shellCalls(); len(calls) != 1 || calls[0] != "zsh" {
		t.Fatalf("SetShell calls = %v, want [zsh]", calls)
	}
}

func TestTerminalPageRunErrors(t *testing.T) {
	terminal := newFakeTerminal()
	terminal.setRunErr(errors.New("exec: command not found"))
	h := terminalHarness(t, terminal)
	tester := h.tester()
	h.openPage(tester, "命令")
	input, _ := tester.Find("命令输入")
	tester.ClickAt(input.X+input.W/2, input.Y+input.H/2)
	tester.Type("nope")
	tester.Key(0, ui.KeyEnter)
	if !pumpFrames(tester, func() bool { return tester.HasText("错误：exec: command not found") }, 3*time.Second) {
		t.Fatalf("terminal run error not reported; texts = %v", tester.Texts())
	}
}

// hasTextPrefix reports whether any painted text starts with prefix (menu
// items may be painted with their state suffix).
func hasTextPrefix(texts []string, prefix string) bool {
	for _, text := range texts {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// TestCommandPageCopyIsUserFacing pins B18: the page is named 命令, the working
// directory is labelled as such (with the full path in its tooltip), the shell
// picker has a visible Shell label, and the explanation states the user benefit
// instead of the implementation limits (no PTY / persistent shell).
func TestCommandPageCopyIsUserFacing(t *testing.T) {
	terminal := newFakeTerminal()
	h := terminalHarness(t, terminal)
	tester := h.tester()
	h.openPage(tester, "命令")

	h.assertTexts(tester, "工作目录", "/work/proj", "Shell",
		"一次性命令 · 在工作目录执行 · 输出可复制")
	h.assertNoTexts(tester, "简单命令运行器（无 PTY / 持久 shell）", "当前")
}
