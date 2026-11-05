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
	"github.com/zeokeel/zeokeel/internal/winproc"
)

// sessionLogTTL bounds how often the Session Log re-reads the event table.
const sessionLogTTL = 500 * time.Millisecond

// settingTerminalShell is the store key the Choose shell control writes. It
// is a plain string setting (shell name, e.g. "zsh"), so it survives a
// restart without a schema change.
const settingTerminalShell = "terminal.shell"

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

// startTabPanel is the right panel's Start page: the guide a fresh panel shows
// before the user picks a tab. The cards are brand surfaces on the theme's
// spacing grid, and each performs the action its label promises; the terminal
// card also exposes Choose shell.
func (a *App) startTabPanel(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Column(c).Grow(1).Padding(th.Space(6), th.Space(5)).
		Justify(ui.Center).Gap(th.Space(3)).Children(func() {
		// A heading and one line of help: the earlier floating icon read as a
		// button/loading state and the centered column left a large blank above
		// (UX-13).
		ui.Column(c).Gap(th.Space(1)).Children(func() {
			ui.Text(c, a.tr("Workspace tools", "工作区工具")).FontSize(14).
				FontWeight(600).TextColor(t.LabelPrimary)
			ui.Text(c, a.tr("Open files, a terminal, or this session's log.",
				"打开文件、终端，或查看本会话的日志。")).FontSize(12).
				FixedLineHeight(18).TextColor(t.LabelTertiary).MaxLines(2)
		})
		a.startCard(c, t, icons.Folder(), t.Accent,
			a.T("files.workspace_files"),
			a.tr("Browse files in this session's workspace", "浏览此会话工作区的文件"),
			a.filesRootTooltip(),
			func() { a.rightTab = filesTab })
		a.startTerminalCard(c, t)
		a.startCard(c, t, icons.SessionLog(), t.LabelSecondary,
			a.tr("Session Log", "会话日志"),
			a.tr("Read this session's raw event log (read-only)",
				"查看当前会话的原始事件日志（只读）"),
			"",
			func() { a.rightTab = sessionLogTab })
	})
}

// startCard is one guide card: a brand raised surface with a glyph disc, a
// title and a description. The whole card is a native ButtonBase, so it takes
// the focus and Enter/Space and the small description is not a dead target.
func (a *App) startCard(c *ui.Context, t *design.Tokens, ic *ui.SVG, tint ui.Color,
	title, description, tooltip string, onClick func()) {
	th := t.Theme()
	card := ui.ButtonBase(c).MinHeight(th.Space(19)).Padding(th.Space(4)).
		Gap(th.Space(3)).Radius(th.Radius).Border(1, t.BorderL2).
		Background(t.BgRaised).AlignItems(ui.Center).Justify(ui.Start).Label(title)
	if tooltip != "" {
		card = card.Tooltip(tooltip)
	}
	if card.Hovered() {
		card = card.Background(t.BgHover).Border(1, t.BorderL3)
	}
	card.Children(func() {
		ui.Row(c).Size(th.Space(8), th.Space(8)).Radius(th.Space(4)).
			Background(t.BgHover).Center().Children(func() {
			ui.Icon(c, ic).Size(th.Space(4.5), th.Space(4.5)).TextColor(tint)
		})
		ui.Column(c).Grow(1).Gap(th.Space(1)).Children(func() {
			ui.Text(c, title).FontSize(th.FontSize).FixedLineHeight(th.Space(5)).
				FontWeight(600).TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
			ui.Text(c, description).FontSize(th.FontSize - 2).FixedLineHeight(th.Space(4)).
				TextColor(t.LabelTertiary).MaxLines(2)
		})
	})
	card.OnClick(onClick)
}

