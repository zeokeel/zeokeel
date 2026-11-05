package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/store"
)

// sessionLogTTL bounds how often the Session Log re-reads the event table.
const sessionLogTTL = 500 * time.Millisecond

// settingTerminalShell is the store key the Choose shell control writes. It
// is a plain string setting (shell name, e.g. "zsh"), so it survives a
// restart without a schema change.
const settingTerminalShell = "terminal.shell"

// startFolderTint is the Start card's folder glyph color from the DSH
// reference design (retired 2026-11-05). It is a tint of
// its own, not the warning status color.
var startFolderTint = ui.Hex("#FFBC4D")

// startTabState is the per-App state of the Start page and the shell
// picker. App lives in app.go, which this change does not own, so the state
// is keyed by the App like shellUIState and liveMenuState. Only the view
// goroutine reads it, and the static App{Ctrl: nil} path never does.
type startTabState struct {
	// shellMenuOpen is shared by the Start card's chevron and the Terminal
	// tab's Choose shell control.
	shellMenuOpen bool
	// shellLoaded/shellName cache the persisted terminal shell.
	shellLoaded bool
	shellName   string
	// lastComposerContext is the frame the slash palette was last built in.
	// runSlashCommand (called from session_live.go without a context) uses it
	// for the toast some commands show.
	lastComposerContext *ui.Context

	// screen/screenSet make Start the default page when the panel starts
	// showing on another screen (Home <-> Session).
	screen    Screen
	screenSet bool

	// Session Log cache: the event list is store-backed and this panel is
	// redrawn on every frame, so re-read at most every logTTL.
	logSession string
	logAt      time.Time
	logEvents  []store.Event
	logErr     error
	logLoaded  bool
}

var startTabStates sync.Map // map[*App]*startTabState

// startUI returns the Start/Terminal state of a, making it on first use.
func (a *App) startUI() *startTabState {
	if s, ok := startTabStates.Load(a); ok {
		return s.(*startTabState)
	}
	actual, _ := startTabStates.LoadOrStore(a, &startTabState{})
	return actual.(*startTabState)
}

// noteRightScreen keeps Start as the default page when the right panel starts
// showing on a different screen. DSH keeps a tab layout per session; this
// build has one panel, so the per-screen page is its closest equivalent.
// Settings is an overlay: it keeps the screen it was opened from as the
// background, so opening or closing the modal must not reset the panel page.
func (a *App) noteRightScreen() {
	st := a.startUI()
	screen := a.backgroundScreen()
	if !st.screenSet {
		st.screenSet = true
		st.screen = screen
		return
	}
	if st.screen != screen {
		st.screen = screen
		a.rightTab = startTab
	}
}

// startTabPanel is DSH's Start page: the three guide cards a fresh right
// panel shows before the user picks a tab. Each card performs the action its
// label promises; the terminal card also exposes Choose shell, as DSH does
// beside "New terminal".
func (a *App) startTabPanel(c *ui.Context, t *design.Tokens) {
	ui.Column(c).Grow(1).Padding(design.Space6, design.Space5).
		Justify(ui.Center).Gap(design.Space3).Children(func() {
		ui.Row(c).Justify(ui.Center).Padding(0, 0, design.Space2, 0).Children(func() {
			ui.Row(c).Size(44, 44).Radius(design.RadiusMax).Background(t.BgHover).
				Center().Children(func() {
				ui.Icon(c, icons.Sparkles()).FontSize(22).TextColor(t.LabelTertiary)
			})
		})
		a.startCard(c, t, icons.Folder(), startFolderTint,
			a.T("files.workspace_files"),
			a.tr("Browse files in this session's workspace", "浏览此会话工作区的文件"),
			func() { a.rightTab = filesTab })
		a.startTerminalCard(c, t)
		a.startCard(c, t, icons.SessionLog(), t.LabelTertiary,
			a.tr("Session Log", "会话日志"),
			a.tr("Read this session's raw event log (read-only)",
				"查看当前会话的原始事件日志（只读）"),
			func() { a.rightTab = sessionLogTab })
	})
}

// startCard is one guide card: a glyph, a title and a description. The whole
// card is clickable, so the small description is not a dead target.
func (a *App) startCard(c *ui.Context, t *design.Tokens, ic *ui.SVG, tint ui.Color,
	title, description string, onClick func()) {
	card := ui.Row(c).MinHeight(76).Padding(design.Space4, design.Space4).
		Gap(design.Space3).Radius(design.RadiusMD).Border(1, t.BorderL2).
		AlignItems(ui.Center).Role(ui.RoleButton).Focusable().Label(title)
	if card.Hovered() {
		card = card.Background(t.BgHover)
	}
	card.Children(func() {
		ui.Row(c).Size(28, 28).Center().Children(func() {
			ui.Icon(c, ic).FontSize(20).TextColor(tint)
		})
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(14).FixedLineHeight(20).FontWeight(600).
				TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
			ui.Text(c, description).FontSize(12).FixedLineHeight(17).
				TextColor(t.LabelTertiary).MaxLines(2)
		})
	})
	card.OnClick(onClick)
}

