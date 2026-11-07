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
// shell picker, scrollable mono output and Run/Stop.
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

	// Files pane state: the current relative directory, the selected file, the
	// last error and the per-path preview cache.
	filesDir      string
	filesSelected string
	filesErr      string
	filesPreview  map[string]*filePreview

	// Changes pane state: the cached uncommitted changes (re-read on the native
	// Refresh action), the load error and the per-file diff fold state.
	changesLoaded bool
	changes       []contracts.FileChange
	changesErr    error
	changesOpen   map[string]bool

	// term is the Terminal pane's state (the command runner appends from a
	// background goroutine, so its output guard lives in terminalState).
	term *terminalState

	// Agents pane state: the selected teammate, the steer draft and the last
	// action status/error.
	selectedMemberID string
	steerDraft       string
	teamStatus       string
	teamErr          string

	// Tasks pane state: the cached shared task board (loaded lazily and after
	// every mutation) and the new-task draft.
	tasksLoaded bool
	tasks       []contracts.BoardTask
	tasksErr    string
	taskDraft   string
}

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
			open:         make(map[string]bool),
			ctx:          ctx,
			locale:       locale,
			filesPreview: make(map[string]*filePreview),
			changesOpen:  make(map[string]bool),
			term:         newTerminalState(),
		}
		// The session log is optional: the inspector still renders without a
		// durable store (headless tests), it just shows the empty log copy.
		var log eventReader
		if raw, ok := ctx.Get(contracts.KeyStore); ok {
			log, _ = raw.(eventReader)
		}

		unsubscribe, err := chat.Subscribe(func(string) {
			runtime.Post(func() { runtime.Invalidate() })
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
					var board contracts.TaskBoard
					if service, ok := ctx.Get(contracts.KeyTaskBoard); ok {
						board = service
					}
					buildPanel(c, design.Of(c), runtime, chat, log, workspace, terminal, team, board, config, moduleState)
				},
			})
		})
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeyChat)).
		WithName("ZeoKeel session inspector")
}

// eventReader is the narrow slice of the durable session log the Log pane
// reads; contracts.Store (and therefore the SQLite store module) implements it.
type eventReader interface {
	Last(ctx context.Context, sessionID string, limit int) ([]rig.SessionEvent, error)
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
