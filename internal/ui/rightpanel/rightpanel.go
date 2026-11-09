// Package rightpanel contributes the session inspector: the right sidebar of
// the conversation screen (contracts.SlotSessionPanelRight). It is a UI module
// (kernel Descriptor + host UI services) rendered entirely with mygo's native
// controls and the brand design system (internal/design): a native tab strip
// over the overview, tools, log, files, changes, terminal, agents and tasks
// panes inside t.Panel cards. It never draws a control (or a Canvas) itself.
//
// The module reads the chat read model (contracts.ChatService) and never writes
// it: the overview shows the active session's title, message count, update time
// and running/error badges; the tools pane aggregates every ToolCall of the
// session with a status badge and a collapsible summary; the model pane shows
// the effective model configuration, with the API key reduced to "set/not set"
// so a secret can never reach the screen; the log pane lists the newest durable
// session events; the files pane browses the current workspace (contracts.
// WorkspaceService, looked up optionally) with directory navigation and a
// read-only text/Markdown/image preview; the changes pane lists the current
// workspace's uncommitted files with native collapsible unified diffs; the
// terminal pane is a simple command runner (no PTY/persistent shell) with a
// shell picker, scrollable mono output and Run/Stop; the agents pane merges the
// async team roster (steer/interrupt/close) with the read-only synchronous
// subagent delegations of the active session.
//
// The host owns the panel width and the open/close control: the module only
// fills the slot. Chat changes arrive on the agent goroutine, so the module
// hops to the UI thread with UIRuntime.Post and requests a frame; the
// subscription and the slot registration are kernel-effect owned, so disposal
// unwinds both.
package rightpanel