// startTerminalCard is the New terminal guide card. Its chevron is DSH's
// Choose shell menu; the card body opens the Terminal tab with the
// remembered shell.
func (a *App) startTerminalCard(c *ui.Context, t *design.Tokens) {
	st := a.startUI()
	card := ui.Row(c).MinHeight(76).Padding(design.Space4, design.Space4).
		Gap(design.Space3).Radius(design.RadiusMD).Border(1, t.BorderL2).
		AlignItems(ui.Center).Role(ui.RoleButton).Focusable().
		Label(a.T("files.new_terminal"))
	if card.Hovered() {
		card = card.Background(t.BgHover)
	}
	card.Children(func() {
		ui.Row(c).Size(28, 28).Center().Children(func() {
			ui.Icon(c, icons.Terminal()).FontSize(20).TextColor(t.Accent)
		})
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Row(c).Gap(design.Space1).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.T("files.new_terminal")).FontSize(14).FixedLineHeight(20).
					FontWeight(600).TextColor(t.LabelPrimary)
				// The chevron is its own menu-button control (not just a
				// drawing inside the card's button), so Choose shell is
				// reachable from the keyboard without activating the card.
				chevron := ui.Row(c).Size(18, 18).Center().Radius(design.RadiusXS).
					Label(a.tr("Choose shell", "选择 shell")).
					Role(ui.RoleMenuButton).Focusable()
				if chevron.Hovered() {
					chevron = chevron.Background(t.BgHover)
				}
				chevron.Children(func() {
					ui.Icon(c, icons.ChevronDown()).FontSize(13).TextColor(t.LabelTertiary)
				})
				chevron.Expanded(st.shellMenuOpen)
				chevron.OnClick(func() { st.shellMenuOpen = !st.shellMenuOpen })
				ui.Popover(c, chevron, &st.shellMenuOpen, func() { a.shellPickerMenu(c, t) })
			})
			ui.Text(c, a.tr("Run commands in the Session workspace",
				"在会话工作区运行命令")).FontSize(12).FixedLineHeight(17).
				TextColor(t.LabelTertiary).MaxLines(2)
		})
	})
	card.OnClick(func() { a.rightTab = terminalTab })
}

// terminalShell is one selectable command interpreter.
type terminalShell struct {
	name  string // persisted value
	label string // menu label
	path  string // executable, or "" for the platform default
}

var (
	shellsOnce  sync.Once
	shellsCache []terminalShell
)

// terminalShells lists the small set of shells this build offers: the
// platform default plus the common installed interpreters. Ordered by
// usefulness on the host platform. Detection runs once: PATH lookups on
// every frame would be wasted work.
func terminalShells() []terminalShell {
	shellsOnce.Do(func() { shellsCache = detectTerminalShells() })
	return shellsCache
}

// detectTerminalShells probes the platform's shells.
func detectTerminalShells() []terminalShell {
	if runtime.GOOS == "windows" {
		all := []terminalShell{
			{name: "cmd", label: "Command Prompt", path: "cmd"},
			{name: "powershell", label: "Windows PowerShell", path: "powershell"},
			{name: "pwsh", label: "PowerShell 7", path: "pwsh"},
			{name: "git-bash", label: "Git Bash", path: "bash"},
		}
		return existingShells(all)
	}
	return existingShells([]terminalShell{
		{name: "sh", label: "sh", path: "/bin/sh"},
		{name: "bash", label: "Bash", path: "/bin/bash"},
		{name: "zsh", label: "Zsh", path: "/bin/zsh"},
		{name: "fish", label: "Fish", path: "/usr/bin/fish"},
	})
}

// existingShells keeps the candidates whose executable is present, so the
// menu never offers a shell that cannot start.
func existingShells(all []terminalShell) []terminalShell {
	out := make([]terminalShell, 0, len(all))
	for _, sh := range all {
		if sh.path == "" {
			out = append(out, sh)
			continue
		}
		if _, err := exec.LookPath(sh.path); err == nil {
			out = append(out, sh)
		}
	}
	if len(out) == 0 {
		out = append(out, terminalShell{name: defaultShellName(), label: defaultShellLabel(), path: ""})
	}
	return out
}

// defaultShellName is the shell a fresh install uses.
func defaultShellName() string {
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return "sh"
}

// defaultShellLabel is the human label of the platform default.
func defaultShellLabel() string {
	if runtime.GOOS == "windows" {
		return "Command Prompt"
	}
	return "sh"
}

