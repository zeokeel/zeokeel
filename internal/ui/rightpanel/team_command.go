package rightpanel

// Palette commands that open a specific inspector pane. The stable IDs are the
// contract the session header's team chip and the command palette use:
//
//	rightpanel.team.open  — the team (Agents) pane
//	rightpanel.tasks.open — the shared task board
//
// Each command opens the inspector when the host keeps it closed and selects
// the pane in the same click, so a palette entry or a header chip is a real
// one-step entry instead of an invisible state change.

import (
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

const (
	// TeamOpenCommandID opens the inspector's team pane (stable ID).
	TeamOpenCommandID = "rightpanel.team.open"
	// TasksOpenCommandID opens the inspector's task-board pane (stable ID).
	TasksOpenCommandID = "rightpanel.tasks.open"
)

// registerOpenCommands (re)registers the two pane-opening commands with the
// current language's copy. Both hints name the pane's capability so the command
// palette search for 团队/任务 (team/tasks) finds them.
func (st *state) registerOpenCommands() error {
	st.closeOpenCommands()
	if st.runtime == nil {
		return nil
	}
	teamCommand, err := st.runtime.RegisterCommand(contracts.CommandSpec{
		ID:    TeamOpenCommandID,
		Title: st.tr("打开团队面板", "Open team panel"),
		Hint: st.tr(
			"查看智能体团队：队友、同步子代理、运行状态与团队任务",
			"Inspect the agent team: teammates, subagents, run state and team tasks"),
		Order: 920,
		Run: func() error {
			st.openPane(tabAgents)
			return nil
		},
	})
	if err != nil {
		return err
	}
	tasksCommand, err := st.runtime.RegisterCommand(contracts.CommandSpec{
		ID:    TasksOpenCommandID,
		Title: st.tr("打开任务板", "Open task board"),
		Hint: st.tr(
			"查看团队共享任务：负责人、依赖、阻塞与完成度",
			"Open the shared team task board: owners, dependencies, blockers and progress"),
		Order: 921,
		Run: func() error {
			st.openPane(tabTasks)
			return nil
		},
	})
	if err != nil {
		teamCommand()
		return err
	}
	st.openCommands = []func(){teamCommand, tasksCommand}
	st.openCommandLang = uikit.Language(st.locale)
	return nil
}

// closeOpenCommands unregisters the two pane-opening commands.
func (st *state) closeOpenCommands() {
	for _, unregister := range st.openCommands {
		unregister()
	}
	st.openCommands = nil
}

// syncOpenCommands re-registers the commands when the language changed, so the
// palette titles and hints follow the UI language.
func (st *state) syncOpenCommands() {
	if st.openCommands == nil || uikit.Language(st.locale) != st.openCommandLang {
		_ = st.registerOpenCommands()
	}
}

// openPane selects one inspector pane, opening the inspector first when the
// host keeps it closed. The inspector slot only builds while the host shows it,
// so a stale lastBuiltFrame means "closed" (the host built at least one frame
// after our last build).
func (st *state) openPane(tab int) {
	if st.runtime == nil {
		return
	}
	st.tab = tab
	switch tab {
	case tabTasks:
		// Re-entering the pane re-reads the shared board: the agent may have
		// written tasks while the pane was closed.
		st.tasksLoaded = false
		st.tasksExpanded = ""
	case tabAgents:
		st.teamErr = ""
		st.teamStatus = ""
	}
	if !st.inspectorVisible() {
		st.runInspectorToggle()
	}
	st.runtime.Invalidate()
}

// inspectorVisible reports whether the inspector slot built in the most recent
// frame. The host builds frames continuously while the window is active; an
// idle window still builds the frame that followed the close.
func (st *state) inspectorVisible() bool {
	if st.runtime == nil {
		return false
	}
	return st.runtime.Frame() == st.lastBuiltFrame
}

// runInspectorToggle runs the host's toggle-inspector command, the one source
// of the inspector's open state.
func (st *state) runInspectorToggle() {
	for _, command := range st.runtime.Commands() {
		if command.ID == inspectorToggleCommandID && command.Run != nil {
			_ = command.Run()
			return
		}
	}
}