// startTerminalCard is the New terminal guide card. Its chevron is the Choose
// shell menu; the card body opens the Terminal tab with the remembered shell.
func (a *App) startTerminalCard(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	st := a.startUI()
	label := a.T("files.new_terminal")
	card := ui.ButtonBase(c).MinHeight(th.Space(19)).Padding(th.Space(4)).
		Gap(th.Space(3)).Radius(th.Radius).Border(1, t.BorderL2).
		Background(t.BgRaised).AlignItems(ui.Center).Justify(ui.Start).Label(label)
	if card.Hovered() {
		card = card.Background(t.BgHover).Border(1, t.BorderL3)
	}
	card.Children(func() {
		ui.Row(c).Size(th.Space(8), th.Space(8)).Radius(th.Space(4)).
			Background(t.BgHover).Center().Children(func() {
			ui.Icon(c, icons.Terminal()).Size(th.Space(4.5), th.Space(4.5)).
				TextColor(t.Accent)
		})
		ui.Column(c).Grow(1).Gap(th.Space(1)).Children(func() {
			ui.Row(c).Gap(th.Space(1)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, label).FontSize(th.FontSize).FixedLineHeight(th.Space(5)).
					FontWeight(600).TextColor(t.LabelPrimary)
				// The chevron is its own native menu button (not just a
				// drawing inside the card), so Choose shell is reachable from
				// the keyboard without activating the card.
				choose := a.tr("Choose shell", "选择 shell")
				chevron := ui.ButtonBase(c).Size(th.Space(4.5), th.Space(4.5)).
					Center().Radius(design.RadiusXS).Label(choose).Tooltip(choose)
				if chevron.Hovered() {
					chevron = chevron.Background(t.BgHover)
				}
				chevron.Children(func() {
					ui.Icon(c, icons.ChevronDown()).Size(th.Space(3), th.Space(3)).
						TextColor(t.LabelTertiary)
				})
				chevron.Expanded(st.shellMenuOpen)
				chevron.OnClick(func() { st.shellMenuOpen = !st.shellMenuOpen })
				ui.Popover(c, chevron, &st.shellMenuOpen, func() { a.shellPickerMenu(c, t) })
			})
			ui.Text(c, a.tr("Run commands in the Session workspace",
				"在会话工作区运行命令")).FontSize(th.FontSize - 2).
				FixedLineHeight(th.Space(4)).TextColor(t.LabelTertiary).MaxLines(2)
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
// current one marked. It uses the shared branded popover panel and radio rows,
// clamped to the right panel, so it keeps the focus, keyboard and checked
// semantics the other menus have.
func (a *App) shellPickerMenu(c *ui.Context, t *design.Tokens) {
	st := a.startUI()
	current := a.terminalShellName()
	a.popoverMenuPanelIn(c, t, a.rightSidebarPanelWidth(c), func() {
		ui.Text(c, a.tr("Choose shell", "选择 shell")).FontSize(11).
			FixedLineHeight(16).TextColor(t.LabelTertiary).
			Padding(design.Space1, design.Space2, 0, design.Space2)
		for _, sh := range terminalShells() {
			sh := sh
			label := a.shellMenuLabel(sh)
			a.popoverMenuRow(c, t, label, sh.path, sh.name == current, nil, func() {
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
	a.invalidateFilesDirCache()
	a.fileErr = ""
	c.Toast(a.tr("Reloaded the directory", "已重新加载目录"))
}

// sessionLogPanel is the read-only Session Log tab. It renders the persisted
// event trace the controller exposes (store-backed) in the brand panel, so a
// session's raw events stay inspectable without the experimental inspector.
func (a *App) sessionLogPanel(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Row(c).Height(th.Space(9)).PaddingX(th.Space(3)).Gap(th.Space(2)).
		AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.BorderL2).
		Children(func() {
			ui.Icon(c, icons.SessionLog()).Size(th.Space(3.5), th.Space(3.5)).
				TextColor(t.LabelSecondary)
			ui.Text(c, a.tr("Session Log", "会话日志")).FontSize(th.FontSize - 1).
				FixedLineHeight(th.Space(4.5)).FontWeight(600).
				TextColor(t.LabelPrimary).Grow(1)
			ui.Badge(c, a.tr("Read-only", "只读"))
		})
	if a.Ctrl == nil {
		a.rightEmpty(c, t, icons.SessionLog(),
			a.tr("The Session Log needs the live session service",
				"会话日志需要实时会话服务"))
		return
	}
	sessionID := a.Ctrl.CurrentSessionID()
	if snap := a.currentSnapshot(); snap != nil && snap.SessionID != "" {
		sessionID = snap.SessionID
	}
	if sessionID == "" {
		a.rightEmpty(c, t, icons.SessionLog(), a.tr("No session selected", "未选择会话"))
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
		a.rightEmpty(c, t, icons.SessionLog(), fmt.Sprintf(a.tr("Could not read the event log: %v",
			"无法读取事件日志：%v"), st.logErr))
		return
	}
	events := st.logEvents
	if len(events) == 0 {
		a.rightEmpty(c, t, icons.Data(), a.tr("No events recorded yet", "暂无事件记录"))
		return
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(th.Space(1), th.Space(2), th.Space(2)).Gap(th.Space(0.5)).
			Children(func() {
				for _, event := range events {
					a.sessionLogRow(c, t, event)
				}
			})
	})
}

// sessionLogRow is one event: sequence, local time, type and a bounded payload
// preview. The log is read-only, so the row has no handler.
func (a *App) sessionLogRow(c *ui.Context, t *design.Tokens, event store.Event) {
	th := t.Theme()
	ui.Row(c).MinHeight(th.Space(8)).Padding(th.Space(1), th.Space(2)).
		Gap(th.Space(2)).Radius(design.RadiusXS).AlignItems(ui.Start).
		Children(func() {
			ui.Column(c).Width(th.Space(11)).Children(func() {
				ui.Text(c, fmt.Sprintf("#%d", event.Seq)).FontSize(th.FontSize - 4).
					FixedLineHeight(th.Space(3.5)).TextColor(t.LabelTertiary)
				ui.Text(c, event.TS.Local().Format("15:04:05")).FontSize(th.FontSize - 4).
					FixedLineHeight(th.Space(3.5)).TextColor(t.LabelTertiary)
			})
			ui.Column(c).Grow(1).Gap(th.Space(0.5)).Children(func() {
				ui.Text(c, event.Type).FontSize(th.FontSize - 2).
					FixedLineHeight(th.Space(4.5)).FontWeight(500).
					TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
				if preview := eventPayloadPreview(event.Payload); preview != "" {
					ui.Text(c, preview).FontSize(th.FontSize - 3).
						FixedLineHeight(th.Space(4)).TextColor(t.LabelTertiary).
						MaxLines(2).Ellipsis("…")
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
	winproc.Hide(cmd)
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