// findTerminalShell resolves a persisted name to a candidate.
func findTerminalShell(name string) (terminalShell, bool) {
	for _, sh := range terminalShells() {
		if sh.name == name {
			return sh, true
		}
	}
	return terminalShell{}, false
}

// terminalShellName returns the selected shell, reading the persisted value
// once and caching it. An unknown persisted name falls back to the platform
// default instead of offering a shell the menu cannot show.
func (a *App) terminalShellName() string {
	st := a.startUI()
	if st.shellLoaded && st.shellName != "" {
		return st.shellName
	}
	st.shellLoaded = true
	st.shellName = defaultShellName()
	if a.Ctrl != nil && a.Ctrl.Store() != nil {
		if v, err := a.Ctrl.Store().GetSetting(settingTerminalShell); err == nil && v != "" {
			if _, ok := findTerminalShell(v); ok {
				st.shellName = v
			}
		}
	}
	return st.shellName
}

// terminalShellLabel is the short label shown in the Terminal header.
func (a *App) terminalShellLabel() string {
	name := a.terminalShellName()
	if sh, ok := findTerminalShell(name); ok {
		return sh.label
	}
	return name
}

// setTerminalShell selects and persists a shell. Persistence failure still
// applies the choice for this session and reports through a toast, so a
// read-only store cannot strand the user on a shell that will not start.
func (a *App) setTerminalShell(c *ui.Context, name string) {
	sh, ok := findTerminalShell(name)
	if !ok {
		return
	}
	st := a.startUI()
	st.shellLoaded = true
	st.shellName = sh.name
	if a.Ctrl != nil && a.Ctrl.Store() != nil {
		if err := a.Ctrl.Store().SetSetting(settingTerminalShell, sh.name); err != nil {
			c.Toast(a.tr("Shell selected for this session; the choice could not be saved",
				"已为本次会话选择 shell，但选择无法保存"))
			return
		}
	}
	c.Toast(a.tr("Shell: ", "Shell：") + sh.label)
}

// shellPickerMenu is the Choose shell menu: the installed shells with the
// current one marked. Choosing one persists immediately.
func (a *App) shellPickerMenu(c *ui.Context, t *design.Tokens) {
	st := a.startUI()
	ui.Column(c).Width(230).Gap(1).Children(func() {
		ui.Text(c, a.tr("Choose shell", "选择 shell")).FontSize(11).
			FixedLineHeight(16).TextColor(t.LabelTertiary).
			Padding(design.Space1, design.Space2)
		current := a.terminalShellName()
		for _, sh := range terminalShells() {
			sh := sh
			label := a.shellMenuLabel(sh)
			row := ui.Row(c).Height(32).PaddingX(design.Space2).Gap(design.Space2).
				Radius(design.RadiusSM).AlignItems(ui.Center).
				Label(label).Role(ui.RoleMenuItemRadio).
				Checked(sh.name == current).Focusable()
			if row.Hovered() || sh.name == current {
				row = row.Background(t.BgHover)
			}
			row.Children(func() {
				ui.Column(c).Grow(1).Gap(0).Children(func() {
					ui.Text(c, label).FontSize(13).
						FixedLineHeight(18).TextColor(t.LabelPrimary)
					ui.Text(c, sh.path).FontSize(10).FixedLineHeight(14).
						TextColor(t.LabelTertiary)
				})
				if sh.name == current {
					ui.Text(c, "✓").FontSize(12).TextColor(t.Accent)
				}
			})
			row.OnClick(func() {
				st.shellMenuOpen = false
				a.setTerminalShell(c, sh.name)
			})
		}
	})
}

// shellMenuLabel is "Bash · /bin/bash" on Unix and "Git Bash · bash" on
// Windows; the executable path is shown in a second line anyway, so the
// label stays just the shell name. It is exported for tests through the
// trailing path line.
func (a *App) shellMenuLabel(sh terminalShell) string { return sh.label }

// reloadFiles is the Files tab's Reload control: it re-reads the current
// directory (which the panel does on every frame) or re-opens the previewed
// file from disk. Clearing fileErr makes a transient read failure retry.
func (a *App) reloadFiles(c *ui.Context) {
	if a.filePath != "" {
		rel := a.filePath
		a.openFilePreview(rel)
		c.Toast(a.tr("Reloaded the file from disk", "已从磁盘重新加载文件"))
		return
	}
	a.fileErr = ""
	c.Toast(a.tr("Reloaded the directory", "已重新加载目录"))
}

