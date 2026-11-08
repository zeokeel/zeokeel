package rightpanel

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

const (
	// maxTerminalLines is how many trailing output lines the pane keeps.
	maxTerminalLines = 200
	// maxTerminalBytes is how many trailing output bytes the pane keeps.
	maxTerminalBytes = 128 << 10
)

// terminalState is the Terminal pane's state. The Run goroutine appends the
// output and clears the running flag, so output/running/cancel are guarded by
// mu; input and the menu/error fields are only touched on the UI thread.
type terminalState struct {
	mu      sync.Mutex
	output  string
	running bool
	cancel  context.CancelFunc

	input         string
	shellMenuOpen bool
	err           string
}

func newTerminalState() *terminalState { return &terminalState{} }

// snapshot returns the retained output and whether a command is running.
func (s *terminalState) snapshot() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.output, s.running
}

// isRunning reports whether a command is in flight.
func (s *terminalState) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// beginRun marks one command as running and stores its cancel func.
func (s *terminalState) beginRun(cancel context.CancelFunc) {
	s.mu.Lock()
	s.running = true
	s.cancel = cancel
	s.mu.Unlock()
}

// finishRun clears the running flag after the command returned.
func (s *terminalState) finishRun() {
	s.mu.Lock()
	s.running = false
	s.cancel = nil
	s.mu.Unlock()
}

// cancelRun cancels the in-flight command (no-op while idle).
func (s *terminalState) cancelRun() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// appendOutput appends one chunk and keeps the trailing window.
func (s *terminalState) appendOutput(chunk string) {
	s.mu.Lock()
	s.output = truncateTerminalOutput(s.output + chunk)
	s.mu.Unlock()
}

// clearOutput empties the retained output.
func (s *terminalState) clearOutput() {
	s.mu.Lock()
	s.output = ""
	s.mu.Unlock()
}

// truncateTerminalOutput keeps the last maxTerminalBytes bytes and the last
// maxTerminalLines lines (rune safe at the byte cut).
func truncateTerminalOutput(output string) string {
	if len(output) > maxTerminalBytes {
		cut := len(output) - maxTerminalBytes
		for cut < len(output) && !utf8.RuneStart(output[cut]) {
			cut++
		}
		output = output[cut:]
	}
	lines := strings.Split(output, "\n")
	if len(lines) > maxTerminalLines {
		output = strings.Join(lines[len(lines)-maxTerminalLines:], "\n")
	}
	return output
}

// buildTerminalPane is the inspector's Terminal tab: working directory, shell
// picker, clear action, the scrollable mono output and the command input with
// run/stop. It never owns a PTY: one command per Run through TerminalService.
func buildTerminalPane(
	c *ui.Context,
	t *design.Tokens,
	terminal contracts.TerminalService,
	runtime contracts.UIRuntime,
	st *state,
) {
	if terminal == nil {
		buildEmptyPane(c, uikit.T(st.locale, "终端未启用", "Terminal is not enabled"))
		return
	}
	term := st.term
	th := t.Theme()
	output, running := term.snapshot()

	ui.Column(c).FillWidth().Grow(1).Children(func() {
		// Header: working directory + shell picker + clear.
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			dir := terminal.WorkingDir(context.Background())
			if strings.TrimSpace(dir) == "" {
				dir = "."
			}
			design.Apply(ui.Text(c, dir), t.Font(design.TypeXXXS)).
				TextColor(t.LabelTertiary).Grow(1).MaxLines(1).Ellipsis("…")

			shellLabel := firstNonEmpty(terminal.Shell(), "shell")
			shell := ui.ButtonBase(c).Height(th.Space(7)).PaddingX(design.Space2).
				Radius(design.RadiusSM).Label(uikit.T(st.locale, "选择 shell", "Choose shell"))
			shell = rowTint(t, shell, false)
			shell.Children(func() {
				ui.Icon(c, icons.Terminal()).Size(design.Space3, design.Space3).
					TextColor(t.LabelSecondary)
				design.Apply(ui.Text(c, shellLabel), t.Font(design.TypeXXS)).
					TextColor(t.LabelSecondary).MaxLines(1).Ellipsis("…")
				ui.Icon(c, icons.ChevronDown()).Size(design.Space3, design.Space3).
					TextColor(t.LabelTertiary)
			})
			shell.Expanded(term.shellMenuOpen)
			shell.OnClick(func() { term.shellMenuOpen = !term.shellMenuOpen })
			ui.Popover(c, shell, &term.shellMenuOpen, func() {
				buildShellPicker(c, t, terminal, runtime, st)
			})

		})
		if term.err != "" {
			uikit.ErrorLine(c, term.err)
		}

		// Output: one mono text in a native scroll, with the empty hint.
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			ui.Column(c).FillWidth().Padding(design.Space2).Children(func() {
				if strings.TrimSpace(output) == "" {
					design.Apply(ui.Text(c, uikit.T(st.locale, "在工作区根目录运行命令", "Run commands in the workspace root")), t.TextMuted(design.TypeXXS))
					return
				}
				design.Apply(ui.Text(c, output), t.MonoFamily()).FontSize(11).
					FixedLineHeight(18).TextColor(t.LabelPrimary)
			})
		})

		// Input row: native text input submits, Run/Stop swaps while running.
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.TextInput(c, &term.input).Label(uikit.T(st.locale, "命令", "Command")).
				Placeholder(uikit.T(st.locale, "输入命令…", "Enter a command…")).
				Grow(1).OnSubmit(func() { runTerminalCommand(terminal, runtime, st) })
			if running {
				stop := ui.Button(c, uikit.T(st.locale, "停止", "Stop")).TextColor(t.DangerText)
				if stop.Clicked() {
					term.cancelRun()
				}
				return
			}
			run := design.Apply(ui.PrimaryButton(c, uikit.T(st.locale, "运行", "Run")), t.PrimaryButton()).
				Disabled(strings.TrimSpace(term.input) == "")
			if run.Clicked() && strings.TrimSpace(term.input) != "" {
				runTerminalCommand(terminal, runtime, st)
			}
		})
		design.Apply(ui.Text(c, uikit.T(st.locale, "简单命令运行器（无 PTY / 持久 shell）", "Simple command runner (no PTY / persistent shell)")), t.TextMuted(design.TypeXXXS))
	})
}