import (
	"context"
	"fmt"
	"strings"

	"github.com/zeokeel/zeokeel-rig"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// ModuleID is the compile-time module identity.
const ModuleID = "zeokeel-ui/rightpanel"

// SlotItemID is the id of the inspector's contribution to the right panel.
const SlotItemID = "rightpanel.inspector"

// slotOrder places the inspector first in the right panel.
const slotOrder = 10

// Config is the module's typed assembly config (all fields have defaults).
type Config struct {
	// Title is the panel heading (default "检查").
	Title string `json:"title"`
	// OverviewTab/ToolsTab/ModelTab/LogTab/FilesTab are the tab labels.
	OverviewTab string `json:"overviewTab"`
	ToolsTab    string `json:"toolsTab"`
	ModelTab    string `json:"modelTab"`
	LogTab      string `json:"logTab"`
	FilesTab    string `json:"filesTab"`
	ChangesTab  string `json:"changesTab"`
	TerminalTab string `json:"terminalTab"`
	AgentsTab   string `json:"agentsTab"`
	TasksTab    string `json:"tasksTab"`
	// EmptyText is the overview copy when there is no active session.
	EmptyText string `json:"emptyText"`
}

// resolved trims the configured strings, keeping explicit overrides only; the
// localized defaults are resolved per frame from the host locale.
func (c Config) resolved() Config {
	return Config{
		Title:       strings.TrimSpace(c.Title),
		OverviewTab: strings.TrimSpace(c.OverviewTab),
		ToolsTab:    strings.TrimSpace(c.ToolsTab),
		ModelTab:    strings.TrimSpace(c.ModelTab),
		LogTab:      strings.TrimSpace(c.LogTab),
		FilesTab:    strings.TrimSpace(c.FilesTab),
		ChangesTab:  strings.TrimSpace(c.ChangesTab),
		TerminalTab: strings.TrimSpace(c.TerminalTab),
		AgentsTab:   strings.TrimSpace(c.AgentsTab),
		TasksTab:    strings.TrimSpace(c.TasksTab),
		EmptyText:   strings.TrimSpace(c.EmptyText),
	}
}

func (c Config) titleLabel(locale contracts.LocaleService) string {
	return firstNonEmpty(c.Title, uikit.T(locale, "检查", "Inspector"))
}

func (c Config) overviewLabel(locale contracts.LocaleService) string {
	return firstNonEmpty(c.OverviewTab, uikit.T(locale, "概览", "Overview"))
}

func (c Config) toolsLabel(locale contracts.LocaleService) string {
	return firstNonEmpty(c.ToolsTab, uikit.T(locale, "工具", "Tools"))
}

func (c Config) logLabel(locale contracts.LocaleService) string {
	return firstNonEmpty(c.LogTab, uikit.T(locale, "日志", "Log"))
}

func (c Config) filesLabel(locale contracts.LocaleService) string {
	return firstNonEmpty(c.FilesTab, uikit.T(locale, "文件", "Files"))
}

func (c Config) changesLabel(locale contracts.LocaleService) string {
	return firstNonEmpty(c.ChangesTab, uikit.T(locale, "变更", "Changes"))
}

func (c Config) terminalLabel(locale contracts.LocaleService) string {
	return firstNonEmpty(c.TerminalTab, uikit.T(locale, "终端", "Terminal"))
}

func (c Config) agentsLabel(locale contracts.LocaleService) string {
	return firstNonEmpty(c.AgentsTab, uikit.T(locale, "团队", "Agents"))
}

func (c Config) tasksLabel(locale contracts.LocaleService) string {
	return firstNonEmpty(c.TasksTab, uikit.T(locale, "任务", "Tasks"))
}

func (c Config) emptyLabel(locale contracts.LocaleService) string {
	return firstNonEmpty(c.EmptyText, uikit.T(locale, "还没有活动会话", "No active session"))
}

// state is the module's UI state (one instance per loaded module, only touched
// on the UI thread): the selected pane and the per-tool summary fold state.
type state struct {
	tab  int
	open map[string]bool

	// ctx is the module's kernel context: the Files pane looks up the optional
	// workspace backend with ctx.Get(contracts.KeyWorkspace) (no Requires).
	ctx cordis.Context

	// locale localizes the inspector's own copy on every frame.
	locale contracts.LocaleService
	// cfg is the resolved module config: the pane-switch commands need the
	// localized pane names for their current-pane hint.
	cfg Config
	// runtime registers the pane-switch commands and requests frames.
	runtime contracts.UIRuntime
	// paneCommands are the registered command unregisters plus the language and
	// pane they were built for, so the hint follows a pane/language switch.
	paneCommands    []func()
	paneCommandLang string
	paneCommandTab  int

	// Files pane state: the current relative directory, the selected file, the
	// last error, the per-path preview cache, the name filter and whether hidden
	// (dot) entries show.
	filesDir        string
	filesSelected   string
	filesErr        string
	filesPreview    map[string]*filePreview
	filesFilter     string
	filesShowHidden bool
	// fileEditor is the read-only official code editor showing the selected
	// text file's preview (syntax highlighting, line numbers, virtualization);
	// it is rebuilt only when another file is selected.
	fileEditor     *editor.State
	fileEditorPath string

	// Files pane listing cache (P1): the directory entries are read on a
	// directory/workspace change or an explicit Refresh, never from rendering
	// code on every frame. filesCursor is the row the native list has chosen,
	// by path, so the choice survives a listing reload.
	filesEntries   []contracts.FileEntry
	filesLoaded    bool
	filesLoadErr   error
	filesLoadedDir string
	filesWorkspace string
	filesCursor    string
	filesList      ui.ListState
	filesListSel   int

	// Log pane cache (P1): the newest durable events of the active session are
	// read on a session change or when the chat reports new events, never on
	// every frame.
	logEvents  []rig.SessionEvent
	logLoaded  bool
	logErr     error
	logSession string
	logList    ui.ListState

	// Tools pane: the native list and the row Enter acts on.
	toolsList ui.ListState
	toolsSel  int

	// Agents pane: the native teammate list and the read-only delegation list,
	// with the row chosen in each.
	agentsList   ui.ListState
	agentsSel    int
	subagentList ui.ListState
	subagentsSel int

	// Tasks pane: the native task list.
	tasksList ui.ListState

	// Changes pane state: the cached uncommitted changes (re-read on the native
	// Refresh action), the load error, the per-file diff fold state and the
	// per-file official diff viewers (editor.DiffState, built once per diff).
	changesLoaded bool
	changes       []contracts.FileChange
	changesErr    error
	changesOpen   map[string]bool
	diffs         map[string]*diffView

	// term is the Terminal pane's state (the command runner appends from a
	// background goroutine, so its output guard lives in terminalState).
	term *terminalState

	// Agents pane state: the selected teammate, the steer draft, the last
	// action status/error and the close confirmation (closing a teammate is
	// destructive, so it goes through a native AlertDialog). The synchronous
	// delegations share the list but keep a separate selection: their detail is
	// read-only.
	selectedMemberID   string
	selectedSubagentID string
	steerDraft         string
	teamStatus         string
	teamErr            string
	memberCloseOpen    bool
	memberCloseID      string
	memberCloseName    string

	// Files pane artifact history: the expanded preview rows (keyed by
	// artifact id), the official List's state and choice, and the last download
	// status.
	artifactsOpen      map[string]bool
	artifactsList      ui.ListState
	artifactsSel       int
	artifactsNotice    string
	artifactsErr       string
	artifactsSavedPath string

	// Tasks pane state: the cached shared task board (loaded lazily and after
	// every mutation), the new-task draft, the delete confirmation (a native
	// Modal: the destructive action is never the default), the list choice, the
	// expanded task detail and the explicit-refresh loading flag.
	tasksLoaded       bool
	tasks             []contracts.BoardTask
	tasksErr          string
	tasksLoading      bool
	tasksSel          int
	tasksExpanded     string
	tasksRemoveOpen   bool
	taskDraft         string
	taskDeleteOpen    bool
	taskDeleteID      string
	taskDeleteSubject string

	// notifier raises the team completion toasts; nil when the host has no
	// notification service.
	notifier contracts.Notifier
	// teamSeen remembers the last observed run state per member ID, so a run
	// that finishes while the window is idle raises one toast (deduped by the
	// finished stamp); team_notify.go owns the transition and pruning.
	teamSeen map[string]teamSeenState
	// lastBuiltFrame is the UIRuntime frame number of the last inspector build:
	// the "open team/tasks panel" commands compare it with the live frame to
	// tell whether the inspector is currently visible (the slot does not build
	// while the host keeps it closed).
	lastBuiltFrame uint64

	// openCommands are the palette commands that open the team/tasks pane; they
	// are re-registered when the language changes.
	openCommands    []func()
	openCommandLang string

	// Team spawn dialog state: the native "new teammate" form. The spawn itself
	// runs off the UI thread; the dialog shows a labelled spinner while it is in
	// flight and can be cancelled.
	teamSpawnOpen   bool
	teamSpawnName   string
	teamSpawnPrompt string
	teamSpawnModel  string
	teamSpawnErr    string
	teamSpawnBusy   bool
	teamSpawnCancel context.CancelFunc

	// --- Phase 2 shell-depth fields (audit-shell) ---------------------------
	// logFilter is the Log pane's type/payload filter (its query is matched
	// against the event type and payload, case-insensitively).
	logFilter string
	// filesLoading and changesLoading mark a directory listing / workspace
	// changes read that runs off the UI thread: the pane shows a spinner and
	// disables the refresh action until the posted result lands, instead of
	// blocking the frame on disk or git I/O.
	filesLoading   bool
	changesLoading bool
}

// tr localizes the inspector's own copy for the current frame.
func (st *state) tr(zh, en string) string { return uikit.T(st.locale, zh, en) }

// Module registers the session inspector slot.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		chat, err := ctx.Require(contracts.KeyChat)
		if err != nil {
			return err
		}
		config := cfg.resolved()
		locale, _ := ctx.Get(contracts.KeyLocale)
		moduleState := &state{
			open:          make(map[string]bool),
			ctx:           ctx,
			locale:        locale,
			cfg:           config,
			runtime:       runtime,
			filesPreview:  make(map[string]*filePreview),
			filesListSel:  -1,
			toolsSel:      -1,
			agentsSel:     -1,
			subagentsSel:  -1,
			tasksSel:      -1,
			teamSeen:      make(map[string]teamSeenState),
			changesOpen:   make(map[string]bool),
			diffs:         make(map[string]*diffView),
			artifactsOpen: make(map[string]bool),
			artifactsSel:  -1,
			term:          newTerminalState(),
		}
		// The host binds Cmd/Ctrl+Alt+Left/Right to these two window commands and
		// shows them in the command palette: they cycle the inspector's pane
		// without opening the picker first.
		if err := moduleState.registerPaneCommands(); err != nil {
			return err
		}
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(moduleState.closePaneCommands)}
		}); err != nil {
			moduleState.closePaneCommands()
			return err
		}
		// The session log is optional: the inspector still renders without a
		// durable store (headless tests), it just shows the empty log copy.
		var log eventReader
		if raw, ok := ctx.Get(contracts.KeyStore); ok {
			log, _ = raw.(eventReader)
		}
		// The team/tasks open commands and the completion toasts are optional
		// host capabilities: without them the panes still render and just have
		// no palette entry / toast.
		if raw, ok := ctx.Get(contracts.KeyNotifier); ok {
			moduleState.notifier, _ = raw.(contracts.Notifier)
		}
		if err := moduleState.registerOpenCommands(); err != nil {
			return fmt.Errorf("rightpanel: open-pane commands: %w", err)
		}
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(moduleState.closeOpenCommands)}
		}); err != nil {
			moduleState.closeOpenCommands()
			return err
		}

		unsubscribe, err := chat.Subscribe(func(string) {
			// A chat change may add durable events to the active session and may
			// follow a team/task mutation; drop the cached log and the cached
			// task board on the UI thread so the next frame re-reads them once
			// instead of every frame reading them (P1), and raise the one-shot
			// team completion toasts. Without the task invalidation an agent
			// writing team_task_* left the open Tasks pane showing stale rows
			// until the user pressed Refresh.
			runtime.Post(func() {
				moduleState.logLoaded = false
				moduleState.tasksLoaded = false
				moduleState.observeTeamRunChanges()
				runtime.Invalidate()
			})
		})
		if err != nil {
			return fmt.Errorf("rightpanel: subscribe chat: %w", err)
		}
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(unsubscribe)}
		}); err != nil {
			unsubscribe()
			return err
		}

		return own(ctx, func() (func(), error) {
			return runtime.RegisterSlotItem(contracts.SlotItem{
				Slot:  contracts.SlotSessionPanelRight,
				ID:    SlotItemID,
				Order: slotOrder,
				Build: func(c *ui.Context) {
					var workspace contracts.WorkspaceService
					if service, ok := ctx.Get(contracts.KeyWorkspace); ok {
						workspace = service
					}
					var terminal contracts.TerminalService
					if service, ok := ctx.Get(contracts.KeyTerminal); ok {
						terminal = service
					}
					var team contracts.TeamService
					if service, ok := ctx.Get(contracts.KeyTeam); ok {
						team = service
					}
					// The synchronous delegation read model is the optional
					// read-only half of the provided subagent service: the
					// Agents pane merges it with the teammate roster when
					// present and keeps working when it is not.
					var activities contracts.SubagentActivityLister
					if service, ok := ctx.Get(contracts.KeySubagents); ok {
						if lister, ok := any(service).(contracts.SubagentActivityLister); ok {
							activities = lister
						}
					}
					var board contracts.TaskBoard
					if service, ok := ctx.Get(contracts.KeyTaskBoard); ok {
						board = service
					}
					// The artifact history is the optional lister half of the
					// host artifact presenter; the Files pane only reads it.
					var artifacts contracts.ArtifactLister
					if service, ok := ctx.Get(contracts.KeyArtifactPresenter); ok {
						artifacts, _ = service.(contracts.ArtifactLister)
					}
					buildPanel(c, design.Of(c), runtime, chat, log, workspace, terminal, team, activities, board, artifacts, config, moduleState)
				},
			})
		})
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeyChat)).
		WithName("ZeoKeel session inspector")
}