// sessionLogPanel is the read-only Session Log tab. It renders the persisted
// event trace the controller exposes (store-backed), so a session's raw
// events stay inspectable without the experimental inspector.
func (a *App) sessionLogPanel(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Height(34).PaddingX(design.Space3).Gap(design.Space2).
		AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2).
		Children(func() {
			ui.Icon(c, icons.SessionLog()).FontSize(15).TextColor(t.LabelSecondary)
			ui.Text(c, a.tr("Session Log", "会话日志")).FontSize(13).
				FixedLineHeight(20).FontWeight(600).TextColor(t.LabelPrimary).Grow(1)
			ui.Text(c, a.tr("Read-only", "只读")).FontSize(11).FixedLineHeight(16).
				TextColor(t.LabelTertiary)
		})
	if a.Ctrl == nil {
		a.rightEmpty(c, t, a.tr("The Session Log needs the live session service",
			"会话日志需要实时会话服务"))
		return
	}
	sessionID := a.Ctrl.CurrentSessionID()
	if snap := a.currentSnapshot(); snap != nil && snap.SessionID != "" {
		sessionID = snap.SessionID
	}
	if sessionID == "" {
		a.rightEmpty(c, t, a.tr("No session selected", "未选择会话"))
		return
	}
	st := a.startUI()
	if !st.logLoaded || st.logSession != sessionID || time.Since(st.logAt) > sessionLogTTL {
		events, err := a.Ctrl.Events(context.Background(), sessionID)
		st.logLoaded = true
		st.logSession = sessionID
		st.logAt = time.Now()
		st.logEvents = events
		st.logErr = err
	}
	if st.logErr != nil {
		a.rightEmpty(c, t, fmt.Sprintf(a.tr("Could not read the event log: %v",
			"无法读取事件日志：%v"), st.logErr))
		return
	}
	events := st.logEvents
	if len(events) == 0 {
		a.rightEmpty(c, t, a.tr("No events recorded yet", "暂无事件记录"))
		return
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(design.Space1, design.Space2, design.Space2, design.Space2).
			Gap(1).Children(func() {
			for _, event := range events {
				a.sessionLogRow(c, t, event)
			}
		})
	})
}

// sessionLogRow is one event: sequence, local time, type and a bounded
// payload preview. The log is read-only, so the row has no handler.
func (a *App) sessionLogRow(c *ui.Context, t *design.Tokens, event store.Event) {
	ui.Row(c).MinHeight(30).Padding(design.Space1, design.Space2).
		Gap(design.Space2).Radius(design.RadiusXS).AlignItems(ui.Start).
		Children(func() {
			ui.Text(c, fmt.Sprintf("#%d", event.Seq)).FontSize(10).
				FixedLineHeight(15).TextColor(t.LabelTertiary).Width(34)
			ui.Text(c, event.TS.Local().Format("15:04:05")).FontSize(10).
				FixedLineHeight(15).TextColor(t.LabelTertiary).Width(54)
			ui.Column(c).Grow(1).Gap(1).Children(func() {
				ui.Text(c, event.Type).FontSize(12).FixedLineHeight(17).
					FontWeight(500).TextColor(t.LabelPrimary).MaxLines(1).
					Ellipsis("…")
				if preview := eventPayloadPreview(event.Payload); preview != "" {
					ui.Text(c, preview).FontSize(11).FixedLineHeight(15).
						TextColor(t.LabelTertiary).MaxLines(2).Ellipsis("…")
				}
			})
		})
}

// eventPayloadPreview compacts a raw event payload onto a bounded line, so a
// large tool result cannot dominate the log.
func eventPayloadPreview(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		return truncateLogText(string(raw))
	}
	return truncateLogText(out.String())
}

// truncateLogText bounds a log preview to 180 characters.
func truncateLogText(s string) string {
	const max = 180
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// runShellCommandFor runs command through the selected shell in dir. The
// platform default keeps the old behavior (cmd /C on Windows, /bin/sh -c
// elsewhere); a named shell is resolved through PATH and invoked with its
// own non-interactive command flag.
func runShellCommandFor(ctx context.Context, dir, shellName, command string) ([]byte, error) {
	var cmd *exec.Cmd
	switch shellName {
	case "cmd":
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	case "powershell", "pwsh":
		cmd = exec.CommandContext(ctx, shellName, "-NoProfile", "-NonInteractive", "-Command", command)
	case "sh", "bash", "zsh", "fish", "git-bash":
		path := shellName
		if sh, ok := findTerminalShell(shellName); ok && sh.path != "" {
			path = sh.path
		}
		cmd = exec.CommandContext(ctx, path, "-c", command)
	default:
		if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(ctx, "cmd", "/C", command)
		} else {
			cmd = exec.CommandContext(ctx, "/bin/sh", "-c", command)
		}
	}
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

// terminalShellNames is a small helper for diagnostics and tests.
func terminalShellNames() []string {
	shells := terminalShells()
	names := make([]string, 0, len(shells))
	for _, sh := range shells {
		names = append(names, sh.name)
	}
	sort.Strings(names)
	return names
}
