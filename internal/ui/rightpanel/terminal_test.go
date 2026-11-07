package rightpanel_test

// Tests for the inspector's Terminal pane: the optional terminal backend, the
// shell picker, asynchronous command runs (output, exit code, errors, stop),
// clear and the disabled/empty states.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/rightpanel"
)

// fakeTerminal is a scripted contracts.TerminalService. Run can block until a
// command is released (or the context is canceled) to exercise the async path.
type fakeTerminal struct {
	mu sync.Mutex

	dir      string
	shell    string
	shells   []string
	setShell []string
	setErr   error

	runCalls []string
	runOut   contracts.TerminalOutput
	runErr   error
	runBlock bool
	started  chan string
	release  chan struct{}
}

func newFakeTerminal() *fakeTerminal {
	return &fakeTerminal{
		dir:     "/work/proj",
		shell:   "bash",
		shells:  []string{"bash", "zsh"},
		started: make(chan string, 8),
		release: make(chan struct{}, 8),
	}
}

func (f *fakeTerminal) WorkingDir(context.Context) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dir
}

func (f *fakeTerminal) Shell() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shell
}

func (f *fakeTerminal) Shells() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.shells...)
}

func (f *fakeTerminal) SetShell(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setShell = append(f.setShell, name)
	if f.setErr != nil {
		return f.setErr
	}
	f.shell = name
	return nil
}

func (f *fakeTerminal) Run(ctx context.Context, command string) (contracts.TerminalOutput, error) {
	f.mu.Lock()
	f.runCalls = append(f.runCalls, command)
	block := f.runBlock
	result, err := f.runOut, f.runErr
	f.mu.Unlock()

	if block {
		select {
		case f.started <- command:
		default:
		}
		select {
		case <-ctx.Done():
			return contracts.TerminalOutput{}, ctx.Err()
		case <-f.release:
		}
	}
	return result, err
}

func (f *fakeTerminal) runs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.runCalls...)
}

func (f *fakeTerminal) shellCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.setShell...)
}

func (f *fakeTerminal) setRunOutput(result contracts.TerminalOutput) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runOut = result
}

func (f *fakeTerminal) setRunErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runErr = err
}