// Pane-switch command metadata. The host binds the shortcuts and the command
// palette renders CommandSpec.Shortcut verbatim.
const (
	panePrevCommandID = "rightpanel.pane.prev"
	paneNextCommandID = "rightpanel.pane.next"
	panePrevShortcut  = "Cmd/Ctrl + Alt + ←"
	paneNextShortcut  = "Cmd/Ctrl + Alt + →"
)

// registerPaneCommands (re)registers the pane-switch commands with the current
// pane's name as their hint. The host re-registers nothing: the module keeps its
// two commands in sync with the pane and the host language.
func (st *state) registerPaneCommands() error {
	if st.runtime == nil {
		return nil
	}
	st.closePaneCommands()
	labels := paneLabels(st.cfg, st.locale)
	if len(labels) == 0 {
		return nil
	}
	if st.tab < 0 || st.tab >= len(labels) {
		st.tab = 0
	}
	hint := st.tr("当前：", "Current: ") + labels[st.tab]
	prev, err := st.runtime.RegisterCommand(contracts.CommandSpec{
		ID:       panePrevCommandID,
		Title:    st.tr("上一个检查器面板", "Previous inspector pane"),
		Hint:     hint,
		Order:    910,
		Shortcut: panePrevShortcut,
		Run:      func() error { st.stepPane(-1); return nil },
	})
	if err != nil {
		return err
	}
	next, err := st.runtime.RegisterCommand(contracts.CommandSpec{
		ID:       paneNextCommandID,
		Title:    st.tr("下一个检查器面板", "Next inspector pane"),
		Hint:     hint,
		Order:    911,
		Shortcut: paneNextShortcut,
		Run:      func() error { st.stepPane(1); return nil },
	})
	if err != nil {
		prev()
		return err
	}
	st.paneCommands = []func(){prev, next}
	st.paneCommandLang = uikit.Language(st.locale)
	st.paneCommandTab = st.tab
	return nil
}

