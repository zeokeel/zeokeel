package rightpanel_test

// Tests for the Terminal pane's shell-depth additions: copying the whole
// retained output and the paused-follow indicator with its jump-to-end action.

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/rightpanel"
)

func TestTerminalCopyAllOutput(t *testing.T) {
	terminal := newFakeTerminal()
	terminal.setRunOutput(contracts.TerminalOutput{Output: "probe terminal output\n", ExitCode: 0})
	h := newHarnessWithTerminal(t, newFakeChat(), rightpanel.Config{}, terminal)
	tester := h.tester()
	h.selectPane(tester, "终端")

	runTerminalCommand(t, h, tester, "echo probe")
	waitForText(t, tester, "probe terminal output")
	if !tester.HasText("复制全部输出") {
		t.Fatalf("terminal pane has no copy-all action; texts = %v", tester.Texts())
	}
	h.click(tester, "复制全部输出")
	if got := tester.Clipboard(); !strings.Contains(got, "probe terminal output") {
		t.Fatalf("clipboard after copy-all = %q, want the terminal output", got)
	}
}
