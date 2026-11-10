package rightpanel

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

const (
	// maxTerminalLines is how many trailing output lines the page keeps.
	maxTerminalLines = 200
	// maxTerminalBytes is how many trailing output bytes the page keeps.
	maxTerminalBytes = 128 << 10
	// terminalCopyNoticeDuration is how long the copy-all action shows its
	// check acknowledgement before returning to the copy glyph.
	terminalCopyNoticeDuration = 1500 * time.Millisecond
)

// terminalState is the Terminal page's state. The Run goroutine appends the
// output and clears the running flag, so output/running/cancel are guarded by
// mu; input and the menu/error fields are only touched on the UI thread.
type terminalState struct {
	mu      sync.Mutex
	output  string
	running bool
	cancel  context.CancelFunc

	input string
	// shell is the choice of the official ui.Select picker; SetShell is called
	// when it changes.
	shell string
	// outputScroll follows the retained output's end until the user scrolls
	// away from it (docs/ui/scroll.md), as a log does.
	outputScroll ui.ScrollState
	err          string
	// copiedAt stamps the last "copy all output" action: the header shows a
	// short check acknowledgement, then asks for the frame that clears it.
	copiedAt time.Time
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
	s.copiedAt = time.Time{}
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

// buildTerminalPage is the sidebar's 终端 page: working directory, shell
// picker, clear/copy actions, the scrollable mono output and the command input
// with run/stop. It never owns a PTY: one command per Run through
// TerminalService.
func buildTerminalPage(
	c *ui.Context,
	t *design.Tokens,
	terminal contracts.TerminalService,
	st *state,
) {
	if terminal == nil {
		buildScrollableBody(c, func() {
			buildEmptyPane(c, st.tr("终端未启用", "Terminal is not enabled"))
		})
		return
	}
	term := st.term
	output, running := term.snapshot()
	hasOutput := strings.TrimSpace(output) != ""
	// The copy-all check is an acknowledgement, not a badge: show it for a
	// short window and ask for the frame that clears it.
	copied := !term.copiedAt.IsZero()
	if copied {
		if remaining := terminalCopyNoticeDuration - c.Now().Sub(term.copiedAt); remaining > 0 {
			c.After(remaining)
		} else {
			term.copiedAt = time.Time{}
			copied = false
		}
	}

	ui.Column(c).FillWidth().Grow(1).Children(func() {
		// Header: working directory, copy/clear actions and the shell choice.
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
			Padding(design.Space2, design.Space3).Children(func() {
			dir := terminal.WorkingDir(context.Background())
			if strings.TrimSpace(dir) == "" {
				dir = "."
			}
			design.Apply(ui.Text(c, dir), t.Font(design.TypeXXXS)).
				TextColor(t.LabelTertiary).Grow(1).MaxLines(1).Ellipsis("…")
			if hasOutput {
				copyLabel := st.tr("复制全部输出", "Copy all output")
				uikit.IconButton(c, uikit.IconButtonOptions{
					Key:      "rightpanel.terminal.copy",
					Label:    copyLabel,
					Tooltip:  copyLabel,
					Icon:     icons.Copy(),
					Size:     design.IconSizeS,
					InkColor: terminalCopyInk(t, copied),
					OnClick: func() {
						c.WriteClipboard(output)
						term.copiedAt = c.Now()
						st.invalidate()
					},
				})
				clearLabel := st.tr("清空", "Clear")
				uikit.IconButton(c, uikit.IconButtonOptions{
					Key:     "rightpanel.terminal.clear",
					Label:   clearLabel,
					Tooltip: clearLabel,
					Icon:    icons.Trash(),
					Size:    design.IconSizeS,
					OnClick: func() {
						term.clearOutput()
						st.invalidate()
					},
				})
			}
			buildShellSelect(c, t, terminal, st)
		})
		if term.err != "" {
			uikit.ErrorLine(c, term.err)
		}
		// Output: one mono text in a native scroll that follows the end as new
		// output comes, until the user scrolls up (docs/ui/scroll.md).
		following := !terminalFollowPaused(output, term.outputScroll)
		if following {
			term.outputScroll.Y = math.MaxFloat32
		}
		ui.Scroll(c).FillWidth().Grow(1).TrackScroll(&term.outputScroll).Children(func() {
			ui.Column(c).FillWidth().Padding(design.Space2, design.Space3).Children(func() {
				if !hasOutput {
					design.Apply(ui.Text(c, st.tr("在工作区根目录运行命令", "Run commands in the workspace root")),
						t.TextMuted(design.TypeXXS))
					return
				}
				design.Apply(ui.Text(c, output), t.MonoText(design.TypeXXXS)).
					TextColor(t.LabelPrimary).Selectable()
			})
		})
		if hasOutput && !following {
			// The scroll no longer follows the end: say so and give the one
			// action that resumes it, instead of silently showing stale rows.
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
				PaddingX(design.Space3).Children(func() {
				design.Apply(ui.Text(c, st.tr("已暂停跟随", "Follow paused")),
					t.TextMuted(design.TypeXXXS)).Grow(1)
				if ui.Button(c, st.tr("回到底部", "Jump to end")).Clicked() {
					term.outputScroll.Y = math.MaxFloat32
					st.invalidate()
				}
			})
		}

		// Input row: native text input submits, Run/Stop swaps while running.
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
			Padding(design.Space1, design.Space3).Children(func() {
			ui.TextInput(c, &term.input).Label(st.tr("命令", "Command")).
				Placeholder(st.tr("输入命令…", "Enter a command…")).
				Grow(1).OnSubmit(func() { runTerminalCommand(terminal, st.runtime, st) })
			if running {
				stop := ui.Button(c, st.tr("停止", "Stop")).TextColor(t.DangerText)
				if stop.Clicked() {
					term.cancelRun()
				}
				return
			}
			run := design.Apply(ui.PrimaryButton(c, st.tr("运行", "Run")), t.PrimaryButton()).
				Disabled(strings.TrimSpace(term.input) == "")
			if run.Clicked() && strings.TrimSpace(term.input) != "" {
				runTerminalCommand(terminal, st.runtime, st)
			}
		})
		design.Apply(ui.Text(c, st.tr("简单命令运行器（无 PTY / 持久 shell）",
			"Simple command runner (no PTY / persistent shell)")),
			t.TextMuted(design.TypeXXXS)).PaddingX(design.Space3).PaddingY(design.Space2)
	})
}

// terminalCopyInk is the copy glyph's ink: success while the acknowledgement
// shows, secondary once the notice window closed.
func terminalCopyInk(t *design.Tokens, copied bool) ui.Color {
	if copied {
		return t.SuccessText
	}
	return t.LabelSecondary
}

// terminalFollowPaused reports whether the output scroll no longer follows the
// end: the terminal keeps following while it is at the end (or empty), and the
// user scrolling up pauses it until the jump-to-end action resumes it.
func terminalFollowPaused(output string, scroll ui.ScrollState) bool {
	return strings.TrimSpace(output) != "" && scroll.Y < scroll.MaxY
}

// buildShellSelect is the official ui.Select picker of TerminalService.Shells
// (docs/ui/select.md): a drop-down whose value is the chosen shell, named for
// assistive technology, with a muted "current" label beside it. A changed
// choice calls SetShell and shows failures in DangerText.
func buildShellSelect(c *ui.Context, t *design.Tokens, terminal contracts.TerminalService,
	st *state) {
	term := st.term
	if term.shell == "" {
		term.shell = terminal.Shell()
	}
	shells := terminal.Shells()
	if len(shells) == 0 {
		// No picker without options: show the shell the service reports.
		design.Apply(ui.Text(c, firstNonEmpty(terminal.Shell(), "shell")), t.Font(design.TypeXXS)).
			TextColor(t.LabelSecondary).NoWrap()
		return
	}
	design.Apply(ui.Text(c, st.tr("当前", "Current")), t.TextMuted(design.TypeXXXS)).NoWrap()
	selector := ui.Select(c, &term.shell, shells).
		Label(st.tr("选择 shell", "Choose shell"))
	if selector.Changed() {
		if err := terminal.SetShell(term.shell); err != nil {
			term.err = st.tr("设置 shell 失败：", "Failed to set the shell: ") + err.Error()
		} else {
			term.err = ""
		}
		st.invalidate()
	}
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
	term.copiedAt = time.Time{}
	ctx, cancel := context.WithCancel(context.Background())
	term.beginRun(cancel)
	term.appendOutput("$ " + command + "\n")
	term.input = ""
	term.err = ""
	if runtime != nil {
		runtime.Invalidate()
	}

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
		if runtime != nil {
			runtime.Post(func() { runtime.Invalidate() })
		}
	}()
}