// waitForText frames the tester until want shows up (async command completion
// is delivered through UIRuntime.Post, which a frame drains).
func waitForText(t *testing.T, tester *ui.Tester, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		tester.Frame()
		if tester.HasText(want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q; texts = %v", want, tester.Texts())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitForRun waits until the fake terminal observed a Run call.
func waitForRun(t *testing.T, terminal *fakeTerminal) {
	t.Helper()
	select {
	case <-terminal.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the terminal Run call")
	}
}

// runTerminalCommand focuses the input, types command and presses Run.
func runTerminalCommand(t *testing.T, h *harness, tester *ui.Tester, command string) {
	t.Helper()
	h.click(tester, "命令")
	tester.Type(command)
	tester.Frame()
	h.click(tester, "运行")
}

// --- tests ------------------------------------------------------------------

func TestTerminalPaneWithoutBackend(t *testing.T) {
	h := newHarness(t, newFakeChat(), rightpanel.Config{})
	tester := h.tester()
	h.click(tester, "终端")
	h.assertTexts(tester, "终端未启用")
}

func TestTerminalPaneHeaderShellAndClear(t *testing.T) {
	terminal := newFakeTerminal()
	h := newHarnessWithTerminal(t, newFakeChat(), rightpanel.Config{}, terminal)
	tester := h.tester()
	h.click(tester, "终端")

	h.assertTexts(tester,
		"/work/proj", "bash", "清空", "运行",
		"在工作区根目录运行命令",
		"简单命令运行器（无 PTY / 持久 shell）")

	// The native shell picker lists Shells and calls SetShell.
	h.click(tester, "选择 shell")
	h.assertTexts(tester, "zsh", "当前")
	h.click(tester, "zsh")
	if calls := terminal.shellCalls(); len(calls) != 1 || calls[0] != "zsh" {
		t.Fatalf("SetShell calls = %v, want [zsh]", calls)
	}
	if !tester.HasText("zsh") {
		t.Fatalf("shell label did not update; texts = %v", tester.Texts())
	}

	// A failing SetShell is shown in DangerText.
	terminal.setErr = errors.New("shell not installed")
	h.click(tester, "选择 shell")
	h.click(tester, "bash")
	h.assertTexts(tester, "设置 shell 失败：shell not installed")
}

func TestTerminalRunAsyncAndClear(t *testing.T) {
	terminal := newFakeTerminal()
	terminal.runBlock = true
	terminal.setRunOutput(contracts.TerminalOutput{Output: "hello output\n", ExitCode: 0})
	h := newHarnessWithTerminal(t, newFakeChat(), rightpanel.Config{}, terminal)
	tester := h.tester()
	h.click(tester, "终端")

	runTerminalCommand(t, h, tester, "echo hi")
	waitForRun(t, terminal)
	tester.Frame()
	h.assertTexts(tester, "$ echo hi", "停止")
	if got := terminal.runs(); len(got) != 1 || got[0] != "echo hi" {
		t.Fatalf("Run calls = %v, want [echo hi]", got)
	}

	// Releasing the command posts its output and returns the panel to Run.
	terminal.release <- struct{}{}
	waitForText(t, tester, "hello output")
	tester.Frame()
	h.assertTexts(tester, "运行")
	h.assertNoTexts(tester, "停止")

	// Clear drops the retained output and shows the hint again.
	h.click(tester, "清空")
	h.assertTexts(tester, "在工作区根目录运行命令")
	h.assertNoTexts(tester, "hello output")
}

func TestTerminalNonZeroExit(t *testing.T) {
	terminal := newFakeTerminal()
	terminal.setRunOutput(contracts.TerminalOutput{Output: "boom\n", ExitCode: 3})
	h := newHarnessWithTerminal(t, newFakeChat(), rightpanel.Config{}, terminal)
	tester := h.tester()
	h.click(tester, "终端")

	runTerminalCommand(t, h, tester, "false")
	waitForText(t, tester, "[退出码 3]")
}

func TestTerminalRunError(t *testing.T) {
	terminal := newFakeTerminal()
	terminal.setRunErr(errors.New("spawn failed"))
	h := newHarnessWithTerminal(t, newFakeChat(), rightpanel.Config{}, terminal)
	tester := h.tester()
	h.click(tester, "终端")

	runTerminalCommand(t, h, tester, "boom")
	waitForText(t, tester, "错误：spawn failed")
}

func TestTerminalSubmitRuns(t *testing.T) {
	terminal := newFakeTerminal()
	terminal.setRunOutput(contracts.TerminalOutput{Output: "ok\n"})
	h := newHarnessWithTerminal(t, newFakeChat(), rightpanel.Config{}, terminal)
	tester := h.tester()
	h.click(tester, "终端")

	h.click(tester, "命令")
	tester.Type("echo ok")
	tester.Key(0, ui.KeyEnter)
	waitForText(t, tester, "ok")
	if got := terminal.runs(); len(got) != 1 || got[0] != "echo ok" {
		t.Fatalf("Run calls = %v, want the Enter-submitted command", got)
	}
}

func TestTerminalStopCancelsRun(t *testing.T) {
	terminal := newFakeTerminal()
	terminal.runBlock = true
	h := newHarnessWithTerminal(t, newFakeChat(), rightpanel.Config{}, terminal)
	tester := h.tester()
	h.click(tester, "终端")

	runTerminalCommand(t, h, tester, "sleep 60")
	waitForRun(t, terminal)
	tester.Frame()
	h.assertTexts(tester, "停止")

	h.click(tester, "停止")
	waitForText(t, tester, "[已停止]")
	tester.Frame()
	h.assertTexts(tester, "运行")
}

func TestTerminalCustomTabLabel(t *testing.T) {
	h := newHarness(t, newFakeChat(), rightpanel.Config{TerminalTab: "Terminal"})
	tester := h.tester()
	h.assertTexts(tester, "Terminal")
	h.click(tester, "Terminal")
	h.assertTexts(tester, "终端未启用")
}