// closePaneCommands unregisters the pane-switch commands.
func (st *state) closePaneCommands() {
	for _, unregister := range st.paneCommands {
		unregister()
	}
	st.paneCommands = nil
}

// syncPaneCommands re-registers the commands when the pane or language changed,
// so the palette hint always names the pane that is showing.
func (st *state) syncPaneCommands() {
	if st.paneCommands == nil ||
		uikit.Language(st.locale) != st.paneCommandLang || st.tab != st.paneCommandTab {
		_ = st.registerPaneCommands()
	}
}

// stepPane moves the inspector's selection by delta, wrapping around the pane
// ring; the host shortcut and the command palette both run it.
func (st *state) stepPane(delta int) {
	labels := paneLabels(st.cfg, st.locale)
	if len(labels) == 0 {
		return
	}
	next := (st.tab + delta + len(labels)) % len(labels)
	st.selectPane(next, st.runtime)
}

// selectPane sets the active inspector pane and refreshes what the pane shows:
// entering the Tasks pane re-reads the shared board (the agent may have written
// tasks while another pane was open), and the Agents pane clears its last action
// status. It is the one path used by the selector menu, the pane-step commands
// and the open-pane palette commands.
func (st *state) selectPane(tab int, runtime contracts.UIRuntime) {
	labels := paneLabels(st.cfg, st.locale)
	if len(labels) == 0 {
		return
	}
	if tab < 0 || tab >= len(labels) {
		tab = 0
	}
	st.tab = tab
	switch tab {
	case tabTasks:
		st.tasksLoaded = false
		st.tasksErr = ""
		st.tasksExpanded = ""
	case tabAgents:
		st.teamErr = ""
		st.teamStatus = ""
	}
	if runtime != nil {
		runtime.Invalidate()
	}
}