// buildShellPicker is the native popover menu listing TerminalService.Shells;
// choosing one calls SetShell and shows failures in DangerText.
func buildShellPicker(c *ui.Context, t *design.Tokens, terminal contracts.TerminalService, runtime contracts.UIRuntime, st *state) {
	term := st.term
	current := terminal.Shell()
	panel := design.Apply(ui.Column(c), t.Panel()).
		Padding(design.Space1).Gap(design.Space1)
	panel.Children(func() {
		for _, name := range terminal.Shells() {
			name := name
			c.Key("shell/" + name)
			row := ui.ButtonBase(c).FillWidth().Height(28).PaddingX(design.Space2).
				Radius(design.RadiusSM).Label(name)
			row = rowTint(t, row, name == current)
			row.Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					design.Apply(ui.Text(c, name), t.Font(design.TypeXXS)).
						TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
					if name == current {
						design.Apply(ui.Badge(c, uikit.T(st.locale, "当前", "Current")), t.Badge())
					}
				})
			})
			row.OnClick(func() {
				if err := terminal.SetShell(name); err != nil {
					term.err = uikit.T(st.locale, "设置 shell 失败：", "Failed to set the shell: ") + err.Error()
				} else {
					term.err = ""
				}
				term.shellMenuOpen = false
				runtime.Invalidate()
			})
		}
	})
}

// runTerminalCommand starts the trimmed input on a background goroutine and
// returns immediately; the output and exit code are appended when it finishes
// and the frame is invalidated through UIRuntime.Post.
func runTerminalCommand(terminal contracts.TerminalService, runtime contracts.UIRuntime, st *state) {
	term := st.term
	command := strings.TrimSpace(term.input)
	if command == "" || term.isRunning() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	term.beginRun(cancel)
	term.appendOutput("$ " + command + "\n")
	term.input = ""
	term.err = ""
	runtime.Invalidate()

	go func() {
		result, err := terminal.Run(ctx, command)
		stopped := err != nil && ctx.Err() != nil
		cancel()
		var chunk strings.Builder
		chunk.WriteString(result.Output)
		if result.Output != "" && !strings.HasSuffix(result.Output, "\n") {
			chunk.WriteString("\n")
		}
		if result.ExitCode != 0 {
			fmt.Fprintf(&chunk, uikit.T(st.locale, "[退出码 %d]\n", "[exit code %d]\n"), result.ExitCode)
		}
		if err != nil {
			if stopped {
				chunk.WriteString(uikit.T(st.locale, "[已停止]\n", "[stopped]\n"))
			} else {
				chunk.WriteString(uikit.T(st.locale, "错误：", "Error: ") + err.Error() + "\n")
			}
		}
		term.finishRun()
		term.appendOutput(chunk.String())
		runtime.Post(func() { runtime.Invalidate() })
	}()
}