// paneMenuGroups is the pane selector's menu order: the orchestration panes
// first (the team and its shared task board are the daily entry points), then
// the working panes, then the record panes. The separators make the grouping
// visible without adding another level of nesting.
func paneMenuGroups() [][]int {
	return [][]int{
		{tabAgents, tabTasks},
		{tabTools, tabFiles, tabChanges, tabTerminal},
		{tabOverview, tabLog},
	}
}

// eventReader is the narrow slice of the durable session log the Log pane
// reads; contracts.Store (and therefore the SQLite store module) implements it.
type eventReader interface {
	Last(ctx context.Context, sessionID string, limit int) ([]rig.SessionEvent, error)
}

// teamServiceFromState returns the optional live team backend. The inspector
// reads it lazily (contracts.KeyTeam is optional) so a headless host without the
// agent module still renders the rest of the panel.
func teamServiceFromState(st *state) contracts.TeamService {
	if st == nil {
		return nil
	}
	raw, ok := st.ctx.Get(contracts.KeyTeam)
	if !ok || raw == nil {
		return nil
	}
	team, _ := raw.(contracts.TeamService)
	return team
}

// own registers one contribution and ties its unregistration to ctx's effect
// lifetime, so activation failure and module disposal leave no residue.
func own(ctx cordis.Context, register func() (func(), error)) error {
	unregister, err := register()
	if err != nil {
		return err
	}
	if _, err := ctx.Effect(func() cordis.Effect {
		return cordis.Effect{cordis.CleanupOf(unregister)}
	}); err != nil {
		unregister()
		return err
	}
	return nil
}

// firstNonEmpty returns the first non-empty trimmed string.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
